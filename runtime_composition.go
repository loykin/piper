package piper

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/loykin/piper/internal/event"
	"github.com/loykin/piper/internal/pipelinedispatch"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/internal/queue"
	storemod "github.com/loykin/piper/internal/store"
	"github.com/loykin/piper/pkg/credential"
	"github.com/loykin/piper/pkg/notebook"
	"github.com/loykin/piper/pkg/notebook/dispatch/localdriver"
	notebookk8sdriver "github.com/loykin/piper/pkg/notebook/dispatch/localdriver/k8s"
	"github.com/loykin/piper/pkg/notebook/notebookdriver"
	notebookdocker "github.com/loykin/piper/pkg/notebook/notebookdriver/docker"
	"github.com/loykin/piper/pkg/serving"
	servinglocaldriver "github.com/loykin/piper/pkg/serving/dispatch/localdriver"
	servingk8sdriver "github.com/loykin/piper/pkg/serving/dispatch/localdriver/k8s"
	servingdocker "github.com/loykin/piper/pkg/serving/servingdriver/docker"
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
	var driver serving.Driver
	statusSink := serving.NewStatusSink(repos.Serving)

	switch cfg.Runtime.Type {
	case RuntimeDocker, RuntimeBaremetal:
		local, err := servinglocaldriver.New(servinglocaldriver.Config{
			RuntimeID:      servingLocalRuntimeID,
			Infrastructure: cfg.Runtime.Type,
			Docker:         servingdocker.Config{Network: cfg.Runtime.Docker.Network},
			LogClient:      localLogPushClient{store: repos.Log, metrics: repos.Metric},
			EnvResolver:    credentials.ResolveEnv,
			ReportStatus: func(projectID, name, status, endpoint string) error {
				return statusSink.Update(context.Background(), projectID, servingLocalRuntimeID, name, status, endpoint)
			},
		})
		if err != nil {
			return result, fmt.Errorf("create serving local driver: %w", err)
		}
		driver = local
	case RuntimeK8s:
		k8s, err := servingk8sdriver.New(servingk8sdriver.Config{
			RuntimeID:            servingK8sLocalRuntimeID,
			Namespaces:           cfg.Runtime.K8s.Namespaces,
			Client:               cfg.Runtime.K8s.Client,
			ArtifactFetcherImage: cfg.Runtime.K8s.PipelineRunnerImage,
			ArtifactPullPolicy:   corev1.PullPolicy(cfg.Runtime.K8s.ImagePullPolicy),
			WorkloadURL:          cfg.Runtime.K8s.WorkloadURL,
			WorkloadToken:        cfg.Server.WorkloadToken,
			LogClient:            localLogPushClient{store: repos.Log, metrics: repos.Metric},
			ReportStatus: func(projectID, name, status, endpoint string) error {
				return statusSink.Update(context.Background(), projectID, servingK8sLocalRuntimeID, name, status, endpoint)
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

	manager := serving.NewWithStatusSink(repos.Serving, driver, statusSink)
	manager.SetRuntime(cfg.Runtime.Type)
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
	var driver notebook.Driver
	statusSink := notebook.NewStatusSink(repos.Notebook, repos.NotebookVolume)

	switch cfg.Runtime.Type {
	case RuntimeDocker, RuntimeBaremetal:
		infrastructure := notebookdriver.ModeDocker
		if cfg.Runtime.Type == RuntimeBaremetal {
			infrastructure = notebookdriver.ModeProcess
		}
		local, err := localdriver.New(localdriver.Config{
			RuntimeID:        notebookLocalRuntimeID,
			Infrastructure:   infrastructure,
			PlacementRuntime: cfg.Runtime.Type,
			Docker:           notebookdocker.Config{Network: cfg.Runtime.Docker.Network},
			NotebooksRoot:    cfg.Notebook.NotebooksRoot,
			PortRange:        cfg.Notebook.PortRange,
			LogClient:        localLogPushClient{store: repos.Log, metrics: repos.Metric},
			EnvResolver:      credentials.ResolveEnv,
			ReportStatus: func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error {
				return statusSink.Update(context.Background(), projectID, notebookLocalRuntimeID, name, status, endpoint, workDir, token, pid, env)
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
			RuntimeID: notebookK8sLocalRuntimeID, Namespaces: cfg.Runtime.K8s.Namespaces,
			Client: cfg.Runtime.K8s.Client, LogClient: localLogPushClient{store: repos.Log, metrics: repos.Metric},
			ReportStatus: func(projectID, name, status, endpoint, workDir, token string, pid int, env string) error {
				return statusSink.Update(context.Background(), projectID, notebookK8sLocalRuntimeID, name, status, endpoint, workDir, token, pid, env)
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

	manager := notebook.NewWithStatusSink(repos.Notebook, repos.NotebookVolume, driver, statusSink)
	manager.SetRuntime(cfg.Runtime.Type)
	result.manager = manager
	return result, nil
}

func composePipelineRuntime(cfg Config, ctx context.Context, repos *storemod.Repos, q *queue.Queue, publisher event.Publisher) (pipelinedispatch.ExecutionBackend, runtimeObserver, error) {
	complete := func(result proto.TaskResult) error {
		applied, err := q.CompleteApplied(context.Background(), result)
		if applied {
			// Only persist metrics/publish metric.recorded for a report that
			// actually transitioned the step — a duplicate report for an
			// already-terminal step (e.g. k8s Job recovery re-observing a
			// Job that finished before the last server restart, see
			// k8slauncher.RecoverJobs) must not re-insert metrics or
			// re-trigger metric-based Alert Rule notifications.
			persistTaskMetrics(context.Background(), repos.Metric, publisher, result)
		}
		return err
	}
	logClient := localLogPushClient{store: repos.Log, metrics: repos.Metric, events: publisher}

	switch cfg.Runtime.Type {
	case RuntimeK8s:
		backend, err := pipelinedispatch.NewK8sBackend(pipelinedispatch.K8sBackendConfig{
			Context: ctx, Client: cfg.Runtime.K8s.Client, Namespaces: cfg.Runtime.K8s.Namespaces,
			PipelineRunnerImage: cfg.Runtime.K8s.PipelineRunnerImage, ImagePullPolicy: cfg.Runtime.K8s.ImagePullPolicy,
			TTLAfterFinished: cfg.Runtime.K8s.TTLAfterFinished, MasterURL: cfg.Runtime.K8s.WorkloadURL,
			WorkloadToken: cfg.Server.WorkloadToken, LogClient: logClient, Complete: complete, RenewLeases: q.RenewLeases,
		})
		return backend, backend, err
	case RuntimeDocker:
		concurrency := cfg.Runtime.Docker.Concurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		backend, err := pipelinedispatch.NewDockerBackend(pipelinedispatch.DockerBackendConfig{
			Network: cfg.Runtime.Docker.Network, OutputDir: cfg.OutputDir, Concurrency: concurrency,
			MasterURL: cfg.Runtime.Docker.WorkloadURL, WorkloadToken: cfg.Server.WorkloadToken,
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
