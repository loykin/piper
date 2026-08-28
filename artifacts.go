package piper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/pkg/project"
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
	objs, err := st.List(ctx, prefix, "")
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

// cleanupOrphanArtifacts removes local workspace directories with no
// matching run row. deleteRunWithArtifacts now deletes the DB row before its
// artifacts (see its comment), which trades "orphan artifact directory" for
// "orphan DB row" as the failure mode when the two steps don't both
// complete — this sweep is what actually reclaims the former.
//
// This always sweeps outputDir, regardless of whether an artifact Store is
// configured: outputDir holds each run's own ephemeral workspace (fed.md
// §13.6 — distinct from the immutable artifact repository), and that
// workspace's lifetime is tied to the run's own lifecycle, not to whether
// artifacts additionally live in a separate blobstore. Only the local
// filesystem layout is swept; blobstore-backed artifact storage (S3, a
// LocalStore rooted under outputDir, etc.) is a different namespace — the
// caller is responsible for excluding it via excludeDirs (see
// Piper.cleanupOrphanArtifacts).
//
// excludeDirs lists non-run subdirectories that legitimately live under
// outputDir and must never be swept, no matter their age — for example the
// default serving model dir (outputDir/models, see Piper.modelDir) or a
// LocalStore's own root. A run ID never collides with these names, so
// excluding them by exact basename is safe.
func cleanupOrphanArtifacts(ctx context.Context, repo runExistenceChecker, outputDir string, excludeDirs ...string) {
	if outputDir == "" {
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

// deleteArtifactsFromStore removes a run's artifact copies from the
// artifact repository (Store) only — used by artifactTTL retention, which
// must retire a run's artifact blobs without disturbing the run's own
// record or workspace (fed.md §13.6: the two have independent lifecycles).
// A nil store is a no-op: with no artifact repository configured, there is
// nothing here to delete — deleteRunWorkspace covers the workspace copy
// once the run itself expires via runTTL.
func deleteArtifactsFromStore(ctx context.Context, st storage.Store, runID string) error {
	if st == nil {
		return nil
	}
	objs, err := st.List(ctx, runID+"/", "")
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return nil
	}
	keys := make([]string, len(objs))
	for i, o := range objs {
		keys[i] = o.Key
	}
	return st.Delete(ctx, keys...)
}

// deleteRunWorkspace removes a run's local ephemeral workspace directory —
// independent of whether an artifact Store is configured, since the
// workspace's lifetime is tied to the run's own lifecycle (runTTL / explicit
// deletion), not to the separate artifact repository's (artifactTTL).
func deleteRunWorkspace(outputDir, runID string) error {
	if outputDir == "" {
		return nil
	}
	runDir := filepath.Join(outputDir, runID)
	if err := os.RemoveAll(runDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// downloadArtifactStore streams an artifact from the store to an
// http.ResponseWriter. It returns notFound=true (writing nothing) when the
// object cannot be found, so the caller can decide between the generic
// "artifact not found" response and a storage-backend-mismatch diagnostic
// before anything is written.
func downloadArtifactStore(w http.ResponseWriter, r *http.Request, st storage.Store, runID, step, rest string) (notFound bool) {
	key := fmt.Sprintf("%s/%s/%s", runID, step, rest)
	filename := filepath.Base(rest)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := storage.ServeHTTP(r.Context(), st, key, w); err != nil {
		return true
	}
	return false
}

// downloadArtifactLocal streams a local artifact file to an
// http.ResponseWriter. It returns notFound=true (writing nothing) when the
// file does not exist, so the caller can decide between the generic
// "artifact not found" response and a storage-backend-mismatch diagnostic.
// An invalid path is fully handled here (400 already written) and always
// reports notFound=false so the caller does nothing further.
func downloadArtifactLocal(w http.ResponseWriter, r *http.Request, outputDir, runID, step, rest string) (notFound bool) {
	localPath := filepath.Join(outputDir, runID, step, filepath.FromSlash(rest))
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return false
	}
	baseAbs, _ := filepath.Abs(outputDir)
	if !strings.HasPrefix(absPath, baseAbs+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return false
	}
	if _, statErr := os.Stat(absPath); statErr != nil {
		return true
	}
	http.ServeFile(w, r, absPath)
	return false
}

// runStorageBackend implements viewer.RunStorageLookup: it returns the
// storage-identity stamp recorded on runID's row (see run.Run.StorageBackend),
// so a consumer of that run's artifacts (viewer materialization, artifact
// download/list) can tell a legitimately-missing artifact apart from one
// made unreachable by a storage backend change since the run's data was
// written. Returns ("", nil) when the run itself can't be found — the
// caller's own not-found handling already covers that case.
func (p *Piper) runStorageBackend(ctx context.Context, projectID, runID string) (string, error) {
	r, err := p.repos.Run.Get(ctx, projectID, runID)
	if err != nil || r == nil {
		return "", err
	}
	return r.StorageBackend, nil
}

// storageBackendMismatch reports whether runID's stored artifacts were
// written under a storage backend that is no longer the live one. A run
// with no stamp (predates this feature) or whose stamp matches the live
// backend never mismatches — see docs on the storage-identity stamp.
func (p *Piper) storageBackendMismatch(ctx context.Context, runID string) bool {
	pctx, _ := project.FromContext(ctx)
	backend, err := p.runStorageBackend(ctx, pctx.ID, runID)
	if err != nil || backend == "" {
		return false
	}
	return backend != p.storageIdentity
}

// writeStorageBackendMismatchJSON writes the same {error, code, retryable}
// shape run.Handler's writeMemberError uses for
// memberclient.ErrStorageBackendMismatch, for callers (like ServeDownload)
// that write directly to an http.ResponseWriter instead of going through a
// gin.Context.
func writeStorageBackendMismatchJSON(w http.ResponseWriter) {
	body := struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}{
		Error:     memberclient.ErrStorageBackendMismatch.Error(),
		Code:      memberclient.ErrorCodeStorageBackendMismatch,
		Retryable: false,
	}
	b, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(b)
}
