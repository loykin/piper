package pipeline

import (
	"strings"
	"testing"
)

// Adversarial QA 2026-11-18 F6: $PIPER_SCRIPT_PATH with nothing to set it is
// rejected at parse time instead of failing the step at runtime.
func TestValidateRejectsScriptPathWithoutPath(t *testing.T) {
	base := "apiVersion: piper/v1\nkind: Pipeline\nmetadata:\n  name: p\nspec:\n  steps:\n    - name: s\n      run:\n"
	cases := map[string]bool{
		`        command: [sh, -c, '.venv/bin/python "$PIPER_SCRIPT_PATH"']`:                                      false,
		"        path: train.py\n        command: [python3, $PIPER_SCRIPT_PATH]":                                  true,
		"        source: git\n        repo: https://example.com/r.git\n        command: [ls, $PIPER_SCRIPT_PATH]": true,
		"        command: [echo, hi]": true,
	}
	for run, ok := range cases {
		_, err := Parse([]byte(base + run + "\n"))
		if ok && err != nil {
			t.Errorf("%s: unexpected error %v", run, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "PIPER_SCRIPT_PATH")) {
			t.Errorf("%s: err = %v, want a $PIPER_SCRIPT_PATH error", run, err)
		}
	}
}
