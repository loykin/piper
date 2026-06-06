package source

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/piper/piper/pkg/blobstore"
	"github.com/piper/piper/pkg/pipeline"
)

type S3Fetcher struct {
	cfg Config
}

func (f *S3Fetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	if f.cfg.StorageURL == "" {
		return "", fmt.Errorf("s3 source: storage URL not configured (set storage.url or source.s3.*)")
	}

	// When a snapshot prefix is set, download the entire snapshot directory.
	// This preserves package structure (models/, utils/, etc.) uploaded alongside the entry point.
	if run.SnapshotPrefix != "" {
		return f.fetchSnapshot(ctx, run, destDir)
	}

	// Legacy single-file fetch (no deps).
	if run.Path == "" {
		return "", fmt.Errorf("s3 source: path is required")
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

// fetchSnapshot downloads the entire snapshot prefix into destDir, preserving directory structure,
// then returns the absolute path of the entry point file within destDir.
func (f *S3Fetcher) fetchSnapshot(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	entry := run.Path
	if run.Notebook != "" {
		entry = run.Notebook
	}
	if entry == "" {
		return "", fmt.Errorf("s3 source: path or notebook is required")
	}

	st, err := blobstore.Open(f.cfg.StorageURL, "")
	if err != nil {
		return "", fmt.Errorf("s3 source: open store: %w", err)
	}

	prefix := run.SnapshotPrefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	slog.Info("s3 source fetch snapshot", "prefix", prefix, "entry", entry, "dest", destDir)

	if err := blobstore.DownloadDir(ctx, st, prefix, destDir); err != nil {
		return "", fmt.Errorf("s3 source: download snapshot %s: %w", prefix, err)
	}

	entryPath := filepath.Join(destDir, filepath.FromSlash(entry))
	slog.Info("s3 source snapshot ready", "entry", entryPath)
	return entryPath, nil
}
