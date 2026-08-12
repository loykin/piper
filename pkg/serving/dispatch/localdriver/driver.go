// Package localdriver implements serving.Driver directly in-process for
// docker and baremetal (process) runtimes — no remote worker/tunnel
// involved, mirroring fed.md §13.2's Pipeline and the Notebook domain's
// direct-runtime treatment (pkg/notebook/dispatch/localdriver).
//
// K8s is not covered here: pkg/serving/worker/driver/k8s doesn't implement
// the shared servingdriver.Driver interface docker/process share, so it
// needs its own adaptation as a separate follow-up rather than fitting this
// package's shape.
package localdriver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/loykin/piper/internal/artifact"
	"github.com/loykin/piper/internal/logsink"
	iprocess "github.com/loykin/piper/internal/process"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/serving"
	servingdriver "github.com/loykin/piper/pkg/serving/worker/driver"
	servingdocker "github.com/loykin/piper/pkg/serving/worker/driver/docker"
	servingprocess "github.com/loykin/piper/pkg/serving/worker/driver/process"
)

// EnvResolver resolves manifest.EnvVar entries (including credentialRef)
// into "KEY=value" strings. Mirrors pkg/serving/dispatch.EnvResolver,
// redefined locally so this package has no dependency on the remote-dispatch
// package.
type EnvResolver func(ctx context.Context, projectID string, env []manifest.EnvVar) ([]string, error)

// Config configures a direct, in-process serving driver.
type Config struct {
	// WorkerID is a fixed local identity used to populate Service.WorkerID.
	// A real per-worker ID is meaningless once dispatch is in-process (there
	// is only ever one owner), but the field stays populated so Manager's
	// existing ownership-check code (UpdateStatus comparing svc.WorkerID)
	// keeps working unchanged.
	WorkerID string
	// Infrastructure selects the underlying servingdriver.Driver: "docker" or "baremetal".
	Infrastructure string
	Docker         servingdocker.Config
	LogClient      logsink.PushClient
	// EnvResolver expands credentialRef entries in spec.Options.Env. Optional.
	EnvResolver EnvResolver
	// HealthCheckTimeout bounds how long Deploy's background goroutine waits
	// for the service to answer its health path before reporting it failed.
	// Zero means 30s (matches pkg/serving/worker/worker.go's deploy()).
	HealthCheckTimeout time.Duration
	// ReportStatus delivers an async status update once a backgrounded
	// health check actually completes or fails — mirrors
	// pkg/serving/worker/worker.go's pushStatus, called in-process instead
	// of over a tunnel. Required.
	ReportStatus func(projectID, name, status, endpoint string) error
}

type activeService struct {
	projectID, name string
	gen             uint64 // distinguishes stale OnExit callbacks from a superseded deploy
	exitAs          string // overrides the exit status OnExit reports (see failDeploy)
}

// Driver implements serving.Driver directly against a local
// servingdriver.Driver (docker or process). Call Recover once at startup to
// reattach to services that survived a restart.
type Driver struct {
	cfg    Config
	driver servingdriver.Driver

	mu       sync.Mutex
	services map[string]*activeService // "projectID:name" -> active
	nextGen  uint64
}

// New constructs a Driver. cfg.ReportStatus is required.
func New(cfg Config) (*Driver, error) {
	if cfg.WorkerID == "" {
		return nil, fmt.Errorf("localdriver: WorkerID is required")
	}
	if cfg.ReportStatus == nil {
		return nil, fmt.Errorf("localdriver: ReportStatus is required")
	}
	var drv servingdriver.Driver
	switch cfg.Infrastructure {
	case "docker":
		cfg.Docker.WorkerID = cfg.WorkerID
		d, err := servingdocker.New(cfg.Docker)
		if err != nil {
			return nil, fmt.Errorf("localdriver: docker driver: %w", err)
		}
		drv = d
	case "baremetal":
		drv = servingprocess.New(servingprocess.Config{WorkerID: cfg.WorkerID})
	default:
		return nil, fmt.Errorf("localdriver: unsupported infrastructure %q", cfg.Infrastructure)
	}
	return &Driver{
		cfg:      cfg,
		driver:   drv,
		services: make(map[string]*activeService),
	}, nil
}

func serviceKey(projectID, name string) string { return projectID + ":" + name }

// runtimeName returns a name safe for process supervisors and Docker
// containers. Uses "__" separator since ":" is invalid in many runtime contexts.
func runtimeName(projectID, name string) string {
	if projectID == "" {
		return name
	}
	return projectID + "__" + name
}

