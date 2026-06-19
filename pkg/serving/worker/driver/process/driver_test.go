package process

import (
	"context"
	"testing"
	"time"

	"github.com/piper/piper/pkg/serving"
	servingdriver "github.com/piper/piper/pkg/serving/worker/driver"
	"github.com/piper/piper/pkg/serving/worker/driver/drivertest"
)

func TestProcessDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() servingdriver.Driver {
		return New(Config{WorkerID: "contract-test"})
	})
}

func TestProcessRecoverableContract(t *testing.T) {
	drivertest.RunRecoverableContract(t, func() interface {
		servingdriver.Driver
		servingdriver.Recoverable
	} {
		d := New(Config{WorkerID: "contract-test"})
		d.pidDir = t.TempDir()
		return d
	})
}

var _ servingdriver.Driver = (*Driver)(nil)
var _ servingdriver.Recoverable = (*Driver)(nil)

func TestRecoverReportsTerminalWithoutRegisteringRecovered(t *testing.T) {
	d := New(Config{WorkerID: "terminal-test"})
	d.pidDir = t.TempDir()
	meta := processMeta{ProjectID: "project-a", Name: "demo", RuntimeName: "project-a__demo", Port: 18080}
	if err := d.writeMeta(meta.RuntimeName, meta); err != nil {
		t.Fatal(err)
	}
	var recovered bool
	var terminal servingdriver.RecoveredHandle
	var status string
	if err := d.Recover(context.Background(), func(servingdriver.RecoveredHandle) func(string) {
		recovered = true
		return func(string) {}
	}, func(handle servingdriver.RecoveredHandle, got string) {
		terminal, status = handle, got
	}); err != nil {
		t.Fatal(err)
	}
	if recovered {
		t.Fatal("terminal process was reported as recovered")
	}
	if terminal.RuntimeName != meta.RuntimeName || terminal.Name != meta.Name {
		t.Fatalf("terminal handle = %#v", terminal)
	}
	if status != serving.StatusStopped {
		t.Fatalf("status = %q, want %q", status, serving.StatusStopped)
	}
}

func TestDeployValidationStopsSink(t *testing.T) {
	sink := &countingSink{}
	d := New(Config{WorkerID: "validation-test"})
	if _, err := d.Deploy(context.Background(), servingdriver.DeployRequest{LogSink: sink}); err == nil {
		t.Fatal("expected validation error")
	}
	if sink.stops != 1 {
		t.Fatalf("sink stopped %d times, want 1", sink.stops)
	}
}

type countingSink struct{ stops int }

func (*countingSink) Append(string, string, string, string, time.Time) {}
func (s *countingSink) Stop()                                          { s.stops++ }
