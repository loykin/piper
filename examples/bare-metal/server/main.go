// Server mode example — run as master server
//
// Runs piper as an HTTP server with a gRPC agent server for worker dispatch.
//
//	# Start the server
//	go run ./examples/bare-metal/server
//
//	# Start a worker in another terminal
//	go run ./examples/bare-metal/worker --agent-addr=:9090
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
	agentAddr := flag.String("agent-addr", ":9090", "gRPC agent server address")
	flag.Parse()

	p, err := piper.New(piper.Config{
		DBPath:    "./piper.db",
		OutputDir: "./piper-outputs",
		Server: piper.ServerConfig{
			Addr: *addr,
		},
		Pipeline: piper.PipelineConfig{
			DispatchMode: "agent",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("piper server starting on %s (gRPC agent: %s)", *addr, *agentAddr)
	fmt.Printf("LISTEN_ADDR=%s\n", *addr)

	if err := p.Serve(ctx, piper.ServeOption{AgentAddr: *agentAddr}); err != nil {
		log.Fatal(err)
	}
}
