package pipelineworker

import (
	"strings"
	"testing"

	"github.com/piper/piper/internal/proto"
)

func TestTaskStorageForWorkerUsesTaskStorage(t *testing.T) {
	task := &proto.Task{
		StorageURL:   "s3://bucket?endpoint=http://minio:9000",
		StorageToken: "storage-secret",
	}
	gotURL, gotToken := taskStorageForWorker(task, "http://master:8080", "worker-secret", false)
	if gotURL != task.StorageURL {
		t.Fatalf("storageURL = %q, want %q", gotURL, task.StorageURL)
	}
	if gotToken != task.StorageToken {
		t.Fatalf("storageToken = %q, want %q", gotToken, task.StorageToken)
	}
}

func TestMergeExecutionEnvTokenOnlySecretDoesNotInheritGlobalGitUser(t *testing.T) {
	got := mergeExecutionEnv(
		[]string{"PIPER_GIT_USER=global-user", "PIPER_GIT_TOKEN=global-token", "OTHER=base"},
		[]string{"PIPER_GIT_TOKEN=step-token"},
	)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "PIPER_GIT_USER=") {
		t.Fatalf("env inherited global git user: %#v", got)
	}
	if !strings.Contains(joined, "PIPER_GIT_TOKEN=step-token") {
		t.Fatalf("env missing step token: %#v", got)
	}
	if !strings.Contains(joined, "OTHER=base") {
		t.Fatalf("env dropped unrelated base value: %#v", got)
	}
}

func TestMergeExecutionEnvStepGitUserOverridesGlobalGitUser(t *testing.T) {
	got := mergeExecutionEnv(
		[]string{"PIPER_GIT_USER=global-user", "PIPER_GIT_TOKEN=global-token"},
		[]string{"PIPER_GIT_TOKEN=step-token", "PIPER_GIT_USER=step-user"},
	)
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "PIPER_GIT_USER=step-user") || !strings.Contains(joined, "PIPER_GIT_TOKEN=step-token") {
		t.Fatalf("env did not use step git credential: %#v", got)
	}
}

func TestTaskStorageForWorkerMapsMasterLocalStoreToMasterHTTPStore(t *testing.T) {
	task := &proto.Task{StorageURL: "file:///var/lib/piper/store"}
	gotURL, gotToken := taskStorageForWorker(task, "http://master:8080/", "worker-secret", false)
	if gotURL != "http://master:8080/store" {
		t.Fatalf("storageURL = %q, want master /store", gotURL)
	}
	if gotToken != "worker-secret" {
		t.Fatalf("storageToken = %q, want worker token", gotToken)
	}
}

func TestTaskStorageForWorkerKeepsMasterLocalStoreForEmbeddedWorker(t *testing.T) {
	task := &proto.Task{StorageURL: "file:///var/lib/piper/store"}
	gotURL, gotToken := taskStorageForWorker(task, "http://master:8080/", "worker-secret", true)
	if gotURL != task.StorageURL {
		t.Fatalf("storageURL = %q, want %q", gotURL, task.StorageURL)
	}
	if gotToken != "" {
		t.Fatalf("storageToken = %q, want empty", gotToken)
	}
}
