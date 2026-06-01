package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	FrameRPCRequest  = "rpc_request"
	FrameRPCResponse = "rpc_response"
)

type Frame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Status  string          `json:"status,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Hub struct {
	mu      sync.RWMutex
	tunnels map[string]*AgentTunnel

	subMu sync.Mutex
	subs  map[string][]chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		tunnels: make(map[string]*AgentTunnel),
		subs:    make(map[string][]chan struct{}),
	}
}

// WaitConnected blocks until the named agent tunnel is registered or ctx expires.
func (h *Hub) WaitConnected(ctx context.Context, agentID string) error {
	h.mu.RLock()
	ok := h.tunnels[agentID] != nil
	h.mu.RUnlock()
	if ok {
		return nil
	}
	ch := make(chan struct{})
	h.subMu.Lock()
	h.subs[agentID] = append(h.subs[agentID], ch)
	h.subMu.Unlock()
	select {
	case <-ctx.Done():
		return fmt.Errorf("agent tunnel %q is not connected: %w", agentID, ctx.Err())
	case <-ch:
		return nil
	}
}

func (h *Hub) Accept(w http.ResponseWriter, r *http.Request, agentID string) error {
	if agentID == "" {
		http.Error(w, "agent id is required", http.StatusBadRequest)
		return nil
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return err
	}

	t := newAgentTunnel(agentID, conn)
	h.register(t)
	defer h.unregister(agentID, t)
	return t.readLoop(r.Context())
}

func (h *Hub) SendRPC(ctx context.Context, agentID, method string, payload any, result any) error {
	h.mu.RLock()
	t := h.tunnels[agentID]
	h.mu.RUnlock()
	if t == nil {
		return fmt.Errorf("agent tunnel %q is not connected", agentID)
	}
	return t.SendRPC(ctx, method, payload, result)
}

func (h *Hub) Connected(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.tunnels[agentID] != nil
}

func (h *Hub) register(t *AgentTunnel) {
	h.mu.Lock()
	old := h.tunnels[t.agentID]
	h.tunnels[t.agentID] = t
	h.mu.Unlock()
	if old != nil {
		old.close(websocket.StatusPolicyViolation, "replaced by newer tunnel")
	}
	h.subMu.Lock()
	chans := h.subs[t.agentID]
	delete(h.subs, t.agentID)
	h.subMu.Unlock()
	for _, ch := range chans {
		close(ch)
	}
}

func (h *Hub) unregister(agentID string, t *AgentTunnel) {
	h.mu.Lock()
	if h.tunnels[agentID] == t {
		delete(h.tunnels, agentID)
	}
	h.mu.Unlock()
	t.close(websocket.StatusNormalClosure, "")
}

type AgentTunnel struct {
	agentID string
	conn    *websocket.Conn

	writeMu sync.Mutex
	pending sync.Map // request id -> chan Frame
}

func newAgentTunnel(agentID string, conn *websocket.Conn) *AgentTunnel {
	return &AgentTunnel{agentID: agentID, conn: conn}
}

func (t *AgentTunnel) SendRPC(ctx context.Context, method string, payload any, result any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan Frame, 1)
	t.pending.Store(id, ch)
	defer t.pending.Delete(id)

	if err := t.write(ctx, Frame{
		Type:    FrameRPCRequest,
		ID:      id,
		Method:  method,
		Payload: payloadBytes,
	}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case frame := <-ch:
		if frame.Error != "" {
			return fmt.Errorf("agent rpc %s: %s", method, frame.Error)
		}
		if result != nil && len(frame.Payload) > 0 {
			return json.Unmarshal(frame.Payload, result)
		}
		return nil
	}
}

func (t *AgentTunnel) readLoop(ctx context.Context) error {
	defer func() { _ = t.conn.CloseNow() }()
	for {
		var frame Frame
		if err := wsjson.Read(ctx, t.conn, &frame); err != nil {
			return err
		}
		if frame.Type == FrameRPCResponse {
			if chAny, ok := t.pending.Load(frame.ID); ok {
				ch := chAny.(chan Frame)
				select {
				case ch <- frame:
				default:
				}
			}
		}
	}
}

func (t *AgentTunnel) write(ctx context.Context, frame Frame) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return wsjson.Write(ctx, t.conn, frame)
}

func (t *AgentTunnel) close(code websocket.StatusCode, reason string) {
	_ = t.conn.Close(code, reason)
}
