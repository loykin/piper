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
	t.Setenv("PIPER_WORKERS_PIPELINE_CONCURRENCY", "7")
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 1\nworkers:\n  pipeline:\n    concurrency: 6\n"))
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("concurrency", 0, "")
	l.MustBindFlag("workers.pipeline.concurrency", flags.Lookup("concurrency"))
	if err := flags.Set("concurrency", "8"); err != nil {
		t.Fatal(err)
	}
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers.Pipeline.Concurrency != 8 {
		t.Fatalf("got %d, want 8", cfg.Workers.Pipeline.Concurrency)
	}
}

func TestLoaderUnchangedFlagDoesNotOverrideFile(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 1\nworkers:\n  pipeline:\n    concurrency: 6\n"))
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.Int("concurrency", 0, "")
	l.MustBindFlag("workers.pipeline.concurrency", flags.Lookup("concurrency"))
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers.Pipeline.Concurrency != 6 {
		t.Fatalf("got %d, want 6", cfg.Workers.Pipeline.Concurrency)
	}
}

func TestLoaderEnvironmentOverridesFile(t *testing.T) {
	t.Setenv("PIPER_WORKERS_PIPELINE_CONCURRENCY", "7")
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 1\nworkers:\n  pipeline:\n    concurrency: 6\n"))
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workers.Pipeline.Concurrency != 7 {
		t.Fatalf("got %d, want 7", cfg.Workers.Pipeline.Concurrency)
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
	l.SetConfigFile(writeConfig(t, "version: 1\nserver:\n  http_addr: ':1234'\n"))
	if _, err := l.Load(); err != nil {
		t.Fatal(err)
	}
	sources := l.Sources()
	if sources["log.format"] != "environment" || sources["server.http_addr"] != "config" || sources["server.run.retries"] != "default" {
		t.Fatalf("unexpected sources: %#v", sources)
	}
}

func TestLoaderEnvironmentJSONCollection(t *testing.T) {
	t.Setenv("PIPER_WORKERS_NOTEBOOK_GPUS", `["0","1"]`)
	l := NewLoader()
	cfg, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.Workers.Notebook.GPUs, ",") != "0,1" {
		t.Fatalf("unexpected GPUs: %#v", cfg.Workers.Notebook.GPUs)
	}
}

func TestLoaderRejectsUnknownAndMissingVersion(t *testing.T) {
	for _, body := range []string{"version: 1\nworkers:\n  pipeline:\n    runtim: docker\n", "workers: {}\n"} {
		l := NewLoader()
		l.SetConfigFile(writeConfig(t, body))
		if _, err := l.Load(); err == nil {
			t.Fatalf("expected error for %q", body)
		}
	}
}

func TestLoaderUnknownKeyIncludesFullPath(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(writeConfig(t, "version: 1\nworkers:\n  pipeline:\n    runtim: docker\n"))
	_, err := l.Load()
	if err == nil || !strings.Contains(err.Error(), "workers.pipeline.runtim") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoaderExplicitMissingFile(t *testing.T) {
	l := NewLoader()
	l.SetConfigFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if _, err := l.Load(); err == nil {
		t.Fatal("expected missing file error")
	}
}
