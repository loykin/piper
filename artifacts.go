package piper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loykin/piper/pkg/storage"
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
	if containsDotDot(runID) {
		return nil, fmt.Errorf("invalid run id")
	}
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

// runExistenceChecker is the narrow slice of run.Repository the orphan sweep
// needs — kept separate from the full interface so it's trivial to fake in
// tests.
type runExistenceChecker interface {
	ExistingIDs(ctx context.Context, ids []string) (map[string]bool, error)
}

// cleanupOrphanArtifacts removes local artifact directories with no matching
// run row. deleteRunWithArtifacts now deletes the DB row before its
// artifacts (see its comment), which trades "orphan artifact directory" for
// "orphan DB row" as the failure mode when the two steps don't both
// complete — this sweep is what actually reclaims the former. Only the
// local filesystem layout is swept; blobstore-backed artifact storage (S3,
// etc.) needs prefix enumeration with a different cost profile and isn't
// covered here.
//
// excludeDirs lists non-run subdirectories that legitimately live under
// outputDir and must never be swept, no matter their age — for example the
// default serving model dir (outputDir/models, see Piper.modelDir). A run ID
// never collides with these names, so excluding them by exact basename is
// safe.
func cleanupOrphanArtifacts(ctx context.Context, repo runExistenceChecker, st storage.Store, outputDir string, excludeDirs ...string) {
	if st != nil || outputDir == "" {
		return
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("orphan artifact sweep: read output dir failed", "err", err)
		}
		return
	}
	exclude := make(map[string]bool, len(excludeDirs))
	for _, d := range excludeDirs {
		exclude[d] = true
	}
	// Skip anything recent enough that its run may simply not have committed
	// to the DB yet — Create() happens before a run produces output, but
	// give it a comfortable margin rather than racing a fresh run.
	const graceAge = 10 * time.Minute
	now := time.Now()
	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || exclude[e.Name()] {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < graceAge {
			continue
		}
		candidates = append(candidates, e.Name())
	}
	if len(candidates) == 0 {
		return
	}
	existing, err := repo.ExistingIDs(ctx, candidates)
	if err != nil {
		slog.Warn("orphan artifact sweep: check existing runs failed", "err", err)
		return
	}
	for _, name := range candidates {
		if existing[name] {
			continue
		}
		dir := filepath.Join(outputDir, name)
		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("orphan artifact sweep: remove failed", "dir", dir, "err", err)
			continue
		}
		slog.Info("orphan artifact sweep: removed directory with no matching run", "run_id", name)
	}
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
