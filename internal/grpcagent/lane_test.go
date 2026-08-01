package grpcagent

import (
	"sync"
	"testing"
)

func TestClassifyPushMethodMapsKnownMethods(t *testing.T) {
	cases := map[string]Lane{
		"pipeline.task_result":   LaneDurable,
		"pipeline.lease_renew":   LaneTelemetry,
		"log.append":             LaneBulk,
		"notebook.status_update": LaneControl,
		"unknown.method":         LaneControl,
	}
	for method, want := range cases {
		if got := classifyPushMethod(method); got != want {
			t.Errorf("classifyPushMethod(%q) = %v, want %v", method, got, want)
		}
	}
}

func TestBoundedLaneQueueDrainsByPriority(t *testing.T) {
	q := newBoundedLaneQueue()
	q.enqueueLane(LaneBulk, "log.append", []byte("bulk"))
	q.enqueueLane(LaneTelemetry, "pipeline.lease_renew", []byte("telemetry"))
	q.enqueueLane(LaneDurable, "pipeline.task_result", []byte("durable"))
	q.enqueueLane(LaneControl, "rpc.response", []byte("control"))

	want := []Lane{LaneControl, LaneDurable, LaneTelemetry, LaneBulk}
	for _, wantLane := range want {
		item, ok := q.next()
		if !ok {
			t.Fatalf("next() returned ok=false, want an item for lane %v", wantLane)
		}
		if item.lane != wantLane {
			t.Fatalf("drained lane = %v, want %v", item.lane, wantLane)
		}
	}
}

func TestBoundedLaneQueueDropsOldestBulkWithCounter(t *testing.T) {
	q := newBoundedLaneQueue()
	for i := 0; i < laneCap+5; i++ {
		q.enqueueLane(LaneBulk, "log.append", []byte{byte(i)})
	}
	_, _, _, bulkDropped := q.droppedCounts()
	if bulkDropped != 5 {
		t.Fatalf("bulk dropped = %d, want 5", bulkDropped)
	}
	item, ok := q.next()
	if !ok {
		t.Fatal("expected an item")
	}
	if item.payload[0] != 5 {
		t.Fatalf("oldest surviving item = %v, want the 6th enqueued (index 5) since the first 5 were dropped", item.payload)
	}
}

func TestTelemetryLaneCoalescesToLatest(t *testing.T) {
	q := newBoundedLaneQueue()
	q.enqueueLane(LaneTelemetry, "pipeline.lease_renew", []byte("first"))
	q.enqueueLane(LaneTelemetry, "pipeline.lease_renew", []byte("second"))
	q.enqueueLane(LaneTelemetry, "pipeline.lease_renew", []byte("third"))

	_, _, telemetryDropped, _ := q.droppedCounts()
	if telemetryDropped != 2 {
		t.Fatalf("telemetry dropped = %d, want 2 (coalesced)", telemetryDropped)
	}
	item, ok := q.next()
	if !ok {
		t.Fatal("expected an item")
	}
	if string(item.payload) != "third" {
		t.Fatalf("payload = %q, want %q (latest)", item.payload, "third")
	}
}

func TestBoundedLaneQueueConcurrentPushPop(t *testing.T) {
	q := newBoundedLaneQueue()
	const n = 500
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			q.enqueueLane(LaneControl, "rpc.response", []byte{byte(i)})
		}
	}()
	received := 0
	go func() {
		defer wg.Done()
		for received < n {
			if _, ok := q.next(); ok {
				received++
			}
		}
	}()
	wg.Wait()
	if received != n {
		t.Fatalf("received = %d, want %d", received, n)
	}
}

func TestBoundedLaneQueueCloseUnblocksNext(t *testing.T) {
	q := newBoundedLaneQueue()
	done := make(chan bool, 1)
	go func() {
		_, ok := q.next()
		done <- ok
	}()
	q.close()
	if ok := <-done; ok {
		t.Fatal("next() returned ok=true after close with no pending items")
	}
	if q.enqueueLane(LaneControl, "x", nil) {
		t.Fatal("enqueue after close should return false")
	}
}
