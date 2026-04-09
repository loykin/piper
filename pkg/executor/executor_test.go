package executor

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
)

func cfg(t *testing.T) ExecConfig {
	return ExecConfig{
		WorkDir:   t.TempDir(),
		InputDir:  t.TempDir(),
		OutputDir: t.TempDir(),
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

func TestNew_python(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: "python"}}
	ex := New(step)
	if _, ok := ex.(*PythonExecutor); !ok {
		t.Errorf("want *PythonExecutor, got %T", ex)
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

// ─── CommandExecutor ─────────────────────────────────────────────────────────

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
	cancel() // already cancelled

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
	// nil stdout/stderr should fall back to os.Stdout/Stderr without panic
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

// ─── PythonExecutor ───────────────────────────────────────────────────────────

func TestPythonExecutor_localScript(t *testing.T) {
	// PythonExecutor는 "python" 바이너리를 직접 호출하므로
	// PATH에 "python"이 없으면 스킵
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not in PATH")
	}

	workDir := t.TempDir()
	script := workDir + "/hello.py"
	_ = os.WriteFile(script, []byte("print('hello from python')"), 0644)

	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "py",
		Run:  pipeline.Run{Type: "python", Source: "local", Path: "hello.py"},
	}
	c := cfg(t)
	c.WorkDir = workDir
	c.Stdout = &buf

	ex := &PythonExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "hello from python") {
		t.Errorf("stdout: %q", buf.String())
	}
}

func TestPythonExecutor_unknownSource(t *testing.T) {
	step := &pipeline.Step{
		Name: "py",
		Run:  pipeline.Run{Type: "python", Source: "ftp"},
	}
	ex := &PythonExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestPythonExecutor_emptyPath(t *testing.T) {
	step := &pipeline.Step{
		Name: "py",
		Run:  pipeline.Run{Type: "python", Source: "local", Path: ""},
	}
	ex := &PythonExecutor{}
	err := ex.Execute(context.Background(), step, cfg(t))
	if err == nil {
		t.Fatal("expected error for empty path")
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