// ArtifactTarget is TargetLocal: Piper's own artifact resolver (see
// service_api.go's resolveServiceModel/resolveModelURI) fully resolves the
// model to a local host path before Deploy is ever called — s3/http(s)
// downloads happen there, not in this driver. This mirrors "bare-metal
// drivers return TargetLocal" from pkg/serving/driver.go's doc comment.
//
// Docker direct-runtime uses the same target and therefore the same
// PIPER_MODEL_DIR-as-env-var handoff the existing (remote) docker serving
// driver already uses — that driver does not bind-mount the model directory
// into the container. That is a pre-existing limitation of
// pkg/serving/worker/driver/docker, not something introduced or fixed here;
// §13.1 froze current behavior rather than redesigning it.
func (d *Driver) ArtifactTarget() artifact.Target { return artifact.TargetLocal }

// Deploy reserves the service slot and starts the runtime, then launches a
// background health check — Manager.Deploy is fully synchronous and trusts
// the returned *serving.Service.Status immediately (unlike Notebook's
// Manager, which never trusts Driver.Start's return value), so the fast
// path here must itself return status=starting and only report "running"
// asynchronously once the health check passes, mirroring
// pkg/serving/worker/worker.go's deploy() exactly.
func (d *Driver) Deploy(ctx context.Context, spec serving.ModelService, art artifact.Resolved, yamlStr string) (*serving.Service, error) {
	projectID := spec.Metadata.ProjectID
	name := spec.Metadata.Name
	rt := spec.Spec.Run
	if len(rt.Command) == 0 {
		return nil, fmt.Errorf("localdriver: run.command must not be empty")
	}
	if rt.Port == 0 {
		return nil, fmt.Errorf("localdriver: run.port must be set")
	}
	if err := serving.ValidateDirectPlacement(spec, d.cfg.Infrastructure); err != nil {
		return nil, fmt.Errorf("localdriver: %w", err)
	}

	key := serviceKey(projectID, name)
	d.mu.Lock()
	if _, exists := d.services[key]; exists {
		d.mu.Unlock()
		return nil, fmt.Errorf("service %q is already active", name)
	}
	d.nextGen++
	gen := d.nextGen
	d.services[key] = &activeService{projectID: projectID, name: name, gen: gen}
	d.mu.Unlock()

	modelDir := art.LocalPath

	var image string
	var dockerSpec *manifest.DriverDockerSpec
	if spec.Spec.Driver.Docker != nil {
		image = spec.Spec.Driver.Docker.Image
		dockerSpec = spec.Spec.Driver.Docker
	}
	var gpus string
	if spec.Spec.Driver.Process != nil {
		gpus = spec.Spec.Driver.Process.GPUs
	}

	// Merge: base system vars <- plain options.env <- pre-resolved secrets (highest precedence).
	deployEnv := map[string]string{"PIPER_MODEL_DIR": modelDir, "PIPER_SERVICE_NAME": name}
	for _, e := range spec.Spec.Options.Env {
		if e.ValueFrom == nil && e.Name != "" && e.Value != "" {
			deployEnv[e.Name] = e.Value
		}
	}
	var resolvedEnv []string
	if d.cfg.EnvResolver != nil && len(spec.Spec.Options.Env) > 0 {
		var err error
		resolvedEnv, err = d.cfg.EnvResolver(ctx, projectID, spec.Spec.Options.Env)
		if err != nil {
			d.removeService(key, gen)
			return nil, fmt.Errorf("serving env resolution: %w", err)
		}
		for _, kv := range resolvedEnv {
			if idx := strings.IndexByte(kv, '='); idx > 0 {
				deployEnv[kv[:idx]] = kv[idx+1:]
			}
		}
	}

	rn := runtimeName(projectID, name)
	var sink logsink.LogSink
	if d.cfg.LogClient != nil {
		sink = logsink.NewRedactingSink(logsink.NewGRPCLogSink(projectID, d.cfg.LogClient), logsink.ValuesFromEnv(resolvedEnv))
	}

	endpoint, err := d.driver.Deploy(ctx, servingdriver.DeployRequest{
		ProjectID:   projectID,
		Name:        name,
		RuntimeName: rn,
		Image:       image,
		Docker:      dockerSpec,
		Command:     rt.Command,
		Env:         deployEnv,
		Port:        rt.Port,
		HealthPath:  rt.HealthPath,
		GPUs:        gpus,
		LogSink:     sink,
		OnExit: func(status string) {
			d.mu.Lock()
			svc := d.services[key]
			current := svc != nil && svc.gen == gen
			if current {
				if svc.exitAs != "" {
					status = svc.exitAs
				}
				delete(d.services, key)
			}
			d.mu.Unlock()
			if current {
				d.report(projectID, name, status, "")
			}
		},
	})
	if err != nil {
		d.removeService(key, gen)
		return nil, err
	}

	healthPath := rt.HealthPath
	if healthPath == "" {
		healthPath = "/"
	}
	healthTimeout := d.cfg.HealthCheckTimeout
	if healthTimeout <= 0 {
		healthTimeout = 30 * time.Second
	}
	go func() {
		if err := iprocess.WaitReady(context.Background(), endpoint+healthPath, healthTimeout); err != nil {
			slog.Warn("localdriver: serving health check timed out", "name", name, "endpoint", endpoint)
			if d.failDeploy(key, gen) {
				if stopErr := d.driver.Stop(context.Background(), rn); stopErr != nil {
					slog.Warn("localdriver: serving stop after health failure failed", "name", name, "err", stopErr)
					d.report(projectID, name, serving.StatusFailed, "")
				}
				// If Stop succeeds, the runtime's own OnExit callback fires
				// and reports "failed" via the exitAs override set by
				// failDeploy above, matching worker.go's failService.
			}
			return
		}
		if d.updateEndpoint(key, gen) {
			d.report(projectID, name, serving.StatusRunning, endpoint)
		}
	}()

	return &serving.Service{
		Name:      name,
		ProjectID: projectID,
		Artifact:  artifactLabel(spec),
		Status:    serving.StatusStarting,
		Endpoint:  endpoint,
		WorkerID:  d.cfg.WorkerID,
		YAML:      yamlStr,
	}, nil
}

