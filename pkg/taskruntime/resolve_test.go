package taskruntime

import (
	"encoding/json"
	"testing"

	"github.com/piper/piper/pkg/pipeline"
	"github.com/piper/piper/pkg/proto"
)

func TestResolveImagePriority(t *testing.T) {
	cases := []struct {
		name          string
		runnerImage   string
		runImage      string
		pipelineImage string
		defaultImage  string
		want          string
		wantErr       bool
	}{
		{name: "runner", runnerImage: "runner:1", runImage: "run:1", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "runner:1"},
		{name: "run", runImage: "run:1", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "run:1"},
		{name: "pipeline", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "pipeline:1"},
		{name: "worker_default", defaultImage: "default:1", want: "default:1"},
		{name: "missing", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := makeImageTestTask(t, tc.runnerImage, tc.runImage, tc.pipelineImage)
			got, err := ResolveImage(task, tc.defaultImage)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("image = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveNamespace(t *testing.T) {
	cases := []struct {
		name      string
		placement string
		def       string
		want      string
	}{
		{name: "from_placement", placement: "gpu-jobs", def: "default", want: "gpu-jobs"},
		{name: "fallback", placement: "", def: "default", want: "default"},
		{name: "empty_fallback", placement: "", def: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pl pipeline.Pipeline
			pl.Spec.Placement.Namespace = tc.placement
			plJSON, _ := json.Marshal(pl)
			task := &proto.Task{Pipeline: plJSON}
			got := ResolveNamespace(task, tc.def)
			if got != tc.want {
				t.Fatalf("namespace = %q, want %q", got, tc.want)
			}
		})
	}
}

func makeImageTestTask(t *testing.T, runnerImage, runImage, pipelineImage string) *proto.Task {
	t.Helper()
	var step pipeline.Step
	step.Runner.Image = runnerImage
	step.Run.Image = runImage
	var pl pipeline.Pipeline
	pl.Spec.Defaults.Image = pipelineImage
	stepJSON, _ := json.Marshal(step)
	plJSON, _ := json.Marshal(pl)
	return &proto.Task{StepName: "train", Step: stepJSON, Pipeline: plJSON}
}
