// Package driver defines the runtime-driver contract used by serving workers.
package driver

import (
	"context"

	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/pkg/manifest"
)

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
	Port        int
	HealthPath  string
	GPUs        string
	LogSink     logsink.LogSink
	OnExit      func(status string)
}
