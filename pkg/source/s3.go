package source

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/piper/piper/pkg/pipeline"
)

type S3Fetcher struct {
	cfg Config
}

func (f *S3Fetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Path == "" {
		return "", fmt.Errorf("s3 source: path is required")
	}

	endpoint := f.cfg.S3Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(f.cfg.S3AccessKey, f.cfg.S3SecretKey, ""),
		Secure: f.cfg.S3UseSSL,
	})
	if err != nil {
		return "", fmt.Errorf("s3 client init failed: %w", err)
	}

	bucket := f.cfg.S3Bucket
	key := run.Path
	destFile := filepath.Join(destDir, filepath.Base(key))

	slog.Info("s3 download", "bucket", bucket, "key", key, "dest", destFile)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	if err := client.FGetObject(ctx, bucket, key, destFile, minio.GetObjectOptions{}); err != nil {
		return "", fmt.Errorf("s3 download failed: %w", err)
	}

	slog.Info("s3 fetch done", "file", destFile)
	return destFile, nil
}
