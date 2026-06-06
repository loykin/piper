package workload

import (
	"testing"
	"time"
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

func TestProcessSupervisorStatusWhileRunning(t *testing.T) {
	s := NewProcessSupervisor()

	if _, _, err := s.Start(ProcessSpec{
		Name:    "status-check",
		Command: []string{"sh", "-c", "sleep 30"},
		Port:    18086,
	}, nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop("status-check") //nolint:errcheck

	time.Sleep(100 * time.Millisecond)
	status, ok := s.Status("status-check")
	if !ok {
		t.Fatal("Status returned ok=false for running process")
	}
	if status == "stopped" || status == "failed" {
		t.Fatalf("status = %q, expected running/starting", status)
	}
}
