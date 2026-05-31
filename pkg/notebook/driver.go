package notebook

import "context"

// Driver abstracts the backend that actually runs notebook server processes.
// Master never executes processes directly — always delegates to a Driver.
type Driver interface {
	Start(ctx context.Context, spec NotebookServerSpec, yamlStr string) (*NotebookServer, error)
	Stop(ctx context.Context, nb *NotebookServer) error
}
