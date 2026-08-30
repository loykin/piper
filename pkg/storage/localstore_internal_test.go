package storage

import "testing"

func TestValidateAbsoluteCleanPath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "absolute clean path", path: "/var/piper/store", wantErr: false},
		{name: "root", path: "/", wantErr: false},
		{name: "relative path rejected", path: "var/piper/store", wantErr: true},
		{name: "empty path rejected", path: "", wantErr: true},
		{name: "leading .. above root is clamped by Clean, not rejected here", path: "/../../etc", wantErr: false},
		{name: "internal .. segment is resolved by Clean, not rejected here", path: "/var/../piper", wantErr: false},
		{name: "trailing .. segment is resolved by Clean, not rejected here", path: "/var/piper/..", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAbsoluteCleanPath(tc.path)
			if tc.wantErr && err == nil {
				t.Fatalf("validateAbsoluteCleanPath(%q) = nil, want error", tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateAbsoluteCleanPath(%q) = %v, want nil", tc.path, err)
			}
		})
	}
}
