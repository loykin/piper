package piper

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/loykin/piper/internal/pipelinedispatch"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/queue"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/dispatch/localdriver"
	notebookk8sdriver "github.com/loykin/piper/pkg/notebook/dispatch/localdriver/k8s"
	notebookworkerdriver "github.com/loykin/piper/pkg/notebook/worker/driver"
	notebookdocker "github.com/loykin/piper/pkg/notebook/worker/driver/docker"
	"github.com/loykin/piper/pkg/serving"
	servinglocaldriver "github.com/loykin/piper/pkg/serving/dispatch/localdriver"
	servingk8sdriver "github.com/loykin/piper/pkg/serving/dispatch/localdriver/k8s"
	servingdocker "github.com/loykin/piper/pkg/serving/worker/driver/docker"
)

// runtimeObserver is implemented by runtimes that reconcile infrastructure
// state asynchronously. Runtime selection belongs in this composition file;
// domain managers only depend on their driver contracts.
type runtimeObserver interface {
	Observe(context.Context)
}

type servingRuntime struct {
	manager   *serving.Manager
	k8sDriver *servingk8sdriver.Driver
	observer  runtimeObserver
}

func composeServingRuntime(cfg Config, repos *storemod.Repos, credentials *credential.Store) (servingRuntime, error) {
	var result servingRuntime
	var manager *serving.Manager
	var driver serving.Driver

	switch cfg.Runtime.Type {
	case RuntimeDocker, RuntimeBaremetal:
		local, err := servinglocaldriver.New(servinglocaldriver.Config{
			WorkerID:       servingLocalWorkerID,
			Infrastructure: cfg.Runtime.Type,
			Docker:         servingdocker.Config{Network: cfg.Runtime.Docker.Network},
			LogClient:      localLogPushClient{store: repos.Log, metrics: repos.Metric},
			EnvResolver:    credentials.ResolveEnv,
			ReportStatus: func(projectID, name, status, endpoint string) error {
				return manager.UpdateStatus(context.Background(), projectID, servingLocalWorkerID, name, status, endpoint)
			},
		})
		if err != nil {
			return result, fmt.Errorf("create serving local driver: %w", err)
		}
		driver = local
	case RuntimeK8s:
		k8s, err := servingk8sdriver.New(servingk8sdriver.Config{
			WorkerID:             servingK8sLocalWorkerID,
			Namespaces:           cfg.Runtime.K8s.Namespaces,
			Client:               cfg.Runtime.K8s.Client,
			ArtifactFetcherImage: cfg.Runtime.K8s.PipelineRunnerImage,
			ArtifactPullPolicy:   corev1.PullPolicy(cfg.Runtime.K8s.ImagePullPolicy),
			WorkloadURL:          cfg.Runtime.K8s.WorkloadURL,
			WorkerToken:          cfg.Server.WorkerToken,
			LogClient:            localLogPushClient{store: repos.Log, metrics: repos.Metric},
			ReportStatus: func(projectID, name, status, endpoint string) error {
				return manager.UpdateStatus(context.Background(), projectID, servingK8sLocalWorkerID, name, status, endpoint)
			},
		})
		if err != nil {
			return result, fmt.Errorf("create serving k8s driver: %w", err)
		}
		driver = k8s
		result.k8sDriver = k8s
		result.observer = k8s
	default:
		return result, fmt.Errorf("runtime.type must be k8s, docker, or baremetal")
	}

	manager = serving.New(repos.Serving, driver)
	result.manager = manager
	return result, nil
}

type notebookRuntime struct {
	manager   *notebook.Manager
	workspace notebook.WorkspaceReader
	observer  runtimeObserver
}

