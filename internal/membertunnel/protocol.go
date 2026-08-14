// Package membertunnel implements the outbound gRPC tunnel a remote Member
// dials to reach its Home (fed.md §13.4) — the network transport for
// internal/memberclient.Client, alongside the in-process implementation in
// the root package's memberclient_local.go.
//
// Deliberately excluded from this package, with a documented reattachment
// point for later: per-Member credential rotation/revocation (today: one
// static token per Member, compared for equality — see server.go) and Home
// HA (exactly one Home process is assumed). SubmitRun and generic project
// mutations are durably deduplicated by the owning Member and can be retried
// across a reconnect.
package membertunnel

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/loykin/piper/internal/logstore"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
)

// Method names shared by the Member-side dispatch table (dispatch.go) and
// the Home-side RemoteMemberClient callers (remote.go) — each must match a
// memberclient.Client method 1:1. ServeArtifact has no dedicated method: it
// reads ranged chunks through MethodProjectRequest (see remote.go).
const (
	MethodSubmitRun      = "SubmitRun"
	MethodSubmitSweep    = "SubmitSweep"
	MethodListRuns       = "ListRuns"
	MethodGetRun         = "GetRun"
	MethodCancelRun      = "CancelRun"
	MethodRerunRun       = "RerunRun"
	MethodDeleteRun      = "DeleteRun"
	MethodListSteps      = "ListSteps"
	MethodRetryStep      = "RetryStep"
	MethodQueryLogs      = "QueryLogs"
	MethodQueryMetrics   = "QueryMetrics"
	MethodListArtifacts  = "ListArtifacts"
	MethodProjectRequest = "ProjectRequest"
)

// callEnvelope wraps a method's request DTO with the AuthContext/ProjectRef
// Home resolved for this call. Auth is signed and bound to Ref before it
// crosses the tunnel; Member verifies both identity and expiry before
// dispatch. ProjectRef itself is routing data rather than a secret.
type callEnvelope[T any] struct {
	Auth memberclient.AuthContext `json:"auth"`
	Ref  project.ProjectRef       `json:"ref"`
	Req  T                        `json:"req"`
}

// Wrapper request DTOs for memberclient.Client methods that take more than
// one trailing argument after (ctx, auth, ref) — the wire envelope needs a
// single Req value per call. Methods with zero or one trailing argument use
// that argument (or struct{}) directly as Req; see dispatch.go/remote.go.
type (
	RerunRunRequest struct {
		RunID      string `json:"run_id"`
		FailedOnly bool   `json:"failed_only"`
	}
	RetryStepRequest struct {
		RunID    string `json:"run_id"`
		StepName string `json:"step_name"`
	}
	QueryLogsRequest struct {
		RunID    string `json:"run_id"`
		StepName string `json:"step_name"`
		AfterID  int64  `json:"after_id"`
	}
	QueryMetricsRequest struct {
		RunID    string `json:"run_id"`
		StepName string `json:"step_name"`
	}
)

// logLines/metrics exist only so QueryLogs/QueryMetrics have a named Resp
// type to hang generic instantiation on (bare []*logstore.Line works fine
// as a generic type argument — these aliases just keep call sites in
// dispatch.go/remote.go readable).
type (
	logLines   = []*logstore.Line
	logMetrics = []*logstore.Metric
)

// encodeCall marshals (auth, ref, req) into a callEnvelope and returns the
// JSON bytes for a MemberRPCCommand payload. Used by remote.go (Home side).
func encodeCall[Req any](auth memberclient.AuthContext, ref project.ProjectRef, req Req) ([]byte, error) {
	return json.Marshal(callEnvelope[Req]{Auth: auth, Ref: ref, Req: req})
}

func verifyCall(payload []byte, key, homeID, memberID, method string, now time.Time) error {
	var env callEnvelope[json.RawMessage]
	if err := json.Unmarshal(payload, &env); err != nil {
		return fmt.Errorf("membertunnel: decode authorization envelope: %w", err)
	}
	if env.Ref.HomeID != homeID || env.Ref.MemberID != memberID {
		return fmt.Errorf("membertunnel: delegated project owner does not match this connection")
	}
	if err := memberclient.VerifyDelegation(env.Auth, env.Ref, method, env.Req, key, now); err != nil {
		return fmt.Errorf("membertunnel: %w", err)
	}
	return nil
}

// decodeCall is the Member-side counterpart, used by dispatch.go.
func decodeCall[Req any](payload []byte) (callEnvelope[Req], error) {
	var env callEnvelope[Req]
	if err := json.Unmarshal(payload, &env); err != nil {
		return env, fmt.Errorf("membertunnel: decode request: %w", err)
	}
	return env, nil
}
