// Embedded mode example — mount piper onto an existing HTTP server
//
// Pattern for attaching the piper API as a sub-path of your own web app.
// Reuses the existing app's authentication and middleware as-is.
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

	"github.com/piper/piper/pkg/piper"
	"github.com/piper/piper/pkg/ui"
)

func main() {
	p, err := piper.New(piper.Config{
		DBPath:    "./piper-embedded.db",
		OutputDir: os.TempDir() + "/piper-embedded",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	// App router
	mux := http.NewServeMux()

	// Existing app API
	mux.HandleFunc("/api/v1/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `{"message": "hello from my app"}`)
	})

	// Mount piper API + UI under /piper/
	// Only import pkg/ui when the UI is needed
	piperHandler := p.Handler(nil)
	mux.Handle("/piper/runs", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/runs/", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/api/", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/", http.StripPrefix("/piper", ui.Handler()))

	srv := &http.Server{Addr: ":8080", Handler: mux}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

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
