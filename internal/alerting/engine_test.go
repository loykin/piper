package alerting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/loykin/piper/internal/event"
	storemod "github.com/loykin/piper/internal/store"
	pkgalerting "github.com/loykin/piper/pkg/alerting"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notify"
	"github.com/loykin/piper/pkg/pipeline"
	"github.com/loykin/piper/pkg/pipeline/run"
	"github.com/loykin/piper/pkg/project"
)

type testCredentialResolver struct{}

func (testCredentialResolver) ResolveNotification(context.Context, string, string) (credential.NotificationCredential, error) {
	return credential.NotificationCredential{Kind: credential.KindWebhook, Data: map[string]string{"url": "https://example.com/hook"}}, nil
}

type captureNotifier struct{ messages chan notify.Message }

func (n captureNotifier) Send(_ context.Context, message notify.Message) error {
	n.messages <- message
	return nil
}

type closedBus struct{}

func (closedBus) Publish(event.Event) {}
func (closedBus) Subscribe() (<-chan event.Event, func()) {
	ch := make(chan event.Event)
	close(ch)
	return ch, func() {}
}

func TestEngineStopsWhenEventSubscriptionCloses(t *testing.T) {
	engine := NewEngine(nil, testCredentialResolver{})
	done := engine.Start(context.Background(), closedBus{})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("engine did not stop after its event subscription closed")
	}
}

func TestEngineDeliversPipelineOutcomeTemplate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages := make(chan notify.Message, 1)
	engine := NewEngine(nil, testCredentialResolver{})
	engine.openNotifier = func(string, map[string]string) (notify.Notifier, error) {
		return captureNotifier{messages: messages}, nil
	}
	done := engine.Start(ctx, event.NewHub())
	pl := &pipeline.Pipeline{
		Spec: pipeline.PipelineSpec{OnFailure: &pipeline.OnOutcome{Notify: []pipeline.NotifyAction{{
			CredentialRef: "ops",
			Message:       "{{.PipelineName}}/{{.RunID}}={{.Status}}",
		}}}},
	}
	pl.Metadata.Name = "training"
	engine.NotifyPipelineOutcome(ctx, "p", "run-1", run.StatusFailed, pl)
	select {
	case message := <-messages:
		if message.Body != "training/run-1=failed" || message.Fields["project_id"] != "p" {
			t.Fatalf("message = %#v", message)
		}
	case <-time.After(time.Second):
		t.Fatal("pipeline outcome notification was not delivered")
	}
	cancel()
	<-done
}

func TestEngineMatchesEventAndPersistsDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repos, err := storemod.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	if err := repos.Project.Create(ctx, &project.Project{ID: "p", Name: "p"}); err != nil {
		t.Fatal(err)
	}
	notifyJSON, _ := json.Marshal([]string{"ops"})
	rule := &pkgalerting.Rule{ID: "r", ProjectID: "p", Name: "failures", Source: pkgalerting.SourceEvent, EventType: "run.completed", When: `fields.status == "failed"`, Notify: []string{"ops"}, NotifyJSON: string(notifyJSON), CooldownSeconds: 60, Enabled: true}
	if err := repos.AlertRule.Create(ctx, rule); err != nil {
		t.Fatal(err)
	}
	messages := make(chan notify.Message, 1)
	engine := NewEngine(repos.AlertRule, testCredentialResolver{})
	engine.openNotifier = func(string, map[string]string) (notify.Notifier, error) {
		return captureNotifier{messages: messages}, nil
	}
	if err := engine.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	hub := event.NewHub()
	done := engine.Start(ctx, hub)
	hub.Publish(event.New("p", "run.completed", map[string]any{"run_id": "run-1", "status": "failed"}))
	select {
	case message := <-messages:
		if message.Fields["event_type"] != "run.completed" {
			t.Fatalf("message=%+v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification was not delivered")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got, _ := repos.AlertRule.Get(ctx, "p", "r")
		if got.LastSuccessAt != nil {
			cancel()
			<-done
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("last_success_at was not persisted")
}
