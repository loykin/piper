package grpcagent

import (
	"errors"
	"sync"

	"github.com/loykin/piper/internal/agentpb"
)

// Lane classifies a frame (RPC response, push, or proxy data) by how it
// should be scheduled relative to other frames sharing the single worker
// tunnel. Lower-numbered lanes are drained first; see boundedLaneQueue.
type Lane int

const (
	// LaneControl carries RPC responses and cancel/ack-style pushes. Small,
	// latency-sensitive, never dropped.
	LaneControl Lane = iota
	// LaneDurable carries pushes that already have their own on-disk retry
	// mechanism on the sender side (e.g. pipeline task results via
	// pkg/pipeline/worker/driver/resultoutbox.go), so queue-side drop is
	// safe: the sender will simply retry on the next connection.
	LaneDurable
	// LaneTelemetry carries pushes where only the latest value matters (e.g.
	// lease renewal's active-task-ID list) — coalescing to the newest item is
	// correct and desirable, not a loss.
	LaneTelemetry
	// LaneBulk carries high-volume traffic: log lines and proxy data. Log
	// lines may be dropped under sustained overload (with a counter); proxy
	// bytes must never be silently dropped — see boundedLaneQueue.
	LaneBulk
)

// classifyPushMethod maps a worker-pushed StatusPush method name to the lane
// it should be scheduled on. Matching is done on the literal wire string
// (not imported symbols) so this package stays decoupled from the
// pipeline/notebook/serving packages that define those method constants —
// grpcagent carries method + JSON payload only, it does not know domain
// types. Unrecognized methods default to LaneControl: unknown traffic is
// assumed small and latency-sensitive rather than silently deprioritized.
func classifyPushMethod(method string) Lane {
	switch method {
	case "pipeline.task_result":
		return LaneDurable
	case "pipeline.lease_renew":
		return LaneTelemetry
	case "log.append":
		return LaneBulk
	default:
		return LaneControl
	}
}

// classifyFrameLane maps a whole outbound WorkerMessage frame to a lane.
// Used by the client-side send queue, whose items are complete frames
// (registration, RPC response, push, proxy data/close) rather than just
// StatusPush method+payload pairs — see classifyPushMethod for that case.
func classifyFrameLane(msg *agentpb.WorkerMessage) Lane {
	switch p := msg.GetPayload().(type) {
	case *agentpb.WorkerMessage_Push:
		return classifyPushMethod(p.Push.GetMethod())
	case *agentpb.WorkerMessage_ProxyData:
		return LaneBulk
	default: // Register, Response, ProxyClose: small and latency-sensitive
		return LaneControl
	}
}

// boundedLaneQueue is a multi-lane, priority-drained message queue shared by
// one worker tunnel's outbound direction (either client→master pushes or
// master→worker sends). Lower lanes are always drained before higher ones,
// so control traffic can never be starved behind a saturated bulk lane.
//
// Per-lane policy:
//   - LaneControl, LaneDurable: unbounded-in-practice bounded FIFO (a large
//     cap only guards against runaway memory growth; hitting it drops the
//     oldest item and increments droppedControl/droppedDurable so that's
//     observable, but this should never happen in normal operation).
//   - LaneTelemetry: single-slot, coalesce-to-latest. A new item replaces
//     whatever hasn't been sent yet.
//   - LaneBulk: bounded FIFO, drop-oldest with a counter on overflow. Callers
//     that must never silently lose data (proxy bytes) must not enqueue raw
//     payloads here without their own explicit-failure handling — see
//     finding 21's proxy channel overflow policy, which closes the channel
//     instead of enqueueing past capacity.
type boundedLaneQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool

	control   []laneItem
	durable   []laneItem
	telemetry *laneItem // nil = empty
	bulk      []laneItem

	droppedControl   int64
	droppedDurable   int64
	droppedTelemetry int64
	droppedBulk      int64
}

type laneItem struct {
	lane    Lane
	method  string
	payload []byte
	// msg carries a fully-formed outbound frame for the client-side send
	// queue (enqueueMsg), as an alternative to method+payload (used by the
	// server-side push queue, whose items are StatusPush method+payload
	// pairs, not whole frames). Exactly one of (method/payload) or msg is
	// meaningful per item depending on which enqueue variant created it.
	msg *agentpb.WorkerMessage
	// result, if non-nil, receives the outcome of actually sending msg (see
	// enqueueMsgWait). Buffered size 1 so the sender goroutine never blocks
	// on a caller that stopped waiting.
	result chan error
}

// laneCap bounds the control/durable/bulk FIFOs. It exists purely as a
// runaway-memory guard; normal operation never gets close to it because
// LaneControl/LaneDurable items are drained promptly and LaneBulk drops the
// oldest item on overflow rather than growing without bound.
const laneCap = 4096

