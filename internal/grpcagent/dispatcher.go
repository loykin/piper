package grpcagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/loykin/piper/internal/agentpb"
)

// Handler is the function signature for RPC method handlers on the worker side.
type Handler = func(ctx context.Context, payload json.RawMessage) (any, error)

// Dispatcher routes incoming RPCCommand frames to registered handlers.
type Dispatcher struct {
	handlers map[string]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

// Register adds a raw handler for the given RPC method name.
func (d *Dispatcher) Register(method string, handler Handler) error {
	if method == "" {
		return fmt.Errorf("rpc method is required")
	}
	if handler == nil {
		return fmt.Errorf("rpc handler for %q is nil", method)
	}
	if _, exists := d.handlers[method]; exists {
		return fmt.Errorf("rpc method %q already registered", method)
	}
	d.handlers[method] = handler
	return nil
}

// RegisterJSON is a typed helper that unmarshals the payload to T before calling handler.
func RegisterJSON[T, R any](d *Dispatcher, method string, handler func(context.Context, T) (R, error)) error {
	return d.Register(method, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req T
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("unmarshal %s request: %w", method, err)
		}
		return handler(ctx, req)
	})
}

// handleCmd dispatches an RPCCommand (master → worker) and returns the
// RPCResponse to send back.
func (d *Dispatcher) handleCmd(ctx context.Context, cmd *agentpb.RPCCommand) *agentpb.RPCResponse {
	return d.dispatch(ctx, cmd.RequestId, cmd.Method, cmd.Payload)
}

// handleRequest dispatches an RPCRequest (worker → master) and returns the
// RPCResponse to send back. Mirrors handleCmd for the opposite direction —
// used by Server for worker-initiated methods like pipeline.step_upsert.
func (d *Dispatcher) handleRequest(ctx context.Context, req *agentpb.RPCRequest) *agentpb.RPCResponse {
	return d.dispatch(ctx, req.RequestId, req.Method, req.Payload)
}

// dispatch is the direction-agnostic core shared by handleCmd/handleRequest:
// look up the registered handler for method, invoke it, and shape the result
// (or error) into an RPCResponse correlated by requestID.
func (d *Dispatcher) dispatch(ctx context.Context, requestID, method string, payload []byte) *agentpb.RPCResponse {
	resp := &agentpb.RPCResponse{RequestId: requestID}
	h := d.handlers[method]
	if h == nil {
		resp.Error = fmt.Sprintf("method %q is not supported", method)
		return resp
	}
	result, err := h(ctx, json.RawMessage(payload))
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			resp.Error = fmt.Sprintf("marshal response: %v", err)
			return resp
		}
		resp.Payload = data
	}
	return resp
}
