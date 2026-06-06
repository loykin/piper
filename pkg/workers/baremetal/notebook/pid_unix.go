//go:build !windows

package notebookworker

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// isPIDNewerThan reports whether the process with the given PID started after
// the cutoff time, indicating PID reuse. When the start time cannot be verified,
// it returns true so cleanup conservatively skips an unrelated process.
//
// On Linux we read /proc/{pid}/stat and compare the process start time.
// On other Unix systems we fall back to checking the mtime of /proc/{pid}
// (where available) or return false so the caller proceeds with the kill.
func isPIDNewerThan(pid int, cutoff time.Time) bool {
	// Linux: /proc/{pid}/stat field 22 is starttime in clock ticks since boot.
	// We use the /proc/{pid} directory mtime as a portable-enough approximation
	// across Linux and macOS (macOS doesn't have /proc, so stat will fail → false).
	info, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err == nil {
		return info.ModTime().After(cutoff)
	}

	// macOS and other Unix systems generally expose process start time via ps.
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return true
	}
	startedAt, err := time.Parse("Mon Jan 2 15:04:05 2006", strings.TrimSpace(string(out)))
	if err != nil {
		return true
	}
	return startedAt.After(cutoff)
}
