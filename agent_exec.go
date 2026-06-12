package piper

// This file wires agent exec interception into any binary that imports
// "github.com/piper/piper". The baremetal driver calls os.Executable()
// to find the current binary, then runs it with "agent exec --task=..."
// as a subprocess. Without this init(), binaries that embed piper as a
// library would re-enter main() instead of executing the step.
//
// The init() below intercepts "agent exec" args early and exits after
// the step completes, so main() never runs in subprocess mode.

import (
	"context"
	"flag"
	"log/slog"
	"os"

	agentpkg "github.com/piper/piper/pkg/pipeline/worker/agent"
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
	masterURL := fs.String("master", "", "")
	workerToken := fs.String("worker-token", "", "")
	storageToken := fs.String("storage-token", "", "")
	outputDir := fs.String("output-dir", "./piper-outputs", "")
	inputDir := fs.String("input-dir", "", "")
	storageURL := fs.String("storage-url", "", "")
	reportMode := fs.String("report-mode", string(agentpkg.ReportModeFile), "")
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
	if err != nil {
		slog.Error("agent exec: decode task", "err", err)
		return 1
	}

	r, err := agentpkg.New(agentpkg.Config{
		MasterURL:    *masterURL,
		WorkerToken:  *workerToken,
		StorageToken: *storageToken,
		OutputDir:    *outputDir,
		InputDir:     *inputDir,
		StorageURL:   *storageURL,
	})
	if err != nil {
		slog.Error("agent exec: init runner", "err", err)
		return 1
	}

	result := r.Run(context.Background(), task)
	if err := agentpkg.DeliverResult(result, agentpkg.ReportMode(*reportMode), *resultFile, r); err != nil {
		slog.Error("agent exec: deliver result", "err", err)
		return 1
	}
	return 0
}
