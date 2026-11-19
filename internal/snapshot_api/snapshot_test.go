package snapshot_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateSnapshot(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"name": "snap-1"}})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 2*time.Second, "", "", false)
	name, err := c.CreateSnapshot(context.Background())
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if name != "snap-1" {
		t.Fatalf("got %s", name)
	}
}

func TestRetryUntilPrometheusReady(t *testing.T) {
	var readyCalls, snapCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/-/ready":
			readyCalls++
			if readyCalls < 2 {
				http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
			}
		case "/api/v1/admin/tsdb/snapshot":
			snapCalls++
			if snapCalls < 2 {
				http.Error(w, "starting", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]string{"name": "snap-1"}})
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 2*time.Second, "", "", false)
	if err := c.WaitUntilReady(context.Background(), 30*time.Second, nil); err != nil {
		t.Fatalf("wait ready: %v", err)
	}
	name, err := c.CreateSnapshotWithRetry(context.Background(), 30*time.Second, nil)
	if err != nil || name != "snap-1" {
		t.Fatalf("got %q, %v", name, err)
	}
	if readyCalls != 2 || snapCalls != 2 {
		t.Fatalf("unexpected calls: ready=%d snapshot=%d", readyCalls, snapCalls)
	}
}

func TestCreateSnapshotErrorIncludesStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "admin API disabled", http.StatusMethodNotAllowed)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 2*time.Second, "", "", false)
	_, err := c.CreateSnapshotWithRetry(context.Background(), 30*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "405 Method Not Allowed: admin API disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitUntilReadyGivesUp(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 2*time.Second, "", "", false)
	err := c.WaitUntilReady(context.Background(), 1500*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildArchive(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "chunk"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	ds := NewDiskSnapshotter(d)
	b, err := ds.BuildInMemoryArchive(6)
	if err != nil {
		t.Fatalf("build archive: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("archive is empty")
	}

	archiveOutDir := t.TempDir()
	archivePath := filepath.Join(archiveOutDir, "snapshot.tar.gz")
	size, err := ds.BuildArchiveToFile(archivePath, 6)
	if err != nil {
		t.Fatalf("build archive to file: %v", err)
	}
	if size == 0 {
		t.Fatal("archive file size is zero")
	}
	st, err := os.Stat(archivePath)
	if err != nil {
		t.Fatalf("stat archive file: %v", err)
	}
	if st.Size() != size {
		t.Fatalf("archive file size mismatch: got %d want %d", st.Size(), size)
	}
}

func TestIsTempArchiveName(t *testing.T) {
	d := &DiskSnapshotter{}
	generated, err := d.ArchiveFilename(time.Date(2026, 6, 21, 22, 55, 21, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		want bool
	}{
		{"20260621T225521Z-0123456789abcdef-" + generated, true},
		{"20260924T215949Z-00ff00ff00ff00ff-prom-snapshot_2026-09-24T215949Z_a1b2c3.tar.gz", true},
		{generated, false}, // final archive name in the targets, no snapshot prefix
		{"20260621T225521Z-0123456789abcdef-" + generated + ".part", false},
		{"20260621T225521Z-0123456789ABCDEF-" + generated, false}, // hex is lowercase
		{"20260621T225521Z-0123456789abcdef-prom-snapshot_2026-06-21T225521Z_A1B2C3.tar.gz", false},
		{"notes.txt", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsTempArchiveName(tt.name); got != tt.want {
			t.Errorf("IsTempArchiveName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
