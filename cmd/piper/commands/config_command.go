package commands

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "validate and inspect configuration"}
	cmd.AddCommand(newConfigValidateCmd(loader), newConfigShowCmd(loader))
	return cmd
}

func newConfigValidateCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use: "validate", Short: "validate the effective configuration",
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateServer(cfg); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "configuration is valid")
			return err
		},
	}
	return cmd
}

func newConfigShowCmd(loader *cliconfig.Loader) *cobra.Command {
	var sources bool
	cmd := &cobra.Command{
		Use: "show", Short: "print the redacted effective configuration",
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateServer(cfg); err != nil {
				return err
			}
			cfg.Storage.Token = redact(cfg.Storage.Token)
			cfg.Storage.URL = redactURL(cfg.Storage.URL)
			cfg.Source.Git.Token = redact(cfg.Source.Git.Token)
			cfg.Server.WorkerToken = redact(cfg.Server.WorkerToken)
			cfg.Server.AuthSigningKey = redact(cfg.Server.AuthSigningKey)
			cfg.Server.SecretEncryptionKey = redact(cfg.Server.SecretEncryptionKey)
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
	cmd.Flags().BoolVar(&sources, "sources", false, "include the winning source for each key")
	return cmd
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
