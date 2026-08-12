package commands

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	piper "github.com/loykin/piper"
	cliconfig "github.com/loykin/piper/cmd/piper/config"
	"github.com/loykin/piper/internal/membertunnel"
	"github.com/spf13/cobra"
)

func newServerCmd(loader *cliconfig.Loader, factory PiperFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "server",
		Short:   "start the piper API server",
		PreRunE: makePreRunE(loader),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := loader.Load()
			if err != nil {
				return err
			}
			if err := cliconfig.ValidateServer(root); err != nil {
				return err
			}
			p, err := factory()
			if err != nil {
				return err
			}
			defer func() { _ = p.Close() }()

			if root.Deployment.Mode == cliconfig.DeploymentModeMember {
				return runMemberMode(root, p)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			return p.Serve(ctx, piper.ServeOption{Addr: root.Server.HTTPAddr})
		},
	}

	return cmd
}

// runMemberMode connects p to its configured Home over an outbound tunnel
// (fed.md §13.5) and serves memberclient.Client calls from it via
// piper.NewLocalMemberClient. Unlike home mode, it never calls p.Serve —
// a Member exposes no inbound HTTP/UI (fed.md §10.7).
func runMemberMode(root cliconfig.RootConfig, p *piper.Piper) error {
	memberID := resolveMemberID(root)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cli := membertunnel.NewClient(membertunnel.Config{
		HomeURL:  root.Home.URL,
		HomeID:   root.Home.ID,
		MemberID: memberID,
		Token:    root.Home.EnrollmentToken,
	}, piper.NewLocalMemberClient(p))

	slog.Info("member mode: connecting to home", "home_url", root.Home.URL, "home_id", root.Home.ID, "member_id", memberID)
	return cli.Run(ctx)
}

// resolveMemberID returns deployment.member_id, or a stable hostname-derived
// default when unset.
func resolveMemberID(root cliconfig.RootConfig) string {
	if root.Deployment.MemberID != "" {
		return root.Deployment.MemberID
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "member"
	} else {
		host = "member-" + host
	}
	return sanitizeName(host)
}

// sanitizeName converts an arbitrary string into a DNS-label-safe
// identifier: lowercase, [a-z0-9-] only, at most 63 characters.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}
