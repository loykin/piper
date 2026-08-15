package k8s

import "testing"

func TestRuntimeSelectorUsesSameEncodingAsLabels(t *testing.T) {
	runtimeID := " Runtime/One. "
	labels := map[string]string{LabelRuntimeID: LabelValue(runtimeID)}
	want := ManagedSelector() + "," + LabelRuntimeID + "=" + labels[LabelRuntimeID]
	if got := RuntimeSelector(runtimeID); got != want {
		t.Fatalf("RuntimeSelector() = %q, want %q", got, want)
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