func composeNotebookRuntime(cfg Config, repos *storemod.Repos, credentials *credential.Store) (notebookRuntime, error) {
	var result notebookRuntime
	var manager *notebook.Manager
	var driver notebook.Driver

	switch cfg.Runtime.Type {
	case RuntimeDocker, RuntimeBaremetal:
		infrastructure := notebookworkerdriver.ModeDocker
		if cfg.Runtime.Type == RuntimeBaremetal {
			infrastructure = notebookworkerdriver.ModeProcess
		}
		local, err := localdriver.New(localdriver.Config{
			WorkerID:         notebookLocalWorkerID,
			Infrastructure:   infrastructure,
			PlacementRuntime: cfg.Runtime.Type,
			Docker:           notebookdocker.Config{Network: cfg.Runtime.Docker.Network},
			NotebooksRoot:    cfg.NotebookWorker.NotebooksRoot,
			PortRange:        cfg.NotebookWorker.PortRange,
			LogClient:        localLogPushClient{store: repos.Log, metrics: repos.Metric},
			EnvResolver:      credentials.ResolveEnv,
			ReportStatus: func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error {
				return manager.UpdateStatus(context.Background(), projectID, notebookLocalWorkerID, name, status, endpoint, workDir, token, pid, env)
			},
		})
		if err != nil {
			return result, fmt.Errorf("create notebook local driver: %w", err)
		}
		driver = local
		result.workspace = notebook.LocalWorkspaceReader{}
	case RuntimeK8s:
		result.workspace = &notebookk8sdriver.WorkspaceReader{
			Client: cfg.Runtime.K8s.Client, RestConfig: cfg.Runtime.K8s.RestConfig, Namespaces: cfg.Runtime.K8s.Namespaces,
		}
		k8s, err := notebookk8sdriver.New(notebookk8sdriver.Config{
			WorkerID: notebookK8sLocalWorkerID, Namespaces: cfg.Runtime.K8s.Namespaces,
			Client: cfg.Runtime.K8s.Client, LogClient: localLogPushClient{store: repos.Log, metrics: repos.Metric},
			ReportStatus: func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error {
				return manager.UpdateStatus(context.Background(), projectID, notebookK8sLocalWorkerID, name, status, endpoint, workDir, token, pid, env)
			},
		})
		if err != nil {
			return result, fmt.Errorf("create notebook k8s driver: %w", err)
		}
		driver = k8s
		result.observer = k8s
	default:
		return result, fmt.Errorf("runtime.type must be k8s, docker, or baremetal")
	}

	manager = notebook.New(repos.Notebook, repos.NotebookVolume, driver)
	result.manager = manager
	return result, nil
}

func composePipelineRuntime(cfg Config, ctx context.Context, repos *storemod.Repos, q *queue.Queue) (pipelinedispatch.ExecutionBackend, runtimeObserver, error) {
	complete := func(result proto.TaskResult) error {
		persistTaskMetrics(context.Background(), repos.Metric, result)
		return q.Complete(context.Background(), result)
	}
	logClient := localLogPushClient{store: repos.Log, metrics: repos.Metric}

	switch cfg.Runtime.Type {
	case RuntimeK8s:
		backend, err := pipelinedispatch.NewK8sBackend(pipelinedispatch.K8sBackendConfig{
			Context: ctx, Client: cfg.Runtime.K8s.Client, Namespaces: cfg.Runtime.K8s.Namespaces,
			PipelineRunnerImage: cfg.Runtime.K8s.PipelineRunnerImage, ImagePullPolicy: cfg.Runtime.K8s.ImagePullPolicy,
			TTLAfterFinished: cfg.Runtime.K8s.TTLAfterFinished, MasterURL: cfg.Runtime.K8s.WorkloadURL,
			WorkerToken: cfg.Server.WorkerToken, LogClient: logClient, Complete: complete, RenewLeases: q.RenewLeases,
		})
		return backend, backend, err
	case RuntimeDocker:
		concurrency := cfg.Runtime.Docker.Concurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		backend, err := pipelinedispatch.NewDockerBackend(pipelinedispatch.DockerBackendConfig{
			Network: cfg.Runtime.Docker.Network, OutputDir: cfg.OutputDir, Concurrency: concurrency,
			MasterURL: cfg.Runtime.Docker.WorkloadURL, WorkerToken: cfg.Server.WorkerToken,
			LogClient: logClient, Complete: complete, RenewLeases: q.RenewLeases,
		})
		return backend, backend, err
	case RuntimeBaremetal:
		concurrency := cfg.Runtime.Baremetal.Concurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		backend, err := pipelinedispatch.NewBaremetalBackend(pipelinedispatch.BaremetalBackendConfig{
			MetaDir: cfg.Runtime.Baremetal.MetaDir, OutputDir: cfg.OutputDir, Concurrency: concurrency,
			LogClient: logClient, Complete: complete, RenewLeases: q.RenewLeases,
		})
		return backend, backend, err
	default:
		return nil, nil, fmt.Errorf("runtime.type must be k8s, docker, or baremetal")
	}
}
