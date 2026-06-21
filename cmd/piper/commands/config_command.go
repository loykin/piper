package commands

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	cliconfig "github.com/piper/piper/cmd/piper/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Validate and inspect configuration"}
	cmd.AddCommand(newConfigValidateCmd(loader), newConfigShowCmd(loader))
	return cmd
}

func newConfigValidateCmd(loader *cliconfig.Loader) *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use: "validate", Short: "Validate the effective configuration",
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load()
			if err != nil {
				return err
			}
			if err := validateRole(cfg, role); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
			return err
		},
	}
	cmd.Flags().StringVar(&role, "command", "server", "role: server, worker, notebook-worker, serving-worker, k8s-worker")
	return cmd
}

func newConfigShowCmd(loader *cliconfig.Loader) *cobra.Command {
	var role string
	var sources bool
	cmd := &cobra.Command{
		Use: "show", Short: "Print the redacted effective configuration",
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load()
			if err != nil {
				return err
			}
			if err := validateRole(cfg, role); err != nil {
				return err
			}
			cfg.Storage.Token = redact(cfg.Storage.Token)
			cfg.Storage.URL = redactURL(cfg.Storage.URL)
			cfg.Source.Git.Token = redact(cfg.Source.Git.Token)
			cfg.Server.WorkerToken = redact(cfg.Server.WorkerToken)
			cfg.Server.AuthSigningKey = redact(cfg.Server.AuthSigningKey)
			cfg.Worker.WorkerToken = redact(cfg.Worker.WorkerToken)
			cfg.Worker.StorageToken = redact(cfg.Worker.StorageToken)
			data, err := yaml.Marshal(cfg)
			if err != nil {
				return err
			}
			if sources {
				byKey := loader.Sources()
				keys := make([]string, 0, len(byKey))
				for key := range byKey {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "# %s = %s\n", key, byKey[key]); err != nil {
						return err
					}
				}
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	cmd.Flags().StringVar(&role, "command", "server", "role to validate before printing")
	cmd.Flags().BoolVar(&sources, "sources", false, "include the winning source for each key")
	return cmd
}

func validateRole(cfg cliconfig.RootConfig, role string) error {
	switch role {
	case "server":
		return cliconfig.ValidateServer(cfg)
	case "worker":
		return cliconfig.ValidatePipeline(cfg)
	case "notebook-worker":
		return cliconfig.ValidateNotebook(cfg)
	case "serving-worker":
		return cliconfig.ValidateServing(cfg)
	case "k8s-worker":
		return cliconfig.ValidateK8s(cfg)
	default:
		return fmt.Errorf("unknown command role %q", role)
	}
}

func redact(value string) string {
	if value == "" {
		return ""
	}
	return "******"
}

func redactURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return "******"
	}
	if u.User != nil {
		u.User = url.User("******")
	}
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "accesskey") || strings.Contains(lower, "signature") {
			q.Set(key, "******")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
