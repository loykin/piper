package mcp

import (
	"net/http"
	"testing"
)

func TestOriginHostPolicy(t *testing.T) {
	policy := OriginHostPolicy{
		AllowedHosts:   []string{"piper.example.com"},
		AllowedOrigins: []string{"https://trusted-ai-client.example.com"},
	}

	cases := []struct {
		name    string
		host    string
		origin  string
		wantErr bool
	}{
		{"allowed host, no origin (non-browser client)", "piper.example.com", "", false},
		{"allowed host, allowed origin", "piper.example.com", "https://trusted-ai-client.example.com", false},
		{"disallowed host", "evil.example.com", "", true},
		{"allowed host, disallowed origin", "piper.example.com", "https://evil.example.com", true},
		{"rebinding attempt: attacker DNS resolves to piper but claims a different host header", "attacker.example.com", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "http://piper.example.com/api/projects/p1/mcp", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			err := policy.Validate(req)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestOriginHostPolicyPermissiveWhenUnconfigured(t *testing.T) {
	policy := OriginHostPolicy{}
	req, _ := http.NewRequest(http.MethodPost, "http://anything/api/projects/p1/mcp", nil)
	req.Host = "anything"
	if err := policy.Validate(req); err != nil {
		t.Errorf("expected no error with an unconfigured policy, got %v", err)
	}
}
