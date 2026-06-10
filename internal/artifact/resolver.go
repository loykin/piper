// Package artifact provides the central artifact resolver for all execution modes.
//
// The resolver is the single authority for translating an artifact reference
// (pipeline / step / artifact name / run) into a concrete path or URI that
// a serving runtime or task input can consume.
//
// Callers must specify the Target so the resolver can return the appropriate
// address without guessing:
//
//	local → Resolved.LocalPath (absolute filesystem path)
//	s3    → Resolved.S3URI    (s3://bucket/key prefix)
//
// K8s serving requires S3 storage. Requesting TargetS3 without S3 configured
// returns a clear configuration error rather than a runtime failure.
package artifact

import "context"

// Target is the execution environment that will consume the resolved artifact.
type Target string

const (
	// TargetLocal resolves the artifact to a local filesystem path.
	TargetLocal Target = "local"
	// TargetS3 resolves the artifact to an S3 URI.
	// Requires S3 to be configured; returns an error otherwise.
	TargetS3 Target = "s3"
)

// Resolved holds the concrete addresses for a resolved artifact.
// Only the field matching the requested Target is guaranteed to be non-empty.
type Resolved struct {
	RunID     string
	LocalPath string
	S3URI     string
}

// Resolver translates an artifact reference to a concrete address.
//
// pipeline is the pipeline metadata.name.
// step and artifact are the step name and output artifact name from the pipeline spec.
// run is "latest" to select the most recent successful run, or an explicit run ID.
type Resolver interface {
	Resolve(ctx context.Context, pipeline, step, artifact, run string, target Target) (Resolved, error)
}
