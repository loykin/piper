// Package driver defines the runtime-driver contract used by serving workers.
package driver

import (
	"context"

	"github.com/loykin/piper/internal/logsink"
	"github.com/loykin/piper/pkg/manifest"
)

// ContainerModelDir is the stable in-container location of a resolved model
// artifact for Docker serving runtimes.
const ContainerModelDir = "/piper/model"

type Driver interface {
	Deploy(context.Context, DeployRequest) (string, error)
	Stop(context.Context, string) error
	Status(context.Context, string) string
	KillAll(context.Context) error
}

type Recoverable interface {
	Recover(
		context.Context,
		func(RecoveredHandle) func(status string),
		func(RecoveredHandle, string),
	) error
}

type RecoveredHandle struct {
	ProjectID   string
	Name        string
	RuntimeName string
	Port        int
}

type DeployRequest struct {
	ProjectID   string
	Name        string
	RuntimeName string
	Image       string
	Docker      *manifest.DriverDockerSpec
	Command     []string
	Env         map[string]string
	// ModelDir is the artifact directory on the Piper host. Docker runtimes
	// bind-mount it read-only at ContainerModelDir; process runtimes already
	// access the host path directly through Env.
	ModelDir   string
	Port       int
	HealthPath string
	GPUs       string
	LogSink    logsink.LogSink
	OnExit     func(status string)
}
