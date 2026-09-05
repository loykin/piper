// Embedded mode example — mount piper onto an existing HTTP server
//
// Pattern for attaching the piper API as a sub-path of your own web app.
// Reuses the existing app's authentication and middleware as-is. This is
// API-only, deliberately: the admin UI is a cmd/piper concern (see
// internal/ui's doc comment) and isn't part of the library's public
// surface, so a library consumer builds their own UI against the API, or
// runs the official server binary/container alongside their app instead.
//
//	go run ./examples/embedded
//
//	# piper API (sub-path mount)
//	curl http://localhost:8080/piper/runs
//
//	# Existing app API
//	curl http://localhost:8080/api/v1/hello
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	piper "github.com/loykin/piper"
)

func main() {
	p, err := piper.New(piper.Config{
		Auth:      piper.AuthConfig{Trusted: true},
		DBPath:    "./piper-embedded.db",
		OutputDir: os.TempDir() + "/piper-embedded",
		Server:    piper.ServerConfig{AllowInsecureDevKey: true},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// App router
	mux := http.NewServeMux()

	// Existing app API
	mux.HandleFunc("/api/v1/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `{"message": "hello from my app"}`)
	})

	// Mount piper's API under /piper/ — API only, no UI (see package doc above).
	piperHandler := p.HandlerContext(ctx, nil)
	mux.Handle("/piper/runs", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/runs/", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/api/", http.StripPrefix("/piper", piperHandler))

	srv := &http.Server{Addr: ":8080", Handler: mux}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	log.Println("server starting on :8080")
	log.Println("app:   http://localhost:8080/api/v1/hello")
	log.Println("piper: http://localhost:8080/piper/")

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
