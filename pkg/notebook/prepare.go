package notebook

import (
	"fmt"
	"strings"
)

const (
	PrepareStepCommand = "command"

	PrepareBackendProcess = "process"
	PrepareBackendDocker  = "docker"
	PrepareBackendK8s     = "k8s"
)

// Validate checks the syntactic structure of the preparation spec.
func (s *NotebookPrepareSpec) Validate() error {
	if s == nil {
		return nil
	}
	for i, step := range s.Steps {
		if step.Type == "" {
			return fmt.Errorf("prepare.steps[%d].type is required", i)
		}
		switch step.Type {
		case PrepareStepCommand:
			if len(step.Command) == 0 {
				return fmt.Errorf("prepare.steps[%d].command is required for type %q", i, step.Type)
			}
		default:
			return fmt.Errorf("prepare.steps[%d].type %q is not supported", i, step.Type)
		}
		if step.Backend != "" &&
			step.Backend != PrepareBackendProcess &&
			step.Backend != PrepareBackendDocker &&
			step.Backend != PrepareBackendK8s {
			return fmt.Errorf("prepare.steps[%d].backend %q is not supported", i, step.Backend)
		}
	}
	return nil
}

// StepsForBackend filters the preparation steps for the given backend.
func (s *NotebookPrepareSpec) StepsForBackend(backend string) ([]NotebookPrepareStep, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, nil
	}
	out := make([]NotebookPrepareStep, 0, len(s.Steps))
	for i, step := range s.Steps {
		if step.Backend != "" && step.Backend != backend {
			continue
		}
		if step.Type != PrepareStepCommand {
			return nil, fmt.Errorf("prepare.steps[%d].type %q is not supported", i, step.Type)
		}
		out = append(out, step)
	}
	return out, nil
}

// BuildLaunchScript converts the selected preparation steps into a shell script
// that runs before the final notebook command.
func BuildLaunchScript(prefixLines []string, steps []NotebookPrepareStep, baseCommand []string, workDir string) (string, error) {
	if len(baseCommand) == 0 {
		return "", fmt.Errorf("base command must not be empty")
	}
	var b strings.Builder
	b.WriteString("set -e\n")
	for _, line := range prefixLines {
		if line == "" {
			continue
		}
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	if workDir != "" {
		b.WriteString("cd ")
		b.WriteString(shellQuote(workDir))
		b.WriteByte('\n')
	}
	for _, step := range steps {
		b.WriteString(shellJoin(step.Command))
		b.WriteByte('\n')
	}
	b.WriteString("exec ")
	b.WriteString(shellJoin(baseCommand))
	b.WriteByte('\n')
	return b.String(), nil
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafeWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func isShellSafeWord(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z',
			'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '_', '-', '.', '/', ':', ',', '=', '+', '@', '%':
		default:
			return false
		}
	}
	return true
}
