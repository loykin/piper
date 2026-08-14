package membertunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/projectclient"
	"github.com/loykin/piper/pkg/project"
)

// remoteMemberClient implements memberclient.Client for one enrolled
// Member's connection, sending an RPC per method over the tunnel and
// awaiting the correlated response. Returned by Server.Client.
type remoteMemberClient struct {
	memberID string
	token    string
	send     func(*agentpb.HomeMessage) error

	mu      sync.Mutex
	pending map[string]chan *agentpb.MemberRPCResponse
	closed  bool
}

func newRemoteMemberClient(memberID, token string, send func(*agentpb.HomeMessage) error) *remoteMemberClient {
	return &remoteMemberClient{
		memberID: memberID,
		token:    token,
		send:     send,
		pending:  make(map[string]chan *agentpb.MemberRPCResponse),
	}
}

// deliver routes an incoming MemberRPCResponse to the goroutine waiting on
// it. Called from the Connect handler's recv loop (server.go).
func (r *remoteMemberClient) deliver(resp *agentpb.MemberRPCResponse) {
	r.mu.Lock()
	ch := r.pending[resp.RequestId]
	delete(r.pending, resp.RequestId)
	r.mu.Unlock()
	if ch != nil {
		ch <- resp
	}
}

// closeAll unblocks every in-flight call once the tunnel connection ends.
func (r *remoteMemberClient) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for id, ch := range r.pending {
		close(ch)
		delete(r.pending, id)
	}
}

func call[Req, Resp any](ctx context.Context, r *remoteMemberClient, method string, auth memberclient.AuthContext, ref project.ProjectRef, req Req) (Resp, error) {
	var zero Resp
	requestPayload, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("membertunnel: encode request for delegation: %w", err)
	}
	auth, err = memberclient.SignDelegation(auth, ref, method, requestPayload, r.token, time.Now())
	if err != nil {
		return zero, err
	}
	payload, err := encodeCall(auth, ref, req)
	if err != nil {
		return zero, err
	}

	requestID := uuid.NewString()
	ch := make(chan *agentpb.MemberRPCResponse, 1)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return zero, fmt.Errorf("%w: member %q is not connected", memberclient.ErrMemberUnavailable, r.memberID)
	}
	r.pending[requestID] = ch
	r.mu.Unlock()

	cmd := &agentpb.HomeMessage{Payload: &agentpb.HomeMessage_RpcCmd{RpcCmd: &agentpb.MemberRPCCommand{
		RequestId: requestID,
		Method:    method,
		Payload:   payload,
	}}}
	if err := r.send(cmd); err != nil {
		r.mu.Lock()
		delete(r.pending, requestID)
		r.mu.Unlock()
		return zero, fmt.Errorf("%w: send to member %q: %v", memberclient.ErrMemberUnavailable, r.memberID, err)
	}
	defer func() {
		r.mu.Lock()
		if r.pending[requestID] == ch {
			delete(r.pending, requestID)
		}
		r.mu.Unlock()
	}()

	select {
	case resp, ok := <-ch:
		if !ok {
			return zero, fmt.Errorf("%w: connection to member %q closed", memberclient.ErrMemberUnavailable, r.memberID)
		}
		if resp.Error != "" {
			return zero, errors.New(resp.Error)
		}
		var out Resp
		if len(resp.Payload) > 0 {
			if err := json.Unmarshal(resp.Payload, &out); err != nil {
				return zero, fmt.Errorf("membertunnel: decode response: %w", err)
			}
		}
		return out, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

func (r *remoteMemberClient) SubmitRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitRunRequest) (memberclient.SubmitRunResponse, error) {
	return call[memberclient.SubmitRunRequest, memberclient.SubmitRunResponse](ctx, r, MethodSubmitRun, auth, ref, req)
}

func (r *remoteMemberClient) SubmitSweep(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.SubmitSweepRequest) (memberclient.SubmitSweepResponse, error) {
	return call[memberclient.SubmitSweepRequest, memberclient.SubmitSweepResponse](ctx, r, MethodSubmitSweep, auth, ref, req)
}

func (r *remoteMemberClient) ListRuns(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req memberclient.ListRunsRequest) (memberclient.ListRunsResponse, error) {
	return call[memberclient.ListRunsRequest, memberclient.ListRunsResponse](ctx, r, MethodListRuns, auth, ref, req)
}

func (r *remoteMemberClient) GetRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) (memberclient.RunDetail, error) {
	return call[string, memberclient.RunDetail](ctx, r, MethodGetRun, auth, ref, runID)
}

func (r *remoteMemberClient) CancelRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error {
	_, err := call[string, struct{}](ctx, r, MethodCancelRun, auth, ref, runID)
	return err
}

func (r *remoteMemberClient) RerunRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string, failedOnly bool) (string, error) {
	return call[RerunRunRequest, string](ctx, r, MethodRerunRun, auth, ref, RerunRunRequest{RunID: runID, FailedOnly: failedOnly})
}

func (r *remoteMemberClient) DeleteRun(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) error {
	_, err := call[string, struct{}](ctx, r, MethodDeleteRun, auth, ref, runID)
	return err
}

func (r *remoteMemberClient) ListSteps(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) ([]memberclient.StepSummary, error) {
	return call[string, []memberclient.StepSummary](ctx, r, MethodListSteps, auth, ref, runID)
}

func (r *remoteMemberClient) RetryStep(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID, stepName string) (string, error) {
	return call[RetryStepRequest, string](ctx, r, MethodRetryStep, auth, ref, RetryStepRequest{RunID: runID, StepName: stepName})
}

func (r *remoteMemberClient) QueryLogs(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID, stepName string, afterID int64) (logLines, error) {
	return call[QueryLogsRequest, logLines](ctx, r, MethodQueryLogs, auth, ref, QueryLogsRequest{RunID: runID, StepName: stepName, AfterID: afterID})
}

func (r *remoteMemberClient) QueryMetrics(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID, stepName string) (logMetrics, error) {
	return call[QueryMetricsRequest, logMetrics](ctx, r, MethodQueryMetrics, auth, ref, QueryMetricsRequest{RunID: runID, StepName: stepName})
}

func (r *remoteMemberClient) ListArtifacts(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, runID string) ([]any, error) {
	return call[string, []any](ctx, r, MethodListArtifacts, auth, ref, runID)
}

func (r *remoteMemberClient) DoProjectRequest(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, req projectclient.Request) (projectclient.Response, error) {
	return call[projectclient.Request, projectclient.Response](ctx, r, MethodProjectRequest, auth, ref, req)
}

// ServeArtifact streams bytes directly and has no RPC-command shape yet —
// a remote Member needs a separate multiplexed data channel for this
// (analogous in spirit to the worker tunnel's ProxyData framing, but
// scoped to the Member's own artifacts rather than an arbitrary target).
// Deliberately deferred; not exercised by the run vertical slice this pass
// verifies.
func (r *remoteMemberClient) ServeArtifact(_ context.Context, _ memberclient.AuthContext, _ project.ProjectRef, w http.ResponseWriter, _ *http.Request, _, _, _ string) {
	http.Error(w, "artifact download over a remote Member tunnel is not yet supported", http.StatusNotImplemented)
}

var _ memberclient.Client = (*remoteMemberClient)(nil)
var _ projectclient.Client = (*remoteMemberClient)(nil)
