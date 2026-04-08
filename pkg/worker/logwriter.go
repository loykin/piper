package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// LogEntry는 master로 전송하는 로그 단위
type LogEntry struct {
	Ts     time.Time `json:"ts"`
	Stream string    `json:"stream"` // stdout | stderr
	Line   string    `json:"line"`
}

// logWriter는 io.Writer를 구현하며, 쓴 내용을 os.Stdout/Stderr로 tee하면서
// 동시에 라인 단위로 버퍼에 저장한다
type logWriter struct {
	stream string
	tee    io.Writer
	lines  []LogEntry
}

func newLogWriter(stream string, tee io.Writer) *logWriter {
	return &logWriter{stream: stream, tee: tee}
}

func (w *logWriter) Write(p []byte) (int, error) {
	// tee: 터미널 출력
	if w.tee != nil {
		w.tee.Write(p)
	}
	// 라인 단위로 파싱해서 버퍼에 저장
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		w.lines = append(w.lines, LogEntry{
			Ts:     time.Now(),
			Stream: w.stream,
			Line:   scanner.Text(),
		})
	}
	return len(p), nil
}

// sendLogs는 버퍼된 로그를 master로 전송한다
func (w *Worker) sendLogs(taskID string, entries []LogEntry) {
	if len(entries) == 0 {
		return
	}

	data, err := json.Marshal(entries)
	if err != nil {
		slog.Warn("log marshal error", "err", err)
		return
	}

	url := fmt.Sprintf("%s/api/tasks/%s/logs", w.cfg.MasterURL, taskID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		slog.Warn("log send error", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	w.setAuth(req)

	resp, err := w.client.Do(req)
	if err != nil {
		// 로그 전송 실패는 치명적이지 않음 — 로컬 파일에는 이미 출력됨
		slog.Warn("log send failed", "err", err)
		return
	}
	resp.Body.Close()
}

// localLogFile은 fallback으로 로컬 파일에도 로그를 저장한다
func localLogFile(outputDir, stepName string) *os.File {
	path := fmt.Sprintf("%s/%s.log", outputDir, stepName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}
