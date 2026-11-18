package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/notify"
	"github.com/f18m/prometheus-snapshot-manager/internal/target"
)

const testSnapshotName = "20260101T000000Z-abcdef"

func newTestManager(t *testing.T, cfg *config.Config, targets []target.Target, dryRun bool) *Manager {
	t.Helper()
	return &Manager{
		cfg:      cfg,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		dryRun:   dryRun,
		targets:  targets,
		notifier: notify.New(config.AppriseConfig{}),
	}
}

func newTestConfig(t *testing.T, cleanupSnapshots bool) *config.Config {
	t.Helper()
	noWait := time.Duration(0)
	cleanup := cleanupSnapshots
	return &config.Config{
		Prometheus: config.PrometheusConfig{
			URL:                    "http://unused",
			Timeout:                5 * time.Second,
			StartupWait:            &noWait,
			SnapshotDir:            filepath.Join(t.TempDir(), "snapshots"),
			SnapshotArchiveTempDir: filepath.Join(t.TempDir(), "temp"),
		},
		Compression: config.CompressionConfig{Level: 6},
		Retention: config.RetentionConfig{
			KeepMinimum:                1,
			CleanupPrometheusSnapshots: &cleanup,
		},
	}
}

// fakePrometheus answers the snapshot API with testSnapshotName, the same way Prometheus does.
func fakePrometheus(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/tsdb/snapshot" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"name": testSnapshotName}})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestRunCycleSnapshotDirCleanup(t *testing.T) {
	tests := []struct {
		name          string
		cleanup       bool
		uploadFails   bool
		wantRemaining bool
	}{
		{name: "removed on success", cleanup: true, uploadFails: false, wantRemaining: false},
		{name: "removed on upload failure", cleanup: true, uploadFails: true, wantRemaining: false},
		{name: "kept when cleanup disabled", cleanup: false, uploadFails: false, wantRemaining: true},
		{name: "kept on failure when cleanup disabled", cleanup: false, uploadFails: true, wantRemaining: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := fakePrometheus(t)
			cfg := newTestConfig(t, tt.cleanup)
			cfg.Prometheus.URL = ts.URL

			snapshotPath := filepath.Join(cfg.Prometheus.SnapshotDir, testSnapshotName)
			if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(snapshotPath, "chunk"), []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}

			// a regular file where the target directory should be makes the upload fail
			targetPath := filepath.Join(t.TempDir(), "backup")
			if tt.uploadFails {
				if err := os.WriteFile(targetPath, []byte("not a dir"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			targets := []target.Target{target.NewLocalTarget("local", targetPath)}
			m := newTestManager(t, cfg, targets, false)

			err := m.RunCycle(t.Context())
			if (err != nil) != tt.uploadFails {
				t.Fatalf("RunCycle error = %v, want failure: %v", err, tt.uploadFails)
			}

			_, statErr := os.Stat(snapshotPath)
			remains := statErr == nil
			if remains != tt.wantRemaining {
				t.Fatalf("snapshot dir remains = %v, want %v", remains, tt.wantRemaining)
			}

			tempEntries, err := os.ReadDir(cfg.Prometheus.SnapshotArchiveTempDir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(tempEntries) != 0 {
				t.Fatalf("archive temp dir must not keep archives, found %d entries", len(tempEntries))
			}
		})
	}
}

func TestCleanupArchiveTempDir(t *testing.T) {
	cfg := newTestConfig(t, true)
	tempDir := cfg.Prometheus.SnapshotArchiveTempDir
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleArchive := "20260924T215949Z-00ff00ff00ff00ff-prom-snapshot_2026-09-24T215949Z_a1b2c3.tar.gz"
	keptFiles := []string{"notes.txt", "prom-snapshot_2026-09-24T215949Z_a1b2c3.tar.gz"}
	for _, name := range append([]string{staleArchive}, keptFiles...) {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// a directory with a matching name is not something this application writes: it must be kept
	keptDir := filepath.Join(tempDir, staleArchive+".d")
	if err := os.MkdirAll(keptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// a symlink with a matching name is not something this application writes: it must be kept, never followed
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(tempDir, "20260101T000000Z-aa-prom-snapshot_2026-01-01T000000Z_000000.tar.gz")); err != nil {
		t.Fatal(err)
	}

	dryRun := newTestManager(t, cfg, nil, true)
	if err := dryRun.CleanupArchiveTempDir(); err != nil {
		t.Fatalf("dry-run cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, staleArchive)); err != nil {
		t.Fatalf("dry-run must not remove anything: %v", err)
	}

	m := newTestManager(t, cfg, nil, false)
	if err := m.CleanupArchiveTempDir(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(tempDir, staleArchive)); !os.IsNotExist(err) {
		t.Fatalf("stale archive must be removed, stat error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tempDir, "20260101T000000Z-aa-prom-snapshot_2026-01-01T000000Z_000000.tar.gz")); err != nil {
		t.Fatalf("symlink must be kept: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target must not be touched: %v", err)
	}
	if _, err := os.Stat(keptDir); err != nil {
		t.Fatalf("directory must be kept: %v", err)
	}
	for _, name := range keptFiles {
		if _, err := os.Stat(filepath.Join(tempDir, name)); err != nil {
			t.Fatalf("unrelated file %s must be kept: %v", name, err)
		}
	}
}

func TestCleanupArchiveTempDirMissingDir(t *testing.T) {
	cfg := newTestConfig(t, true)
	m := newTestManager(t, cfg, nil, false)
	if err := m.CleanupArchiveTempDir(); err != nil {
		t.Fatalf("missing temp dir must not be an error: %v", err)
	}
}
