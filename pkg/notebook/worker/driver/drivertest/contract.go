// Package drivertest provides a reusable contract test suite for notebookdriver.Driver
// implementations. Import this package from each driver's _test.go and call the Run
// functions so every backend is verified against the same behavioral contract.
package drivertest

import (
	"context"
	"testing"

	"github.com/piper/piper/pkg/notebook"
	notebookdriver "github.com/piper/piper/pkg/notebook/worker/driver"
)

// RunContract verifies the core behavioral contract every notebookdriver.Driver must satisfy.
// newDriver is called once per sub-test to give each case a clean instance.
func RunContract(t *testing.T, newDriver func() notebookdriver.Driver) {
	t.Helper()

	t.Run("Stop/unknown_name_is_not_error", func(t *testing.T) {
		if err := newDriver().Stop(context.Background(), "nonexistent"); err != nil {
			t.Fatalf("Stop on unknown name: %v", err)
		}
	})

	t.Run("Status/unknown_name_returns_stopped", func(t *testing.T) {
		if got := newDriver().Status(context.Background(), "nonexistent"); got != notebook.StatusStopped {
			t.Fatalf("Status = %q, want %q", got, notebook.StatusStopped)
		}
	})

	t.Run("KillAll/empty_state_is_not_error", func(t *testing.T) {
		if err := newDriver().KillAll(context.Background()); err != nil {
			t.Fatalf("KillAll on empty state: %v", err)
		}
	})
}

// RunRecoverableContract verifies the Recoverable contract every recoverable driver must satisfy.
func RunRecoverableContract(t *testing.T, newDriver func() interface {
	notebookdriver.Driver
	notebookdriver.Recoverable
}) {
	t.Helper()

	t.Run("Recover/empty_state_fires_no_callbacks", func(t *testing.T) {
		err := newDriver().Recover(
			context.Background(),
			func(notebookdriver.RecoveredHandle) func(string) {
				t.Fatal("onRecovered called on empty state")
				return nil
			},
			func(notebookdriver.RecoveredHandle, string) {
				t.Fatal("onTerminal called on empty state")
			},
		)
		if err != nil {
			t.Fatalf("Recover on empty state: %v", err)
		}
	})
}
