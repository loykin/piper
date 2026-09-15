package orphantrack

import "testing"

func TestTracker_OrphanLifecycle(t *testing.T) {
	tr := New()

	if tr.IsOrphaned("a") {
		t.Fatalf("new tracker should not report any key as orphaned")
	}

	tr.MarkOrphaned("a")

	if !tr.IsOrphaned("a") {
		t.Fatalf("expected key %q to be orphaned after MarkOrphaned", "a")
	}
	if tr.IsOrphaned("b") {
		t.Fatalf("marking one key orphaned should not affect other keys")
	}
}

func TestTracker_StatusChanged(t *testing.T) {
	tr := New()

	if !tr.StatusChanged("a", "starting") {
		t.Fatalf("first call for a key should report changed")
	}
	if tr.StatusChanged("a", "starting") {
		t.Fatalf("repeating the same status should report unchanged")
	}
	if !tr.StatusChanged("a", "running") {
		t.Fatalf("a genuine status change should report changed")
	}
	if tr.StatusChanged("a", "running") {
		t.Fatalf("repeating the new status should report unchanged")
	}

	// Independent keys are tracked separately.
	if !tr.StatusChanged("b", "starting") {
		t.Fatalf("first call for a different key should report changed regardless of other keys' state")
	}
}
