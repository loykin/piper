// Server mode example — run as master server
//
// Runs piper with HTTP API and gRPC worker tunnel on one endpoint.
//
//	# Start the server
//	go run ./examples/bare-metal/server
//
//	# Start a worker in another terminal
//	go run ./examples/bare-metal/worker --master=http://localhost:8080
//
//	# Submit a pipeline run
//	curl -X POST http://localhost:8080/runs \
//	  -H 'Content-Type: application/json' \
//	  -d '{"yaml": "apiVersion: piper/v1\nkind: Pipeline\n..."}'
//
//	# List active agents
//	curl http://localhost:8080/api/agents
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	piper "github.com/piper/piper"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	p, err := piper.New(piper.Config{
		Auth:      piper.AuthConfig{Trusted: true},
		DBPath:    "./piper.db",
		OutputDir: "./piper-outputs",
		Server: piper.ServerConfig{
			Addr: *addr,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("piper server starting on %s", *addr)
	fmt.Printf("LISTEN_ADDR=%s\n", *addr)

	if err := p.Serve(ctx, piper.ServeOption{}); err != nil {
		log.Fatal(err)
	}
}
