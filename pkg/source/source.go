package source

import (
	"context"
	"fmt"

	"github.com/piper/piper/pkg/pipeline"
)

// Fetcher는 step 실행 전 소스 파일을 로컬에 가져온다
type Fetcher interface {
	// Fetch는 소스를 destDir에 내려받고 실행할 파일의 절대경로를 반환한다
	Fetch(ctx context.Context, run pipeline.Run, destDir string) (filePath string, err error)
}

// New는 run.Source에 맞는 Fetcher를 반환한다
func New(run pipeline.Run, cfg Config) (Fetcher, error) {
	switch run.Source {
	case "git":
		return &GitFetcher{cfg: cfg}, nil
	case "s3":
		return &S3Fetcher{cfg: cfg}, nil
	case "http", "https":
		return &HTTPFetcher{}, nil
	case "local", "":
		return &LocalFetcher{}, nil
	default:
		return nil, fmt.Errorf("unknown source type: %q", run.Source)
	}
}

// Config는 fetcher에 필요한 외부 설정 (인증 등)
type Config struct {
	// Git
	GitToken string // HTTP basic auth token (Gitea, GitHub 등)
	GitUser  string

	// S3
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
}
