package tensorboard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/piper/piper/pkg/viewer"
)

type Driver struct{}

func New() *Driver { return &Driver{} }

func (d *Driver) Type() string { return "tensorboard" }

func (d *Driver) Start(ctx context.Context, v *viewer.Viewer, localPath string) error {
	port, err := freePort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}

	cmd := exec.CommandContext(ctx, "tensorboard",
		"--logdir", localPath,
		"--port", fmt.Sprintf("%d", port),
		"--host", "127.0.0.1",
		"--reload_interval", "5",
		"--bind_all=false",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tensorboard start: %w — is tensorboard installed?", err)
	}

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitReady(ctx, endpoint, 15*time.Second); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("tensorboard did not become ready: %w", err)
	}

	v.PID = cmd.Process.Pid
	v.Endpoint = endpoint
	return nil
}

func (d *Driver) Stop(_ context.Context, v *viewer.Viewer) error {
	if v.PID == 0 {
		return nil
	}
	p, err := os.FindProcess(v.PID)
	if err != nil {
		return nil
	}
	return p.Kill()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitReady(ctx context.Context, endpoint string, timeout time.Duration) error {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		resp, err := client.Get(endpoint)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s", timeout)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
