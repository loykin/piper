// Package drivertest provides a reusable contract test suite for pipeline driver.Driver
// implementations. Import this package from each driver's _test.go and call RunContract
// so every backend is verified against the same behavioral contract.
package drivertest

import (
	"context"
	"testing"

	pipelinedriver "github.com/piper/piper/pkg/pipeline/worker/driver"
)

// RunContract verifies the core behavioral contract every pipeline driver.Driver must satisfy.
// newDriver is called once per sub-test to give each case a clean instance.
//
// Note: Stop is not covered here because pipeline handles are always obtained from Start or
// Recover — calling Stop with a zero-value Handle is not a valid usage pattern.
func RunContract(t *testing.T, newDriver func() pipelinedriver.Driver) {
	t.Helper()

	t.Run("Recover/empty_state_returns_empty_slice", func(t *testing.T) {
		handles, err := newDriver().Recover(context.Background())
		if err != nil {
			t.Fatalf("Recover on empty state: %v", err)
		}
		if len(handles) != 0 {
			t.Fatalf("Recover on empty state: got %d handles, want 0", len(handles))
		}
	})
}
