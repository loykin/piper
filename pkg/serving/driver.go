package serving

import (
	"context"

	"github.com/piper/piper/pkg/artifact"
)

// Driver abstracts the backend that actually runs serving processes.
// Master never executes processes directly — always delegates to a Driver.
type Driver interface {
	Deploy(ctx context.Context, spec ModelService, art artifact.Resolved, yamlStr string) (*Service, error)
	Stop(ctx context.Context, svc *Service) error
	Restart(ctx context.Context, spec ModelService, art artifact.Resolved, yamlStr string) error
}
