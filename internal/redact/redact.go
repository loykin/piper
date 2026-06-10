package redact

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key|access[_-]?key)\s*[:=]\s*([^ \t\r\n'",}]+)`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
}

func String(s string) string {
	s = patterns[0].ReplaceAllString(s, `$1=[REDACTED]`)
	s = patterns[1].ReplaceAllString(s, `[REDACTED_AWS_ACCESS_KEY]`)
	return s
}
