//go:build e2e

package piper

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"

	agentpkg "github.com/piper/piper/pkg/pipeline/worker/agent"
	pdriver "github.com/piper/piper/pkg/pipeline/worker/driver"
)

// TestMain intercepts subprocess invocations from the baremetal driver.
// When the driver spawns "piper agent exec --task=...", os.Args[1] is "agent".
// In normal test runs, os.Args[1] is always a -test.* flag, so there is no
// conflict. No production code changes are needed.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "agent" && os.Args[2] == "exec" {
		os.Exit(runAgentExec())
	}
	os.Exit(m.Run())
}

// runAgentExec is a minimal "piper agent exec" handler for e2e tests.
// It avoids importing cmd/piper/commands (which would create an import cycle
// with the root piper package).
func runAgentExec() int {
	var (
		taskB64    string
		outputDir  string
		inputDir   string
		storageURL string
		resultFile string
		gitUser    string
		gitToken   string
	)
	fs := flag.NewFlagSet("agent exec", flag.ContinueOnError)
	fs.StringVar(&taskB64, "task", "", "")
	fs.StringVar(&outputDir, "output-dir", "./piper-outputs", "")
	fs.StringVar(&inputDir, "input-dir", "", "")
	fs.StringVar(&storageURL, "storage-url", "", "")
	fs.StringVar(&resultFile, "result-file", "", "")
	fs.StringVar(&gitUser, "git-user", "", "")
	fs.StringVar(&gitToken, "git-token", "", "")

	// Strip "agent" and "exec" prefix from os.Args.
	args := os.Args[1:]
	for i, a := range args {
		if a == "exec" {
			args = args[i+1:]
			break
		}
	}
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "agent exec parse flags: %v\n", err)
		return 1
	}

	if len(fs.Args()) != 0 {
		fmt.Fprintf(os.Stderr, "agent exec unexpected args: %v\n", fs.Args())
		return 1
	}
	task, err := agentpkg.DecodeTask(taskB64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent exec decode task: %v\n", err)
		return 1
	}
	if gitUser == "" {
		gitUser = os.Getenv("PIPER_GIT_USER")
	}
	if gitUser == "" {
		gitUser = pdriver.EnvValue(task.Env, "PIPER_GIT_USER")
	}
	if gitToken == "" {
		gitToken = os.Getenv("PIPER_GIT_TOKEN")
	}
	if gitToken == "" {
		gitToken = pdriver.EnvValue(task.Env, "PIPER_GIT_TOKEN")
	}

	r, err := agentpkg.New(agentpkg.Config{
		OutputDir:  outputDir,
		InputDir:   inputDir,
		StorageURL: storageURL,
		GitUser:    gitUser,
		GitToken:   gitToken,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent exec runner init: %v\n", err)
		return 1
	}

	result := r.Run(context.Background(), task)

	if err := agentpkg.DeliverResult(result, resultFile); err != nil {
		fmt.Fprintf(os.Stderr, "agent exec deliver result: %v\n", err)
		return 1
	}
	return 0
}
