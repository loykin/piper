package executor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/internal/srcfetch"
	"github.com/piper/piper/internal/testutil"
	"github.com/piper/piper/pkg/manifest"
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

// python has no dedicated type — handled as command
func TestNew_pythonIsCommand(t *testing.T) {
	step := &pipeline.Step{Run: pipeline.Run{Type: "python"}}
	ex := New(step)
	if _, ok := ex.(*CommandExecutor); !ok {
		t.Errorf("type=python should map to CommandExecutor, got %T", ex)
	}
}

// ─── CommandExecutor (no source) ─────────────────────────────────────────────

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

// TestCommandExecutor_processGPUs verifies that driver.process.gpus flows into
// CUDA_VISIBLE_DEVICES for baremetal step execution. No real GPU is required —
// the test only checks that the environment variable is injected correctly.
func TestCommandExecutor_processGPUs(t *testing.T) {
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "gpu-step",
		Driver: manifest.DriverSpec{
			Process: &manifest.DriverProcessSpec{GPUs: "0,1"},
		},
		Run: pipeline.Run{Command: []string{"sh", "-c", `printf "%s" "$CUDA_VISIBLE_DEVICES"`}},
	}
	c := cfg(t)
	c.GPUs = func() string {
		if step.Driver.Process != nil {
			return step.Driver.Process.GPUs
		}
		return ""
	}()
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "0,1" {
		t.Errorf("CUDA_VISIBLE_DEVICES = %q, want 0,1", got)
	}
}

// TestCommandExecutor_resourcesGPUNotInjectedAsCUDA verifies that driver.resources.gpu
// (a scheduler quantity hint like "1") is NOT injected as CUDA_VISIBLE_DEVICES.
// Only driver.process.gpus should set the device selector.
func TestCommandExecutor_resourcesGPUNotInjectedAsCUDA(t *testing.T) {
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "k8s-step",
		Driver: manifest.DriverSpec{
			K8s: &manifest.DriverK8sSpec{Resources: manifest.ResourceSpec{GPU: "1"}}, // K8s resource request, not a device ID
		},
		Run: pipeline.Run{Command: []string{"sh", "-c", `printf "%s" "${CUDA_VISIBLE_DEVICES:-unset}"`}},
	}
	c := cfg(t)
	// GPUs is empty because driver.resources.gpu is a count, not a device selector
	c.GPUs = func() string {
		if step.Driver.Process != nil {
			return step.Driver.Process.GPUs
		}
		return ""
	}()
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "unset" {
		t.Errorf("CUDA_VISIBLE_DEVICES should not be set from resources.gpu, got %q", got)
	}
}

func TestCommandExecutor_stepEnv(t *testing.T) {
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "env",
		Options: manifest.SpecOptions{Env: []manifest.EnvVar{
			{Name: "MASTER_ADDR", Value: "trainer-0"},
			{Name: "WORLD_SIZE", Value: "4"},
		}},
		Run: pipeline.Run{Command: []string{"sh", "-c", "printf '%s:%s' \"$MASTER_ADDR\" \"$WORLD_SIZE\""}},
	}
	c := cfg(t)
	c.Stdout = &buf

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "trainer-0:4" {
		t.Errorf("stdout = %q, want trainer-0:4", got)
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
	// Fetch a script from an HTTP server and verify that PIPER_SCRIPT_PATH is injected
	script := []byte("#!/bin/sh\necho 'from http'")
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(script)
	}))

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
	// After source fetch, verify the WorkDir is changed to fetchDir and the file is visible
	script := []byte("hello content")
	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(script)
	}))

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

func TestCommandExecutor_gitSourceWithBasicAuth(t *testing.T) {
	repoURL := startAuthenticatedExecutorGitHTTPRepo(t, "run.sh", "echo git-source-ok\n", "git-user", "git-token")
	var buf bytes.Buffer
	step := &pipeline.Step{
		Name: "git-run",
		Run: pipeline.Run{
			Source:  "git",
			Repo:    repoURL,
			Branch:  "main",
			Path:    "run.sh",
			Command: []string{"sh", "-c", "sh $PIPER_SCRIPT_PATH"},
		},
	}
	c := cfg(t)
	c.Stdout = &buf
	c.SourceCfg = srcfetch.Config{GitUser: "git-user", GitToken: "git-token"}

	ex := &CommandExecutor{}
	if err := ex.Execute(context.Background(), step, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "git-source-ok") {
		t.Fatalf("expected git script output, got %q", buf.String())
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

func startAuthenticatedExecutorGitHTTPRepo(t *testing.T, fileName, content, user, token string) string {
	t.Helper()
	repoDir := t.TempDir()
	runExecutorGit(t, repoDir, "init", "-b", "main")
	runExecutorGit(t, repoDir, "config", "user.email", "test@test")
	runExecutorGit(t, repoDir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(repoDir, fileName), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runExecutorGit(t, repoDir, "add", fileName)
	runExecutorGit(t, repoDir, "commit", "-m", "init")

	bareDir := filepath.Join(t.TempDir(), "repo.git")
	runExecutorGit(t, "", "clone", "--bare", repoDir, bareDir)

	srv := testutil.NewIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotToken, ok := r.BasicAuth()
		if !ok || gotUser != user || gotToken != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		serveExecutorGitHTTPBackend(t, w, r, filepath.Dir(bareDir))
	}))
	return srv.URL + "/repo.git"
}

func runExecutorGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func serveExecutorGitHTTPBackend(t *testing.T, w http.ResponseWriter, r *http.Request, projectRoot string) {
	t.Helper()
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "http-backend")
	cmd.Env = append(os.Environ(),
		"GIT_PROJECT_ROOT="+projectRoot,
		"GIT_HTTP_EXPORT_ALL=1",
		"REQUEST_METHOD="+r.Method,
		"PATH_INFO="+r.URL.Path,
		"QUERY_STRING="+r.URL.RawQuery,
		"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		"CONTENT_LENGTH="+r.Header.Get("Content-Length"),
	)
	cmd.Stdin = bytes.NewReader(body)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git http-backend: %v", err)
	}
	header, payload, ok := bytes.Cut(out, []byte("\r\n\r\n"))
	if !ok {
		header, payload, ok = bytes.Cut(out, []byte("\n\n"))
	}
	if !ok {
		t.Fatalf("git http-backend returned malformed response: %q", string(out))
	}
	status := http.StatusOK
	for _, line := range bytes.Split(header, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		key := string(bytes.TrimSpace(name))
		val := string(bytes.TrimSpace(value))
		if strings.EqualFold(key, "Status") {
			if strings.HasPrefix(val, "403") {
				status = http.StatusForbidden
			} else if strings.HasPrefix(val, "404") {
				status = http.StatusNotFound
			}
			continue
		}
		w.Header().Add(key, val)
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

// ─── CommandExecutor (source: local) ─────────────────────────────────────────

func TestCommandExecutor_localSource_usesWorkDir(t *testing.T) {
	// source: local uses the WorkDir as-is
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

// ─── ExecConfig.Env ──────────────────────────────────────────────────────────

func TestEnv_builtinVars(t *testing.T) {
	scheduledAt := time.Date(2026, 6, 27, 14, 0, 0, 0, time.UTC)
	runStartedAt := time.Date(2026, 6, 27, 14, 0, 5, 0, time.UTC) // 5s late
	intervalEnd := time.Date(2026, 6, 27, 15, 0, 0, 0, time.UTC)

	c := ExecConfig{
		RunID:    "run-1",
		StepName: "step-1",
		Vars: proto.BuiltinVars{
			ScheduledAt:     &scheduledAt,
			RunStartedAt:    &runStartedAt,
			DataIntervalEnd: &intervalEnd,
		},
	}
	env := c.Env()
	find := func(prefix string) string {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return strings.TrimPrefix(e, prefix)
			}
		}
		return ""
	}

	if got := find("PIPER_SCHEDULED_AT="); got != "2026-06-27T14:00:00Z" {
		t.Errorf("PIPER_SCHEDULED_AT = %q", got)
	}
	if got := find("PIPER_RUN_STARTED_AT="); got != "2026-06-27T14:00:05Z" {
		t.Errorf("PIPER_RUN_STARTED_AT = %q", got)
	}
	if got := find("PIPER_DATA_INTERVAL_END="); got != "2026-06-27T15:00:00Z" {
		t.Errorf("PIPER_DATA_INTERVAL_END = %q", got)
	}
}

func TestEnv_adHocRun_noScheduledVars(t *testing.T) {
	runStartedAt := time.Now().UTC()
	c := ExecConfig{
		RunID:    "run-2",
		StepName: "step-2",
		Vars:     proto.BuiltinVars{RunStartedAt: &runStartedAt},
	}
	env := c.Env()
	for _, e := range env {
		if strings.HasPrefix(e, "PIPER_SCHEDULED_AT=") {
			t.Error("PIPER_SCHEDULED_AT should not be set for ad-hoc run")
		}
		if strings.HasPrefix(e, "PIPER_DATA_INTERVAL_END=") {
			t.Error("PIPER_DATA_INTERVAL_END should not be set for ad-hoc run")
		}
	}
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PIPER_RUN_STARTED_AT=") {
			found = true
		}
	}
	if !found {
		t.Error("PIPER_RUN_STARTED_AT should be set for all runs")
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
