package grpcagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
)

func TestConnectAndServeDispatchesRpcCmdAndSendsResponse(t *testing.T) {
	c := NewClient(ClientConfig{MasterURL: "http://x", AgentID: "worker-1", Infrastructure: "baremetal"})
	if err := c.Dispatcher().Register("ping", func(context.Context, json.RawMessage) (any, error) {
		return map[string]string{"pong": "ok"}, nil
	}); err != nil {
		t.Fatal(err)
	}

	stream := newFakeWorkerStream()
	serveDone := make(chan error, 1)
	go func() { serveDone <- c.serve(context.Background(), stream) }()

	// sent[0] is the registration message the client sends on connect.
	if !stream.waitForSentCount(1) {
		t.Fatal("registration was never sent")
	}

	stream.push(&agentpb.MasterMessage{Payload: &agentpb.MasterMessage_RpcCmd{
		RpcCmd: &agentpb.RPCCommand{RequestId: "req-1", Method: "ping"},
	}})

	if !stream.waitForSentCount(2) {
		t.Fatal("RPC response was never sent")
	}
	resp := stream.sentAt(1).GetResponse()
	if resp == nil {
		t.Fatalf("sent[1] is not an RPCResponse: %#v", stream.sentAt(1))
	}
	if resp.RequestId != "req-1" || resp.Error != "" {
		t.Fatalf("response = %#v, want request_id=req-1 and no error", resp)
	}

	stream.close()
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after stream closed")
	}
}

func TestConnectAndServeHandlesSlowRpcWithoutBlockingProxyFrame(t *testing.T) {
	c := NewClient(ClientConfig{MasterURL: "http://x", AgentID: "worker-1", Infrastructure: "baremetal"})
	slowEntered := make(chan struct{})
	slowRelease := make(chan struct{})
	if err := c.Dispatcher().Register("slow", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(slowEntered)
		<-slowRelease
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Dispatcher().Register("fast", func(context.Context, json.RawMessage) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	stream := newFakeWorkerStream()
	serveDone := make(chan error, 1)
	go func() { serveDone <- c.serve(context.Background(), stream) }()

	if !stream.waitForSentCount(1) {
		t.Fatal("registration was never sent")
	}

	stream.push(&agentpb.MasterMessage{Payload: &agentpb.MasterMessage_RpcCmd{
		RpcCmd: &agentpb.RPCCommand{RequestId: "slow-1", Method: "slow"},
	}})
	select {
	case <-slowEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow handler was never entered — recv loop is blocked before dispatch")
	}

	// While the slow handler is still blocked, a second, unrelated RPC must
	// still be read and dispatched — this is the head-of-line-blocking
	// regression test for finding 20.
	stream.push(&agentpb.MasterMessage{Payload: &agentpb.MasterMessage_RpcCmd{
		RpcCmd: &agentpb.RPCCommand{RequestId: "fast-1", Method: "fast"},
	}})

	if !stream.waitForSentCount(2) {
		t.Fatal("fast RPC response was blocked behind the slow RPC handler")
	}
	fastResp := stream.sentAt(1).GetResponse()
	if fastResp == nil || fastResp.RequestId != "fast-1" {
		t.Fatalf("sent[1] = %#v, want the fast RPC's response", stream.sentAt(1))
	}

	close(slowRelease)
	if !stream.waitForSentCount(3) {
		t.Fatal("slow RPC response was never sent after release")
	}

	stream.close()
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after stream closed")
	}
}
