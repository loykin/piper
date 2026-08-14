package notebook

import (
	"strings"
	"testing"
)

func TestParseRejectsUnknownAndWrongKind(t *testing.T) {
	base := `apiVersion: piper/v1
kind: Notebook
metadata:
  name: demo
spec:
  driver: {}
`
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("canonical manifest: %v", err)
	}
	if _, err := Parse([]byte(strings.Replace(base, "driver: {}", "driver: {}\n  typo: true", 1))); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := Parse([]byte(strings.Replace(base, "kind: Notebook", "kind: NotebookServer", 1))); err == nil {
		t.Fatal("legacy NotebookServer kind was accepted")
	}
}
