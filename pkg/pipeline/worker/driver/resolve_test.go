package driver

import (
	"encoding/json"
	"testing"

	"github.com/piper/piper/internal/proto"
	"github.com/piper/piper/pkg/manifest"
	"github.com/piper/piper/pkg/pipeline"
)

func TestResolveImagePriority(t *testing.T) {
	cases := []struct {
		name          string
		stepImage     string
		pipelineImage string
		defaultImage  string
		want          string
		wantErr       bool
	}{
		{name: "step", stepImage: "step:1", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "step:1"},
		{name: "pipeline", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "pipeline:1"},
		{name: "worker_default", defaultImage: "default:1", want: "default:1"},
		{name: "missing", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := makeImageTestTask(t, tc.stepImage, tc.pipelineImage)
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
		namespace string
		def       string
		want      string
	}{
		{name: "from_step", namespace: "gpu-jobs", def: "default", want: "gpu-jobs"},
		{name: "fallback", namespace: "", def: "default", want: "default"},
		{name: "empty_fallback", namespace: "", def: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var step pipeline.Step
			if tc.namespace != "" {
				step.Driver.K8s = &manifest.DriverK8sSpec{Namespace: tc.namespace}
			}
			stepJSON, _ := json.Marshal(step)
			task := &proto.Task{StepName: "train", Step: stepJSON}
			got := ResolveNamespace(task, tc.def)
			if got != tc.want {
				t.Fatalf("namespace = %q, want %q", got, tc.want)
			}
		})
	}
}

func makeImageTestTask(t *testing.T, stepImage, pipelineImage string) *proto.Task {
	t.Helper()
	var step pipeline.Step
	step.Driver.Image = stepImage
	var pl pipeline.Pipeline
	if pipelineImage != "" {
		pl.Spec.Defaults = &pipeline.PipelineDefaults{
			Driver: manifest.DriverSpec{Image: pipelineImage},
		}
	}
	stepJSON, _ := json.Marshal(step)
	plJSON, _ := json.Marshal(pl)
	return &proto.Task{StepName: "train", Step: stepJSON, Pipeline: plJSON}
}
