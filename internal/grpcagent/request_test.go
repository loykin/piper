package grpcagent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/loykin/piper/internal/agentpb"
)

// TestServerConnectDispatchesRpcRequestAndSendsResponse verifies the
// master-side counterpart to TestConnectAndServeDispatchesRpcCmdAndSendsResponse:
// a worker-initiated RPCRequest arriving on Connect's recv loop is routed to
// the handler registered on Server.Dispatcher() for its method, and the
// result comes back as a correlated MasterMessage_RpcResponse.
func TestServerConnectDispatchesRpcRequestAndSendsResponse(t *testing.T) {
	s := NewServer(nil, nil)
	if err := RegisterJSON(s.Dispatcher(), "pipeline.step_upsert", func(ctx context.Context, req map[string]string) (any, error) {
		return map[string]string{"step": req["step"], "applied": "true", "worker_id": RequestAgentID(ctx)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	stream := newFakeServerStream()
	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Register{
		Register: &agentpb.Registration{Id: "worker-1", Infrastructure: "baremetal"},
	}})

	connectDone := make(chan error, 1)
	go func() { connectDone <- s.Connect(stream) }()

	// sent[0] is nothing yet (Connect only sends in response to frames it
	// receives); push the request once registration has been consumed.
	payload, err := json.Marshal(map[string]string{"step": "train"})
	if err != nil {
		t.Fatal(err)
	}
	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Request{
		Request: &agentpb.RPCRequest{RequestId: "req-1", Method: "pipeline.step_upsert", Payload: payload},
	}})

	if !stream.waitForSentCount(1) {
		t.Fatal("rpc response was never sent")
	}
	resp := stream.sentMessages()[0].GetRpcResponse()
	if resp == nil {
		t.Fatalf("sent[0] is not an RPCResponse: %#v", stream.sentMessages()[0])
	}
	if resp.RequestId != "req-1" || resp.Error != "" {
		t.Fatalf("response = %#v, want request_id=req-1 and no error", resp)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	if result["step"] != "train" || result["applied"] != "true" {
		t.Fatalf("response payload = %#v, want step=train applied=true", result)
	}
	if result["worker_id"] != "worker-1" {
		t.Fatalf("worker_id = %q, want %q (from the authenticated tunnel connection, via RequestAgentID)", result["worker_id"], "worker-1")
	}

	stream.close()
	select {
	case <-connectDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return after stream closed")
	}
}

// TestServerConnectRpcRequestIgnoresPayloadWorkerID verifies that
// RequestAgentID resolves the acting worker from the authenticated tunnel
// connection, not from a worker_id field the request payload claims — a
// worker registered as "worker-1" cannot make its step_upsert act as if it
// came from "attacker-worker" just by putting that in the JSON payload.
func TestServerConnectRpcRequestIgnoresPayloadWorkerID(t *testing.T) {
	s := NewServer(nil, nil)
	type spoofableRequest struct {
		WorkerID string `json:"worker_id"`
	}
	if err := RegisterJSON(s.Dispatcher(), "pipeline.step_upsert", func(ctx context.Context, req spoofableRequest) (any, error) {
		return map[string]string{"payload_worker_id": req.WorkerID, "authenticated_worker_id": RequestAgentID(ctx)}, nil
	}); err != nil {
		t.Fatal(err)
	}

	stream := newFakeServerStream()
	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Register{
		Register: &agentpb.Registration{Id: "worker-1", Infrastructure: "baremetal"},
	}})
	go func() { _ = s.Connect(stream) }()

	payload, err := json.Marshal(spoofableRequest{WorkerID: "attacker-worker"})
	if err != nil {
		t.Fatal(err)
	}
	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Request{
		Request: &agentpb.RPCRequest{RequestId: "req-1", Method: "pipeline.step_upsert", Payload: payload},
	}})

	if !stream.waitForSentCount(1) {
		t.Fatal("rpc response was never sent")
	}
	resp := stream.sentMessages()[0].GetRpcResponse()
	if resp == nil || resp.Error != "" {
		t.Fatalf("response = %#v, want no error", resp)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("unmarshal response payload: %v", err)
	}
	if result["authenticated_worker_id"] != "worker-1" {
		t.Fatalf("authenticated_worker_id = %q, want %q — a spoofed payload worker_id must not override the tunnel's authenticated identity", result["authenticated_worker_id"], "worker-1")
	}
	stream.close()
}

// TestServerConnectRpcRequestUnknownMethod verifies an RPCRequest for an
// unregistered method comes back as an error response, not a dropped frame
// or a panic — mirroring how handleCmd treats an unknown master→worker method.
func TestServerConnectRpcRequestUnknownMethod(t *testing.T) {
	s := NewServer(nil, nil)

	stream := newFakeServerStream()
	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Register{
		Register: &agentpb.Registration{Id: "worker-1", Infrastructure: "baremetal"},
	}})
	go func() { _ = s.Connect(stream) }()

	stream.push(&agentpb.WorkerMessage{Payload: &agentpb.WorkerMessage_Request{
		Request: &agentpb.RPCRequest{RequestId: "req-1", Method: "pipeline.no_such_method"},
	}})

	if !stream.waitForSentCount(1) {
		t.Fatal("rpc response was never sent")
	}
	resp := stream.sentMessages()[0].GetRpcResponse()
	if resp == nil || resp.Error == "" {
		t.Fatalf("response = %#v, want a non-empty error for an unregistered method", resp)
	}
	stream.close()
}

