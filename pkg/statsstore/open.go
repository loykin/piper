package statsstore

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	SpoolDir      string
	SpoolMaxBytes int64
	Logs          BackendConfig
	Metrics       BackendConfig
	Resolve       CredentialResolver
}

type BackendConfig struct {
	URL             string
	CredentialRef   string
	Retention       time.Duration
	ManageRetention bool
}

type Fallback struct {
	Logs         LogBackend
	Metrics      MetricBackend
	Capabilities Capabilities
	Close        func() error
}

func ValidateBackendURL(kind, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("stats.%s.url must be an absolute backend URL", kind)
	}
	if u.User != nil {
		return fmt.Errorf("stats.%s.url must not contain credentials; use credential_ref", kind)
	}
	scheme := strings.ToLower(u.Scheme)
	supported := scheme == "elasticsearch" || scheme == "elasticsearch+https" || scheme == "clickhouse" || scheme == "clickhouse+https" ||
		(kind == "metrics" && (scheme == "influxdb" || scheme == "influxdb+https"))
	if !supported {
		return fmt.Errorf("stats.%s.url has unsupported scheme %q", kind, u.Scheme)
	}
	for key := range u.Query() {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "api_key") {
			return fmt.Errorf("stats.%s.url query must not contain secret field %q; use credential_ref", kind, key)
		}
	}
	return nil
}

func Open(config Config, fallback Fallback) (*Store, error) {
	if fallback.Logs == nil || fallback.Metrics == nil {
		return nil, fmt.Errorf("statistics log and metric fallbacks are required")
	}
	if err := ValidateBackendURL("logs", config.Logs.URL); err != nil {
		return nil, err
	}
	if err := ValidateBackendURL("metrics", config.Metrics.URL); err != nil {
		return nil, err
	}
	if config.Logs.URL == "" && config.Metrics.URL == "" {
		return NewStore(fallback.Logs, fallback.Metrics, fallback.Capabilities, fallback.Close), nil
	}
	resolve := func(c BackendConfig) (map[string]string, error) {
		if c.CredentialRef == "" {
			return map[string]string{}, nil
		}
		if config.Resolve == nil {
			return nil, fmt.Errorf("credential resolver is required for statistics credential_ref")
		}
		return config.Resolve(context.Background(), c.CredentialRef)
	}
	logCredential, err := resolve(config.Logs)
	if err != nil {
		return nil, fmt.Errorf("resolve stats.logs credential_ref: %w", err)
	}
	metricCredential, err := resolve(config.Metrics)
	if err != nil {
		return nil, fmt.Errorf("resolve stats.metrics credential_ref: %w", err)
	}
	logs, metrics := fallback.Logs, fallback.Metrics
	capabilities := fallback.Capabilities
	if config.Logs.URL != "" && config.Logs.URL == config.Metrics.URL && config.Logs.CredentialRef == config.Metrics.CredentialRef {
		switch schemeOf(config.Logs.URL) {
		case "elasticsearch":
			backend, openErr := openElasticsearch(config.Logs.URL, logCredential, config.Logs.Retention, config.Metrics.Retention, config.Logs.ManageRetention || config.Metrics.ManageRetention)
			if openErr != nil {
				return nil, openErr
			}
			logs, metrics = backend, backend
		case "clickhouse":
			backend, openErr := openClickHouse(config.Logs.URL, logCredential, config.Logs.Retention, config.Metrics.Retention, config.Logs.ManageRetention || config.Metrics.ManageRetention)
			if openErr != nil {
				return nil, openErr
			}
			logs, metrics = backend, backend
		}
		capabilities = Capabilities{FullTextSearch: true, TimeRange: true, MetricKeyFilter: true}
	} else {
		if config.Logs.URL != "" {
			logs, err = openLogBackend(config.Logs, logCredential)
			if err != nil {
				return nil, err
			}
			capabilities.FullTextSearch, capabilities.TimeRange = true, true
		}
		if config.Metrics.URL != "" {
			metrics, err = openMetricBackend(config.Metrics, metricCredential)
			if err != nil {
				return nil, err
			}
			capabilities.TimeRange, capabilities.MetricKeyFilter = true, true
		}
	}
	if config.SpoolDir == "" {
		return nil, fmt.Errorf("stats.spool.dir is required when an external statistics backend is configured")
	}
	spool, err := openDiskSpool(filepath.Clean(config.SpoolDir), config.SpoolMaxBytes)
	if err != nil {
		return nil, err
	}
	wrapped := newSpooledBackend(logs, metrics, spool, config.Logs.URL != "", config.Metrics.URL != "")
	store := NewStore(wrapped, wrapped, capabilities, func() error {
		wrapped.close()
		if fallback.Close != nil {
			return fallback.Close()
		}
		return nil
	})
	store.healthFn = wrapped.health
	return store, nil
}

func schemeOf(raw string) string {
	parsed, _ := url.Parse(raw)
	return strings.TrimSuffix(strings.ToLower(parsed.Scheme), "+https")
}
func openLogBackend(c BackendConfig, credential map[string]string) (LogBackend, error) {
	switch schemeOf(c.URL) {
	case "elasticsearch":
		return openElasticsearch(c.URL, credential, c.Retention, 0, c.ManageRetention)
	case "clickhouse":
		return openClickHouse(c.URL, credential, c.Retention, 0, c.ManageRetention)
	default:
		return nil, fmt.Errorf("unsupported log statistics backend")
	}
}
func openMetricBackend(c BackendConfig, credential map[string]string) (MetricBackend, error) {
	switch schemeOf(c.URL) {
	case "elasticsearch":
		return openElasticsearch(c.URL, credential, 0, c.Retention, c.ManageRetention)
	case "clickhouse":
		return openClickHouse(c.URL, credential, 0, c.Retention, c.ManageRetention)
	case "influxdb":
		return openInfluxDB(c.URL, credential, c.Retention, c.ManageRetention)
	default:
		return nil, fmt.Errorf("unsupported metric statistics backend")
	}
}
