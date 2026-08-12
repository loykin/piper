// Server example — run Piper with baremetal direct-runtime.
//
// Pipeline execution runs in-process on the host — no separate worker
// binary or gRPC tunnel involved.
//
//	# Start the server
//	go run ./examples/bare-metal/server
//
//	# Submit a pipeline run
//	curl -X POST http://localhost:8080/runs \
//	  -H 'Content-Type: application/json' \
//	  -d '{"yaml": "apiVersion: piper/v1\nkind: Pipeline\n..."}'
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	piper "github.com/loykin/piper"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	p, err := piper.New(piper.Config{
		Auth:      piper.AuthConfig{Trusted: true},
		DBPath:    "./piper.db",
		OutputDir: "./piper-outputs",
		Server: piper.ServerConfig{
			Addr:                *addr,
			AllowInsecureDevKey: true,
		},
		Runtime: piper.RuntimeConfig{
			Type:      piper.RuntimeBaremetal,
			Baremetal: piper.BaremetalRuntimeConfig{MetaDir: "./piper-meta"},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("piper server starting on %s", *addr)

	if err := p.Serve(ctx, piper.ServeOption{}); err != nil {
		log.Fatal(err)
	}
}
