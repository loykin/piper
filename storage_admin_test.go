package piper

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/project"
)

func TestProjectStorageKey(t *testing.T) {
	ctx := project.WithContext(context.Background(), project.Context{ID: "project-a"})

	got, err := projectStorageKey(ctx, "models/model.bin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "projects/project-a/uploads/models/model.bin" {
		t.Fatalf("key = %q", got)
	}
}

func TestProjectStorageKeyRejectsTraversal(t *testing.T) {
	ctx := project.WithContext(context.Background(), project.Context{ID: "project-a"})

	if _, err := projectStorageKey(ctx, "../project-b/secret"); err == nil {
		t.Fatal("expected traversal key to be rejected")
	}
}

func TestProjectStorageKeyRequiresProject(t *testing.T) {
	if _, err := projectStorageKey(context.Background(), "model.bin"); err == nil {
		t.Fatal("expected missing project context to be rejected")
	}
}
