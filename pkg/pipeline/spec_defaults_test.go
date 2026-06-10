package pipeline

import (
	"testing"

	"github.com/piper/piper/pkg/manifest"
)

func TestApplyDefaultsMergesRuntimeImageWithStepOptions(t *testing.T) {
	p := &Pipeline{
		Spec: PipelineSpec{
			Defaults: &PipelineDefaults{
				Driver: manifest.DriverSpec{
					Docker: &manifest.DriverDockerSpec{Image: "python:3.12"},
					K8s: &manifest.DriverK8sSpec{
						Image:     "python:3.12",
						Namespace: "jobs",
					},
				},
			},
			Steps: []Step{{
				Name: "train",
				Driver: manifest.DriverSpec{
					Docker: &manifest.DriverDockerSpec{CPUs: "4"},
					K8s:    &manifest.DriverK8sSpec{ImagePullPolicy: "IfNotPresent"},
				},
			}},
		},
	}

	got := p.ApplyDefaults().Spec.Steps[0].Driver
	if got.Docker == nil || got.Docker.Image != "python:3.12" || got.Docker.CPUs != "4" {
		t.Fatalf("docker defaults not merged: %#v", got.Docker)
	}
	if got.K8s == nil || got.K8s.Image != "python:3.12" || got.K8s.Namespace != "jobs" || got.K8s.ImagePullPolicy != "IfNotPresent" {
		t.Fatalf("k8s defaults not merged: %#v", got.K8s)
	}
}
