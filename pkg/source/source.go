package source

import (
	"context"
	"fmt"

	"github.com/piper/piper/pkg/pipeline"
)

// Fetcher fetches source files to the local filesystem before a step executes
type Fetcher interface {
	// Fetch downloads the source into destDir and returns the absolute path of the file to execute
	Fetch(ctx context.Context, run pipeline.Run, destDir string) (filePath string, err error)
}

// New returns a Fetcher matching the given run.Source
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

// Config holds external configuration required by fetchers (auth, etc.)
type Config struct {
	// Git
	GitToken string // HTTP basic auth token (Gitea, GitHub, etc.)
	GitUser  string

	// S3
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    bool
}
