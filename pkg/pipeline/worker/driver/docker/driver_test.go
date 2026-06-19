package dockerdriver

import (
	"context"
	"testing"

	dockerclient "github.com/moby/moby/client"

	pipelinedriver "github.com/piper/piper/pkg/pipeline/worker/driver"
	"github.com/piper/piper/pkg/pipeline/worker/driver/drivertest"
)

var _ pipelinedriver.Driver = (*Driver)(nil)

func TestDockerDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pipelinedriver.Driver {
		return NewWithClient(Config{WorkerID: "contract-test"}, &emptyDockerClient{})
	})
}

// emptyDockerClient implements only the methods exercised by the contract tests
// (ContainerList for Recover on empty state). All other methods panic if called.
type emptyDockerClient struct{ dockerclient.APIClient }

func (c *emptyDockerClient) ContainerList(_ context.Context, _ dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{}, nil
}
