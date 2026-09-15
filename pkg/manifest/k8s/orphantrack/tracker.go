// Package orphantrack deduplicates repeated "workload observed" status
// reports and remembers workloads whose DB row is gone (an orphan, until
// process restart), shared by the notebook and serving k8s drivers' Observe
// loops.
package orphantrack

import "sync"

// Tracker holds per-workload-key orphan and last-reported-status state,
// guarded by its own mutex. The zero value is not usable — construct with
// New.
type Tracker struct {
	mu         sync.Mutex
	orphaned   map[string]bool
	lastStatus map[string]string
}

// New returns an empty Tracker.
func New() *Tracker {
	return &Tracker{orphaned: map[string]bool{}, lastStatus: map[string]string{}}
}

// IsOrphaned reports whether key was previously marked orphaned.
func (t *Tracker) IsOrphaned(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.orphaned[key]
}

// MarkOrphaned marks key as orphaned until process restart.
func (t *Tracker) MarkOrphaned(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.orphaned[key] = true
}

// StatusChanged reports whether status differs from the last status recorded
// for key, and records status as the new last-known value either way. A key
// with no prior recorded status has an implicit last status of "" — so the
// first call for a key returns true for any non-empty status (matching both
// original driver implementations, which only ever call this after already
// excluding status == "").
func (t *Tracker) StatusChanged(key, status string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastStatus[key] == status {
		return false
	}
	t.lastStatus[key] = status
	return true
}
