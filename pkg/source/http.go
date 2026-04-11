package source

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/piper/piper/pkg/pipeline"
)

type HTTPFetcher struct{}

func (f *HTTPFetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	rawURL := run.URL
	if rawURL == "" {
		rawURL = run.Path
	}
	if rawURL == "" {
		return "", fmt.Errorf("http source: url or path is required")
	}

	// Extract filename from the last segment of the URL
	name := filepath.Base(strings.Split(rawURL, "?")[0])
	if name == "" || name == "." || name == "/" {
		name = "file"
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	destFile := filepath.Join(destDir, name)

	slog.Info("http download", "url", rawURL, "dest", destFile)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("http source: build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http download failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http download: status %d for %s", resp.StatusCode, rawURL)
	}

	out, err := os.Create(destFile)
	if err != nil {
		return "", err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("http write failed: %w", err)
	}

	slog.Info("http fetch done", "file", destFile)
	return destFile, nil
}
