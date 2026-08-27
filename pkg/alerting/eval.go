package alerting

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	eventConditionRE  = regexp.MustCompile(`^fields\.([A-Za-z_][A-Za-z0-9_]*)\s*(==|!=)\s*"([^"\\]{0,256})"$`)
	metricConditionRE = regexp.MustCompile(`^(<=|>=|<|>|==|!=)\s*(-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)$`)
)

var supportedEventTypes = map[string]struct{}{
	"run.completed":    {},
	"service.status":   {},
	"notebook.running": {},
	"notebook.stopped": {},
	"notebook.failed":  {},
}

func ValidateRule(rule *Rule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.EventType = strings.TrimSpace(rule.EventType)
	rule.When = strings.TrimSpace(rule.When)
	rule.MetricKey = strings.TrimSpace(rule.MetricKey)
	rule.Condition = strings.TrimSpace(rule.Condition)
	if rule.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if rule.CooldownSeconds == 0 {
		rule.CooldownSeconds = DefaultCooldownSeconds
	}
	if rule.CooldownSeconds < MinCooldownSeconds {
		return fmt.Errorf("%w: cooldown_seconds must be at least %d", ErrInvalid, MinCooldownSeconds)
	}
	if len(rule.Notify) == 0 {
		return fmt.Errorf("%w: notify requires at least one credential reference", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(rule.Notify))
	for i := range rule.Notify {
		rule.Notify[i] = strings.TrimSpace(rule.Notify[i])
		if rule.Notify[i] == "" {
			return fmt.Errorf("%w: notify contains an empty credential reference", ErrInvalid)
		}
		if _, ok := seen[rule.Notify[i]]; ok {
			return fmt.Errorf("%w: duplicate notify credential %q", ErrInvalid, rule.Notify[i])
		}
		seen[rule.Notify[i]] = struct{}{}
	}
	switch rule.Source {
	case SourceEvent:
		if _, ok := supportedEventTypes[rule.EventType]; !ok {
			return fmt.Errorf("%w: unsupported event_type %q", ErrInvalid, rule.EventType)
		}
		if rule.When != "" && !eventConditionRE.MatchString(rule.When) {
			return fmt.Errorf("%w: when must use fields.<name> == \"value\" or !=", ErrInvalid)
		}
		rule.MetricKey, rule.Condition = "", ""
	case SourceMetric:
		if rule.MetricKey == "" {
			return fmt.Errorf("%w: metric_key is required", ErrInvalid)
		}
		if !metricConditionRE.MatchString(rule.Condition) {
			return fmt.Errorf("%w: condition must be a numeric comparison such as < 0.8", ErrInvalid)
		}
		rule.EventType, rule.When = "", ""
	default:
		return fmt.Errorf("%w: on must be event or metric", ErrInvalid)
	}
	return nil
}

func MatchEvent(rule *Rule, eventType string, fields map[string]any) bool {
	if rule.Source != SourceEvent || rule.EventType != eventType {
		return false
	}
	if rule.When == "" {
		return true
	}
	parts := eventConditionRE.FindStringSubmatch(rule.When)
	if len(parts) != 4 {
		return false
	}
	actual, ok := fields[parts[1]]
	if !ok {
		return false
	}
	equal := fmt.Sprint(actual) == parts[3]
	if parts[2] == "!=" {
		return !equal
	}
	return equal
}

func MatchMetric(rule *Rule, key string, value float64) bool {
	if rule.Source != SourceMetric || rule.MetricKey != key {
		return false
	}
	parts := metricConditionRE.FindStringSubmatch(rule.Condition)
	if len(parts) != 3 {
		return false
	}
	want, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return false
	}
	switch parts[1] {
	case "<":
		return value < want
	case "<=":
		return value <= want
	case ">":
		return value > want
	case ">=":
		return value >= want
	case "==":
		return value == want
	case "!=":
		return value != want
	default:
		return false
	}
}
