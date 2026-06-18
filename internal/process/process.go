package process

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// ProcessSpec describes a subprocess to launch.
type ProcessSpec struct {
	Name    string
	Command []string
	Env     map[string]string
	PIDFile string
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
	// LogFile redirects stdout and stderr of the process to this file path.
	// The file and its parent directory are created if they do not exist.
	// Empty means no redirection (inherit parent's stdout/stderr).
	LogFile string
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
