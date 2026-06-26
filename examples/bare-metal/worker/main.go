// Worker example — run a gRPC pipeline worker
//
// This binary serves dual roles:
//  1. Worker mode (default): connects to master gRPC, receives and dispatches tasks.
//  2. Agent exec mode: invoked as a subprocess by the baremetal driver for each step.
//
// The baremetal driver calls os.Executable() + "agent exec --task=...", so both modes
// must live in the same binary — exactly like the full piper CLI.
//
//	go run ./examples/bare-metal/worker
//	go run ./examples/bare-metal/worker --master=http://remote:8080
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	worker "github.com/piper/piper/pkg/pipeline/worker"
	"github.com/piper/piper/pkg/pipeline/worker/agent"
)

func main() {
	// Dispatch to agent exec handler when invoked by the baremetal driver.
	if len(os.Args) >= 3 && os.Args[1] == "agent" && os.Args[2] == "exec" {
		runAgentExec(os.Args[3:])
		return
	}

	master := flag.String("master", "http://localhost:8080", "single piper master endpoint")
	workerToken := flag.String("worker-token", "", "worker-to-master authentication token")
	storageToken := flag.String("storage-token", "", "artifact storage authentication token")
	label := flag.String("label", "", "worker label (e.g. gpu, cpu, large-mem)")
	concurrency := flag.Int("concurrency", 4, "max parallel tasks")
	metaDir := flag.String("meta-dir", "", "metadata directory for job state (default: $TMPDIR/piper-meta)")
	flag.Parse()

	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{
			MasterURL:   *master,
			WorkerToken: *workerToken,
			Label:       *label,
			Concurrency: *concurrency,
		},
		Store: worker.StoreConfig{
			StorageToken: *storageToken,
			OutputDir:    os.TempDir() + "/piper-worker-outputs",
		},
		Baremetal: worker.BaremetalConfig{
			MetaDir: *metaDir,
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("worker starting → master=%s label=%q concurrency=%d", *master, *label, *concurrency)

	if err := w.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// runAgentExec parses agent exec flags and runs the step.
// Called when this binary is invoked as "<worker> agent exec --task=...".
func runAgentExec(args []string) {
	fs := flag.NewFlagSet("agent exec", flag.ExitOnError)
	storageToken := fs.String("storage-token", "", "artifact storage authentication token")
	taskB64 := fs.String("task", "", "base64-encoded proto.Task JSON")
	outputDir := fs.String("output-dir", "/piper-outputs", "local output directory")
	inputDir := fs.String("input-dir", "/piper-inputs", "local input directory")
	storageURL := fs.String("storage-url", "", "artifact store URL")
	resultFile := fs.String("result-file", "", "path to write AgentResult JSON")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "agent exec: parse flags: %v\n", err)
		os.Exit(1)
	}

	if len(fs.Args()) != 0 {
		_, _ = fmt.Fprintf(os.Stderr, "agent exec: unexpected positional arguments: %v\n", fs.Args())
		os.Exit(1)
	}
	task, err := agent.DecodeTask(*taskB64)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "agent exec: decode task: %v\n", err)
		os.Exit(1)
	}

	r, err := agent.New(agent.Config{
		StorageToken: *storageToken,
		OutputDir:    *outputDir,
		InputDir:     *inputDir,
		StorageURL:   *storageURL,
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "agent exec: init runner: %v\n", err)
		os.Exit(1)
	}

	result := r.Run(context.Background(), task)
	if err := agent.DeliverResult(result, *resultFile); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "agent exec: deliver result: %v\n", err)
		os.Exit(1)
	}
}
