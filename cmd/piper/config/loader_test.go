package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "piper.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoaderPrecedence(t *testing.T) {
	t.Setenv("PIPER_WORKER_CAPABILITIES_PIPELINE_CONCURRENCY", "7")
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 4\nworker:\n  baremetal: {}\n  capabilities:\n    pipeline:\n      concurrency: 6\n"))
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("concurrency", 0, "")
	l.MustBindFlag("worker.capabilities.pipeline.concurrency", flags.Lookup("concurrency"))
	if err := flags.Set("concurrency", "8"); err != nil {
		t.Fatal(err)
	}
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Capabilities.Pipeline.Concurrency != 8 {
		t.Fatalf("got %d, want 8", cfg.Worker.Capabilities.Pipeline.Concurrency)
	}
}

func TestLoaderUnchangedFlagDoesNotOverrideFile(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 4\nworker:\n  baremetal: {}\n  capabilities:\n    pipeline:\n      concurrency: 6\n"))
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("concurrency", 0, "")
	l.MustBindFlag("worker.capabilities.pipeline.concurrency", flags.Lookup("concurrency"))
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Capabilities.Pipeline.Concurrency != 6 {
		t.Fatalf("got %d, want 6", cfg.Worker.Capabilities.Pipeline.Concurrency)
	}
}

func TestLoaderEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("PIPER_WORKER_CAPABILITIES_PIPELINE_CONCURRENCY", "7")
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 4\nworker:\n  baremetal: {}\n  capabilities:\n    pipeline:\n      concurrency: 6\n"))
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Capabilities.Pipeline.Concurrency != 7 {
		t.Fatalf("got %d, want 7", cfg.Worker.Capabilities.Pipeline.Concurrency)
	}
}

func TestLoaderCachesDecodedConfig(t *testing.T) {
	t.Setenv("PIPER_LOG_FORMAT", "json")
	l := NewLoader()
	first, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIPER_LOG_FORMAT", "text")
	second, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if first.Log.Format != "json" || second.Log.Format != "json" {
		t.Fatalf("cache mismatch: %q %q", first.Log.Format, second.Log.Format)
	}
}

func TestLoaderReportsWinningSources(t *testing.T) {
	t.Setenv("PIPER_LOG_FORMAT", "json")
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 4\nserver:\n  http_addr: ':1234'\n"))
	if _, err := l.Load(); err != nil {
		t.Fatal(err)
	}
	sources := l.Sources()
	if sources["log.format"] != "environment" || sources["server.http_addr"] != "config" || sources["server.data_dir"] != "default" {
		t.Fatalf("unexpected sources: %#v", sources)
	}
}

func TestLoaderEnvironmentJSONCollection(t *testing.T) {
	t.Setenv("PIPER_WORKER_DOCKER_VOLUMES", `[{"name":"data","host_path":"/tmp/data","container_path":"/data"}]`)
	l := NewLoader()
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Docker == nil || len(cfg.Worker.Docker.Volumes) != 1 || cfg.Worker.Docker.Volumes[0].Name != "data" {
		t.Fatalf("unexpected worker config: %#v", cfg.Worker)
	}
}

func TestLoaderRejectsUnknownAndMissingVersion(t *testing.T) {
	for _, body := range []string{"version: 4\nworker:\n  capabilities:\n    notebook:\n      port_rang: 8888-9900\n", "worker: {}\n"} {
		l := NewLoader()
		l.SetConfigFile(writeConfig(t, body))
		if _, err := l.Load(); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestLoaderUnknownKeyIncludesFullPath(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 4\nworker:\n  capabilities:\n    notebook:\n      port_rang: 8888-9900\n"))
	_, err := l.Load()
	if err == nil || !strings.Contains(err.Error(), "worker.capabilities.notebook.port_rang") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoaderRejectsVersion3AndRemovedKeys(t *testing.T) {
	for _, body := range []string{
		"version: 3\n",
		"version: 4\nserver:\n  run:\n    retries: 2\n",
		"version: 4\nworkers: {}\n",
		"version: 4\nworker:\n  pipeline: {}\n",
		"version: 4\nworker:\n  notebook: {}\n",
		"version: 4\nworker:\n  serving: {}\n",
		"version: 4\nworker:\n  gpus: [0]\n",
		"version: 4\nworker:\n  baremetal:\n    capabilities:\n      pipeline: {}\n",
		"version: 4\nworker:\n  docker:\n    capabilities:\n      notebook: {}\n",
		"version: 4\nworker:\n  k8s:\n    id: k8s-a\n",
		"version: 4\nworker:\n  k8s:\n    capabilities:\n      notebook: {}\n",
		"version: 4\nworker:\n  k8s:\n    pipeline_runner:\n      runner_image: piper/piper:latest\n",
	} {
		l := NewLoader()
		l.SetConfigFile(writeConfig(t, body))
		if _, err := l.Load(); err == nil {
			t.Fatalf("expected removed config key to fail: %q", body)
		}
	}
}

func TestLoaderExplicitMissingFile(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := l.Load(); err == nil {
		t.Fatal("expected missing file error")
	}
}
