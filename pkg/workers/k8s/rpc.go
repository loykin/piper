package k8sworker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/piper/piper/internal/tunnel"
)

type RPCHandler = func(ctx context.Context, payload json.RawMessage) (any, error)

type RPCDispatcher struct {
	handlers map[string]RPCHandler
}

func NewRPCDispatcher() *RPCDispatcher {
	return &RPCDispatcher{handlers: make(map[string]RPCHandler)}
}

func (d *RPCDispatcher) Register(method string, handler RPCHandler) error {
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

func (d *RPCDispatcher) Handle(ctx context.Context, frame tunnel.Frame) tunnel.Frame {
	if frame.Type != tunnel.FrameRPCRequest {
		return tunnel.Frame{}
	}
	handler := d.handlers[frame.Method]
	if handler == nil {
		return tunnel.Frame{
			Type:   tunnel.FrameRPCResponse,
			ID:     frame.ID,
			Status: "error",
			Error:  fmt.Sprintf("method %q is not supported", frame.Method),
		}
	}
	result, err := handler(ctx, frame.Payload)
	if err != nil {
		return tunnel.Frame{
			Type:   tunnel.FrameRPCResponse,
			ID:     frame.ID,
			Status: "error",
			Error:  err.Error(),
		}
	}
	var payload json.RawMessage
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return tunnel.Frame{
				Type:   tunnel.FrameRPCResponse,
				ID:     frame.ID,
				Status: "error",
				Error:  fmt.Sprintf("marshal rpc response: %v", err),
			}
		}
		payload = b
	}
	return tunnel.Frame{
		Type:    tunnel.FrameRPCResponse,
		ID:      frame.ID,
		Status:  "ok",
		Payload: payload,
	}
}
