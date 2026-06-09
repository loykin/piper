package event

import (
	"sync"
	"testing"
	"time"
)

func TestHub_SubscribeReceivesPublished(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	defer cancel()

	e := New("run.started", map[string]any{"run_id": "r1"})
	h.Publish(e)

	select {
	case got := <-ch:
		if got.Type != "run.started" {
			t.Errorf("Type = %q, want run.started", got.Type)
		}
		if got.Fields["run_id"] != "r1" {
			t.Errorf("run_id = %v, want r1", got.Fields["run_id"])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout: no event received")
	}
}

func TestHub_CancelStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe()
	cancel()

	h.Publish(New("run.done", nil))

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after cancel")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("channel not closed after cancel")
	}
}

func TestHub_MultipleSubscribers(t *testing.T) {
	h := NewHub()

	const n = 5
	chans := make([]<-chan Event, n)
	cancels := make([]func(), n)
	for i := range n {
		chans[i], cancels[i] = h.Subscribe()
		defer cancels[i]()
	}

	h.Publish(New("test.event", nil))

	for i, ch := range chans {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Errorf("subscriber %d did not receive event", i)
		}
	}
}

func TestHub_CancelOneDoesNotAffectOthers(t *testing.T) {
	h := NewHub()

	ch1, cancel1 := h.Subscribe()
	ch2, cancel2 := h.Subscribe()
	defer cancel2()

	cancel1()

	h.Publish(New("x", nil))

	select {
	case <-ch2:
	case <-time.After(time.Second):
		t.Fatal("remaining subscriber did not receive event")
	}
	_ = ch1
}

func TestHub_ConcurrentPublish(t *testing.T) {
	h := NewHub()
	_, cancel := h.Subscribe()
	defer cancel()

	const count = 100
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Publish(New("concurrent", nil))
		}()
	}
	wg.Wait()
}

func TestNew_PopulatesFields(t *testing.T) {
	before := time.Now().UTC()
	e := New("step.done", map[string]any{"step": "train"})
	after := time.Now().UTC()

	if e.ID == "" {
		t.Error("ID should not be empty")
	}
	if e.Type != "step.done" {
		t.Errorf("Type = %q, want step.done", e.Type)
	}
	if e.At.Before(before) || e.At.After(after) {
		t.Errorf("At = %v not in expected range", e.At)
	}
	if e.Fields["step"] != "train" {
		t.Errorf("Fields[step] = %v, want train", e.Fields["step"])
	}
}

func TestEncode_ValidJSON(t *testing.T) {
	e := New("test", nil)
	data := Encode(e)
	if len(data) == 0 {
		t.Fatal("Encode returned empty bytes")
	}
	if data[0] != '{' {
		t.Errorf("expected JSON object, got: %s", data)
	}
}
