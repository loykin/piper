package pipeline

import (
	"strings"
	"testing"

	"github.com/loykin/piper/pkg/manifest"
)

func TestPipelineValidateDriverRuntimeBranches(t *testing.T) {
	base := func(driver manifest.DriverSpec) *Pipeline {
		return &Pipeline{
			Metadata: manifest.ObjectMeta{Name: "test"},
			Spec: PipelineSpec{Steps: []Step{{
				Name:   "step",
				Run:    Run{Command: []string{"true"}},
				Driver: driver,
			}}},
		}
	}
	tests := []struct {
		name    string
		driver  manifest.DriverSpec
		wantErr string
	}{
		{name: "auto", driver: manifest.DriverSpec{}},
		{name: "baremetal", driver: manifest.DriverSpec{Placement: manifest.PlacementSpec{Runtime: "baremetal"}}},
		{
			name: "docker",
			driver: manifest.DriverSpec{
				Placement: manifest.PlacementSpec{Runtime: "docker"},
				Docker:    &manifest.DriverDockerSpec{Image: "runner:test"},
			},
		},
		{
			name:    "docker image required",
			driver:  manifest.DriverSpec{Placement: manifest.PlacementSpec{Runtime: "docker"}},
			wantErr: "docker.image",
		},
		{
			name: "k8s",
			driver: manifest.DriverSpec{
				Placement: manifest.PlacementSpec{Runtime: "k8s"},
				K8s:       &manifest.DriverK8sSpec{Image: "runner:test", Namespace: "jobs"},
			},
		},
		{
			name:    "unknown",
			driver:  manifest.DriverSpec{Placement: manifest.PlacementSpec{Runtime: "vm"}},
			wantErr: "unsupported",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := base(tt.driver).Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
