package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveSecretEnv(t *testing.T) {
	t.Setenv("CFG_SECRET", "value")
	got, err := resolveSecret("${CFG_SECRET}")
	if err != nil {
		t.Fatalf("resolveSecret error: %v", err)
	}
	if got != "value" {
		t.Fatalf("got %q want value", got)
	}
}

func TestResolveSecretFile(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "secret.txt")
	if err := os.WriteFile(p, []byte("  abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSecret("file:" + p)
	if err != nil {
		t.Fatalf("resolveSecret error: %v", err)
	}
	if got != "abc" {
		t.Fatalf("got %q want abc", got)
	}
}

func TestLoadAndValidate(t *testing.T) {
	d := t.TempDir()
	cfgPath := filepath.Join(d, "config.yaml")
	content := `
prometheus:
  url: "http://localhost:9090"
  timeout: "10s"
schedule:
  interval: "1h"
  timezone: "UTC"
compression:
  format: "tar.gz"
  level: 6
retention:
  keep_within: "24h"
  keep_minimum: 1
targets:
  - name: "local"
    type: "local"
    local:
      path: "/tmp/backups"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	expectedTZ := time.UTC
	if cfg.Schedule.Timezone != expectedTZ {
		t.Fatalf("unexpected timezone: got %v, want %v", cfg.Schedule.Timezone, expectedTZ)
	}
	if cfg.Compression.Level != 6 {
		t.Fatalf("unexpected compression level: %d", cfg.Compression.Level)
	}
	if cfg.Prometheus.SnapshotDir != "/prometheus/snapshots" {
		t.Fatalf("unexpected snapshot dir: %s", cfg.Prometheus.SnapshotDir)
	}
	if cfg.Prometheus.SnapshotArchiveTempDir != "/prometheus/snapshots-mgr-temp" {
		t.Fatalf("unexpected snapshot archive dir: %s", cfg.Prometheus.SnapshotArchiveTempDir)
	}
}
