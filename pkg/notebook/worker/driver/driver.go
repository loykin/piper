// Package driver defines the runtime-driver contract used by notebook workers.
package driver

import (
	"context"

	"github.com/piper/piper/internal/logsink"
	"github.com/piper/piper/pkg/notebook"
)

const (
	ModeProcess = "process"
	ModeDocker  = "docker"
)

// Driver controls a notebook execution backend.
type Driver interface {
	Start(context.Context, StartRequest) (*StartedHandle, error)
	Stop(context.Context, string) error
	KillAll(context.Context) error
	Status(context.Context, string) string
}

// Recoverable is implemented by drivers that can rediscover workloads after a
// worker restart.
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

type StartRequest struct {
	RuntimeName string
	ProjectID   string
	Name        string
	Spec        notebook.Notebook
	WorkDir     string
	Port        int
	Token       string
	BaseURL     string
	ExtraEnv    []string // merged plain + resolved secret env vars ("KEY=value")
	OnExit      func(status string)
	LogSink     logsink.LogSink
}

type StartedHandle struct {
	Endpoint    string
	PID         int
	Token       string
	EnvPath     string
	ContainerID string
}
