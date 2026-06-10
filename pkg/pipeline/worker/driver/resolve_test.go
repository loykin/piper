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
		runtime       string
		stepImage     string
		pipelineImage string
		defaultImage  string
		want          string
		wantErr       bool
	}{
		{name: "docker_step", runtime: "docker", stepImage: "step:1", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "step:1"},
		{name: "docker_pipeline", runtime: "docker", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "pipeline:1"},
		{name: "k8s_step", runtime: "k8s", stepImage: "step:1", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "step:1"},
		{name: "k8s_pipeline", runtime: "k8s", pipelineImage: "pipeline:1", defaultImage: "default:1", want: "pipeline:1"},
		{name: "worker_default", runtime: "docker", defaultImage: "default:1", want: "default:1"},
		{name: "missing", runtime: "k8s", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := makeImageTestTask(t, tc.runtime, tc.stepImage, tc.pipelineImage)
			got, err := ResolveImage(task, tc.runtime, tc.defaultImage)
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

func TestResolveImageDoesNotCrossRuntimeBoundaries(t *testing.T) {
	task := makeImageTestTask(t, "k8s", "k8s:step", "k8s:default")

	got, err := ResolveImage(task, "docker", "docker:worker")
	if err != nil {
		t.Fatal(err)
	}
	if got != "docker:worker" {
		t.Fatalf("image = %q, want docker worker default", got)
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

func makeImageTestTask(t *testing.T, runtime, stepImage, pipelineImage string) *proto.Task {
	t.Helper()
	var step pipeline.Step
	setRuntimeImage(&step.Driver, runtime, stepImage)
	var pl pipeline.Pipeline
	if pipelineImage != "" {
		var defaultsDriver manifest.DriverSpec
		setRuntimeImage(&defaultsDriver, runtime, pipelineImage)
		pl.Spec.Defaults = &pipeline.PipelineDefaults{
			Driver: defaultsDriver,
		}
	}
	stepJSON, _ := json.Marshal(step)
	plJSON, _ := json.Marshal(pl)
	return &proto.Task{StepName: "train", Step: stepJSON, Pipeline: plJSON}
}

func setRuntimeImage(spec *manifest.DriverSpec, runtime, image string) {
	switch runtime {
	case "docker":
		spec.Docker = &manifest.DriverDockerSpec{Image: image}
	case "k8s":
		spec.K8s = &manifest.DriverK8sSpec{Image: image}
	}
}
