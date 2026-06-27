package pipelineworker

import (
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
