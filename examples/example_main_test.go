//go:build example_e2e

package examples

import (
	"context"
	"flag"
	"os"
	"testing"

	agentpkg "github.com/piper/piper/pkg/pipeline/worker/agent"
)

// TestMain intercepts subprocess invocations from the baremetal driver.
// When the driver spawns "<test-binary> agent exec --task=...", os.Args[1] is "agent".
// In normal test runs, os.Args[1] is always a -test.* flag, so there is no conflict.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "agent" && os.Args[2] == "exec" {
		os.Exit(runAgentExecInExamples())
	}
	os.Exit(m.Run())
}

func runAgentExecInExamples() int {
	var (
		taskB64      string
		storageToken string
		outputDir    string
		inputDir     string
		storageURL   string
		resultFile   string
	)
	fs := flag.NewFlagSet("agent exec", flag.ContinueOnError)
	fs.StringVar(&taskB64, "task", "", "")
	fs.StringVar(&storageToken, "storage-token", "", "")
	fs.StringVar(&outputDir, "output-dir", "./piper-outputs", "")
	fs.StringVar(&inputDir, "input-dir", "", "")
	fs.StringVar(&storageURL, "storage-url", "", "")
	fs.StringVar(&resultFile, "result-file", "", "")

	args := os.Args[1:]
	for i, a := range args {
		if a == "exec" {
			args = args[i+1:]
			break
		}
	}
	_ = fs.Parse(args)

	if len(fs.Args()) != 0 {
		return 1
	}
	task, err := agentpkg.DecodeTask(taskB64)
	if err != nil {
		return 1
	}

	r, err := agentpkg.New(agentpkg.Config{
		StorageToken: storageToken,
		OutputDir:    outputDir,
		InputDir:     inputDir,
		StorageURL:   storageURL,
	})
	if err != nil {
		return 1
	}

	result := r.Run(context.Background(), task)

	if err := agentpkg.DeliverResult(result, resultFile); err != nil {
		return 1
	}
	return 0
}
