package commands

import "testing"

func TestStableWorkerIDNormalizesParts(t *testing.T) {
	got := stableWorkerID("notebook", "Mac Mini.local", "process")
	want := "notebook-mac-mini.local-process"
	if got != want {
		t.Fatalf("stableWorkerID = %q, want %q", got, want)
	}
}

func TestStableWorkerIDSkipsEmptyParts(t *testing.T) {
	got := stableWorkerID("k8s", " prod cluster ")
	want := "k8s-prod-cluster"
	if got != want {
		t.Fatalf("stableWorkerID = %q, want %q", got, want)
	}
}

func TestEffectiveNotebookModeDefaultsToProcess(t *testing.T) {
	if got := effectiveNotebookMode(""); got != "process" {
		t.Fatalf("effectiveNotebookMode = %q", got)
	}
}
