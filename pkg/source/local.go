package source

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/piper/piper/pkg/pipeline"
)

// LocalFetcher is used when source is "local" or empty — assumes the file is in workDir
type LocalFetcher struct{}

func (f *LocalFetcher) Fetch(_ context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Path == "" {
		return "", fmt.Errorf("local source: path is required")
	}
	// destDir is the workDir, so join path directly and return
	return filepath.Join(destDir, run.Path), nil
}
