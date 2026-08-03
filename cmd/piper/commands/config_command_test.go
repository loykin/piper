package commands

import (
	"net/url"
	"testing"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
)

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

func TestConfigCommandDoesNotExposeInit(t *testing.T) {
	cmd := newConfigCmd(cliconfig.NewLoader())
	if found, _, err := cmd.Find([]string{"init"}); err == nil && found != cmd {
		t.Fatal("config init should not be exposed")
	}
}
