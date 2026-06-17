package snapshot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestBuildArchive(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "chunk"), []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := BuildArchive(d, 6)
	if err != nil {
		t.Fatalf("build archive: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("archive is empty")
	}
}