// Stop terminates the runtime instance. The final "stopped" status is
// reported asynchronously by the runtime's OnExit callback, not here —
// matching Manager.Stop's contract (it only persists StatusStopping itself).
func (d *Driver) Stop(_ context.Context, svc *serving.Service) error {
	return d.driver.Stop(context.Background(), runtimeName(svc.ProjectID, svc.Name))
}

// Recover reattaches to services that survived a process restart. Call once
// at startup (see fed.md §13.1's already-frozen Recover contract).
func (d *Driver) Recover(ctx context.Context) error {
	recoverable, ok := d.driver.(servingdriver.Recoverable)
	if !ok {
		return nil
	}
	onRecovered := func(rec servingdriver.RecoveredHandle) func(status string) {
		key := serviceKey(rec.ProjectID, rec.Name)
		endpoint := fmt.Sprintf("http://127.0.0.1:%d", rec.Port)
		d.mu.Lock()
		d.nextGen++
		gen := d.nextGen
		d.services[key] = &activeService{projectID: rec.ProjectID, name: rec.Name, gen: gen}
		d.mu.Unlock()
		d.report(rec.ProjectID, rec.Name, serving.StatusRunning, endpoint)

		return func(status string) {
			d.mu.Lock()
			svc := d.services[key]
			current := svc != nil && svc.gen == gen
			if current {
				delete(d.services, key)
			}
			d.mu.Unlock()
			if current {
				d.report(rec.ProjectID, rec.Name, status, "")
			}
		}
	}
	onTerminal := func(rec servingdriver.RecoveredHandle, status string) {
		key := serviceKey(rec.ProjectID, rec.Name)
		d.mu.Lock()
		_, active := d.services[key]
		d.mu.Unlock()
		if !active {
			d.report(rec.ProjectID, rec.Name, status, "")
		}
	}
	return recoverable.Recover(ctx, onRecovered, onTerminal)
}

func (d *Driver) removeService(key string, gen uint64) {
	d.mu.Lock()
	if svc := d.services[key]; svc != nil && svc.gen == gen {
		delete(d.services, key)
	}
	d.mu.Unlock()
}

// updateEndpoint confirms gen is still current without mutating anything
// (the endpoint itself is only carried through to ReportStatus, there is no
// local record of it to update).
func (d *Driver) updateEndpoint(key string, gen uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	svc := d.services[key]
	return svc != nil && svc.gen == gen
}

// failDeploy marks the active service's next OnExit report as "failed"
// (instead of whatever the runtime's natural exit status would be) and
// confirms gen is still current — mirrors worker.go's failService.
func (d *Driver) failDeploy(key string, gen uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	svc := d.services[key]
	if svc == nil || svc.gen != gen {
		return false
	}
	svc.exitAs = serving.StatusFailed
	return true
}

func (d *Driver) report(projectID, name, status, endpoint string) {
	if err := d.cfg.ReportStatus(projectID, name, status, endpoint); err != nil {
		slog.Warn("localdriver: report status failed", "name", name, "status", status, "err", err)
	}
}

func artifactLabel(spec serving.ModelService) string {
	if spec.Spec.Model.FromArtifact != nil {
		return spec.Spec.Model.FromArtifact.Step + "/" + spec.Spec.Model.FromArtifact.Artifact
	}
	return spec.Spec.Model.FromURI
}

var _ serving.Driver = (*Driver)(nil)
