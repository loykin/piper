package serving

import (
	"strings"
	"testing"

	"github.com/loykin/piper/pkg/manifest"
)

func TestModelServiceValidateDriverRuntimeBranches(t *testing.T) {
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
				Docker:    &manifest.DriverDockerSpec{Image: "server:test"},
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
				K8s:       &manifest.DriverK8sSpec{Image: "server:test", Namespace: "serving"},
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
			err := (ModelService{Spec: ModelServiceSpec{Driver: tt.driver}}).Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
