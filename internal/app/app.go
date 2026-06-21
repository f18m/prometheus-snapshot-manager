package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/notify"
	"github.com/f18m/prometheus-snapshot-manager/internal/retention"
	"github.com/f18m/prometheus-snapshot-manager/internal/snapshot"
	"github.com/f18m/prometheus-snapshot-manager/internal/target"
)

type Manager struct {
	cfg      *config.Config
	logger   *slog.Logger
	dryRun   bool
	targets  []target.Target
	notifier *notify.Notifier
}

func New(ctx context.Context, cfg *config.Config, logger *slog.Logger, dryRun bool) (*Manager, error) {
	targets := make([]target.Target, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		switch t.Type {
		case "local":
			targets = append(targets, target.NewLocalTarget(t.Name, t.Local.Path))
		case "sftp":
			targets = append(targets, target.NewSFTPTarget(t.Name, t.SFTP))
		case "s3":
			s3t, err := target.NewS3Target(ctx, t.Name, t.S3)
			if err != nil {
				return nil, err
			}
			targets = append(targets, s3t)
		}
	}
	return &Manager{
		cfg:      cfg,
		logger:   logger,
		dryRun:   dryRun,
		targets:  targets,
		notifier: notify.New(cfg.Notifications.Apprise),
	}, nil
}

func (m *Manager) RunCycle(ctx context.Context) (retErr error) {
	start := time.Now()
	timeout, _ := time.ParseDuration(m.cfg.Prometheus.Timeout)
	snapCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := snapshot.NewClient(
		m.cfg.Prometheus.URL,
		timeout,
		m.cfg.Prometheus.BasicAuth.Username,
		m.cfg.Prometheus.BasicAuth.Password,
		m.cfg.Prometheus.TLSSkipVerify,
	)

	snapshotName := ""
	defer func() {
		status := "success"
		errText := ""
		if retErr != nil {
			status = "failure"
			errText = retErr.Error()
		}
		_ = m.notifier.Send(ctx, notify.Payload{
			Status:       status,
			SnapshotName: snapshotName,
			Duration:     time.Since(start),
			Targets:      m.targetNames(),
			Error:        errText,
		})
	}()

	name, err := client.CreateSnapshot(snapCtx)
	if err != nil {
		return err
	}
	snapshotName = name
	m.logger.Info("snapshot created", "name", name)

	snapshotPath, err := snapshot.WaitForSnapshotDir(snapCtx, m.cfg.Prometheus.SnapshotDir, name, 500*time.Millisecond)
	if err != nil {
		return err
	}

	archiveName, err := snapshot.ArchiveFilename(time.Now().UTC())
	if err != nil {
		return err
	}
	archiveBytes, err := snapshot.BuildArchive(snapshotPath, m.cfg.Compression.Level)
	if err != nil {
		return err
	}

	if err := m.uploadAll(ctx, archiveName, archiveBytes); err != nil {
		return err
	}

	if err := m.Prune(ctx); err != nil {
		return err
	}

	if m.cfg.Retention.CleanupPrometheusSnapshots {
		if m.dryRun {
			m.logger.Info("dry-run cleanup snapshot dir", "path", snapshotPath)
		} else if err := os.RemoveAll(snapshotPath); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Prune(ctx context.Context) error {
	keepWithin := time.Duration(0)
	if m.cfg.Retention.KeepWithin != "" {
		d, err := time.ParseDuration(m.cfg.Retention.KeepWithin)
		if err != nil {
			return err
		}
		keepWithin = d
	}

	policy := retention.Policy{
		KeepWithin:  keepWithin,
		KeepMinimum: m.cfg.Retention.KeepMinimum,
		KeepMaximum: m.cfg.Retention.KeepMaximum,
		KeepDaily:   m.cfg.Retention.KeepDaily,
		KeepWeekly:  m.cfg.Retention.KeepWeekly,
		KeepMonthly: m.cfg.Retention.KeepMonthly,
	}

	for _, t := range m.targets {
		files, err := t.List(ctx)
		if err != nil {
			return err
		}
		items := make([]retention.SnapshotFile, 0, len(files))
		for _, f := range files {
			items = append(items, retention.SnapshotFile{Name: f.Name, Timestamp: f.Timestamp})
		}
		decision := retention.Evaluate(policy, items, time.Now().UTC())

		reasonKeys := make([]string, 0, len(decision.KeepReasons))
		for k := range decision.KeepReasons {
			reasonKeys = append(reasonKeys, k)
		}
		sort.Strings(reasonKeys)
		for _, file := range reasonKeys {
			m.logger.Info("retention keep", "target", t.Name(), "file", file, "reasons", strings.Join(decision.KeepReasons[file], ","))
		}
		for _, del := range decision.Delete {
			if m.dryRun {
				m.logger.Info("dry-run retention delete", "target", t.Name(), "file", del.Name)
				continue
			}
			m.logger.Info("retention delete", "target", t.Name(), "file", del.Name)
			if err := t.Delete(ctx, del.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) uploadAll(ctx context.Context, archiveName string, archive []byte) error {
	if m.dryRun {
		for _, t := range m.targets {
			m.logger.Info("dry-run upload", "target", t.Name(), "file", archiveName, "size", len(archive))
		}
		return nil
	}

	type uploadErr struct {
		target string
		err    error
	}
	ch := make(chan uploadErr, len(m.targets))
	var wg sync.WaitGroup

	for _, t := range m.targets {
		t := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := t.Upload(ctx, archiveName, bytes.NewReader(archive)); err != nil {
				ch <- uploadErr{target: t.Name(), err: err}
				return
			}
			m.logger.Info("upload complete", "target", t.Name(), "file", archiveName)
		}()
	}

	wg.Wait()
	close(ch)
	var errs []string
	for e := range ch {
		errStr := fmt.Sprintf("%s: %v", e.target, e.err)
		errs = append(errs, errStr)
	}
	if len(errs) > 0 {
		return fmt.Errorf("upload failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) targetNames() string {
	names := make([]string, 0, len(m.targets))
	for _, t := range m.targets {
		names = append(names, t.Name())
	}
	return strings.Join(names, ",")
}
