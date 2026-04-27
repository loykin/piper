package piper

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/piper/piper/pkg/artifact"
)

const testBucket = "piper-test"

func fakeS3(t *testing.T) (*httptest.Server, *s3.Client) {
	t.Helper()
	faker := gofakes3.New(s3mem.New())
	srv := httptest.NewServer(faker.Server())
	t.Cleanup(srv.Close)

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(srv.URL)
	})

	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(testBucket),
	}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return srv, client
}

func putObject(t *testing.T, client *s3.Client, key, body string) {
	t.Helper()
	_, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(key),
		Body:   strings.NewReader(body),
	})
	if err != nil {
		t.Fatalf("put object %s: %v", key, err)
	}
}

// ── downloadS3URIToLocal ──────────────────────────────────────────────────────

func TestDownloadS3URIToLocal_singleFile(t *testing.T) {
	_, client := fakeS3(t)
	putObject(t, client, "models/v1/model.bin", "hello")

	dest := t.TempDir()
	if err := downloadS3URIToLocal(context.Background(), client, "s3://"+testBucket+"/models/v1/model.bin", dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "model.bin"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", string(got), "hello")
	}
}

func TestDownloadS3URIToLocal_prefixMultipleFiles(t *testing.T) {
	_, client := fakeS3(t)
	putObject(t, client, "models/v2/a.bin", "aaa")
	putObject(t, client, "models/v2/b.bin", "bbb")
	putObject(t, client, "models/v3/c.bin", "ccc") // 다른 prefix — 제외돼야 함

	dest := t.TempDir()
	if err := downloadS3URIToLocal(context.Background(), client, "s3://"+testBucket+"/models/v2", dest); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"a.bin", "b.bin"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("file %s not found: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("file %s is empty", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "c.bin")); !os.IsNotExist(err) {
		t.Fatal("c.bin from different prefix should not have been downloaded")
	}
}

func TestDownloadS3URIToLocal_emptyPrefix(t *testing.T) {
	_, client := fakeS3(t)
	// 아무 객체도 없음

	err := downloadS3URIToLocal(context.Background(), client, "s3://"+testBucket+"/missing/prefix", t.TempDir())
	if err == nil {
		t.Fatal("expected error for empty prefix, got nil")
	}
}

func TestDownloadS3URIToLocal_invalidURI(t *testing.T) {
	_, client := fakeS3(t)

	err := downloadS3URIToLocal(context.Background(), client, "s3://bucketwithoutkey", t.TempDir())
	if err == nil {
		t.Fatal("expected error for URI without key, got nil")
	}
}

// ── resolveModelURI s3:// ─────────────────────────────────────────────────────

func TestResolveModelURI_s3LocalNoCreds(t *testing.T) {
	p := &Piper{
		cfg:   Config{OutputDir: t.TempDir()},
		s3Cli: nil,
	}
	_, err := p.resolveModelURI(context.Background(), "svc", "s3://bucket/model", artifact.TargetLocal)
	if err == nil {
		t.Fatal("expected error when s3Cli is nil, got nil")
	}
	if !strings.Contains(err.Error(), "S3 credentials") {
		t.Fatalf("error %q should mention S3 credentials", err)
	}
}

func TestResolveModelURI_s3K8sTarget(t *testing.T) {
	p := &Piper{cfg: Config{OutputDir: t.TempDir()}}
	resolved, err := p.resolveModelURI(context.Background(), "svc", "s3://bucket/model/v1", artifact.TargetS3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.S3URI != "s3://bucket/model/v1" {
		t.Fatalf("S3URI = %q, want s3://bucket/model/v1", resolved.S3URI)
	}
}

func TestResolveModelURI_s3LocalDownload(t *testing.T) {
	_, client := fakeS3(t)
	putObject(t, client, "model/weights.bin", "weights")

	modelDir := t.TempDir()
	p := &Piper{
		cfg:   Config{OutputDir: t.TempDir(), Serving: ServingConfig{ModelDir: modelDir}},
		s3Cli: client,
	}
	resolved, err := p.resolveModelURI(context.Background(), "mysvc", "s3://"+testBucket+"/model/weights.bin", artifact.TargetLocal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.LocalPath == "" {
		t.Fatal("expected LocalPath to be set")
	}
	data, err := os.ReadFile(filepath.Join(resolved.LocalPath, "weights.bin"))
	if err != nil {
		t.Fatalf("downloaded file not found: %v", err)
	}
	if string(data) != "weights" {
		t.Fatalf("content = %q, want %q", string(data), "weights")
	}
}
