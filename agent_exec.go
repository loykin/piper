package piper

// This file wires agent exec interception into any binary that imports
// "github.com/loykin/piper". The baremetal driver calls os.Executable()
// to find the current binary, then runs it with "agent exec --task-file=..."
// as a subprocess. Without this init(), binaries that embed piper as a
// library would re-enter main() instead of executing the step.
//
// The init() below intercepts "agent exec" args early and exits after
// the step completes, so main() never runs in subprocess mode.
//
// This is the ONLY "agent exec" implementation: it fires (via Go's
// init-before-main ordering) before any cobra/flag dispatch in main(), so a
// separate cobra subcommand or a binary-local re-implementation can never
// actually run once it imports this package — see docs/backend/develop.md's
// "Storage Ownership Invariant" section for the history of a real bug that
// came from two such implementations drifting apart. examples/bare-metal's
// worker is the one legitimate exception: it deliberately does not import
// this package, to demonstrate the pattern standalone.

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/loykin/piper/internal/proto"
	agentpkg "github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
)

func init() {
	if len(os.Args) < 3 || os.Args[1] != "agent" || os.Args[2] != "exec" {
		return
	}
	os.Exit(runEmbeddedAgentExec())
}

func runEmbeddedAgentExec() int {
	fs := flag.NewFlagSet("agent exec", flag.ContinueOnError)
	taskB64 := fs.String("task", "", "")
	taskFile := fs.String("task-file", "", "")
	storageToken := fs.String("storage-token", "", "")
	outputDir := fs.String("output-dir", "./piper-outputs", "")
	inputDir := fs.String("input-dir", "", "")
	storageURL := fs.String("storage-url", "", "")
	resultFile := fs.String("result-file", "", "")

	args := os.Args[3:] // strip "agent exec"
	if err := fs.Parse(args); err != nil {
		slog.Error("agent exec: parse flags", "err", err)
		return 1
	}

	if len(fs.Args()) != 0 {
		slog.Error("agent exec: unexpected positional arguments", "args", fs.Args())
		return 1
	}

	task, err := agentpkg.DecodeTask(*taskB64)
	if *taskFile != "" {
		task, err = agentpkg.DecodeTaskFile(*taskFile)
	}
	if err != nil {
		slog.Error("agent exec: decode task", "err", err)
		return 1
	}

	// Storage credentials arrive as process env vars, not CLI flags — the
	// baremetal/docker drivers no longer pass --storage-url/--storage-token
	// on the command line (see agent.AgentExecConfig.StorageEnv and
	// docs/backend/develop.md's "Storage Ownership Invariant").
	resolvedStorageURL := *storageURL
	if resolvedStorageURL == "" {
		resolvedStorageURL = os.Getenv("PIPER_STORAGE_URL")
	}
	resolvedStorageToken := *storageToken
	if resolvedStorageToken == "" {
		resolvedStorageToken = os.Getenv("PIPER_STORAGE_TOKEN")
	}

	r, err := agentpkg.New(agentpkg.Config{
		StorageToken: resolvedStorageToken,
		OutputDir:    *outputDir,
		InputDir:     *inputDir,
		StorageURL:   resolvedStorageURL,
		GitUser:      pdriver.EnvValue(task.Env, "PIPER_GIT_USER"),
		GitToken:     pdriver.EnvValue(task.Env, "PIPER_GIT_TOKEN"),
	})
	if err != nil {
		slog.Error("agent exec: init runner", "err", err)
		return 1
	}

	// The baremetal driver's Stop()/cancelRun/timeout paths all work by
	// sending SIGTERM to this process (see provisr's Stop and
	// pkg/pipeline/worker/driver/baremetal). Without a handler here, the OS
	// terminates this process immediately on SIGTERM before any Go code
	// runs, leaving the step's own child process (which
	// executor.CommandExecutor puts in its own process group) orphaned and
	// still running instead of being killed by the ctx.Done() branch in
	// pkg/pipeline/executor/command.go.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	result := r.Run(ctx, task)
	if err := agentpkg.DeliverResult(truncateForTerminationLog(result, *resultFile), *resultFile); err != nil {
		slog.Error("agent exec: deliver result", "err", err)
		return 1
	}
	return 0
}

// truncateForTerminationLog trims an AgentResult JSON to fit within the
// Kubernetes termination message limit (4096 bytes) when resultFile is
// /dev/termination-log — an oversized message is silently dropped by the
// kubelet, which surfaces as a spurious "missing or unreadable result" infra
// failure instead of the step's real outcome (see
// k8slauncher.Launcher.readTerminationResult). A no-op for every other
// resultFile (baremetal/docker write ordinary files with no such limit).
func truncateForTerminationLog(result proto.TaskResult, resultFile string) proto.TaskResult {
	if resultFile != "/dev/termination-log" {
		return result
	}
	const (
		maxError  = 2048
		softLimit = 3584
		hardLimit = 4096
	)
	if len(result.Error) > maxError {
		result.Error = result.Error[:maxError] + "... [truncated]"
	}
	data, err := json.Marshal(agentpkg.AgentResult{Version: 1, Result: result})
	if err != nil || len(data) <= softLimit {
		return result
	}
	// Shrink error further until it fits.
	for len(result.Error) > 0 && len(data) > softLimit {
		cut := len(result.Error) / 2
		if cut == 0 {
			break
		}
		result.Error = result.Error[:cut] + "... [truncated]"
		data, err = json.Marshal(agentpkg.AgentResult{Version: 1, Result: result})
		if err != nil {
			break
		}
	}
	if len(data) > softLimit && len(result.Metrics) > 0 {
		metrics := make(map[string]float64, len(result.Metrics))
		keys := make([]string, 0, len(result.Metrics))
		for key, value := range result.Metrics {
			metrics[key] = value
			keys = append(keys, key)
		}
		result.Metrics = metrics
		sort.Strings(keys)
		for i := len(keys) - 1; i >= 0 && len(data) > softLimit; i-- {
			delete(result.Metrics, keys[i])
			data, err = json.Marshal(agentpkg.AgentResult{Version: 1, Result: result})
			if err != nil {
				break
			}
		}
	}
	if len(data) > hardLimit {
		result.Error = "task failed; detail exceeded termination message limit"
	}
	return result
}
