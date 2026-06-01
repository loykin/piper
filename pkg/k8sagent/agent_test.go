package k8sagent

import "testing"

func TestBuildTunnelURL(t *testing.T) {
	got, err := BuildTunnelURL("https://piper.example.com/base/", "agent 1")
	if err != nil {
		t.Fatalf("BuildTunnelURL returned error: %v", err)
	}
	want := "wss://piper.example.com/base/api/agents/agent%201/tunnel"
	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}
