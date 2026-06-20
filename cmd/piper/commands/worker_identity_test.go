package commands

import "testing"

func TestLoadOrCreateWorkerIDPersistsPerRole(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateWorkerID(dir, "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateWorkerID(dir, "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || second != first {
		t.Fatalf("worker identity was not reused: first=%q second=%q", first, second)
	}
	notebook, err := loadOrCreateWorkerID(dir, "notebook")
	if err != nil {
		t.Fatal(err)
	}
	if notebook == first {
		t.Fatal("separate worker processes must not share an identity")
	}
}
