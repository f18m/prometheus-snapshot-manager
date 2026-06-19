package target

import (
	"context"
	"io"
	"time"
)

type FileInfo struct {
	Name      string
	Timestamp time.Time
}

type Target interface {
	Name() string
	Upload(ctx context.Context, filename string, content io.Reader) error
	List(ctx context.Context) ([]FileInfo, error)
	Delete(ctx context.Context, filename string) error
}
