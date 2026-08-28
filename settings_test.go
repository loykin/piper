package piper

import (
	"strings"
	"testing"
)

func TestStorageIdentity(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"empty means local file storage", "", "file"},
		{"file with absolute path", "file:///data/piper", "file:/data/piper"},
		{"file with relative host+path", "file://./relative/dir", "file:./relative/dir"},
		{"s3 bucket only", "s3://my-bucket", "s3:my-bucket"},
		{"s3 with query params (region/endpoint included, credentials must not leak)", "s3://my-bucket?region=us-east-1&accessKey=AKIA...&secretKey=super-secret", "s3:my-bucket#us-east-1"},
		{"s3 different bucket", "s3://other-bucket?region=us-east-1", "s3:other-bucket#us-east-1"},
		{"s3 with custom endpoint", "s3://data?endpoint=http://minio-a:9000", "s3:data@http://minio-a:9000"},
		{"gs bucket", "gs://my-gcs-bucket?serviceAccountKey=base64secret", "gs:my-gcs-bucket"},
		{"azblob container with account (account included, key must not leak)", "azblob://my-container?accountName=acct&accountKey=secretkey", "azblob:my-container@acct"},
		{"https endpoint with path (path included, query must not leak)", "https://artifacts.example.com/some/path?token=secret", "https://artifacts.example.com/some/path"},
		{"http endpoint with path", "http://artifacts.internal:9000/bucket?token=abc", "http://artifacts.internal:9000/bucket"},
		{"unknown scheme falls back to scheme only", "weird://host/path", "weird"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := storageIdentity(tc.url)
			if got != tc.want {
				t.Errorf("storageIdentity(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestStorageIdentity_DifferentS3EndpointsDiffer(t *testing.T) {
	a := storageIdentity("s3://bucket-a?endpoint=http://minio-a:9000")
	b := storageIdentity("s3://bucket-b?endpoint=http://minio-a:9000")
	if a == b {
		t.Fatalf("expected different buckets to produce different identities, both got %q", a)
	}
}

// TestStorageIdentity_SameBucketDifferentEndpointDiffers is the case
// storageIdentity used to collapse before it started including `endpoint`:
// two entirely separate MinIO/S3-compatible servers that both happen to
// have a bucket of the same name are NOT the same practical target, and
// must not be treated as one.
func TestStorageIdentity_SameBucketDifferentEndpointDiffers(t *testing.T) {
	a := storageIdentity("s3://data?endpoint=http://minio-a:9000")
	b := storageIdentity("s3://data?endpoint=http://minio-b:9000")
	if a == b {
		t.Fatalf("expected the same bucket name against two different endpoints to differ, both got %q", a)
	}
}

// TestStorageIdentity_SameContainerDifferentAccountDiffers: azblob container
// names are only unique within a storage account, not globally — two
// different accounts can each have a "data" container.
func TestStorageIdentity_SameContainerDifferentAccountDiffers(t *testing.T) {
	a := storageIdentity("azblob://data?accountName=accounta")
	b := storageIdentity("azblob://data?accountName=accountb")
	if a == b {
		t.Fatalf("expected the same container name against two different accounts to differ, both got %q", a)
	}
}

// TestStorageIdentity_SameHostDifferentPathDiffers: an http(s) artifact
// endpoint's base path is part of its practical target, e.g.
// https://host/store-a and https://host/store-b are different roots even
// though they share a host.
func TestStorageIdentity_SameHostDifferentPathDiffers(t *testing.T) {
	a := storageIdentity("https://host.example.com/store-a")
	b := storageIdentity("https://host.example.com/store-b")
	if a == b {
		t.Fatalf("expected different base paths on the same host to differ, both got %q", a)
	}
}

func TestStorageIdentity_NeverLeaksQuerySecrets(t *testing.T) {
	urls := []string{
		"s3://my-bucket?region=us-east-1&accessKey=AKIAEXAMPLE&secretKey=topsecretvalue",
		"gs://my-bucket?serviceAccountKey=verysecretbase64blob",
		"azblob://my-container?accountName=acct&accountKey=verysecretkey",
		"https://artifacts.example.com/path?token=leaked-token-value",
	}
	secrets := []string{"topsecretvalue", "verysecretbase64blob", "verysecretkey", "leaked-token-value", "AKIAEXAMPLE"}
	for _, u := range urls {
		got := storageIdentity(u)
		for _, secret := range secrets {
			if strings.Contains(got, secret) {
				t.Errorf("storageIdentity(%q) = %q leaked secret %q", u, got, secret)
			}
		}
	}
}
