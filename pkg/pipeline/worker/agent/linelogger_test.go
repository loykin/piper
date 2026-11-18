package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression test for adversarial QA 2026-11-18 F16: the step's local log
// file must redact credential values from the task env, as the streamed
// lines already are.
func TestLineLoggerRedactsEnvValuesInLogFile(t *testing.T) {
	dir := t.TempDir()
	f := openLogFile(dir, "show")
	if f == nil {
		t.Fatal("openLogFile returned nil")
	}
	logger := newLineLogger(f, []string{"dummy-qa-value-42"})
	logger.append("stdout", "value is dummy-qa-value-42 here")
	_ = f.Close()

	data, err := os.ReadFile(filepath.Join(dir, "show.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "dummy-qa-value-42") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("log file = %q, want the secret redacted", data)
	}
}
