// Worker example — run a master-polling worker
//
// Start the worker while examples/server is running.
// The worker registers with the master, then polls for tasks to execute.
//
//	go run ./examples/bare-metal/worker
//	go run ./examples/bare-metal/worker --master=http://remote-server:8080 --label=gpu
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
	master := flag.String("master", "http://localhost:8080", "piper server URL")
	label := flag.String("label", "", "worker label (e.g. gpu, cpu, large-mem)")
	concurrency := flag.Int("concurrency", 4, "max parallel tasks")
	flag.Parse()

	w, err := worker.New(worker.Config{
		Agent: worker.AgentConfig{
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

	log.Printf("worker starting → master=%s label=%q concurrency=%d", *master, *label, *concurrency)

	if err := w.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
