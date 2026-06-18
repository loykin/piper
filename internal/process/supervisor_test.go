package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loykin/provisr/core/history"
)

func TestProcessSupervisorStartReportsStoppedOnCleanExit(t *testing.T) {
	s := NewProcessSupervisor()
	statusCh := make(chan string, 1)

	_, _, err := s.Start(ProcessSpec{
		Name:    "clean-exit",
		Command: []string{"sh", "-c", "exit 0"},
		Port:    18080,
	}, func(status string) {
		statusCh <- status
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case status := <-statusCh:
		if status != "stopped" {
			t.Fatalf("status = %q, want stopped", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exit status")
	}

	if got := s.Active(); len(got) != 0 {
		t.Fatalf("active = %v, want empty", got)
	}
}

func TestProcessSupervisorStopReportsStopped(t *testing.T) {
	s := NewProcessSupervisor()
	statusCh := make(chan string, 1)

	_, _, err := s.Start(ProcessSpec{
		Name:    "stopped-exit",
		Command: []string{"sh", "-c", "sleep 5"},
		Port:    18081,
	}, func(status string) {
		statusCh <- status
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := s.Stop("stopped-exit"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	select {
	case status := <-statusCh:
		if status != "stopped" {
			t.Fatalf("status = %q, want stopped", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exit status")
	}
}

func TestProcessSupervisorFailedExitReportsFailed(t *testing.T) {
	s := NewProcessSupervisor()
	statusCh := make(chan string, 1)

	_, _, err := s.Start(ProcessSpec{
		Name:    "crash-exit",
		Command: []string{"sh", "-c", "exit 1"},
		Port:    18082,
	}, func(status string) {
		statusCh <- status
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case status := <-statusCh:
		if status != "failed" {
			t.Fatalf("status = %q, want failed", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for exit status")
	}
}

func TestProcessSupervisorKillAllStopsAll(t *testing.T) {
	s := NewProcessSupervisor()
	ch1 := make(chan string, 1)
	ch2 := make(chan string, 1)

	for _, tc := range []struct {
		name string
		port int
		ch   chan string
	}{
		{"kill-all-a", 18083, ch1},
		{"kill-all-b", 18084, ch2},
	} {
		if _, _, err := s.Start(ProcessSpec{
			Name:    tc.name,
			Command: []string{"sh", "-c", "sleep 30"},
			Port:    tc.port,
		}, func(status string) { tc.ch <- status }); err != nil {
			t.Fatalf("start %s: %v", tc.name, err)
		}
	}

	if err := s.KillAll(); err != nil {
		t.Fatalf("KillAll: %v", err)
	}

	for _, ch := range []chan string{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for KillAll exit")
		}
	}

	if got := s.Active(); len(got) != 0 {
		t.Fatalf("active after KillAll = %v, want empty", got)
	}
}

func TestProcessSupervisorRestartSameName(t *testing.T) {
	s := NewProcessSupervisor()

	ch1 := make(chan string, 1)
	if _, _, err := s.Start(ProcessSpec{
		Name:    "restartable",
		Command: []string{"sh", "-c", "sleep 30"},
		Port:    18085,
	}, func(status string) { ch1 <- status }); err != nil {
		t.Fatalf("first start: %v", err)
	}

	// Restart same name — old process should be killed, new one starts.
	ch2 := make(chan string, 1)
	if _, _, err := s.Start(ProcessSpec{
		Name:    "restartable",
		Command: []string{"sh", "-c", "sleep 30"},
		Port:    18085,
	}, func(status string) { ch2 <- status }); err != nil {
		t.Fatalf("second start: %v", err)
	}

	// First process should have exited.
	select {
	case <-ch1:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for first process exit on restart")
	}

	// New process should be active.
	found := false
	for _, name := range s.Active() {
		if name == "restartable" {
			found = true
		}
	}
	if !found {
		t.Fatal("restarted process not in Active()")
	}

	// Clean up.
	_ = s.Stop("restartable")
}

func TestExitSinkBindsCallbackToProcessPID(t *testing.T) {
	sink := newExitSink()
	statusCh := make(chan string, 1)
	sink.register("restartable", func(status string) { statusCh <- status })

	if err := sink.Send(context.Background(), history.Event{
		Type:   history.EventStop,
		Record: history.Record{Name: "restartable", PID: 100, LastStatus: "stopped"},
	}); err != nil {
		t.Fatal(err)
	}
	sink.bindPID("restartable", 200)

	select {
	case status := <-statusCh:
		t.Fatalf("stale PID event reached new callback: %q", status)
	default:
	}

	if err := sink.Send(context.Background(), history.Event{
		Type:   history.EventStop,
		Record: history.Record{Name: "restartable", PID: 200, LastStatus: "failed"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-statusCh:
		if status != "failed" {
			t.Fatalf("status = %q, want failed", status)
		}
	case <-time.After(time.Second):
		t.Fatal("matching PID event was not delivered")
	}
}

func TestProcessSupervisorStatusWhileRunning(t *testing.T) {
	s := NewProcessSupervisor()

	if _, _, err := s.Start(ProcessSpec{
		Name:    "status-check",
		Command: []string{"sh", "-c", "sleep 30"},
		Port:    18086,
	}, nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = s.Stop("status-check") }() //nolint:errcheck

	time.Sleep(100 * time.Millisecond)
	status, ok := s.Status("status-check")
	if !ok {
		t.Fatal("Status returned ok=false for running process")
	}
	if status == "stopped" || status == "failed" {
		t.Fatalf("status = %q, expected running/starting", status)
	}
}

func TestProcessSupervisorLogFileRedirectsOutput(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "sub", "process.log")

	s := NewProcessSupervisor()
	exitCh := make(chan string, 1)
	_, _, err := s.Start(ProcessSpec{
		Name:    "log-redirect",
		Command: []string{"sh", "-c", "echo hello-from-process; echo second-line"},
		LogFile: logFile,
	}, func(status string) { exitCh <- status })
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit in time")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	for _, want := range []string{"hello-from-process", "second-line"} {
		if !strings.Contains(content, want) {
			t.Fatalf("log file = %q, want to contain %q", content, want)
		}
	}
}

func TestProcessSupervisorRecoverFromPIDFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "recover.pid")
	original := NewProcessSupervisor()

	pid, _, err := original.Start(ProcessSpec{
		Name:    "recoverable",
		Command: []string{"sh", "-c", "sleep 30"},
		PIDFile: pidFile,
	}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = original.Stop("recoverable") })

	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("PID file was not written: %v", err)
	}

	recovered := NewProcessSupervisor()
	exitCh := make(chan string, 1)
	recoveredPID, running, err := recovered.Recover(ProcessSpec{
		Name:    "recoverable",
		PIDFile: pidFile,
	}, func(status string) {
		exitCh <- status
	})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !running {
		t.Fatal("recovered process is not running")
	}
	if recoveredPID != pid {
		t.Fatalf("recovered PID = %d, want %d", recoveredPID, pid)
	}

	if err := recovered.Stop("recoverable"); err != nil {
		t.Fatalf("stop recovered process: %v", err)
	}
	select {
	case status := <-exitCh:
		if status != "stopped" {
			t.Fatalf("recovered exit status = %q, want stopped", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for recovered process exit")
	}
}
