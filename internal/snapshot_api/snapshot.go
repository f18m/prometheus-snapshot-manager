package snapshot_api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const archiveTimeLayout = "2006-01-02T150405Z"

// Client is the snapshot-layer API client used by app.Manager to trigger
// Prometheus TSDB snapshots as the first step of each backup cycle.
// See https://prometheus.io/docs/prometheus/latest/querying/api/#snapshot
type Client struct {
	baseURL    string
	httpClient *http.Client
	username   string
	password   string
}

type createSnapshotResponse struct {
	Status string `json:"status"`
	Data   struct {
		Name string `json:"name"`
	} `json:"data"`
}

func NewClient(baseURL string, timeout time.Duration, username, password string, tlsSkipVerify bool) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsSkipVerify}, //nolint:gosec
			},
		},
		username: username,
		password: password,
	}
}

func (c *Client) CreateSnapshot(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/admin/tsdb/snapshot", nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("skip_head", "false")
	req.URL.RawQuery = q.Encode()
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("snapshot API failed: %s", string(b))
	}

	var payload createSnapshotResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Data.Name == "" {
		return "", fmt.Errorf("snapshot response missing data.name")
	}
	return payload.Data.Name, nil
}

func (c *Client) WaitForSnapshotDirReady(ctx context.Context, root, name string, pollInterval time.Duration) (string, error) {
	path := filepath.Join(root, name)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if st, err := os.Stat(path); err == nil && st.IsDir() {
			return path, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for snapshot dir: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// DiskSnapshotter handles filesystem-level snapshot operations after Prometheus
// has created a snapshot directory on disk.
type DiskSnapshotter struct {
	snapshotDir string
}

func NewDiskSnapshotter(snapshotDir string) *DiskSnapshotter {
	return &DiskSnapshotter{snapshotDir: snapshotDir}
}

// BuildInMemoryArchive creates a gzip-compressed tar archive fully in memory (bytes.Buffer),
// walking snapshotDir recursively and adding all non-directory entries with relative paths.
// It returns an error for invalid gzip settings, filesystem/stat/open/read/copy failures,
// and tar/gzip close/write failures, while ensuring writers are closed on error paths.
func (d *DiskSnapshotter) BuildInMemoryArchive(gzipLevel int) ([]byte, error) {
	var buf bytes.Buffer
	if err := d.writeArchive(&buf, gzipLevel); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// BuildArchiveToFile writes a gzip-compressed tar archive to archivePath on disk
// and returns its final size in bytes.
func (d *DiskSnapshotter) BuildArchiveToFile(archivePath string, gzipLevel int) (int64, error) {
	cleanArchivePath := filepath.Clean(archivePath)
	if err := d.validateArchiveOutputPath(cleanArchivePath); err != nil {
		return 0, err
	}

	if err := os.MkdirAll(filepath.Dir(cleanArchivePath), 0o755); err != nil {
		return 0, err
	}

	f, err := os.Create(cleanArchivePath)
	if err != nil {
		return 0, err
	}

	if err := d.writeArchive(f, gzipLevel); err != nil {
		_ = f.Close()
		_ = os.Remove(cleanArchivePath)
		return 0, err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(cleanArchivePath)
		return 0, err
	}

	st, err := os.Stat(cleanArchivePath)
	if err != nil {
		return 0, err
	}

	return st.Size(), nil
}

func (d *DiskSnapshotter) validateArchiveOutputPath(archivePath string) error {
	snapshotDirAbs, err := filepath.Abs(d.snapshotDir)
	if err != nil {
		return err
	}

	archivePathAbs, err := filepath.Abs(archivePath)
	if err != nil {
		return err
	}

	rel, err := filepath.Rel(snapshotDirAbs, archivePathAbs)
	if err != nil {
		return err
	}

	if rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		return fmt.Errorf("archive output path %q must be outside snapshot directory %q", archivePathAbs, snapshotDirAbs)
	}

	return nil
}

func (d *DiskSnapshotter) writeArchive(dst io.Writer, gzipLevel int) error {
	gz, err := gzip.NewWriterLevel(dst, gzipLevel)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)

	err = filepath.WalkDir(d.snapshotDir, func(path string, dir fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if dir.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(d.snapshotDir, path)
		if err != nil {
			return err
		}
		info, err := dir.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = rel
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		f, err := os.Open(filepath.Clean(path))
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}

	return nil
}

func (d *DiskSnapshotter) ArchiveFilename(now time.Time) (string, error) {
	raw := make([]byte, 3)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	fn := fmt.Sprintf("prom-snapshot_%s_%s.tar.gz",
		now.UTC().Format(archiveTimeLayout),
		hex.EncodeToString(raw))

	return fn, nil
}
