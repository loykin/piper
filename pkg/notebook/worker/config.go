package notebookworker

import (
	notebookdriver "github.com/piper/piper/pkg/notebook/worker/driver"
	notebookdocker "github.com/piper/piper/pkg/notebook/worker/driver/docker"
)

const (
	RuntimeProcess = notebookdriver.ModeProcess
	RuntimeDocker  = notebookdriver.ModeDocker
)

type DockerConfig = notebookdocker.Config
type DockerVolume = notebookdocker.Volume
