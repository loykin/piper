package piper

import (
	"context"
	"net/http"

	"github.com/piper/piper/pkg/pipeline"
)

// Hooks는 piper의 모든 확장 포인트.
// 모든 필드는 nil 가능 — nil이면 no-op.
// Config에 포함되므로 HTTP 서버 모드, 라이브러리 모드 모두 동일하게 적용.
//
//	piper.New(piper.Config{
//	    Hooks: piper.Hooks{
//	        Auth:        myAuthFunc,
//	        OnRunEnd:    notifySlack,
//	    },
//	})
type Hooks struct {
	// ── HTTP 레이어 ──────────────────────────────────────────────
	// Middleware는 모든 요청에 적용되는 미들웨어 체인.
	// 인증, CORS, rate limit 등을 여기서 처리.
	// chi, alice 등 어떤 미들웨어 라이브러리와도 호환.
	Middleware []func(http.Handler) http.Handler

	// Auth는 모든 API 요청에 호출되는 인증 훅.
	// error 반환 시 401 응답. nil이면 인증 없음.
	Auth func(r *http.Request) error

	// ── API 훅 ───────────────────────────────────────────────────
	// error 반환 시 해당 요청을 403으로 차단.
	// 권한 체크, 입력 검증, 감사 로그 등에 활용.

	// BeforeCreateRun은 파이프라인 실행 요청 시 호출.
	// yaml을 검사하거나 실행 권한을 체크할 수 있음.
	BeforeCreateRun func(ctx context.Context, r *http.Request, yaml string) error

	// BeforeListRuns는 실행 목록 조회 시 호출.
	// RunFilter를 반환해 사용자별 목록을 필터링할 수 있음.
	BeforeListRuns func(ctx context.Context, r *http.Request) (RunFilter, error)

	// BeforeGetRun은 실행 상세 조회 시 호출.
	// 소유권 체크 등에 활용.
	BeforeGetRun func(ctx context.Context, r *http.Request, runID string) error

	// BeforeGetLogs는 로그 조회 시 호출.
	BeforeGetLogs func(ctx context.Context, r *http.Request, runID, stepName string) error

	// ── 실행 생명주기 ─────────────────────────────────────────────
	// 알림, 모니터링, 외부 시스템 연동 등에 활용.

	OnRunStart func(ctx context.Context, runID string, pl *pipeline.Pipeline)
	OnRunEnd   func(ctx context.Context, runID string, result *pipeline.RunResult)

	OnStepStart func(ctx context.Context, runID, stepName string)
	OnStepEnd   func(ctx context.Context, runID, stepName string, result *pipeline.StepResult)
}

// RunFilter는 BeforeListRuns에서 반환하는 목록 필터.
type RunFilter struct {
	// OwnerID가 있으면 해당 소유자의 run만 반환.
	// 빈 문자열이면 필터 없음.
	OwnerID string

	// PipelineName으로 필터링.
	PipelineName string
}

// ── 훅 호출 헬퍼 (내부용) ────────────────────────────────────────

func (h *Hooks) callAuth(r *http.Request) error {
	if h.Auth == nil {
		return nil
	}
	return h.Auth(r)
}

func (h *Hooks) callBeforeCreateRun(ctx context.Context, r *http.Request, yaml string) error {
	if h.BeforeCreateRun == nil {
		return nil
	}
	return h.BeforeCreateRun(ctx, r, yaml)
}

func (h *Hooks) callBeforeListRuns(ctx context.Context, r *http.Request) (RunFilter, error) {
	if h.BeforeListRuns == nil {
		return RunFilter{}, nil
	}
	return h.BeforeListRuns(ctx, r)
}

func (h *Hooks) callBeforeGetRun(ctx context.Context, r *http.Request, runID string) error {
	if h.BeforeGetRun == nil {
		return nil
	}
	return h.BeforeGetRun(ctx, r, runID)
}

func (h *Hooks) callBeforeGetLogs(ctx context.Context, r *http.Request, runID, stepName string) error {
	if h.BeforeGetLogs == nil {
		return nil
	}
	return h.BeforeGetLogs(ctx, r, runID, stepName)
}

func (h *Hooks) callOnRunStart(ctx context.Context, runID string, pl *pipeline.Pipeline) {
	if h.OnRunStart != nil {
		h.OnRunStart(ctx, runID, pl)
	}
}

func (h *Hooks) callOnRunEnd(ctx context.Context, runID string, result *pipeline.RunResult) {
	if h.OnRunEnd != nil {
		h.OnRunEnd(ctx, runID, result)
	}
}

func (h *Hooks) callOnStepStart(ctx context.Context, runID, stepName string) {
	if h.OnStepStart != nil {
		h.OnStepStart(ctx, runID, stepName)
	}
}

func (h *Hooks) callOnStepEnd(ctx context.Context, runID, stepName string, result *pipeline.StepResult) {
	if h.OnStepEnd != nil {
		h.OnStepEnd(ctx, runID, stepName, result)
	}
}
