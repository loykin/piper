package storage

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestMkdirAllBeneathExistingRoot_MultiLevelCreate exercises the case
// mkdirAllBeneathExistingRoot exists for: none of target's path components
// exist yet, several levels deep.
func TestMkdirAllBeneathExistingRoot_MultiLevelCreate(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c", "d")
	if err := mkdirAllBeneathExistingRoot(target, 0o755); err != nil {
		t.Fatalf("mkdirAllBeneathExistingRoot: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("target not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target is not a directory")
	}
}

// TestMkdirAllBeneathExistingRoot_AlreadyExists confirms the function is a
// no-op (not an error) when target already exists, matching os.MkdirAll.
func TestMkdirAllBeneathExistingRoot_AlreadyExists(t *testing.T) {
	base := t.TempDir()
	if err := mkdirAllBeneathExistingRoot(base, 0o755); err != nil {
		t.Fatalf("mkdirAllBeneathExistingRoot on existing dir: %v", err)
	}
}

// TestMkdirAllBeneathExistingRoot_RejectsRelativePath confirms the entry
// guard rejects a relative target outright rather than silently resolving
// it against the process's working directory.
func TestMkdirAllBeneathExistingRoot_RejectsRelativePath(t *testing.T) {
	if err := mkdirAllBeneathExistingRoot("relative/path", 0o755); err == nil {
		t.Fatal("mkdirAllBeneathExistingRoot accepted a relative path")
	}
}

// TestMkdirAllBeneathExistingRoot_DoesNotFollowIntermediateSymlink is the
// case that motivates walking the whole path through a single os.Root
// capability instead of stat-ing raw absolute paths one ancestor at a time:
// a symlink sitting *in the middle* of target, not just at its parent, must
// not let creation escape to wherever that symlink points.
func TestMkdirAllBeneathExistingRoot_DoesNotFollowIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	// base/link -> outside
	if err := os.Symlink(outside, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(base, "link", "escaped", "deep")
	err := mkdirAllBeneathExistingRoot(target, 0o755)
	if err == nil {
		t.Fatal("mkdirAllBeneathExistingRoot followed a symlink out of the walk")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(statErr) {
		t.Fatalf("directory was created outside the intended tree: %v", statErr)
	}
}
