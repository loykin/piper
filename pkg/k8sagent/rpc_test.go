package k8sagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piper/piper/internal/tunnel"
)

func TestRPCDispatcherHandlesRegisteredMethod(t *testing.T) {
	d := NewRPCDispatcher()
	if err := d.Register("echo", func(ctx context.Context, payload json.RawMessage) (any, error) {
		var req struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return map[string]string{"message": req.Message}, nil
	}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	resp := d.Handle(context.Background(), tunnel.Frame{
		Type:    tunnel.FrameRPCRequest,
		ID:      "1",
		Method:  "echo",
		Payload: json.RawMessage(`{"message":"ok"}`),
	})
	if resp.Status != "ok" {
		t.Fatalf("status = %q error=%q", resp.Status, resp.Error)
	}
	if !strings.Contains(string(resp.Payload), `"message":"ok"`) {
		t.Fatalf("payload = %s", resp.Payload)
	}
}

func TestRPCDispatcherRejectsUnknownMethod(t *testing.T) {
	resp := NewRPCDispatcher().Handle(context.Background(), tunnel.Frame{
		Type:   tunnel.FrameRPCRequest,
		ID:     "1",
		Method: "missing",
	})
	if resp.Status != "error" {
		t.Fatalf("status = %q, want error", resp.Status)
	}
	if !strings.Contains(resp.Error, "missing") {
		t.Fatalf("error = %q", resp.Error)
	}
}
