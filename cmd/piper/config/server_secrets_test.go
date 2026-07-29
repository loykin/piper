package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureServerSecretsGeneratesAndReusesKeys(t *testing.T) {
	dataDir := t.TempDir()
	first := RootConfig{Server: ServerConfig{DataDir: dataDir}}
	result, err := EnsureServerSecrets(&first)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Generated {
		t.Fatal("expected secrets to be generated")
	}
	if first.Server.AuthSigningKey == "" || first.Server.SecretEncryptionKey == "" {
		t.Fatal("generated keys were not applied")
	}

	info, err := os.Stat(filepath.Join(dataDir, ServerSecretsFilename))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("secret file mode = %o, want 600", got)
	}

	second := RootConfig{Server: ServerConfig{DataDir: dataDir}}
	result, err = EnsureServerSecrets(&second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Generated {
		t.Fatal("existing secrets should be reused")
	}
	if second.Server.AuthSigningKey != first.Server.AuthSigningKey ||
		second.Server.SecretEncryptionKey != first.Server.SecretEncryptionKey {
		t.Fatal("server keys changed across restarts")
	}
}

func TestEnsureServerSecretsHonorsExplicitAndInsecureSettings(t *testing.T) {
	dataDir := t.TempDir()
	cfg := RootConfig{Server: ServerConfig{
		DataDir:                  dataDir,
		AuthSigningKey:           "explicit-auth-key",
		SecretEncryptionKey:      "explicit-encryption-key",
		AllowInsecureTrustedMode: true,
		AllowInsecureDevKey:      true,
	}}
	result, err := EnsureServerSecrets(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "" || result.Generated {
		t.Fatalf("unexpected secret file result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dataDir, ServerSecretsFilename)); !os.IsNotExist(err) {
		t.Fatalf("secret file should not exist, got %v", err)
	}
}

func TestEnsureServerSecretsRejectsExposedFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, ServerSecretsFilename)
	if err := os.WriteFile(path, []byte("version: 1\nauth_signing_key: key\nsecret_encryption_key: key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := RootConfig{Server: ServerConfig{DataDir: dataDir}}
	if _, err := EnsureServerSecrets(&cfg); err == nil {
		t.Fatal("expected insecure file permissions to be rejected")
	}
}
