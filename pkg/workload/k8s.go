package workload

import "strings"

// SafeName converts an arbitrary name to a Kubernetes-safe resource name.
// The result is lowercase, uses only [a-z0-9-], and is at most 63 characters.
func SafeName(name string) string {
	safe := strings.ToLower(name)
	var b strings.Builder
	for _, c := range safe {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			b.WriteRune(c)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}
