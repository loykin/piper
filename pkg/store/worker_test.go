package store

import (
	"database/sql"
	"testing"
	"time"
)

// ─── Worker ───────────────────────────────────────────────────────────────────

func TestUpsertWorker_and_GetWorker(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	w := &Worker{
		ID:           "w1",
		Label:        "gpu",
		Hostname:     "host1",
		Concurrency:  4,
		Status:       WorkerStatusOnline,
		InFlight:     0,
		RegisteredAt: now,
		LastSeenAt:   now,
	}
	if err := st.UpsertWorker(w); err != nil {
		t.Fatal(err)
	}

	got, err := st.GetWorker("w1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "gpu" {
		t.Errorf("label: want gpu, got %q", got.Label)
	}
	if got.Status != WorkerStatusOnline {
		t.Errorf("status: want online, got %q", got.Status)
	}
}

func TestUpsertWorker_reregister(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	_ = st.UpsertWorker(&Worker{ID: "w1", Label: "old", Concurrency: 1, Status: WorkerStatusOnline, RegisteredAt: now, LastSeenAt: now})
	_ = st.UpsertWorker(&Worker{ID: "w1", Label: "new", Concurrency: 8, Status: WorkerStatusOnline, RegisteredAt: now, LastSeenAt: now})

	got, _ := st.GetWorker("w1")
	if got.Label != "new" {
		t.Errorf("want label new, got %q", got.Label)
	}
	if got.Concurrency != 8 {
		t.Errorf("want concurrency 8, got %d", got.Concurrency)
	}
}

func TestHeartbeatWorker(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	_ = st.UpsertWorker(&Worker{ID: "w1", Status: WorkerStatusOnline, RegisteredAt: now, LastSeenAt: now})

	if err := st.HeartbeatWorker("w1", 3); err != nil {
		t.Fatal(err)
	}

	got, _ := st.GetWorker("w1")
	if got.InFlight != 3 {
		t.Errorf("want in_flight=3, got %d", got.InFlight)
	}
}

func TestHeartbeatWorker_notFound(t *testing.T) {
	st := openTestStore(t)
	err := st.HeartbeatWorker("nobody", 0)
	if err != sql.ErrNoRows {
		t.Errorf("want ErrNoRows, got %v", err)
	}
}

func TestSetWorkerStatus(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	_ = st.UpsertWorker(&Worker{ID: "w1", Status: WorkerStatusOnline, RegisteredAt: now, LastSeenAt: now})

	if err := st.SetWorkerStatus("w1", WorkerStatusDraining); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetWorker("w1")
	if got.Status != WorkerStatusDraining {
		t.Errorf("want draining, got %q", got.Status)
	}
}

func TestListWorkers_onlineOnly(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	_ = st.UpsertWorker(&Worker{ID: "w1", Status: WorkerStatusOnline, RegisteredAt: now, LastSeenAt: now})
	_ = st.UpsertWorker(&Worker{ID: "w2", Status: WorkerStatusDraining, RegisteredAt: now, LastSeenAt: now})
	_ = st.UpsertWorker(&Worker{ID: "w3", Status: WorkerStatusOffline, RegisteredAt: now, LastSeenAt: now})

	all, _ := st.ListWorkers(false)
	if len(all) != 3 {
		t.Errorf("want 3 total, got %d", len(all))
	}

	active, _ := st.ListWorkers(true)
	if len(active) != 2 {
		t.Errorf("want 2 active (online+draining), got %d", len(active))
	}
}

func TestMarkWorkersOffline(t *testing.T) {
	st := openTestStore(t)
	old := time.Now().Add(-time.Minute).Truncate(time.Second)
	recent := time.Now().Truncate(time.Second)

	_ = st.UpsertWorker(&Worker{ID: "stale", Status: WorkerStatusOnline, RegisteredAt: old, LastSeenAt: old})
	_ = st.UpsertWorker(&Worker{ID: "fresh", Status: WorkerStatusOnline, RegisteredAt: recent, LastSeenAt: recent})

	cutoff := time.Now().Add(-30 * time.Second)
	if err := st.MarkWorkersOffline(cutoff); err != nil {
		t.Fatal(err)
	}

	stale, _ := st.GetWorker("stale")
	fresh, _ := st.GetWorker("fresh")

	if stale.Status != WorkerStatusOffline {
		t.Errorf("stale: want offline, got %q", stale.Status)
	}
	if fresh.Status != WorkerStatusOnline {
		t.Errorf("fresh: want online, got %q", fresh.Status)
	}
}
