package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

const validYAML = `
apiVersion: piper/v1
kind: Pipeline
metadata:
  name: test-pipeline
spec:
  steps:
    - name: step-a
      run:
        type: command
        command: [echo, hello]
    - name: step-b
      depends_on: [step-a]
      run:
        type: command
        command: [echo, world]
`

func TestParse_valid(t *testing.T) {
	p, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "test-pipeline" {
		t.Errorf("name: got %q", p.Metadata.Name)
	}
	if len(p.Spec.Steps) != 2 {
		t.Errorf("want 2 steps, got %d", len(p.Spec.Steps))
	}
	if p.Spec.Steps[1].DependsOn[0] != "step-a" {
		t.Errorf("depends_on not parsed")
	}
}

func TestParse_invalidYAML(t *testing.T) {
	_, err := Parse([]byte("not: valid: yaml: ["))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParse_missingName(t *testing.T) {
	yaml := `
metadata:
  name: ""
spec:
  steps:
    - name: a
      run:
        type: command
        command: [echo]
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected name required error")
	}
}

func TestParse_noSteps(t *testing.T) {
	yaml := `
metadata:
  name: empty
spec:
  steps: []
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected steps required error")
	}
}

func TestParse_duplicateStepName(t *testing.T) {
	yaml := `
metadata:
  name: dup
spec:
  steps:
    - name: a
      run:
        type: command
    - name: a
      run:
        type: command
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected duplicate step error")
	}
}

func TestParse_unknownDependency(t *testing.T) {
	yaml := `
metadata:
  name: bad-dep
spec:
  steps:
    - name: a
      depends_on: [no-such-step]
      run:
        type: command
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected unknown dep error")
	}
}

func TestParse_emptyStepName(t *testing.T) {
	yaml := `
metadata:
  name: p
spec:
  steps:
    - name: ""
      run:
        type: command
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected step name required error")
	}
}

func TestParseFile_valid(t *testing.T) {
	f := filepath.Join(t.TempDir(), "pipe.yaml")
	if err := os.WriteFile(f, []byte(validYAML), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := ParseFile(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Metadata.Name != "test-pipeline" {
		t.Errorf("name mismatch")
	}
}

func TestParseFile_notFound(t *testing.T) {
	_, err := ParseFile("/no/such/file.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
}
