package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func initTestRepo(t *testing.T, path string) *git.Repository {
	t.Helper()
	repo, err := git.PlainInit(path, false)
	if err != nil {
		t.Fatalf("plain init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("add seed: %v", err)
	}
	if _, err := wt.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "seed", Email: "seed@local"},
	}); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	return repo
}

func TestFindGitRepoRoot(t *testing.T) {
	root := t.TempDir()
	_ = initTestRepo(t, root)
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	got, err := FindGitRepoRoot(nested)
	if err != nil {
		t.Fatalf("FindGitRepoRoot: %v", err)
	}
	if got != root {
		t.Fatalf("root = %q, want %q", got, root)
	}
}

func TestWriteFilesToGitRepo_CommitsFiles(t *testing.T) {
	root := t.TempDir()
	repo := initTestRepo(t, root)

	res, err := WriteFilesToGitRepo(context.Background(), root, map[string][]byte{
		"promotions/demo/pipeline.yaml": []byte("promotion:\n  name: demo\n"),
		"promotions/demo/manifest.json": []byte("{\"name\":\"demo\"}\n"),
	}, "promotion demo", Config{GitUser: "tester"})
	if err != nil {
		t.Fatalf("WriteFilesToGitRepo: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatal("expected commit SHA")
	}
	if !strings.HasSuffix(res.Branch, "master") && !strings.HasSuffix(res.Branch, "main") {
		t.Fatalf("unexpected branch %q", res.Branch)
	}
	if len(res.Files) != 2 {
		t.Fatalf("files = %#v, want 2 files", res.Files)
	}
	if got, err := os.ReadFile(filepath.Join(root, "promotions/demo/pipeline.yaml")); err != nil || !strings.Contains(string(got), "promotion:") {
		t.Fatalf("pipeline file = %q err=%v", string(got), err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head.Hash().String() != res.CommitSHA {
		t.Fatalf("head = %s, want %s", head.Hash().String(), res.CommitSHA)
	}
	iter, err := repo.Log(&git.LogOptions{})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	defer iter.Close()
	commit, err := iter.Next()
	if err != nil {
		t.Fatalf("next commit: %v", err)
	}
	if commit.Message != "promotion demo" {
		t.Fatalf("commit message = %q, want promotion demo", commit.Message)
	}
}

func TestWriteFilesToGitRepo_PushesOrigin(t *testing.T) {
	root := t.TempDir()
	repo := initTestRepo(t, root)

	remoteDir := t.TempDir()
	if _, err := git.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("plain init bare: %v", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remoteDir}}); err != nil {
		t.Fatalf("create remote: %v", err)
	}

	res, err := WriteFilesToGitRepo(context.Background(), root, map[string][]byte{
		"promotions/demo/pipeline.yaml": []byte("promotion:\n  name: demo\n"),
	}, "promotion demo push", Config{GitUser: "tester"})
	if err != nil {
		t.Fatalf("WriteFilesToGitRepo: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected push to origin")
	}

	remoteRepo, err := git.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	remoteHead, err := remoteRepo.Head()
	if err != nil {
		t.Fatalf("remote head: %v", err)
	}
	if remoteHead.Hash().String() != res.CommitSHA {
		t.Fatalf("remote head = %s, want %s", remoteHead.Hash().String(), res.CommitSHA)
	}
}
