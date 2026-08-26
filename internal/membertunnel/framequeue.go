package membertunnel

import (
	"context"
	"sync"

	"github.com/loykin/piper/internal/agentpb"
)

// httpFrameQueue is an unbounded, order-preserving queue of HTTP stream
// frames sitting between the tunnel's single shared recv loop and one HTTP
// request/response body being replayed over it.
//
// push must never block: it is called directly from the shared recv loop,
// and a channel-based, bounded buffer that blocks (or worse, silently drops
// and tears down the stream) once full head-of-line-blocks — or corrupts —
// every other RPC/stream sharing the same tunnel connection behind one slow
// consumer, such as a large upload being written to disk. pop blocks the
// stream's own consumer goroutine until a frame is queued, the queue is
// closed, or ctx is done.
type httpFrameQueue struct {
	mu     sync.Mutex
	buf    []*agentpb.MemberHTTPStreamData
	notify chan struct{}
	closed bool
}

func newHTTPFrameQueue() *httpFrameQueue {
	return &httpFrameQueue{notify: make(chan struct{}, 1)}
}

func (q *httpFrameQueue) push(frame *agentpb.MemberHTTPStreamData) {
	q.mu.Lock()
	q.buf = append(q.buf, frame)
	q.mu.Unlock()
	q.wake()
}

// close wakes any blocked pop so it returns (nil, false) once the queue
// drains — used when the underlying tunnel connection ends. Safe to call
// more than once.
func (q *httpFrameQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wake()
}

func (q *httpFrameQueue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *httpFrameQueue) pop(ctx context.Context) (*agentpb.MemberHTTPStreamData, bool) {
	for {
		q.mu.Lock()
		if len(q.buf) > 0 {
			frame := q.buf[0]
			q.buf = q.buf[1:]
			q.mu.Unlock()
			return frame, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, false
		}
		select {
		case <-q.notify:
		case <-ctx.Done():
			return nil, false
		}
	}
}