func newBoundedLaneQueue() *boundedLaneQueue {
	q := &boundedLaneQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// enqueue classifies and adds an item, returning false if the queue is closed.
func (q *boundedLaneQueue) enqueue(method string, payload []byte) bool {
	return q.enqueueLane(classifyPushMethod(method), method, payload)
}

// enqueueLane adds an item to an explicit lane, bypassing classifyPushMethod.
// Used for frame types (RPC responses) that are always LaneControl.
func (q *boundedLaneQueue) enqueueLane(lane Lane, method string, payload []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	item := laneItem{lane: lane, method: method, payload: payload}
	switch lane {
	case LaneControl:
		q.control = appendBounded(q.control, item, laneCap, &q.droppedControl)
	case LaneDurable:
		q.durable = appendBounded(q.durable, item, laneCap, &q.droppedDurable)
	case LaneTelemetry:
		if q.telemetry != nil {
			q.droppedTelemetry++
		}
		q.telemetry = &item
	default: // LaneBulk and anything unrecognized
		q.bulk = appendBounded(q.bulk, item, laneCap, &q.droppedBulk)
	}
	q.cond.Signal()
	return true
}

// enqueueMsg adds a fully-formed outbound WorkerMessage to an explicit lane.
// Used by the client-side send queue, where items are whole frames (
// Register, Response, Push, ProxyData, ProxyClose) rather than StatusPush
// method+payload pairs.
func (q *boundedLaneQueue) enqueueMsg(lane Lane, msg *agentpb.WorkerMessage) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	item := laneItem{lane: lane, msg: msg}
	switch lane {
	case LaneControl:
		q.control = appendBounded(q.control, item, laneCap, &q.droppedControl)
	case LaneDurable:
		q.durable = appendBounded(q.durable, item, laneCap, &q.droppedDurable)
	case LaneTelemetry:
		if q.telemetry != nil {
			q.droppedTelemetry++
		}
		q.telemetry = &item
	default:
		q.bulk = appendBounded(q.bulk, item, laneCap, &q.droppedBulk)
	}
	q.cond.Signal()
	return true
}

// errQueueClosed is returned by enqueueMsgWait when the queue is closed
// before the item could be accepted.
var errQueueClosed = errors.New("grpcagent: send queue closed")

// enqueueMsgWait adds msg to lane and blocks until the dedicated sender
// goroutine (see Client.serve) actually calls stream.Send for it, returning
// that call's result. This gives callers the same synchronous
// send-and-get-the-error contract the old direct-mutex-protected
// stream.Send() calls had, while still letting the sender goroutine order
// frames by lane priority instead of strict arrival order.
func (q *boundedLaneQueue) enqueueMsgWait(lane Lane, msg *agentpb.WorkerMessage) error {
	result := make(chan error, 1)
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errQueueClosed
	}
	item := laneItem{lane: lane, msg: msg, result: result}
	switch lane {
	case LaneControl:
		q.control = appendBounded(q.control, item, laneCap, &q.droppedControl)
	case LaneDurable:
		q.durable = appendBounded(q.durable, item, laneCap, &q.droppedDurable)
	case LaneTelemetry:
		if q.telemetry != nil {
			if q.telemetry.result != nil {
				// Coalesced away by a newer telemetry item, not a failure —
				// the latest value (this one) will still be sent. Unblock
				// the superseded caller so it doesn't hang.
				q.telemetry.result <- nil
			}
			q.droppedTelemetry++
		}
		q.telemetry = &item
	default:
		q.bulk = appendBounded(q.bulk, item, laneCap, &q.droppedBulk)
	}
	q.cond.Signal()
	q.mu.Unlock()
	return <-result
}

// errLaneOverflow is delivered to a dropped item's result channel (if any)
// so a blocked enqueueMsgWait caller unblocks instead of hanging forever.
// For bulk-lane callers (proxy data) this is the correct "loudly fail
// instead of silently corrupting the stream" signal finding 21 requires —
// the caller must treat it as fatal for that proxy channel, not retry into
// the same queue.
var errLaneOverflow = errors.New("grpcagent: lane queue overflow, oldest item dropped")

func appendBounded(items []laneItem, item laneItem, limit int, dropped *int64) []laneItem {
	if len(items) >= limit {
		if items[0].result != nil {
			items[0].result <- errLaneOverflow
		}
		items = items[1:]
		*dropped++
	}
	return append(items, item)
}

// next blocks until an item is available (draining strictly by lane
// priority: control, durable, telemetry, bulk) or the queue is closed.
func (q *boundedLaneQueue) next() (laneItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		if len(q.control) > 0 {
			item := q.control[0]
			q.control = q.control[1:]
			return item, true
		}
		if len(q.durable) > 0 {
			item := q.durable[0]
			q.durable = q.durable[1:]
			return item, true
		}
		if q.telemetry != nil {
			item := *q.telemetry
			q.telemetry = nil
			return item, true
		}
		if len(q.bulk) > 0 {
			item := q.bulk[0]
			q.bulk = q.bulk[1:]
			return item, true
		}
		if q.closed {
			return laneItem{}, false
		}
		q.cond.Wait()
	}
}

func (q *boundedLaneQueue) close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.cond.Broadcast()
	}
	q.mu.Unlock()
}

// droppedCounts returns a snapshot of per-lane drop counters, for tests and
// future metrics wiring.
func (q *boundedLaneQueue) droppedCounts() (control, durable, telemetry, bulk int64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.droppedControl, q.droppedDurable, q.droppedTelemetry, q.droppedBulk
}
