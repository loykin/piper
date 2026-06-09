package driver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Container-side fixed paths used by Docker and K8s drivers.
// All drivers must use these constants so piper agent exec sees consistent
// paths regardless of how the host directories are mounted.
const (
	ContainerOutputDir = "/piper-outputs"
	ContainerInputDir  = "/piper-inputs"
	ContainerResultDir = "/piper-runtime"
	ContainerPiperBin  = "/piper-tools/piper"
)

// RuntimeKey returns a stable runtime-safe identifier shared by all Drivers.
func RuntimeKey(workerID, runID, stepName string, attempt int) string {
	if attempt < 1 {
		attempt = 1
	}
	raw := fmt.Sprintf("%s-%s-%s-a%d", workerID, runID, stepName, attempt)
	var b strings.Builder
	for _, c := range strings.ToLower(raw) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	key := strings.Trim(b.String(), "-")
	if len(key) <= 63 {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	suffix := "-" + hex.EncodeToString(sum[:4])
	return strings.TrimRight(key[:63-len(suffix)], "-") + suffix
}
