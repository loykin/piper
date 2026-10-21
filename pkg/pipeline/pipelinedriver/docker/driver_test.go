package dockerdriver

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	"github.com/loykin/piper/internal/docker/dockertest"
	"github.com/loykin/piper/internal/proto"
	"github.com/loykin/piper/pkg/manifest"
	"github.com/loykin/piper/pkg/pipeline"
	pipelinedriver "github.com/loykin/piper/pkg/pipeline/pipelinedriver"
	"github.com/loykin/piper/pkg/pipeline/pipelinedriver/drivertest"
)

var _ pipelinedriver.Driver = (*Driver)(nil)

func TestDockerDriverContract(t *testing.T) {
	drivertest.RunContract(t, func() pipelinedriver.Driver {
		return NewWithClient(Config{RuntimeID: "contract-test"}, &emptyDockerClient{})
	})
}

// emptyDockerClient implements only the methods exercised by the contract tests
// (ContainerList for Recover on empty state). All other methods panic if called.
type emptyDockerClient struct{ dockerclient.APIClient }

func (c *emptyDockerClient) ContainerList(_ context.Context, _ dockerclient.ContainerListOptions) (dockerclient.ContainerListResult, error) {
	return dockerclient.ContainerListResult{}, nil
}

func TestDockerDriverAppliesStepResources(t *testing.T) {
	step := pipeline.Step{
		Name: "train",
		Driver: manifest.DriverSpec{Docker: &manifest.DriverDockerSpec{
			Image:    "python:3.11",
			CPUs:     "2",
			MemLimit: "1g",
			ShmSize:  "256m",
			Deploy: &manifest.DockerDeploySpec{Resources: manifest.DockerDeployResources{
				Reservations: &manifest.DockerReservations{Devices: []manifest.DockerDevice{{
					Count:        "1",
					Capabilities: []string{"gpu"},
				}}},
			}},
		}},
		Run: pipeline.Run{Command: []string{"echo", "ok"}},
	}
	stepJSON, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	cli := &captureDockerClient{}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	_, err = d.Start(context.Background(), &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Step:     stepJSON,
		Attempt:  1,
		Env:      []string{"APP_TOKEN=secret-value"},
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	host := cli.create.HostConfig
	if host.Resources.NanoCPUs != 2_000_000_000 {
		t.Fatalf("NanoCPUs = %d", host.Resources.NanoCPUs)
	}
	if host.Resources.Memory != 1024*1024*1024 {
		t.Fatalf("Memory = %d", host.Resources.Memory)
	}
	if host.ShmSize != 256*1024*1024 {
		t.Fatalf("ShmSize = %d", host.ShmSize)
	}
	if len(host.Resources.DeviceRequests) != 1 || host.Resources.DeviceRequests[0].Count != 1 {
		t.Fatalf("DeviceRequests = %#v", host.Resources.DeviceRequests)
	}
	if got := cli.create.Config.Env; len(got) != 0 {
		t.Fatalf("container env exposed resolved task env: %#v", got)
	}
	// The agent must run regardless of the step image's own ENTRYPOINT.
	if ep := cli.create.Config.Entrypoint; len(ep) != 1 || ep[0] != pipelinedriver.ContainerPiperBin {
		t.Fatalf("Entrypoint = %#v, want [%q]", ep, pipelinedriver.ContainerPiperBin)
	}
	for _, arg := range cli.create.Config.Cmd {
		if arg == "--task-file=/piper-results/worker-1-run-1-train-a1.task.json" {
			continue
		}
		if strings.Contains(arg, "secret-value") || strings.HasPrefix(arg, "--task=") {
			t.Fatalf("container args exposed task payload: %#v", cli.create.Config.Cmd)
		}
	}
}

func TestDockerDriverWaitErrorCleansRuntimeState(t *testing.T) {
	cli := &captureDockerClient{waitErr: errors.New("daemon unavailable")}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Attempt:  1,
		Env:      []string{"APP_TOKEN=secret-value"},
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	exit, err := d.Wait(context.Background(), handle)
	if err != nil {
		t.Fatalf("Wait returned transport error: %v", err)
	}
	if exit.InfraFailure == nil {
		t.Fatal("Wait returned nil InfraFailure")
	}
	if cli.removedID == "" {
		t.Fatal("container was not removed")
	}
	d.mu.Lock()
	_, active := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if active {
		t.Fatalf("runtime key %q remained active", handle.RuntimeKey)
	}
}

// A step image the daemon has never seen is pulled before the container is
// created, as kubelet does for the k8s runtime.
func TestDockerDriverStartPullsMissingImage(t *testing.T) {
	cli := &captureDockerClient{images: dockertest.Images{Missing: map[string]bool{"python:3.11": true}}}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	if _, err := d.Start(context.Background(), &proto.Task{
		ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1,
	}, pipelinedriver.ExecSpec{RuntimeKey: "worker-1-run-1-train-a1", Image: "python:3.11", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := cli.images.PulledImages(); len(got) != 1 || got[0] != "python:3.11" {
		t.Fatalf("pulled = %v, want [python:3.11]", got)
	}
}

type captureDockerClient struct {
	dockerclient.APIClient
	images     dockertest.Images
	create     dockerclient.ContainerCreateOptions
	copyTo     dockerclient.CopyToContainerOptions
	copied     map[string]*tar.Header
	copiedTask []byte
	copiedFrom string
	result     []byte // result file in the container; nil = absent
	waitStatus int64
	waitErr    error
	removedID  string
	stoppedID  string
}

func (c *captureDockerClient) CopyToContainer(_ context.Context, _ string, opts dockerclient.CopyToContainerOptions) (dockerclient.CopyToContainerResult, error) {
	c.copyTo = opts
	c.copied = map[string]*tar.Header{}
	tr := tar.NewReader(opts.Content)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return dockerclient.CopyToContainerResult{}, err
		}
		c.copied[hdr.Name] = hdr
		if hdr.Typeflag == tar.TypeReg {
			if c.copiedTask, err = io.ReadAll(tr); err != nil {
				return dockerclient.CopyToContainerResult{}, err
			}
		}
	}
	return dockerclient.CopyToContainerResult{}, nil
}

func (c *captureDockerClient) CopyFromContainer(_ context.Context, _ string, opts dockerclient.CopyFromContainerOptions) (dockerclient.CopyFromContainerResult, error) {
	c.copiedFrom = opts.SourcePath
	if c.result == nil {
		return dockerclient.CopyFromContainerResult{}, errors.New("Could not find the file " + opts.SourcePath + " in container")
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: path.Base(opts.SourcePath), Mode: 0644, Size: int64(len(c.result))})
	_, _ = tw.Write(c.result)
	_ = tw.Close()
	return dockerclient.CopyFromContainerResult{Content: io.NopCloser(&buf)}, nil
}

