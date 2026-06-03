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
