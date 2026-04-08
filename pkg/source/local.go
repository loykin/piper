package source

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/piper/piper/pkg/pipeline"
)

// LocalFetcher는 source가 local이거나 비어있을 때 — 파일이 workDir에 있다고 가정
type LocalFetcher struct{}

func (f *LocalFetcher) Fetch(_ context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Path == "" {
		return "", fmt.Errorf("local source: path is required")
	}
	// destDir은 workDir이 넘어오므로 path를 그대로 합쳐서 반환
	return filepath.Join(destDir, run.Path), nil
}
