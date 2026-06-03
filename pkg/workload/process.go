package workload

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ProcessSpec describes a subprocess to launch.
type ProcessSpec struct {
	Name    string
	Command []string
	Env     map[string]string
	// Dir is the working directory for the subprocess.
	// Empty means inherit the parent process's working directory.
	Dir        string
	Port       int
	HealthPath string
	// GPUs selects which GPU devices are visible to the process.
	// Accepts CUDA device indices ("0", "0,1", "all") or "none".
	// Sets CUDA_VISIBLE_DEVICES and ROCR_VISIBLE_DEVICES.
	// Empty string leaves the host defaults unchanged.
	GPUs string
}

// StartProcess launches a subprocess according to spec and returns
// the process PID, its HTTP endpoint, and the underlying *exec.Cmd.
// The process is started under context.Background() so it is not killed
// when the caller's request context expires.
func StartProcess(spec ProcessSpec) (pid int, endpoint string, cmd *exec.Cmd, err error) {
	if len(spec.Command) == 0 {
		return 0, "", nil, fmt.Errorf("workload: command must not be empty")
	}
	if spec.Port == 0 {
		return 0, "", nil, fmt.Errorf("workload: port must be set")
	}

	args := ExpandArgs(spec.Command, spec.Env)
	//nolint:gosec
	cmd = exec.CommandContext(context.Background(), args[0], args[1:]...)

	// Merge caller-supplied env vars on top of the process environment.
	extra := os.Environ()
	for k, v := range spec.Env {
		extra = append(extra, k+"="+v)
	}
	if spec.GPUs != "" {
		extra = append(extra,
			"CUDA_VISIBLE_DEVICES="+spec.GPUs,
			"ROCR_VISIBLE_DEVICES="+spec.GPUs,
		)
	}
	cmd.Env = extra
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}

	if err = cmd.Start(); err != nil {
		return 0, "", nil, fmt.Errorf("start process %q: %w", spec.Name, err)
	}

	endpoint = fmt.Sprintf("http://localhost:%d", spec.Port)
	return cmd.Process.Pid, endpoint, cmd, nil
}

// KillPID sends SIGKILL to the given process ID on a best-effort basis.
func KillPID(pid int) {
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := proc.Kill(); err != nil {
		slog.Warn("workload: kill process failed", "pid", pid, "err", err)
	}
}

// WaitReady polls url until it returns a non-5xx response or the timeout
// elapses. Returns nil on success, or a timeout error.
func WaitReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out waiting for %s", url)
}

// ExpandArgs replaces $VAR, ${VAR}, and $(VAR) placeholders in each arg.
// envMap takes precedence; unknown keys fall back to os.Getenv.
func ExpandArgs(args []string, envMap map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = ExpandVars(a, envMap)
	}
	return out
}

// ExpandVars handles $VAR, ${VAR}, and $(VAR) substitution.
func ExpandVars(s string, envMap map[string]string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // consume '$'
		if i >= len(s) {
			b.WriteByte('$')
			break
		}
		var key string
		var end int
		switch s[i] {
		case '{':
			// ${VAR}
			j := strings.IndexByte(s[i+1:], '}')
			if j < 0 {
				b.WriteByte('$')
				continue
			}
			key = s[i+1 : i+1+j]
			end = i + 1 + j + 1
		case '(':
			// $(VAR)
			j := strings.IndexByte(s[i+1:], ')')
			if j < 0 {
				b.WriteByte('$')
				continue
			}
			key = s[i+1 : i+1+j]
			end = i + 1 + j + 1
		default:
			// $VAR — terminated by non-identifier character
			j := i
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			key = s[i:j]
			end = j
		}
		if key == "" {
			b.WriteByte('$')
			continue
		}
		if v, ok := envMap[key]; ok {
			b.WriteString(v)
		} else {
			b.WriteString(os.Getenv(key))
		}
		i = end
	}
	return b.String()
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
