package notebookworker

import (
	iagent "github.com/piper/piper/internal/agent"
	notebookdocker "github.com/piper/piper/pkg/notebook/worker/driver/docker"
)

const (
	InfrastructureBaremetal = iagent.InfrastructureBaremetal
	InfrastructureDocker    = iagent.InfrastructureDocker
)

type DockerConfig = notebookdocker.Config
type DockerVolume = notebookdocker.Volume