func (c *captureDockerClient) ContainerCreate(_ context.Context, opts dockerclient.ContainerCreateOptions) (dockerclient.ContainerCreateResult, error) {
	if err := c.images.CheckCreate(opts.Config.Image); err != nil {
		return dockerclient.ContainerCreateResult{}, err
	}
	c.create = opts
	return dockerclient.ContainerCreateResult{ID: "container-123456789"}, nil
}

func (c *captureDockerClient) ContainerStart(context.Context, string, dockerclient.ContainerStartOptions) (dockerclient.ContainerStartResult, error) {
	return dockerclient.ContainerStartResult{}, nil
}

func (c *captureDockerClient) ContainerWait(context.Context, string, dockerclient.ContainerWaitOptions) dockerclient.ContainerWaitResult {
	errCh := make(chan error, 1)
	resultCh := make(chan container.WaitResponse, 1)
	result := dockerclient.ContainerWaitResult{
		Error:  errCh,
		Result: resultCh,
	}
	if c.waitErr != nil {
		errCh <- c.waitErr
	} else {
		resultCh <- container.WaitResponse{StatusCode: c.waitStatus}
	}
	return result
}

func (c *captureDockerClient) ContainerRemove(_ context.Context, id string, _ dockerclient.ContainerRemoveOptions) (dockerclient.ContainerRemoveResult, error) {
	c.removedID = id
	return dockerclient.ContainerRemoveResult{}, nil
}

func (c *captureDockerClient) ContainerStop(_ context.Context, id string, _ dockerclient.ContainerStopOptions) (dockerclient.ContainerStopResult, error) {
	c.stoppedID = id
	return dockerclient.ContainerStopResult{}, nil
}