// TestClientSendRequestReceivesCorrelatedResponse verifies the worker-side
// counterpart: SendRequest sends a WorkerMessage_Request and unblocks with
// the result once a matching MasterMessage_RpcResponse arrives.
func TestClientSendRequestReceivesCorrelatedResponse(t *testing.T) {
	c := NewClient(ClientConfig{MasterURL: "http://x", AgentID: "worker-1", Infrastructure: "baremetal"})

	stream := newFakeWorkerStream()
	serveDone := make(chan error, 1)
	go func() { serveDone <- c.serve(context.Background(), stream) }()

	// sent[0] is the registration message the client sends on connect.
	if !stream.waitForSentCount(1) {
		t.Fatal("registration was never sent")
	}

	type reqResult struct {
		Applied bool `json:"applied"`
	}
	sendDone := make(chan error, 1)
	var result reqResult
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sendDone <- c.SendRequest(ctx, "pipeline.step_upsert", map[string]string{"step": "train"}, &result)
	}()

	if !stream.waitForSentCount(2) {
		t.Fatal("RPCRequest was never sent")
	}
	req := stream.sentAt(1).GetRequest()
	if req == nil {
		t.Fatalf("sent[1] is not an RPCRequest: %#v", stream.sentAt(1))
	}
	if req.Method != "pipeline.step_upsert" {
		t.Fatalf("request method = %q, want %q", req.Method, "pipeline.step_upsert")
	}

	respPayload, err := json.Marshal(reqResult{Applied: true})
	if err != nil {
		t.Fatal(err)
	}
	stream.push(&agentpb.MasterMessage{Payload: &agentpb.MasterMessage_RpcResponse{
		RpcResponse: &agentpb.RPCResponse{RequestId: req.RequestId, Payload: respPayload},
	}})

	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("SendRequest returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendRequest did not return after response was pushed")
	}
	if !result.Applied {
		t.Fatal("SendRequest did not unmarshal the response into result")
	}

	stream.close()
	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after stream closed")
	}
}

// TestClientSendRequestPropagatesHandlerError verifies a non-empty
// RPCResponse.Error surfaces as an error from SendRequest, not a silently
// empty result.
func TestClientSendRequestPropagatesHandlerError(t *testing.T) {
	c := NewClient(ClientConfig{MasterURL: "http://x", AgentID: "worker-1", Infrastructure: "baremetal"})

	stream := newFakeWorkerStream()
	go func() { _ = c.serve(context.Background(), stream) }()
	if !stream.waitForSentCount(1) {
		t.Fatal("registration was never sent")
	}

	sendDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sendDone <- c.SendRequest(ctx, "pipeline.run_finalize", nil, nil)
	}()

	if !stream.waitForSentCount(2) {
		t.Fatal("RPCRequest was never sent")
	}
	req := stream.sentAt(1).GetRequest()
	stream.push(&agentpb.MasterMessage{Payload: &agentpb.MasterMessage_RpcResponse{
		RpcResponse: &agentpb.RPCResponse{RequestId: req.RequestId, Error: "run already finalized"},
	}})

	select {
	case err := <-sendDone:
		if err == nil {
			t.Fatal("SendRequest returned nil error, want the handler's error to propagate")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendRequest did not return after response was pushed")
	}
	stream.close()
}

// TestClientSendRequestUnblocksOnDisconnect verifies that a SendRequest call
// given an unbounded context (context.Background(), matching how a DB-write
// call site would realistically be invoked) still returns once the tunnel
// disconnects, instead of hanging forever waiting for a response that will
// now never arrive.
func TestClientSendRequestUnblocksOnDisconnect(t *testing.T) {
	c := NewClient(ClientConfig{MasterURL: "http://x", AgentID: "worker-1", Infrastructure: "baremetal"})

	stream := newFakeWorkerStream()
	serveDone := make(chan error, 1)
	go func() { serveDone <- c.serve(context.Background(), stream) }()
	if !stream.waitForSentCount(1) {
		t.Fatal("registration was never sent")
	}

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- c.SendRequest(context.Background(), "pipeline.step_upsert", map[string]string{"step": "train"}, nil)
	}()

	if !stream.waitForSentCount(2) {
		t.Fatal("RPCRequest was never sent")
	}

	// Disconnect without ever pushing a response.
	stream.close()

	select {
	case err := <-sendDone:
		if err == nil {
			t.Fatal("SendRequest returned nil error on disconnect, want a disconnect error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SendRequest hung past disconnect instead of unblocking on the closed channel")
	}

	select {
	case <-serveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after stream closed")
	}
}
