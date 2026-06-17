package target

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/f18m/prometheus-snapshot-manager/internal/retention"
)

type LocalTarget struct {
	name string
	path string
}

func NewLocalTarget(name, path string) *LocalTarget {
	return &LocalTarget{name: name, path: path}
}

func (t *LocalTarget) Name() string { return t.name }

func (t *LocalTarget) Upload(_ context.Context, filename string, content io.Reader) error {
	if err := os.MkdirAll(t.path, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(t.path, filename))
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, content)
	return err
}

func (t *LocalTarget) List(_ context.Context) ([]FileInfo, error) {
	entries, err := os.ReadDir(t.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
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
	return out, nil
}

func (t *LocalTarget) Delete(_ context.Context, filename string) error {
	p := filepath.Join(t.path, filename)
	if !filepath.IsAbs(t.path) {
		return fmt.Errorf("local target path must be absolute")
	}
	return os.Remove(p)
}
