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
		{"s3 with query params (credentials must not leak)", "s3://my-bucket?region=us-east-1&accessKey=AKIA...&secretKey=super-secret", "s3:my-bucket"},
		{"s3 different bucket", "s3://other-bucket?region=us-east-1", "s3:other-bucket"},
		{"gs bucket", "gs://my-gcs-bucket?serviceAccountKey=base64secret", "gs:my-gcs-bucket"},
		{"azblob container", "azblob://my-container?accountName=acct&accountKey=secretkey", "azblob:my-container"},
		{"https endpoint, no path or query", "https://artifacts.example.com/some/path?token=secret", "https://artifacts.example.com"},
		{"http endpoint", "http://artifacts.internal:9000/bucket?token=abc", "http://artifacts.internal:9000"},
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
