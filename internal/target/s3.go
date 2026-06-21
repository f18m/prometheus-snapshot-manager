package target

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/f18m/prometheus-snapshot-manager/internal/config"
	"github.com/f18m/prometheus-snapshot-manager/internal/retention"
)

type S3Target struct {
	name   string
	cfg    config.S3Config
	client *s3.Client
}

func NewS3Target(ctx context.Context, name string, cfg config.S3Config) (*S3Target, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.AccessKeyID != "" || cfg.SecretAccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = &cfg.Endpoint
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return &S3Target{name: name, cfg: cfg, client: client}, nil
}

func (t *S3Target) Name() string { return t.name }

func (t *S3Target) Upload(ctx context.Context, logger *slog.Logger, filename string, content io.Reader) error {
	uploader := manager.NewUploader(t.client)
	_, err := uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:       &t.cfg.Bucket,
		Key:          stringPtr(t.key(filename)),
		Body:         content,
		StorageClass: types.StorageClass(t.cfg.StorageClass),
	})
	if err != nil {
		return err
	}
	logger.Info("upload complete", "target", t.Name(), "file", t.key(filename))
	return nil
}

func (t *S3Target) List(ctx context.Context) ([]FileInfo, error) {
	in := &s3.ListObjectsV2Input{Bucket: &t.cfg.Bucket, Prefix: stringPtr(t.cfg.Prefix)}
	out := []FileInfo{}
	pager := s3.NewListObjectsV2Paginator(t.client, in)
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			name := strings.TrimPrefix(*obj.Key, t.cfg.Prefix)
			ts, err := retention.ParseArchiveTimestamp(name)
			if err != nil {
				continue
			}
			out = append(out, FileInfo{Name: name, Timestamp: ts})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	return out, nil
}

func (t *S3Target) Delete(ctx context.Context, filename string) error {
	_, err := t.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &t.cfg.Bucket, Key: stringPtr(t.key(filename))})
	return err
}

func (t *S3Target) key(filename string) string {
	if t.cfg.Prefix == "" {
		return filename
	}
	if strings.HasSuffix(t.cfg.Prefix, "/") {
		return t.cfg.Prefix + filename
	}
	return t.cfg.Prefix + "/" + filename
}

func stringPtr(s string) *string { return &s }
