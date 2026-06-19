package commands

import (
	"net/url"
	"testing"
)

func TestParsePodDefaultsReturnsStrictError(t *testing.T) {
	_, err := parsePodDefaults(map[string]any{"spec": map[string]any{"unknownField": true}})
	if err == nil {
		t.Fatal("expected invalid pod defaults to fail")
	}
}

func TestRedactURL(t *testing.T) {
	got := redactURL("s3://bucket?accessKey=user&secretKey=secret&endpoint=http://minio")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("accessKey") != "******" || u.Query().Get("secretKey") != "******" {
		t.Fatalf("credentials leaked: %s", got)
	}
}
