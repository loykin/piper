package source

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/piper/piper/pkg/pipeline"
)

type GitFetcher struct {
	cfg Config
}

func (f *GitFetcher) Fetch(ctx context.Context, run pipeline.Run, destDir string) (string, error) {
	if run.Repo == "" {
		return "", fmt.Errorf("git source: repo is required")
	}
	if run.Path == "" {
		return "", fmt.Errorf("git source: path is required")
	}

	branch := run.Branch
	if branch == "" {
		branch = "main"
	}

	slog.Info("git clone", "repo", run.Repo, "branch", branch, "dest", destDir)

	cloneOpts := &git.CloneOptions{
		URL:           run.Repo,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1, // shallow clone — 속도 최적화
	}

	if f.cfg.GitToken != "" {
		cloneOpts.Auth = &githttp.BasicAuth{
			Username: f.cfg.GitUser,
			Password: f.cfg.GitToken,
		}
	}

	_, err := git.PlainCloneContext(ctx, destDir, false, cloneOpts)
	if err != nil && err != git.ErrRepositoryAlreadyExists {
		return "", fmt.Errorf("git clone failed: %w", err)
	}

	filePath := filepath.Join(destDir, run.Path)
	slog.Info("git fetch done", "file", filePath)
	return filePath, nil
}
