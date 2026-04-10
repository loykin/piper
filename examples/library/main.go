// Library 모드 예제 — in-process 실행
//
// piper를 라이브러리로 임베딩해 파이프라인을 직접 실행한다.
// 서버/worker 없이 단일 프로세스에서 동작한다.
//
//	go run ./examples/library
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/piper/piper/pkg/piper"
	_ "modernc.org/sqlite"
)

const pipelineYAML = `
apiVersion: piper/v1
kind: Pipeline

metadata:
  name: data-pipeline

spec:
  steps:
  - name: extract
    run:
      type: command
      command: ["sh", "-c", "echo 'extracting data...' && echo 'row1,row2,row3' > $PIPER_OUTPUT_DIR/data.csv"]

  - name: transform
    run:
      type: command
      command: ["sh", "-c", "echo 'transforming...' && cat $PIPER_INPUT_DIR/extract/data.csv | tr ',' '\\n' > $PIPER_OUTPUT_DIR/rows.txt"]
    depends_on: [extract]

  - name: load
    run:
      type: command
      command: ["sh", "-c", "echo 'loading...' && wc -l $PIPER_INPUT_DIR/transform/rows.txt"]
    depends_on: [transform]
`

func main() {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	p, err := piper.New(piper.Config{
		DB:        db,
		OutputDir: os.TempDir() + "/piper-example-library",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	result, err := p.Run(context.Background(), []byte(pipelineYAML))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("pipeline finished: failed=%v\n", result.Failed())
	for name, s := range result.Steps {
		status := string(s.Status)
		if s.Err != nil {
			status = "FAILED: " + s.Err.Error()
		}
		fmt.Printf("  step %-12s %s\n", name, status)
	}
}
