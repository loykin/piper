package servingworker

import (
	iagent "github.com/piper/piper/internal/agent"
	servingdocker "github.com/piper/piper/pkg/serving/worker/driver/docker"
)

const (
	InfrastructureBaremetal = iagent.InfrastructureBaremetal
	InfrastructureDocker    = iagent.InfrastructureDocker
)

type DockerConfig = servingdocker.Config
