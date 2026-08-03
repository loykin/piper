package servingworker

import (
	iagent "github.com/loykin/piper/internal/agent"
	servingdocker "github.com/loykin/piper/pkg/serving/worker/driver/docker"
)

const (
	InfrastructureBaremetal = iagent.InfrastructureBaremetal
	InfrastructureDocker    = iagent.InfrastructureDocker
)

type DockerConfig = servingdocker.Config
