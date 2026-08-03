package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/loykin/piper/pkg/pipeline/worker/driver"
	"github.com/spf13/cobra"
)

// newAgentCmd returns the "piper agent" command.
// Used as the K8s Job entrypoint. It reports only to its parent worker.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agent",
		Short:  "Agent that executes a step inside a K8s Pod",
		Hidden: true,
	}
	cmd.AddCommand(newAgentExecCmd())
	return cmd
}

type agentExecFlags struct {
	storageToken string
	taskB64      string
	taskFile     string
	outputDir    string
	inputDir     string
	storageURL   string
	resultFile   string
}

func newAgentExecCmd() *cobra.Command {
	var f agentExecFlags

	cmd := &cobra.Command{
		Use:   "exec [flags]",
		Short: "Execute a step and write the result",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The baremetal driver's Stop()/cancelRun/timeout paths all work
			// by sending SIGTERM to this process (see provisr's Stop and
			// pkg/pipeline/worker/driver/baremetal). Without a handler here,
			// the OS terminates this process immediately on SIGTERM before
			// any Go code runs, leaving the step's own child process (which
			// executor.CommandExecutor puts in its own process group)
			// orphaned and still running instead of being killed by the
			// ctx.Done() branch in pkg/pipeline/executor/command.go.
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			return runAgentExec(ctx, f)
		},
	}

	cmd.Flags().StringVar(&f.storageToken, "storage-token", "", "artifact store token")
	cmd.Flags().StringVar(&f.taskB64, "task", "", "base64-encoded proto.Task JSON")
	cmd.Flags().StringVar(&f.taskFile, "task-file", "", "path to proto.Task JSON")
	cmd.Flags().StringVar(&f.outputDir, "output-dir", "/piper-outputs", "local output directory")
	cmd.Flags().StringVar(&f.inputDir, "input-dir", "/piper-inputs", "local input directory")
	cmd.Flags().StringVar(&f.storageURL, "storage-url", "", "artifact store URL (s3://, file://, http://)")
	cmd.Flags().StringVar(&f.resultFile, "result-file", "", "path to write AgentResult JSON (required)")

	return cmd
}

func runAgentExec(ctx context.Context, f agentExecFlags) error {
	var (
		task *proto.Task
		err  error
	)
	switch {
	case f.taskFile != "":
		task, err = agent.DecodeTaskFile(f.taskFile)
	case f.taskB64 != "":
		task, err = agent.DecodeTask(f.taskB64)
	default:
		err = fmt.Errorf("one of --task-file or --task is required")
	}
	if err != nil {
		return err
	}

	r, err := agent.New(agent.Config{
		StorageToken: f.storageToken,
		OutputDir:    f.outputDir,
		InputDir:     f.inputDir,
		StorageURL:   f.storageURL,
		GitUser:      pdriver.EnvValue(task.Env, "PIPER_GIT_USER"),
		GitToken:     pdriver.EnvValue(task.Env, "PIPER_GIT_TOKEN"),
	})
	if err != nil {
		return fmt.Errorf("runner init: %w", err)
	}

	result := r.Run(ctx, task)

	if err := deliverResult(result, f.resultFile); err != nil {
		return fmt.Errorf("deliver result: %w", err)
	}

	if result.Status == proto.TaskStatusFailed {
		return fmt.Errorf("step %q failed: %s", task.StepName, result.Error)
	}
	return nil
}

// deliverResult writes the TaskResult to the parent worker's result file.
// For /dev/termination-log, the result is first truncated to fit the 4096-byte K8s limit.
func deliverResult(result proto.TaskResult, resultFile string) error {
	if resultFile == "/dev/termination-log" {
		result = truncateForTerminationLog(result)
	}
	return agent.DeliverResult(result, resultFile)
}

// truncateForTerminationLog trims an AgentResult JSON to fit within
// Kubernetes termination message limit (4096 bytes).
func truncateForTerminationLog(result proto.TaskResult) proto.TaskResult {
	const (
		maxError  = 2048
		softLimit = 3584
		hardLimit = 4096
	)
	if len(result.Error) > maxError {
		result.Error = result.Error[:maxError] + "... [truncated]"
	}
	data, err := json.Marshal(agent.AgentResult{Version: 1, Result: result})
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
		data, err = json.Marshal(agent.AgentResult{Version: 1, Result: result})
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
			data, err = json.Marshal(agent.AgentResult{Version: 1, Result: result})
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
