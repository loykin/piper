package membertunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	streams map[string]*httpFrameQueue
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
	for id, q := range r.streams {
		q.close()
		delete(r.streams, id)
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

const artifactChunkSize int64 = 512 << 10

func (r *remoteMemberClient) ServeArtifact(ctx context.Context, auth memberclient.AuthContext, ref project.ProjectRef, w http.ResponseWriter, request *http.Request, runID, step, artifactPath string) {
	path := "/runs/" + url.PathEscape(runID) + "/artifacts/" + url.PathEscape(step)
	for _, segment := range strings.Split(artifactPath, "/") {
		if segment != "" {
			path += "/" + url.PathEscape(segment)
		}
	}
	requestedRange := request.Header.Get("Range")
	var offset int64
	for {
		rangeValue := requestedRange
		if rangeValue == "" {
			rangeValue = fmt.Sprintf("bytes=%d-%d", offset, offset+artifactChunkSize-1)
		}
		response, err := r.DoProjectRequest(ctx, auth, ref, projectclient.Request{
			Method: http.MethodGet, Path: path, Header: http.Header{"Range": []string{rangeValue}},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if offset == 0 {
			for _, name := range []string{"Accept-Ranges", "Content-Disposition", "Content-Type", "ETag", "Last-Modified"} {
				for _, value := range http.Header(response.Header).Values(name) {
					w.Header().Add(name, value)
				}
			}
			status := response.Status
			if requestedRange == "" && status == http.StatusPartialContent {
				status = http.StatusOK
			}
			w.WriteHeader(status)
		}
		if len(response.Body) > 0 {
			if _, err := w.Write(response.Body); err != nil {
				return
			}
		}
		if requestedRange != "" || response.Status != http.StatusPartialContent {
			return
		}
		total, ok := contentRangeTotal(http.Header(response.Header).Get("Content-Range"))
		offset += int64(len(response.Body))
		if !ok || offset >= total || len(response.Body) == 0 {
			return
		}
	}
}

func contentRangeTotal(value string) (int64, bool) {
	slash := strings.LastIndexByte(value, '/')
	if slash < 0 || slash == len(value)-1 || value[slash+1:] == "*" {
		return 0, false
	}
	total, err := strconv.ParseInt(value[slash+1:], 10, 64)
	return total, err == nil && total >= 0
}

var _ memberclient.Client = (*remoteMemberClient)(nil)
var _ projectclient.Client = (*remoteMemberClient)(nil)
