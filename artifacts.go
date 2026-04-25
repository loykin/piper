package piper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type artifactFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type artifactEntry struct {
	Name  string         `json:"name"`
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

// listArtifactsS3 lists objects under prefix runID/ grouped by step/artifact.
func listArtifactsS3(ctx context.Context, client *s3.Client, bucket, runID string) ([]stepArtifacts, error) {
	prefix := runID + "/"
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	type mk struct{ step, artifact string }
	filesMap := map[mk][]artifactFile{}
	var orderedKeys []mk
	seen := map[mk]bool{}
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			rel := strings.TrimPrefix(aws.ToString(obj.Key), prefix)
			parts := strings.SplitN(rel, "/", 3)
			if len(parts) < 3 || parts[2] == "" {
				continue
			}
			key := mk{parts[0], parts[1]}
			if !seen[key] {
				seen[key] = true
				orderedKeys = append(orderedKeys, key)
			}
			var modAt time.Time
			if obj.LastModified != nil {
				modAt = *obj.LastModified
			}
			filesMap[key] = append(filesMap[key], artifactFile{
				Path:       parts[2],
				Size:       aws.ToInt64(obj.Size),
				ModifiedAt: modAt,
			})
		}
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

// deleteArtifacts removes all artifact files for a run (local or S3).
func deleteArtifacts(ctx context.Context, cli *s3.Client, bucket, outputDir, runID string) error {
	if cli != nil && bucket != "" {
		return deleteArtifactsS3(ctx, cli, bucket, runID)
	}
	return deleteArtifactsLocal(outputDir, runID)
}

func deleteArtifactsLocal(outputDir, runID string) error {
	runDir := filepath.Join(outputDir, runID)
	if err := os.RemoveAll(runDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func deleteArtifactsS3(ctx context.Context, client *s3.Client, bucket, runID string) error {
	prefix := runID + "/"
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return err
		}
		var objs []types.ObjectIdentifier
		for _, obj := range page.Contents {
			key := obj.Key
			objs = append(objs, types.ObjectIdentifier{Key: key})
		}
		if len(objs) == 0 {
			continue
		}
		if _, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
		}); err != nil {
			return err
		}
	}
	return nil
}

// downloadArtifactS3Raw streams an S3 artifact file to an http.ResponseWriter.
func downloadArtifactS3Raw(w http.ResponseWriter, r *http.Request, client *s3.Client, bucket, runID, step, rest string) {
	key := fmt.Sprintf("%s/%s/%s", runID, step, rest)
	out, err := client.GetObject(r.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	defer func() { _ = out.Body.Close() }()
	filename := filepath.Base(rest)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if out.ContentLength != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", *out.ContentLength))
	}
	_, _ = io.Copy(w, out.Body)
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
