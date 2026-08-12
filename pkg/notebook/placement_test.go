package notebook

import (
	"testing"

	"github.com/loykin/piper/pkg/manifest"
)

func TestValidateDirectPlacement(t *testing.T) {
	base := func(p manifest.PlacementSpec) Notebook {
		return Notebook{Spec: NotebookSpec{Driver: manifest.DriverSpec{Placement: p}}}
	}
	cases := []struct {
		name    string
		spec    Notebook
		allowed string
		wantErr bool
	}{
		{"empty placement ok", base(manifest.PlacementSpec{}), "docker", false},
		{"matching runtime ok", base(manifest.PlacementSpec{Runtime: "docker"}), "docker", false},
		{"worker rejected", base(manifest.PlacementSpec{Worker: "old-worker"}), "docker", true},
		{"label rejected", base(manifest.PlacementSpec{Label: "gpu"}), "docker", true},
		{"mismatched runtime rejected", base(manifest.PlacementSpec{Runtime: "k8s"}), "docker", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDirectPlacement(tc.spec, tc.allowed)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
