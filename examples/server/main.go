// Server mode example — run as master server
//
// Runs piper as an HTTP server.
// Workers register with this server and poll for tasks to execute.
//
//	# Start the server
//	go run ./examples/server
//
//	# Start a worker in another terminal
//	go run ./examples/worker
//
//	# Submit a pipeline run
//	curl -X POST http://localhost:8080/runs \
//	  -H 'Content-Type: application/json' \
//	  -d '{"yaml": "apiVersion: piper/v1\nkind: Pipeline\n..."}'
//
//	# List active workers
//	curl http://localhost:8080/api/workers
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	piper "github.com/piper/piper"
)

func main() {
	p, err := piper.New(piper.Config{
		DBPath:    "./piper.db",
		OutputDir: "./piper-outputs",
		Server: piper.ServerConfig{
			Addr: ":8080",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("piper server starting on :8080")
	log.Println("UI: http://localhost:8080")
	log.Println("API: http://localhost:8080/runs")

	if err := p.Serve(ctx, piper.ServeOption{}); err != nil {
		log.Fatal(err)
	}
}
