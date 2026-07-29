package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	ServerSecretsFilename = ".server-secrets.yaml"
	serverSecretsVersion  = 1
)

type serverSecrets struct {
	Version             int    `yaml:"version"`
	AuthSigningKey      string `yaml:"auth_signing_key,omitempty"`
	SecretEncryptionKey string `yaml:"secret_encryption_key,omitempty"`
}

// ServerSecretsResult describes how server secrets were resolved without
// exposing their values.
type ServerSecretsResult struct {
	Path      string
	Generated bool
}

// EnsureServerSecrets fills missing production server keys from a persistent,
// owner-readable file in server.data_dir. Explicit config and environment
// values always win. Insecure development modes intentionally skip generation.
func EnsureServerSecrets(cfg *RootConfig) (ServerSecretsResult, error) {
	needAuth := cfg.Server.AuthSigningKey == "" && !cfg.Server.AllowInsecureTrustedMode
	needEncryption := cfg.Server.SecretEncryptionKey == "" && !cfg.Server.AllowInsecureDevKey
	if !needAuth && !needEncryption {
		return ServerSecretsResult{}, nil
	}

	dataDir := cfg.Server.DataDir
	if dataDir == "" {
		dataDir = "./piper-outputs"
	}
	path := filepath.Join(dataDir, ServerSecretsFilename)
	result := ServerSecretsResult{Path: path}

	secrets, exists, err := readServerSecrets(path)
	if err != nil {
		return result, err
	}
	if !exists {
		secrets = serverSecrets{Version: serverSecretsVersion}
	}

	changed := false
	if needAuth {
		if secrets.AuthSigningKey == "" {
			secrets.AuthSigningKey, err = randomServerKey()
			if err != nil {
				return result, fmt.Errorf("config: generate auth signing key: %w", err)
			}
			changed = true
		}
		cfg.Server.AuthSigningKey = secrets.AuthSigningKey
	}
	if needEncryption {
		if secrets.SecretEncryptionKey == "" {
			secrets.SecretEncryptionKey, err = randomServerKey()
			if err != nil {
				return result, fmt.Errorf("config: generate secret encryption key: %w", err)
			}
			changed = true
		}
		cfg.Server.SecretEncryptionKey = secrets.SecretEncryptionKey
	}

	if changed {
		if err := writeServerSecrets(path, secrets); err != nil {
			return result, err
		}
		result.Generated = true
	}
	return result, nil
}

func readServerSecrets(path string) (serverSecrets, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return serverSecrets{}, false, nil
	}
	if err != nil {
		return serverSecrets{}, false, fmt.Errorf("config: inspect server secrets %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return serverSecrets{}, false, fmt.Errorf("config: server secrets %q must be a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return serverSecrets{}, false, fmt.Errorf("config: server secrets %q permissions must not allow group or other access (use chmod 600)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return serverSecrets{}, false, fmt.Errorf("config: read server secrets %q: %w", path, err)
	}
	var secrets serverSecrets
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&secrets); err != nil {
		return serverSecrets{}, false, fmt.Errorf("config: decode server secrets %q: %w", path, err)
	}
	if secrets.Version != serverSecretsVersion {
		return serverSecrets{}, false, fmt.Errorf("config: server secrets %q version must be %d", path, serverSecretsVersion)
	}
	return secrets, true, nil
}

func writeServerSecrets(path string, secrets serverSecrets) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create server data directory for secrets: %w", err)
	}
	data, err := yaml.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("config: encode server secrets: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-secrets-*")
	if err != nil {
		return fmt.Errorf("config: create temporary server secrets: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("config: secure temporary server secrets: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("config: write temporary server secrets: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("config: sync temporary server secrets: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: close temporary server secrets: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("config: install server secrets %q: %w", path, err)
	}
	return nil
}

func randomServerKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
