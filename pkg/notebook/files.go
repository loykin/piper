package notebook

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WalkFiles walks root recursively and returns relative paths of matching files.
// Hidden directories (name starts with ".") and symlink directories are skipped.
// Results are sorted ascending. If maxFiles is hit, truncated is true.
func WalkFiles(root string, ext []string, maxFiles int) (files []string, truncated bool) {
	if maxFiles <= 0 || maxFiles > 1000 {
		maxFiles = 500
	}
	allowed := make(map[string]bool, len(ext))
	for _, e := range ext {
		allowed[e] = true
	}

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsPermission(err) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if len(allowed) > 0 && !allowed[filepath.Ext(d.Name())] {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		if len(files) >= maxFiles {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})

	sort.Strings(files)
	return files, truncated
}

// ReadyResponse builds a FSAccessReady response from a walk result.
func ReadyResponse(files []string, truncated bool) *FSListFilesResponse {
	if files == nil {
		files = []string{}
	}
	return &FSListFilesResponse{
		Files:     files,
		State:     FSAccessReady,
		Truncated: truncated,
	}
}

// TransitioningResponse returns a transitioning response with retry hint.
func TransitioningResponse(msg string) *FSListFilesResponse {
	return &FSListFilesResponse{
		Files:             []string{},
		State:             FSAccessTransitioning,
		RetryAfterSeconds: 2,
		Message:           msg,
	}
}

// UnavailableResponse returns an unavailable response.
func UnavailableResponse(msg string) *FSListFilesResponse {
	return &FSListFilesResponse{
		Files:   []string{},
		State:   FSAccessUnavailable,
		Message: msg,
	}
}
