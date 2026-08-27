package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/project"
)

func TestAlertRuleRepositoryCooldownAndDelivery(t *testing.T) {
	ctx := context.Background()
	repos, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	if err := repos.Project.Create(ctx, &project.Project{ID: "alerts", Name: "alerts"}); err != nil {
		t.Fatal(err)
	}
	notifyJSON, _ := json.Marshal([]string{"ops"})
	rule := &alerting.Rule{ID: "rule-1", ProjectID: "alerts", Name: "failures", Source: alerting.SourceEvent, EventType: "run.completed", When: `fields.status == "failed"`, Notify: []string{"ops"}, NotifyJSON: string(notifyJSON), CooldownSeconds: 60, Enabled: true}
	if err := repos.AlertRule.Create(ctx, rule); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claimed, err := repos.AlertRule.TryClaimFire(ctx, "alerts", rule.ID, now, now.Add(-time.Minute))
	if err != nil || !claimed {
		t.Fatalf("first claim=%v err=%v", claimed, err)
	}
	claimed, err = repos.AlertRule.TryClaimFire(ctx, "alerts", rule.ID, now.Add(time.Second), now.Add(-59*time.Second))
	if err != nil || claimed {
		t.Fatalf("cooldown claim=%v err=%v", claimed, err)
	}
	if err := repos.AlertRule.RecordDelivery(ctx, "alerts", rule.ID, now, true, ""); err != nil {
		t.Fatal(err)
	}
	got, err := repos.AlertRule.Get(ctx, "alerts", rule.ID)
	if err != nil || got == nil || got.LastSuccessAt == nil || len(got.Notify) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
