package mcp

import (
	"testing"
	"time"
)

func TestSessionStoreCreateGet(t *testing.T) {
	store := NewSessionStore(time.Minute)
	sess, err := store.Create("user-1", "proj-1", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID == "" {
		t.Fatal("expected a non-empty session id")
	}
	got, ok := store.Get(sess.ID)
	if !ok {
		t.Fatal("expected the session to be found")
	}
	if got.IdentityID != "user-1" || got.ProjectID != "proj-1" || got.ClientID != "client-1" {
		t.Errorf("unexpected session contents: %+v", got)
	}
}

func TestSessionStoreGetMissing(t *testing.T) {
	store := NewSessionStore(time.Minute)
	if _, ok := store.Get("does-not-exist"); ok {
		t.Fatal("expected not found")
	}
}

func TestSessionStoreExpiry(t *testing.T) {
	store := NewSessionStore(time.Minute)
	now := time.Now()
	store.now = func() time.Time { return now }

	sess, err := store.Create("user-1", "proj-1", "client-1")
	if err != nil {
		t.Fatal(err)
	}

	// Still valid just before expiry.
	store.now = func() time.Time { return now.Add(59 * time.Second) }
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("expected the session to still be valid")
	}

	// Expired once the TTL has elapsed.
	store.now = func() time.Time { return now.Add(61 * time.Second) }
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("expected the session to have expired")
	}

	// Expiry deletes the entry as a side effect.
	if store.Count() != 0 {
		t.Errorf("expected 0 sessions after expiry, got %d", store.Count())
	}
}

func TestSessionStoreTouchExtendsExpiry(t *testing.T) {
	store := NewSessionStore(time.Minute)
	now := time.Now()
	store.now = func() time.Time { return now }
	sess, _ := store.Create("user-1", "proj-1", "client-1")

	store.now = func() time.Time { return now.Add(45 * time.Second) }
	store.Touch(sess.ID)

	// Without the touch this would be expired (45s + 30s > 60s TTL from
	// original creation); the touch resets the clock.
	store.now = func() time.Time { return now.Add(75 * time.Second) }
	if _, ok := store.Get(sess.ID); !ok {
		t.Fatal("expected Touch to have extended the session's life")
	}
}

func TestSessionStoreDelete(t *testing.T) {
	store := NewSessionStore(time.Minute)
	sess, _ := store.Create("user-1", "proj-1", "client-1")
	store.Delete(sess.ID)
	if _, ok := store.Get(sess.ID); ok {
		t.Fatal("expected the session to be gone after Delete")
	}
}

func TestSessionStoreSweepOnCreate(t *testing.T) {
	store := NewSessionStore(time.Minute)
	now := time.Now()
	store.now = func() time.Time { return now }
	old, _ := store.Create("user-1", "proj-1", "client-1")

	store.now = func() time.Time { return now.Add(2 * time.Minute) }
	// Creating a new session opportunistically sweeps the expired one.
	if _, err := store.Create("user-2", "proj-1", "client-1"); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	_, stillThere := store.sessions[old.ID]
	store.mu.Unlock()
	if stillThere {
		t.Error("expected the expired session to have been swept on Create")
	}
}
