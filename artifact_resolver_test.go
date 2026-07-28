package piper

import (
	"strings"
	"testing"
)

func TestArtifactURIForRemoteServing(t *testing.T) {
	tests := []struct {
		name       string
		storageURL string
		want       string
		wantErr    string
	}{
		{name: "s3", storageURL: "s3://models", want: "s3://models/run-1/train/model"},
		{name: "http unsupported", storageURL: "https://piper.example/api", wantErr: "requires s3"},
		{name: "file unsupported", storageURL: "file:///tmp/artifacts", wantErr: "cannot provide artifact URIs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&piperArtifactResolver{storageURL: tt.storageURL}).artifactURI("run-1/train/model")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("artifactURI() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("artifactURI() = %q, want %q", got, tt.want)
			}
		})
	}
}
