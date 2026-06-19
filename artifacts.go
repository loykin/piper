package piper

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piper/piper/pkg/storage"
)

type artifactFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type artifactEntry struct {
	Name  string         `json:"name"`
	Type  string         `json:"type,omitempty"` // viewer hint from pipeline YAML
	Files []artifactFile `json:"files"`
}

type stepArtifacts struct {
	Step      string          `json:"step"`
	Artifacts []artifactEntry `json:"artifacts"`
}

func containsDotDot(p string) bool {
	for _, part := range strings.Split(filepath.ToSlash(p), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// listArtifactsLocal scans outputDir/runID/ grouped by step → artifact → files.
func listArtifactsLocal(outputDir, runID string) ([]stepArtifacts, error) {
	runDir := filepath.Join(outputDir, runID)
	stepDirs, err := os.ReadDir(runDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []stepArtifacts{}, nil
		}
		return nil, err
	}
	var result []stepArtifacts
	for _, stepEnt := range stepDirs {
		if !stepEnt.IsDir() {
			continue
		}
		stepName := stepEnt.Name()
		artDirs, err := os.ReadDir(filepath.Join(runDir, stepName))
		if err != nil {
			continue
		}
		var artifacts []artifactEntry
		for _, artEnt := range artDirs {
			artName := artEnt.Name()
			artRoot := filepath.Join(runDir, stepName, artName)
			var files []artifactFile
			if artEnt.IsDir() {
				_ = filepath.Walk(artRoot, func(p string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					rel, _ := filepath.Rel(artRoot, p)
					files = append(files, artifactFile{
						Path:       filepath.ToSlash(rel),
						Size:       info.Size(),
						ModifiedAt: info.ModTime().UTC(),
					})
					return nil
				})
			} else {
				info, _ := artEnt.Info()
				files = append(files, artifactFile{
					Path:       artName,
					Size:       info.Size(),
					ModifiedAt: info.ModTime().UTC(),
				})
			}
			if len(files) > 0 {
				artifacts = append(artifacts, artifactEntry{Name: artName, Files: files})
			}
		}
		if len(artifacts) > 0 {
			result = append(result, stepArtifacts{Step: stepName, Artifacts: artifacts})
		}
	}
	return result, nil
}

// listArtifactsStore lists objects under prefix runID/ from a blobstore.
func listArtifactsStore(ctx context.Context, st storage.Store, runID string) ([]stepArtifacts, error) {
	prefix := runID + "/"
	objs, err := st.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	type mk struct{ step, artifact string }
	filesMap := map[mk][]artifactFile{}
	var orderedKeys []mk
	seen := map[mk]bool{}
	for _, obj := range objs {
		rel := strings.TrimPrefix(obj.Key, prefix)
		parts := strings.SplitN(rel, "/", 3)
		if len(parts) < 3 || parts[2] == "" {
			continue
		}
		key := mk{parts[0], parts[1]}
		if !seen[key] {
			seen[key] = true
			orderedKeys = append(orderedKeys, key)
		}
		filesMap[key] = append(filesMap[key], artifactFile{
			Path:       parts[2],
			Size:       obj.Size,
			ModifiedAt: obj.ModifiedAt,
		})
	}
	stepMap := map[string][]artifactEntry{}
	var stepOrder []string
	seenStep := map[string]bool{}
	for _, k := range orderedKeys {
		if !seenStep[k.step] {
			seenStep[k.step] = true
			stepOrder = append(stepOrder, k.step)
		}
		stepMap[k.step] = append(stepMap[k.step], artifactEntry{Name: k.artifact, Files: filesMap[k]})
	}
	var result []stepArtifacts
	for _, step := range stepOrder {
		result = append(result, stepArtifacts{Step: step, Artifacts: stepMap[step]})
	}
	return result, nil
}

// deleteArtifacts removes all artifact files for a run.
// Uses the blobstore if configured; falls back to local filesystem.
func deleteArtifacts(ctx context.Context, st storage.Store, outputDir, runID string) error {
	if st != nil {
		// List all keys under runID/ and delete them.
		objs, err := st.List(ctx, runID+"/")
		if err != nil {
			return err
		}
		if len(objs) > 0 {
			keys := make([]string, len(objs))
			for i, o := range objs {
				keys[i] = o.Key
			}
			return st.Delete(ctx, keys...)
		}
		return nil
	}
	runDir := filepath.Join(outputDir, runID)
	if err := os.RemoveAll(runDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// downloadArtifactStore streams an artifact from the store to an http.ResponseWriter.
func downloadArtifactStore(w http.ResponseWriter, r *http.Request, st storage.Store, runID, step, rest string) {
	key := fmt.Sprintf("%s/%s/%s", runID, step, rest)
	filename := filepath.Base(rest)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := storage.ServeHTTP(r.Context(), st, key, w); err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
	}
}

// downloadArtifactLocal streams a local artifact file to an http.ResponseWriter.
func downloadArtifactLocal(w http.ResponseWriter, r *http.Request, outputDir, runID, step, rest string) {
	localPath := filepath.Join(outputDir, runID, step, filepath.FromSlash(rest))
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	baseAbs, _ := filepath.Abs(outputDir)
	if !strings.HasPrefix(absPath, baseAbs+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	http.ServeFile(w, r, absPath)
}
