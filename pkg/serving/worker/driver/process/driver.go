package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	processinfra "github.com/piper/piper/internal/process"
	"github.com/piper/piper/internal/processlog"
	"github.com/piper/piper/pkg/serving"
	"github.com/piper/piper/pkg/serving/worker/driver"
)

type Config struct {
	WorkerID string
}

type processMeta struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	RuntimeName string `json:"runtime_name"`
	Port        int    `json:"port"`
}

type Driver struct {
	supervisor *processinfra.ProcessSupervisor
	pidDir     string
	mu         sync.Mutex
	collectors map[string]func()
}

func New(cfg Config) *Driver {
	return &Driver{
		supervisor: processinfra.NewProcessSupervisor(),
		pidDir:     filepath.Join(os.TempDir(), "piper-serving-pids", cfg.WorkerID),
		collectors: make(map[string]func()),
	}
}

func (d *Driver) Deploy(_ context.Context, req driver.DeployRequest) (string, error) {
	if req.RuntimeName == "" {
		d.stopSink(req)
		return "", fmt.Errorf("serving process: runtime name is required")
	}
	if len(req.Command) == 0 {
		d.stopSink(req)
		return "", fmt.Errorf("serving process: command is required")
	}
	if req.Port == 0 {
		d.stopSink(req)
		return "", fmt.Errorf("serving process: port is required")
	}

	meta := processMeta{ProjectID: req.ProjectID, Name: req.Name, RuntimeName: req.RuntimeName, Port: req.Port}
	if err := d.writeMeta(req.RuntimeName, meta); err != nil {
		d.stopSink(req)
		return "", fmt.Errorf("write serving process meta for %q: %w", req.Name, err)
	}
	metaPath := d.metaFile(req.RuntimeName)
	logFile := filepath.Join(os.TempDir(), "piper-serving", req.RuntimeName+".log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		_ = os.Remove(metaPath)
		d.stopSink(req)
		return "", fmt.Errorf("create serving log dir for %q: %w", req.Name, err)
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = os.Remove(metaPath)
		d.stopSink(req)
		return "", fmt.Errorf("truncate serving log for %q: %w", req.Name, err)
	}
	_ = f.Close()

	if req.LogSink != nil {
		stop := processlog.StartCollector(logFile, "svc:"+req.Name, "runtime", req.LogSink)
		d.mu.Lock()
		d.collectors[req.RuntimeName] = stop
		d.mu.Unlock()
	}

	_, endpoint, err := d.supervisor.Start(processinfra.ProcessSpec{
		Name:       req.RuntimeName,
		Command:    req.Command,
		Env:        req.Env,
		Port:       req.Port,
		HealthPath: req.HealthPath,
		GPUs:       req.GPUs,
		LogFile:    logFile,
		PIDFile:    d.pidFile(req.RuntimeName),
	}, func(status string) {
		_ = os.Remove(metaPath)
		d.stopCollector(req.RuntimeName)
		d.stopSink(req)
		if req.OnExit != nil {
			req.OnExit(status)
		}
	})
	if err != nil {
		_ = os.Remove(metaPath)
		d.stopCollector(req.RuntimeName)
		d.stopSink(req)
		return "", err
	}
	return endpoint, nil
}

func (d *Driver) Recover(_ context.Context, onRecovered func(driver.RecoveredHandle) func(string), onTerminal func(driver.RecoveredHandle, string)) error {
	entries, err := os.ReadDir(d.pidDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		metaPath := filepath.Join(d.pidDir, entry.Name())
		data, err := os.ReadFile(metaPath)
		if err != nil {
			_ = os.Remove(metaPath)
			continue
		}
		var meta processMeta
		if err := json.Unmarshal(data, &meta); err != nil || meta.RuntimeName == "" || meta.Name == "" {
			_ = os.Remove(metaPath)
			continue
		}
		handle := driver.RecoveredHandle{ProjectID: meta.ProjectID, Name: meta.Name, RuntimeName: meta.RuntimeName, Port: meta.Port}
		var onExit func(string)
		onExitReady := make(chan struct{})
		_, running, recoverErr := d.supervisor.Recover(processinfra.ProcessSpec{
			Name: meta.RuntimeName, Port: meta.Port, PIDFile: d.pidFile(meta.RuntimeName),
		}, func(status string) {
			_ = os.Remove(metaPath)
			<-onExitReady
			if onExit != nil {
				onExit(status)
			}
		})
		if recoverErr != nil || !running {
			_ = os.Remove(metaPath)
			close(onExitReady)
			onTerminal(handle, serving.StatusStopped)
			continue
		}
		onExit = onRecovered(handle)
		close(onExitReady)
	}
	return nil
}

func (d *Driver) Stop(_ context.Context, runtimeName string) error {
	return d.supervisor.Stop(runtimeName)
}

func (d *Driver) Status(_ context.Context, runtimeName string) string {
	if status, ok := d.supervisor.Status(runtimeName); ok {
		return status
	}
	return serving.StatusStopped
}

func (d *Driver) KillAll(_ context.Context) error {
	return d.supervisor.KillAll()
}

func (d *Driver) pidFile(runtimeName string) string {
	return filepath.Join(d.pidDir, runtimeName+".pid")
}

func (d *Driver) metaFile(runtimeName string) string {
	return filepath.Join(d.pidDir, runtimeName+".meta.json")
}

func (d *Driver) writeMeta(runtimeName string, meta processMeta) error {
	if err := os.MkdirAll(d.pidDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(d.metaFile(runtimeName), data, 0o644)
}

func (d *Driver) stopCollector(runtimeName string) {
	d.mu.Lock()
	stop := d.collectors[runtimeName]
	delete(d.collectors, runtimeName)
	d.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (d *Driver) stopSink(req driver.DeployRequest) {
	if req.LogSink != nil {
		req.LogSink.Stop()
	}
}
