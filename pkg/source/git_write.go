package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitWriteResult summarizes a repo write-back.
type GitWriteResult struct {
	RepoRoot  string
	Branch    string
	CommitSHA string
	Pushed    bool
	Files     []string
}

// FindGitRepoRoot walks upward from start until it finds a .git directory or file.
func FindGitRepoRoot(start string) (string, error) {
	if strings.TrimSpace(start) == "" {
		return "", fmt.Errorf("git repository root is empty")
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve git repository root: %w", err)
	}
	for {
		gitPath := filepath.Join(abs, ".git")
		if fi, err := os.Stat(gitPath); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return "", fmt.Errorf("no git repository found at or above %q", start)
}

// WriteFilesToGitRepo writes relPath -> content into the git repo that contains
// start, commits the changes, and pushes the current branch when origin exists.
func WriteFilesToGitRepo(ctx context.Context, start string, files map[string][]byte, message string, cfg Config) (*GitWriteResult, error) {
	repoRoot, err := FindGitRepoRoot(start)
	if err != nil {
		return nil, err
	}

	repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open git repo: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git worktree: %w", err)
	}

	written := make([]string, 0, len(files))
	for relPath, content := range files {
		relPath = filepath.Clean(relPath)
		if relPath == "." || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "..") {
			return nil, fmt.Errorf("invalid repo path %q", relPath)
		}
		fullPath := filepath.Join(repoRoot, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return nil, fmt.Errorf("create repo dir %q: %w", filepath.Dir(relPath), err)
		}
		if err := os.WriteFile(fullPath, content, 0644); err != nil {
			return nil, fmt.Errorf("write repo file %q: %w", relPath, err)
		}
		if _, err := worktree.Add(relPath); err != nil {
			return nil, fmt.Errorf("git add %q: %w", relPath, err)
		}
		written = append(written, relPath)
	}

	author := &object.Signature{
		Name:  gitAuthorName(cfg),
		Email: gitAuthorEmail(cfg),
		When:  time.Now().UTC(),
	}
	commitHash, err := worktree.Commit(message, &git.CommitOptions{
		Author:    author,
		Committer: author,
	})
	if err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}

	res := &GitWriteResult{
		RepoRoot:  repoRoot,
		CommitSHA: commitHash.String(),
		Files:     written,
	}
	if head, headErr := repo.Head(); headErr == nil {
		res.Branch = head.Name().Short()
	} else {
		res.Branch = "main"
	}

	if remote, remoteErr := repo.Remote("origin"); remoteErr == nil && remote != nil {
		pushOpts := &git.PushOptions{
			RemoteName: "origin",
			RefSpecs: []config.RefSpec{
				config.RefSpec(fmt.Sprintf("%s:%s", plumbing.NewBranchReferenceName(res.Branch).String(), plumbing.NewBranchReferenceName(res.Branch).String())),
			},
		}
		if remoteURL := remoteURL(remote); strings.HasPrefix(remoteURL, "http://") || strings.HasPrefix(remoteURL, "https://") {
			if cfg.GitToken != "" || cfg.GitUser != "" {
				pushOpts.Auth = &githttp.BasicAuth{
					Username: cfg.GitUser,
					Password: cfg.GitToken,
				}
			}
		}
		if err := repo.PushContext(ctx, pushOpts); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return nil, fmt.Errorf("git push: %w", err)
		}
		res.Pushed = true
	}

	return res, nil
}

func gitAuthorName(cfg Config) string {
	if strings.TrimSpace(cfg.GitUser) != "" {
		return cfg.GitUser
	}
	return "piper"
}

func gitAuthorEmail(cfg Config) string {
	if strings.TrimSpace(cfg.GitUser) != "" {
		return cfg.GitUser + "@local"
	}
	return "piper@local"
}

func remoteURL(remote *git.Remote) string {
	if remote == nil {
		return ""
	}
	if rc := remote.Config(); rc != nil && len(rc.URLs) > 0 {
		return rc.URLs[0]
	}
	return ""
}
