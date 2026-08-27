package pipeline

import (
	"strings"
	"testing"

	"github.com/loykin/piper/pkg/manifest"
)

func notificationTestPipeline() *Pipeline {
	return &Pipeline{
		TypeMeta: manifest.TypeMeta{APIVersion: manifest.APIVersionV1, Kind: "Pipeline"},
		Metadata: manifest.ObjectMeta{Name: "notify"},
		Spec: PipelineSpec{Steps: []Step{{
			Name: "step",
			Run:  Run{Command: []string{"true"}},
		}}},
	}
}

func TestValidatePipelineOutcomeNotifications(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Pipeline)
		want string
	}{
		{
			name: "failure deploy is unsupported",
			edit: func(p *Pipeline) { p.Spec.OnFailure = &OnOutcome{Deploy: &DeployTrigger{}} },
			want: "on_failure.deploy is not supported",
		},
		{
			name: "credential reference is required",
			edit: func(p *Pipeline) { p.Spec.OnSuccess = &OnOutcome{Notify: []NotifyAction{{}}} },
			want: "credential_ref is required",
		},
		{
			name: "message template must parse",
			edit: func(p *Pipeline) {
				p.Spec.OnFailure = &OnOutcome{Notify: []NotifyAction{{CredentialRef: "ops", Message: "{{"}}}
			},
			want: "message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := notificationTestPipeline()
			tt.edit(p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestValidatePipelineOutcomeNotificationAcceptsTemplate(t *testing.T) {
	p := notificationTestPipeline()
	p.Spec.OnSuccess = &OnOutcome{Notify: []NotifyAction{{
		CredentialRef: "ops",
		Message:       "pipeline {{.PipelineName}} run {{.RunID}} is {{.Status}}",
	}}}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
