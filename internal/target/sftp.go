package target

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"sort"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/retention"
	"github.com/f18m/prometheus-snapshot-manager/internal/utils"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SFTPTarget struct {
	name string
	cfg  config.SFTPConfig
}

func NewSFTPTarget(name string, cfg config.SFTPConfig) *SFTPTarget {
	return &SFTPTarget{name: name, cfg: cfg}
}

func (t *SFTPTarget) Name() string { return t.name }

func (t *SFTPTarget) Upload(ctx context.Context, logger *slog.Logger, filename string, content io.Reader) error {
	c, s, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	defer s.Close()

	if err := s.MkdirAll(t.cfg.RemotePath); err != nil {
		return err
	}

	fullPath := path.Join(t.cfg.RemotePath, filename)

	f, err := s.Create(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()
	written, err := io.Copy(f, content)
	if err != nil {
		return err
	}
	logger.Info("upload complete", "target", t.Name(), "file", fullPath, "written", utils.FormatBytesSI(written))
	return nil
}

func (t *SFTPTarget) List(ctx context.Context) ([]FileInfo, error) {
	c, s, err := t.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	defer s.Close()
	entries, err := s.ReadDir(t.cfg.RemotePath)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ts, err := retention.ParseArchiveTimestamp(e.Name())
		if err != nil {
			continue
		}
		out = append(out, FileInfo{Name: e.Name(), Timestamp: ts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	return out, nil
}

func (t *SFTPTarget) Delete(ctx context.Context, filename string) error {
	c, s, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	defer s.Close()
	return s.Remove(path.Join(t.cfg.RemotePath, filename))
}

func (t *SFTPTarget) connect(ctx context.Context) (*ssh.Client, *sftp.Client, error) {
	conf := &ssh.ClientConfig{User: t.cfg.User, Timeout: 15 * time.Second}

	if t.cfg.Password != "" {
		conf.Auth = append(conf.Auth, ssh.Password(t.cfg.Password))
	}
	if t.cfg.KeyData != "" {
		signer, err := ssh.ParsePrivateKey([]byte(t.cfg.KeyData))
		if err != nil {
			return nil, nil, err
		}
		conf.Auth = append(conf.Auth, ssh.PublicKeys(signer))
	}
	if t.cfg.KeyFile != "" {
		key, err := os.ReadFile(t.cfg.KeyFile)
		if err != nil {
			return nil, nil, err
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, nil, err
		}
		conf.Auth = append(conf.Auth, ssh.PublicKeys(signer))
	}
	if len(conf.Auth) == 0 {
		return nil, nil, fmt.Errorf("sftp target %s missing auth method", t.name)
	}

	if t.cfg.HostKeyCheck {
		if t.cfg.KnownHostsFile == "" {
			return nil, nil, fmt.Errorf("known_hosts_file required when host_key_check=true")
		}
		cb, err := knownhosts.New(t.cfg.KnownHostsFile)
		if err != nil {
			return nil, nil, err
		}
		conf.HostKeyCallback = cb
	} else {
		conf.HostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec
	}

	addr := net.JoinHostPort(t.cfg.Host, fmt.Sprintf("%d", t.cfg.Port))
	dialer := &net.Dialer{}
	nc, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	cc, chans, reqs, err := ssh.NewClientConn(nc, addr, conf)
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	client := ssh.NewClient(cc, chans, reqs)
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, sftpClient, nil
}
