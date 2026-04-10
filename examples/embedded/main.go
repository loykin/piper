// Embedded 모드 예제 — 기존 HTTP 서버에 piper 마운트
//
// 자신의 웹 앱에 piper API를 서브패스로 붙이는 패턴.
// 인증/미들웨어를 기존 앱 것을 그대로 재사용한다.
//
//	go run ./examples/embedded
//
//	# piper API (서브패스 마운트)
//	curl http://localhost:8080/piper/runs
//
//	# 기존 앱 API
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

	// 기존 앱 라우터
	mux := http.NewServeMux()

	// 기존 앱 API
	mux.HandleFunc("/api/v1/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `{"message": "hello from my app"}`)
	})

	// piper API + UI를 /piper/ 아래에 마운트
	piperHandler := p.Handler(nil)
	piperUI := p.UIHandler()
	mux.Handle("/piper/runs", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/runs/", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/api/", http.StripPrefix("/piper", piperHandler))
	mux.Handle("/piper/", http.StripPrefix("/piper", piperUI))

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
