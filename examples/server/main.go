// Server 모드 예제 — master 서버 실행
//
// piper를 HTTP 서버로 실행한다.
// worker들이 이 서버에 등록하고 task를 폴링해서 실행한다.
//
//	# 서버 시작
//	go run ./examples/server
//
//	# 다른 터미널에서 worker 시작
//	go run ./examples/worker
//
//	# 파이프라인 실행 요청
//	curl -X POST http://localhost:8080/runs \
//	  -H 'Content-Type: application/json' \
//	  -d '{"yaml": "apiVersion: piper/v1\nkind: Pipeline\n..."}'
//
//	# 활성 worker 목록 조회
//	curl http://localhost:8080/api/workers
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/piper/piper/pkg/piper"
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
