package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

// ─── New factory ─────────────────────────────────────────────────────────────

func TestNew_local(t *testing.T) {
	run := pipeline.Run{Source: "local"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*LocalFetcher); !ok {
		t.Errorf("want *LocalFetcher, got %T", f)
	}
}

func TestNew_emptySource(t *testing.T) {
	// 빈 source도 local로 처리
	run := pipeline.Run{Source: ""}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*LocalFetcher); !ok {
		t.Errorf("want *LocalFetcher, got %T", f)
	}
}

func TestNew_git(t *testing.T) {
	run := pipeline.Run{Source: "git"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*GitFetcher); !ok {
		t.Errorf("want *GitFetcher, got %T", f)
	}
}

func TestNew_s3(t *testing.T) {
	run := pipeline.Run{Source: "s3"}
	f, err := New(run, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.(*S3Fetcher); !ok {
		t.Errorf("want *S3Fetcher, got %T", f)
	}
}

func TestNew_unknown(t *testing.T) {
	run := pipeline.Run{Source: "ftp"}
	_, err := New(run, Config{})
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

// ─── LocalFetcher ─────────────────────────────────────────────────────────────

func TestLocalFetcher_withPath(t *testing.T) {
	f := &LocalFetcher{}
	destDir := t.TempDir()
	run := pipeline.Run{Path: "train.py"}

	got, err := f.Fetch(context.Background(), run, destDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(destDir, "train.py")
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestLocalFetcher_emptyPath(t *testing.T) {
	f := &LocalFetcher{}
	_, err := f.Fetch(context.Background(), pipeline.Run{Path: ""}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestLocalFetcher_nestedPath(t *testing.T) {
	f := &LocalFetcher{}
	got, err := f.Fetch(context.Background(), pipeline.Run{Path: "src/model/train.py"}, "/workdir")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workdir/src/model/train.py" {
		t.Errorf("unexpected path: %q", got)
	}
}
