package redact

import (
	"regexp"
	"sort"
	"strings"
)

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key|access[_-]?key)\s*[:=]\s*([^ \t\r\n'",}]+)`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func String(s string) string {
	s = patterns[0].ReplaceAllString(s, `$1=[REDACTED]`)
	s = patterns[1].ReplaceAllString(s, `[REDACTED_AWS_ACCESS_KEY]`)
	return s
}

// Values redacts known sensitive values from s after applying the built-in
// pattern-based redaction. Values shorter than four bytes are ignored to avoid
// masking common words or numbers.
func Values(s string, values []string) string {
	s = String(s)
	if s == "" || len(values) == 0 {
		return s
	}
	seen := map[string]struct{}{}
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) < 4 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		filtered = append(filtered, value)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return len(filtered[i]) > len(filtered[j])
	})
	for _, value := range filtered {
		s = strings.ReplaceAll(s, value, "[REDACTED]")
	}
	return s
}
