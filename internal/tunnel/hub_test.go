package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/piper/piper/internal/testutil"
)

func TestHubSendRPCRoundTrip(t *testing.T) {
	hub := NewHub()
	touched := 0
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := hub.Accept(w, r, "agent-1", func() { touched++ }); err != nil {
			t.Logf("accept ended: %v", err)
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		var req Frame
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			t.Errorf("read rpc request: %v", err)
			return
		}
		if req.Type != FrameRPCRequest || req.Method != "ping" {
			t.Errorf("request = type %q method %q, want rpc_request ping", req.Type, req.Method)
			return
		}
		payload, _ := json.Marshal(map[string]string{"pong": "ok"})
		if err := wsjson.Write(ctx, conn, Frame{
			Type:    FrameRPCResponse,
			ID:      req.ID,
			Status:  "ok",
			Payload: payload,
		}); err != nil {
			t.Errorf("write rpc response: %v", err)
		}
	}()

	if err := hub.WaitConnected(ctx, "agent-1"); err != nil {
		t.Fatalf("WaitConnected: %v", err)
	}

	var result struct {
		Pong string `json:"pong"`
	}
	if err := hub.SendRPC(ctx, "agent-1", "ping", map[string]string{"hello": "world"}, &result); err != nil {
		t.Fatalf("SendRPC returned error: %v", err)
	}
	if result.Pong != "ok" {
		t.Fatalf("result.Pong = %q, want ok", result.Pong)
	}
	<-done
}

func TestHubSendRPCRequiresConnectedTunnel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := NewHub().SendRPC(ctx, "missing", "ping", nil, nil); err == nil {
		t.Fatal("expected error for disconnected tunnel")
	}
}