// TestDockerDriverStartWaitSucceedsAndCleansUpContainer freezes fed.md 13.1's
// "Start and terminal completion" behavior on the pure success path
// (StatusCode==0), which TestDockerDriverWaitErrorCleansRuntimeState does not
// cover — that test only exercises the transport-error branch.
func TestDockerDriverStartWaitSucceedsAndCleansUpContainer(t *testing.T) {
	cli := &captureDockerClient{result: []byte(`{"version":1,"result":{"task_id":"run-1:train","status":"done","attempt":1}}`)}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID:       "run-1:train",
		RunID:    "run-1",
		StepName: "train",
		Attempt:  1,
	}, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	exit, err := d.Wait(context.Background(), handle)
	if err != nil {
		t.Fatalf("Wait returned transport error: %v", err)
	}
	if exit.InfraFailure != nil {
		t.Fatalf("unexpected InfraFailure: %v", exit.InfraFailure)
	}
	if exit.Result == nil || exit.Result.TaskID != "run-1:train" || exit.Result.Status != "done" {
		t.Fatalf("exit.Result = %#v, want the result copied out of the container", exit.Result)
	}
	if cli.copiedFrom != containerResultFile("worker-1-run-1-train-a1") {
		t.Fatalf("result copied from %q", cli.copiedFrom)
	}
	if cli.removedID == "" {
		t.Fatal("container was not removed on the success path")
	}
	d.mu.Lock()
	_, active := d.active[handle.RuntimeKey]
	d.mu.Unlock()
	if active {
		t.Fatalf("runtime key %q remained active", handle.RuntimeKey)
	}
}

func (c *captureDockerClient) ImageInspect(ctx context.Context, image string, opts ...dockerclient.ImageInspectOption) (dockerclient.ImageInspectResult, error) {
	return c.images.ImageInspect(ctx, image, opts...)
}

func (c *captureDockerClient) ImagePull(ctx context.Context, image string, opts dockerclient.ImagePullOptions) (dockerclient.ImagePullResponse, error) {
	return c.images.ImagePull(ctx, image, opts)
}

// The task file (resolved secrets) never touches the host: it is copied into
// the container, chowned to the image user, with the roots a non-root user
// could not create under / itself. Only the piper binary is mounted.
func TestDockerDriverStartCopiesTaskFileIntoContainer(t *testing.T) {
	cli := &captureDockerClient{}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	task := &proto.Task{ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1, Env: []string{"APP_TOKEN=secret-value"}}
	if _, err := d.Start(context.Background(), task, pipelinedriver.ExecSpec{
		RuntimeKey: "worker-1-run-1-train-a1",
		Image:      "python:3.11",
		OutputDir:  t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if cli.copyTo.DestinationPath != "/" || !cli.copyTo.CopyUIDGID {
		t.Fatalf("copy options = %+v, want DestinationPath / with CopyUIDGID", cli.copyTo)
	}
	wantModes := map[string]int64{
		"piper-runtime/": 0700,
		"piper-outputs/": 0755,
		"piper-inputs/":  0755,
		"piper-runtime/worker-1-run-1-train-a1.task.json": 0600,
	}
	for name, mode := range wantModes {
		hdr := cli.copied[name]
		if hdr == nil || hdr.Mode != mode {
			t.Fatalf("copied %q = %+v, want mode %o", name, hdr, mode)
		}
	}
	var copied proto.Task
	if err := json.Unmarshal(cli.copiedTask, &copied); err != nil || copied.ID != task.ID || len(copied.Env) != 1 {
		t.Fatalf("copied task = %s (%v)", cli.copiedTask, err)
	}
	mounts := cli.create.HostConfig.Mounts
	if len(mounts) != 1 || mounts[0].Target != pipelinedriver.ContainerPiperBin || !mounts[0].ReadOnly {
		t.Fatalf("mounts = %+v, want only the read-only piper binary", mounts)
	}
}

func TestDockerDriverWaitWithoutResultFileIsInfraFailure(t *testing.T) {
	cli := &captureDockerClient{waitStatus: 1}
	d := NewWithClient(Config{RuntimeID: "worker-1"}, cli)
	handle, err := d.Start(context.Background(), &proto.Task{
		ID: "run-1:train", RunID: "run-1", StepName: "train", Attempt: 1,
	}, pipelinedriver.ExecSpec{RuntimeKey: "worker-1-run-1-train-a1", Image: "python:3.11"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	exit, err := d.Wait(context.Background(), handle)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if exit.Result != nil || exit.InfraFailure == nil || !strings.Contains(exit.InfraFailure.Error(), "exited 1 without result file") {
		t.Fatalf("exit = %+v, want InfraFailure for a missing result file", exit)
	}
	if cli.removedID == "" {
		t.Fatal("container was not removed")
	}
}
