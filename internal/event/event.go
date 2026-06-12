// Package event provides a lightweight in-process pub/sub event bus.
package event

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Event is a structured event emitted by the piper server.
// ProjectID is empty for infrastructure events (worker/agent connect).
type Event struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	ProjectID string         `json:"project_id,omitempty"`
	At        time.Time      `json:"at"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Publisher can emit events to all current subscribers.
type Publisher interface {
	Publish(Event)
}

// Bus supports both publishing and subscribing.
type Bus interface {
	Publisher
	Subscribe() (<-chan Event, func())
}

// Hub is a fan-out in-process event bus.
// It implements Bus. Slow subscribers are dropped (non-blocking send).
type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

// NewHub creates a new Hub ready to use.
func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan Event]struct{})}
}

// Subscribe returns a receive-only channel and a cancel function.
// The cancel function must be called to release resources.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Publish fans the event out to all current subscribers.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

// New constructs a project-scoped Event with a generated ID and the current UTC timestamp.
func New(projectID, eventType string, fields map[string]any) Event {
	return Event{
		ID:        newID(),
		Type:      eventType,
		ProjectID: projectID,
		At:        time.Now().UTC(),
		Fields:    fields,
	}
}

// NewInfra constructs an infrastructure Event (no project scope).
func NewInfra(eventType string, fields map[string]any) Event {
	return Event{
		ID:     newID(),
		Type:   eventType,
		At:     time.Now().UTC(),
		Fields: fields,
	}
}

// Encode serialises an Event to JSON for use in SSE data fields.
func Encode(e Event) []byte {
	data, _ := json.Marshal(e)
	return data
}

func newID() string {
	return fmt.Sprintf("evt-%s", time.Now().UTC().Format("20060102150405.000000000"))
}
