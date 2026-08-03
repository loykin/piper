package notebookworker

import (
	"context"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/notebook"
	notebookdriver "github.com/loykin/piper/pkg/notebook/worker/driver"
)

// blockingKillAllDriver's KillAll blocks until its ctx is done, simulating a
// hung container/process runtime during shutdown.
type blockingKillAllDriver struct{}

func (blockingKillAllDriver) Start(context.Context, notebookdriver.StartRequest) (*notebookdriver.StartedHandle, error) {
	return nil, nil
}
func (blockingKillAllDriver) Stop(context.Context, string) error { return nil }
func (blockingKillAllDriver) KillAll(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (blockingKillAllDriver) Status(context.Context, string) string { return notebook.StatusStopped }

// TestKillAllShutdownIsBoundedByGracePeriod verifies that Run's deferred
// cleanup does not block indefinitely on a hung KillAll — it must be bounded
// by Config.ShutdownGrace.
func TestKillAllShutdownIsBoundedByGracePeriod(t *testing.T) {
	grace := 150 * time.Millisecond
	w := New(Config{ID: "nb-test-id", Infrastructure: InfrastructureBaremetal, ShutdownGrace: grace})
	w.driver = blockingKillAllDriver{}

	start := time.Now()
	// MasterURL is unset, so client.Run returns immediately with an error;
	// the deferred KillAll cleanup then runs against the blocking driver.
	_ = w.Run(context.Background())
	elapsed := time.Since(start)

	if elapsed > grace+500*time.Millisecond {
		t.Fatalf("Run took %v, want close to the %v ShutdownGrace budget", elapsed, grace)
	}
}
