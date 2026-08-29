package mcp

import "testing"

func TestValidateProtocolVersionHeader(t *testing.T) {
	cases := []struct {
		name         string
		header       string
		isInitialize bool
		wantErr      bool
	}{
		{"missing on initialize is ok", "", true, false},
		{"missing on non-initialize is rejected", "", false, true},
		{"supported version", ProtocolVersion20251125, false, false},
		{"unsupported version", "2024-01-01", false, true},
		{"unsupported version on initialize still rejected", "2024-01-01", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProtocolVersionHeader(tc.header, tc.isInitialize)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateProtocolVersionHeader(%q, %v) err = %v, wantErr %v", tc.header, tc.isInitialize, err, tc.wantErr)
			}
		})
	}
}
