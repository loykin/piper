package alerting

import "testing"

func TestValidateAndMatchRules(t *testing.T) {
	eventRule := &Rule{Name: "failures", Source: SourceEvent, EventType: "run.completed", When: `fields.status == "failed"`, Notify: []string{"ops"}, CooldownSeconds: 60}
	if err := ValidateRule(eventRule); err != nil {
		t.Fatal(err)
	}
	if !MatchEvent(eventRule, "run.completed", map[string]any{"status": "failed"}) {
		t.Fatal("failure event did not match")
	}
	if MatchEvent(eventRule, "run.completed", map[string]any{"status": "success"}) {
		t.Fatal("success event matched failure rule")
	}

	metricRule := &Rule{Name: "accuracy", Source: SourceMetric, MetricKey: "accuracy", Condition: "< 0.8", Notify: []string{"ops"}, CooldownSeconds: 60}
	if err := ValidateRule(metricRule); err != nil {
		t.Fatal(err)
	}
	if !MatchMetric(metricRule, "accuracy", .7) || MatchMetric(metricRule, "accuracy", .9) {
		t.Fatal("metric comparison mismatch")
	}
}

func TestValidateRuleRejectsUnsafeOrUnboundedShapes(t *testing.T) {
	cases := []*Rule{
		{Name: "bad-event", Source: SourceEvent, EventType: "run.status", Notify: []string{"ops"}, CooldownSeconds: 60},
		{Name: "bad-expression", Source: SourceEvent, EventType: "run.completed", When: `delete_everything()`, Notify: []string{"ops"}, CooldownSeconds: 60},
		{Name: "no-cooldown", Source: SourceMetric, MetricKey: "loss", Condition: "> 1", Notify: []string{"ops"}, CooldownSeconds: 1},
	}
	for _, rule := range cases {
		if err := ValidateRule(rule); err == nil {
			t.Fatalf("rule %+v should be rejected", rule)
		}
	}
}
