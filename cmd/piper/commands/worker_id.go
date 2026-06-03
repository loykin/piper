package commands

import (
	"strings"
	"unicode"
)

func stableWorkerID(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		s := normalizeWorkerIDPart(part)
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "-")
}

func normalizeWorkerIDPart(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
