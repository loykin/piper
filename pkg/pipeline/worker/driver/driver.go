package driver

import (
	"context"
	"time"

	"github.com/piper/piper/internal/proto"
)

// Handle identifies a running runtime resource (subprocess, container, or K8s Job).
// All fields must survive a worker restart so Recover() can re-attach.
type Handle struct {
	RuntimeKey string // sanitized unique key: workerID-runID-stepName-aAttempt
	WorkerID   string
	TaskID     string
	RunID      string
	StepName   string
	Attempt    int
	// ResultPath is the HOST-side path to the AgentResult JSON.
	// Empty for K8s (result is in the pod termination log, read via K8s API).
	ResultPath string
}

// Exit is the terminal state returned by Wait.
// Exactly one of Result or InfraFailure is set, unless the driver read the
// result file and returned it directly in Result.
type Exit struct {
	// Result is set when the driver could parse the AgentResult itself
	// (e.g. K8s reads the termination log via K8s API).
	// When set it is authoritative; ResultPath is ignored.
	Result *proto.TaskResult

	// ResultPath is the HOST-side path to the AgentResult JSON file written
	// by piper agent exec. The caller reads this file to get the result.
	// Empty when Result is already set or when InfraFailure occurred.
	ResultPath string

	// InfraFailure is set when the runtime infrastructure failed (OOM, SIGKILL
	// without writing a result file, K8s Job disappeared, etc.).
	// Nil means the result file or Result field is authoritative.
	InfraFailure error
}

// ExecSpec is the ENVIRONMENT-AGNOSTIC description of what to execute.
// Each Driver translates this into its own runtime primitives internally.
// The Worker never needs to know about container paths, binary injection, or
// mount configuration — all of that lives inside the Driver.
type ExecSpec struct {
	RuntimeKey string

	// Image is the resolved container image. Empty for baremetal (no container).
	// Must be pre-resolved by the worker before calling Driver.Start(); drivers
	// must not re-derive this from the task payload.
	Image string

	// Namespace is the resolved K8s namespace. Empty for baremetal and docker
	// (ignored by those drivers). Pre-resolved and validated by the K8s worker.
	Namespace string

	// Artifact store connection (passed through to piper agent exec).
	MasterURL  string
	Token      string
	StorageURL string

	// OutputDir is the output directory on the host filesystem.
	// Baremetal uses it directly; Docker bind-mounts it into the container.
	// K8s ignores it (uses emptyDir volumes managed by Kubernetes).
	OutputDir string

	// Env carries additional environment variables injected into the execution
	// environment (e.g. PIPER_GIT_USER, PIPER_GIT_TOKEN).
	Env []string
}

// Driver manages the lifecycle of execution environments for pipeline steps.
// Implementations are responsible for:
//   - Translating ExecSpec into runtime-specific configuration (paths, mounts, args)
//   - Calling BuildAgentExec internally with the correct environment-side paths
//   - Returning a Handle whose ResultPath points to the HOST-side result file
//
// Implementations must be safe for concurrent use.
type Driver interface {
	// Start launches a new execution unit (subprocess / container / K8s Job)
	// for the given task and returns a Handle for tracking.
	Start(ctx context.Context, task *proto.Task, spec ExecSpec) (Handle, error)

	// Wait blocks until the execution unit reaches a terminal state or ctx
	// is cancelled. The returned Exit contains either a parsed Result or a
	// host-side ResultPath that the caller should read.
	Wait(ctx context.Context, handle Handle) (Exit, error)

	// Stop signals the execution unit to stop and waits up to grace for a
	// clean exit before force-killing.
	Stop(ctx context.Context, handle Handle, grace time.Duration) error

	// Recover re-attaches to execution units that survived a driver restart.
	// Returns handles for all units that are still running or whose results
	// have not yet been collected.
	Recover(ctx context.Context) ([]Handle, error)
}

// Observable is an optional interface implemented by drivers that require a
// background reconcile loop (e.g. K8s, which polls the K8s API for Job status).
// The Worker calls Observe(ctx) in a background goroutine when present.
type Observable interface {
	Observe(ctx context.Context)
}

// RunStopper is implemented by runtimes that can atomically stop all execution
// units for a run, including units not yet recovered into worker memory.
type RunStopper interface {
	StopRun(ctx context.Context, runID, namespace string) error
}
