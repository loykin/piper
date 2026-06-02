package serving

import (
	"context"

	"github.com/piper/piper/pkg/artifact"
)

// Driver abstracts the backend that actually runs serving processes.
// Master never executes processes directly — always delegates to a Driver.
type Driver interface {
	// ArtifactTarget declares how this driver expects the model artifact to be
	// delivered. K8s-based drivers return TargetS3 (pods cannot access the
	// server's local filesystem); bare-metal drivers return TargetLocal.
	ArtifactTarget() artifact.Target
	Deploy(ctx context.Context, spec ModelService, art artifact.Resolved, yamlStr string) (*Service, error)
	Stop(ctx context.Context, svc *Service) error
	Restart(ctx context.Context, spec ModelService, art artifact.Resolved, yamlStr string) error
}
