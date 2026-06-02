package source

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/piper/piper/pkg/blobstore"
	"github.com/piper/piper/pkg/pipeline"
)

type S3Fetcher struct {
	cfg Config
}

func (f *S3Fetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Path == "" {
		return "", fmt.Errorf("s3 source: path is required")
	}
	if f.cfg.StorageURL == "" {
		return "", fmt.Errorf("s3 source: storage URL not configured (set storage.url or source.s3.*)")
	}

	st, err := blobstore.Open(f.cfg.StorageURL, "")
	if err != nil {
		return "", fmt.Errorf("s3 source: open store: %w", err)
	}

	key := run.Path
	slog.Info("s3 source fetch", "key", key, "dest", destDir)

	rc, err := st.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("s3 source: get %s: %w", key, err)
	}
	defer func() { _ = rc.Close() }()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, filepath.Base(key))
	out, err := os.Create(destFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, rc); err != nil {
		return "", fmt.Errorf("s3 source: write %s: %w", key, err)
	}
	slog.Info("s3 source fetch done", "file", destFile)
	return destFile, nil
}
