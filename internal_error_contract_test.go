package piper

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// TestNoRawErrorsIn500Responses fails when a handler writes a 500 whose body
// is built from an error value. Unexpected failures go through
// httpx.InternalError, which logs the error with a reference id and answers
// only a message and that ref — raw errors carry SQL, file paths, and
// upstream bodies, and each handler used to format them differently.
func TestNoRawErrorsIn500Responses(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "*.go").Output()
	if err != nil {
		t.Skip("git not available:", err)
	}
	raw := regexp.MustCompile(`StatusInternalServerError,\s*gin\.H\{"error":\s*[^\s"}][^}]*\}`)
	for _, file := range strings.Fields(string(out)) {
		if strings.HasSuffix(file, "_test.go") || strings.HasPrefix(file, "internal/httpx/") || strings.HasPrefix(file, "examples/") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range raw.FindAll(src, -1) {
			t.Errorf("%s: 500 built from a value — use httpx.InternalError: %s", file, m)
		}
	}
}
