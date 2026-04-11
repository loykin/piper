package executor

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

func cfg(t *testing.T) ExecConfig {
	return ExecConfig{
		WorkDir:   t.TempDir(),
		InputDir:  t.TempDir(),
		OutputDir: t.TempDir(),
		StepName:  t.Name(),
	}
}

// ─── New ─────────────────────────────────────────────────────────────────────

func TestNew_command(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: "command"}}
	ex := New(step)
	if _, ok := ex.(*CommandExecutor); !ok {
		t.Errorf("want *CommandExecutor, got %T", ex)
	}
}

func TestNew_notebook(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: "notebook"}}
	ex := New(step)
	if _, ok := ex.(*NotebookExecutor); !ok {
		t.Errorf("want *NotebookExecutor, got %T", ex)
	}
}

func TestNew_defaultIsCommand(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: ""}}
	ex := New(step)
	if _, ok := ex.(*CommandExecutor); !ok {
		t.Errorf("unknown type should default to CommandExecutor, got %T", ex)
	}
}

// python은 별도 타입 없음 — command로 처리
func TestNew_pythonIsCommand(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: "python"}}
	ex := New(step)
	if _, ok := ex.(*CommandExecutor); !ok {
		t.Errorf("type=python should map to CommandExecutor, got %T", ex)
	}
}

// ─── CommandExecutor (source 없음) ───────────────────────────────────────────

func TestCommandExecutor_success(t *testing.T) {
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "echo",
		Run:  pipeline.Run{Command: []string{"echo", "hello"}},
	}
	c := cfg(t)
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", buf.String())
	}
}

func TestCommandExecutor_capturesEnv(t *testing.T) {
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "env",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "echo $PIPER_OUTPUT_DIR"}},
	}
	c := cfg(t)
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), c.OutputDir) {
		t.Errorf("PIPER_OUTPUT_DIR not in env, stdout: %q", buf.String())
	}
}

func TestCommandExecutor_emptyCommand(t *testing.T) {
	step := &pipeline.Step{Name: "empty", Run: pipeline.Run{Command: nil}}
	ex := &CommandExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestCommandExecutor_nonZeroExit(t *testing.T) {
	step := &pipeline.Step{
		Name: "fail",
		Run:  pipeline.Run{Command: []string{"sh", "-c", "exit 1"}},
	}
	ex := &CommandExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
}

func TestCommandExecutor_contextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	step := &pipeline.Step{
		Name: "sleep",
		Run:  pipeline.Run{Command: []string{"sleep", "10"}},
	}
	ex := &CommandExecutor{}
	err := ex.Execute(ctx, step, cfg(t))
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestCommandExecutor_nilWriters(t *testing.T) {
	step := &pipeline.Step{
		Name: "ok",
		Run:  pipeline.Run{Command: []string{"true"}},
	}
	c := cfg(t)
	c.Stdout = nil
	c.Stderr = nil

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
}

// ─── CommandExecutor (source: http) ──────────────────────────────────────────

func TestCommandExecutor_httpSource_scriptPath(t *testing.T) {
	// HTTP 서버에서 스크립트를 fetch하고 PIPER_SCRIPT_PATH가 주입되는지 확인
	script := []byte("#!/bin/sh\necho 'from http'")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(script)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "http-run",
		Run: pipeline.Run{
			Source:  "http",
			URL:     srv.URL + "/run.sh",
			Command: []string{"sh", "-c", "echo $PIPER_SCRIPT_PATH"},
		},
	}
	c := cfg(t)
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "run.sh") {
		t.Errorf("PIPER_SCRIPT_PATH should contain filename, got %q", buf.String())
	}
}

func TestCommandExecutor_httpSource_workDirIsFetchDir(t *testing.T) {
	// source fetch 후 WorkDir이 fetchDir로 바뀌어 파일이 보이는지 확인
	script := []byte("hello content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(script)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "ls-run",
		Run: pipeline.Run{
			Source:  "http",
			URL:     srv.URL + "/data.txt",
			Command: []string{"sh", "-c", "cat data.txt"},
		},
	}
	c := cfg(t)
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "hello content") {
		t.Errorf("expected file content, got %q", buf.String())
	}
}

func TestCommandExecutor_unknownSource(t *testing.T) {
	step := &pipeline.Step{
		Name: "bad",
		Run:  pipeline.Run{Source: "ftp", Command: []string{"echo", "x"}},
	}
	ex := &CommandExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

// ─── CommandExecutor (source: local) ─────────────────────────────────────────

func TestCommandExecutor_localSource_usesWorkDir(t *testing.T) {
	// source: local 이면 WorkDir 그대로 사용
	workDir := t.TempDir()
	_ = os.WriteFile(workDir+"/hello.sh", []byte("echo 'local script'"), 0755)

	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "local-run",
		Run: pipeline.Run{
			Source:  "local",
			Path:    "hello.sh",
			Command: []string{"sh", "hello.sh"},
		},
	}
	c := cfg(t)
	c.WorkDir = workDir
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "local script") {
		t.Errorf("stdout: %q", buf.String())
	}
}

// ─── NotebookExecutor ─────────────────────────────────────────────────────────

func TestNotebookExecutor_unknownSource(t *testing.T) {
	step := &pipeline.Step{
		Name: "nb",
		Run:  pipeline.Run{Type: "notebook", Source: "ftp"},
	}
	ex := &NotebookExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestNotebookExecutor_emptyPath(t *testing.T) {
	step := &pipeline.Step{
		Name: "nb",
		Run:  pipeline.Run{Type: "notebook", Source: "local", Path: ""},
	}
	ex := &NotebookExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}
