package driver

import (
	"testing"

	"github.com/loykin/piper/internal/proto"
)

func TestRunDispatchStoreSaveLoadDelete(t *testing.T) {
	store, err := NewRunDispatchStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dispatch := proto.RunDispatch{
		ProjectID:    "proj-1",
		RunID:        "run-1",
		PipelineYAML: "apiVersion: piper/v1\nkind: Pipeline\n",
		Env:          map[string][]string{"seed": {"SECRET=abc"}},
	}
	if err := store.Save(dispatch); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.Load("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Load ok = false after Save")
	}
	if got.RunID != dispatch.RunID || got.PipelineYAML != dispatch.PipelineYAML {
		t.Fatalf("Load = %#v, want %#v", got, dispatch)
	}
	if got.Env["seed"][0] != "SECRET=abc" {
		t.Fatalf("Env not round-tripped: %#v", got.Env)
	}

	if err := store.Delete("run-1"); err != nil {
		t.Fatal(err)
	}
	_, ok, err = store.Load("run-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("Load ok = true after Delete")
	}

	// Deleting an already-absent entry must not error (idempotent, matches
	// ResultOutbox.Ack's same os.IsNotExist tolerance).
	if err := store.Delete("run-1"); err != nil {
		t.Fatalf("Delete on already-deleted entry: %v", err)
	}
}

func TestRunDispatchStoreLoadMissingReturnsNotOK(t *testing.T) {
	store, err := NewRunDispatchStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := store.Load("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("Load ok = true for a RunID never saved")
	}
}

func TestRunDispatchStoreLoadAllAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := NewRunDispatchStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Save(proto.RunDispatch{RunID: "run-a", ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}
	if err := first.Save(proto.RunDispatch{RunID: "run-b", ProjectID: "proj-1"}); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewRunDispatchStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	all, err := restarted.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("LoadAll returned %d entries, want 2: %#v", len(all), all)
	}
	if _, ok := all["run-a"]; !ok {
		t.Error("run-a missing from LoadAll")
	}
	if _, ok := all["run-b"]; !ok {
		t.Error("run-b missing from LoadAll")
	}
}

func TestRunDispatchStoreSaveRequiresRunID(t *testing.T) {
	store, err := NewRunDispatchStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(proto.RunDispatch{ProjectID: "proj-1"}); err == nil {
		t.Fatal("Save with empty RunID should error")
	}
}
