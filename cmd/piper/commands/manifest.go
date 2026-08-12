package commands

import (
	"context"
	"fmt"

	cliconfig "github.com/loykin/piper/cmd/piper/config"
	"github.com/loykin/piper/internal/manifestmigrate"
	"github.com/spf13/cobra"
)

// newManifestCmd returns the `piper manifest` sub-command group.
func newManifestCmd(loader *cliconfig.Loader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "inspect and migrate stored pipeline/notebook/serving manifests",
	}
	cmd.AddCommand(newManifestMigrateCmd(loader))
	return cmd
}

// newManifestMigrateCmd scans every stored Pipeline template, Notebook, and
// ModelService manifest for a non-empty placement.worker/placement.label
// field (fed.md §13.6 — both fields are rejected by every direct runtime,
// since a Piper installation only ever owns one runtime and has nothing to
// route to by worker ID or label). Default is a dry-run report; --apply
// cleans up what it finds.
func newManifestMigrateCmd(loader *cliconfig.Loader) *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "find (and optionally fix) stored manifests with a removed placement.worker/label field",
		Long: `Scans every stored Pipeline template, Notebook, and ModelService manifest
across all projects for a non-empty driver.placement.worker or
driver.placement.label field. Both were only ever meaningful when multiple
remote workers could be registered; a direct-runtime installation owns
exactly one runtime and rejects both outright at dispatch time.

Without --apply, this only reports what it finds.

With --apply:
  - Notebook and ModelService records are rewritten in place.
  - Pipeline templates are fixed by creating a new version with the field
    removed; the affected version itself is left untouched, matching how
    templates are already edited elsewhere (every edit is a new version,
    never an in-place rewrite of history).`,
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loader.Load()
			if err != nil {
				return err
			}
			repos, err := openRepos(cfg)
			if err != nil {
				return err
			}
			defer func() { _ = repos.Close() }()

			ctx := context.Background()
			var findings []manifestmigrate.Finding
			if apply {
				findings, err = manifestmigrate.Apply(ctx, repos)
			} else {
				findings, err = manifestmigrate.Scan(ctx, repos)
			}
			if err != nil {
				return err
			}

			printManifestFindings(cmd, findings, apply)
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "fix what's found (default: dry-run report only)")
	return cmd
}

func printManifestFindings(cmd *cobra.Command, findings []manifestmigrate.Finding, applied bool) {
	out := cmd.OutOrStdout()
	if len(findings) == 0 {
		fmt.Fprintln(out, "No stored manifests carry a placement.worker or placement.label field.")
		return
	}
	fmt.Fprintf(out, "Found %d manifest(s) with a placement.worker/label field:\n\n", len(findings))
	for _, f := range findings {
		switch f.Kind {
		case manifestmigrate.KindPipeline:
			fmt.Fprintf(out, "  [pipeline] project=%s name=%s version=%d", f.ProjectID, f.Name, f.Version)
		default:
			fmt.Fprintf(out, "  [%s] project=%s name=%s", f.Kind, f.ProjectID, f.Name)
		}
		switch {
		case !applied:
			fmt.Fprintln(out)
		case f.Err != nil:
			fmt.Fprintf(out, " — FAILED: %v\n", f.Err)
		case f.Kind == manifestmigrate.KindPipeline:
			fmt.Fprintf(out, " — fixed: created version %d\n", f.NewVersion)
		default:
			fmt.Fprintln(out, " — fixed")
		}
	}
	if !applied {
		fmt.Fprintln(out, "\nRe-run with --apply to fix these.")
	}
}
