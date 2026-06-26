package k8s

import "testing"

func TestWorkerSelectorUsesSameEncodingAsLabels(t *testing.T) {
	workerID := " Worker/One. "
	labels := map[string]string{LabelWorkerID: LabelValue(workerID)}
	want := ManagedSelector() + "," + LabelWorkerID + "=" + labels[LabelWorkerID]
	if got := WorkerSelector(workerID); got != want {
		t.Fatalf("WorkerSelector() = %q, want %q", got, want)
	}
}

func TestLabelValueProducesValidBoundaries(t *testing.T) {
	got := LabelValue("..worker/id.." + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	if len(got) > 63 {
		t.Fatalf("label value length = %d", len(got))
	}
	if got == "" || got[0] == '-' || got[0] == '_' || got[0] == '.' {
		t.Fatalf("invalid label start: %q", got)
	}
	last := got[len(got)-1]
	if last == '-' || last == '_' || last == '.' {
		t.Fatalf("invalid label end: %q", got)
	}
}
