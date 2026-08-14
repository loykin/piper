package serving

import (
	"strings"
	"testing"
)

const canonicalServiceYAML = `apiVersion: piper/v1
kind: ModelService
metadata:
  name: demo
spec:
  model:
    from_uri: file:///model
  run:
    command: [serve]
    port: 8080
  driver: {}
`

func TestParseRejectsUnknownAndWrongKind(t *testing.T) {
	if _, err := Parse([]byte(canonicalServiceYAML)); err != nil {
		t.Fatalf("canonical manifest: %v", err)
	}
	if _, err := Parse([]byte(strings.Replace(canonicalServiceYAML, "driver: {}", "driver: {}\n  typo: true", 1))); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if _, err := Parse([]byte(strings.Replace(canonicalServiceYAML, "kind: ModelService", "kind: Service", 1))); err == nil {
		t.Fatal("wrong kind was accepted")
	}
}

func TestParseRequiresExactlyOneModelSource(t *testing.T) {
	both := strings.Replace(canonicalServiceYAML, "from_uri: file:///model", "from_uri: file:///model\n    from_artifact:\n      pipeline: train\n      step: export\n      artifact: model\n      run: latest", 1)
	if _, err := Parse([]byte(both)); err == nil {
		t.Fatal("both model sources were accepted")
	}
}
