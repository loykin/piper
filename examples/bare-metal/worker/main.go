// Worker example — run a gRPC pipeline worker
//
// Start the worker while examples/bare-metal/server is running.
// The worker connects to the master gRPC agent server and waits for tasks.
//
//	go run ./examples/bare-metal/worker
//	go run ./examples/bare-metal/worker --agent-addr=remote:9090 --master=http://remote:8080 --label=gpu
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	worker "github.com/piper/piper/pkg/pipeline/worker"
)

func main() {
	agentAddr := flag.String("agent-addr", "localhost:9090", "gRPC address of piper master agent server")
	master := flag.String("master", "http://localhost:8080", "piper server URL (for agent exec callbacks)")
	label := flag.String("label", "", "worker label (e.g. gpu, cpu, large-mem)")
	concurrency := flag.Int("concurrency", 4, "max parallel tasks")
	flag.Parse()

	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{
			Addr:        *agentAddr,
			Label:       *label,
			Concurrency: *concurrency,
		},
		Store: worker.StoreConfig{
			MasterURL: *master,
			OutputDir: os.TempDir() + "/piper-worker-outputs",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("worker starting → agent-addr=%s master=%s label=%q concurrency=%d", *agentAddr, *master, *label, *concurrency)

	if err := w.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
