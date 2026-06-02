package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
)

// Handler is the function signature for RPC method handlers on the worker side.
type Handler = func(ctx context.Context, payload json.RawMessage) (any, error)

// Dispatcher routes incoming RPC frames to registered handlers and returns a response frame.
type Dispatcher struct {
	handlers map[string]Handler
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{handlers: make(map[string]Handler)}
}

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

func (d *Dispatcher) Handle(ctx context.Context, frame Frame) Frame {
	if frame.Type != FrameRPCRequest {
		return Frame{}
	}
	handler := d.handlers[frame.Method]
	if handler == nil {
		return Frame{
			Type:   FrameRPCResponse,
			ID:     frame.ID,
			Status: "error",
			Error:  fmt.Sprintf("method %q is not supported", frame.Method),
		}
	}
	result, err := handler(ctx, frame.Payload)
	if err != nil {
		return Frame{
			Type:   FrameRPCResponse,
			ID:     frame.ID,
			Status: "error",
			Error:  err.Error(),
		}
	}
	var payload json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return Frame{
				Type:   FrameRPCResponse,
				ID:     frame.ID,
				Status: "error",
				Error:  fmt.Sprintf("marshal rpc response: %v", err),
			}
		}
		payload = b
	}
	return Frame{
		Type:    FrameRPCResponse,
		ID:      frame.ID,
		Status:  "ok",
		Payload: payload,
	}
}

// RegisterJSON is a typed helper that unmarshals the payload to T before calling handler.
func RegisterJSON[T, R any](d *Dispatcher, method string, handler func(context.Context, T) (R, error)) error {
	return d.Register(method, func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req T
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	})
}
