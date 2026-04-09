package worker

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLogWriter_singleLine(t *testing.T) {
	w := newLogWriter("stdout", nil)
	_, _ = w.Write([]byte("hello world\n"))

	if len(w.lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(w.lines))
	}
	if w.lines[0].Line != "hello world" {
		t.Errorf("got %q", w.lines[0].Line)
	}
	if w.lines[0].Stream != "stdout" {
		t.Errorf("stream: got %q", w.lines[0].Stream)
	}
}

func TestLogWriter_multipleLines(t *testing.T) {
	w := newLogWriter("stderr", nil)
	_, _ = w.Write([]byte("line1\nline2\nline3\n"))

	if len(w.lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(w.lines))
	}
	for i, want := range []string{"line1", "line2", "line3"} {
		if w.lines[i].Line != want {
			t.Errorf("line %d: want %q, got %q", i, want, w.lines[i].Line)
		}
	}
}

func TestLogWriter_noTrailingNewline(t *testing.T) {
	// bufio.Scanner ScanLines는 trailing newline 없어도 마지막 라인 방출
	w := newLogWriter("stdout", nil)
	_, _ = w.Write([]byte("no newline"))
	if len(w.lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(w.lines))
	}
	if w.lines[0].Line != "no newline" {
		t.Errorf("got %q", w.lines[0].Line)
	}
}

func TestLogWriter_eachWriteIndependent(t *testing.T) {
	// 각 Write는 독립적으로 스캔 — "part"와 "ial\n"은 별개 라인
	w := newLogWriter("stdout", nil)
	_, _ = w.Write([]byte("part"))  // → "part" (no newline, but EOF triggers scan)
	_, _ = w.Write([]byte("ial\n")) // → "ial"
	if len(w.lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(w.lines))
	}
}

func TestLogWriter_teeWriter(t *testing.T) {
	var buf bytes.Buffer
	w := newLogWriter("stdout", &buf)
	_, _ = w.Write([]byte("tee this\n"))

	if !strings.Contains(buf.String(), "tee this") {
		t.Errorf("tee not written: %q", buf.String())
	}
}

func TestLogWriter_timestamp(t *testing.T) {
	before := time.Now()
	w := newLogWriter("stdout", nil)
	_, _ = w.Write([]byte("ts check\n"))
	after := time.Now()

	if len(w.lines) != 1 {
		t.Fatal("no lines")
	}
	ts := w.lines[0].Ts
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp out of range: %v", ts)
	}
}

func TestLogWriter_empty(t *testing.T) {
	w := newLogWriter("stdout", nil)
	n, err := w.Write([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("want 0, got %d", n)
	}
	if len(w.lines) != 0 {
		t.Errorf("want no lines, got %d", len(w.lines))
	}
}
