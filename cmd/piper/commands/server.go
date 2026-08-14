package commands

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	piper "github.com/loykin/piper"
	cliconfig "github.com/loykin/piper/cmd/piper/config"
	"github.com/loykin/piper/internal/agentpb"
	"github.com/loykin/piper/internal/memberclient"
	"github.com/loykin/piper/internal/membertunnel"
	"github.com/loykin/piper/pkg/project"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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

			return runHomeMode(root, p)
		},
	}

	return cmd
}

// runHomeMode serves the user-facing Home API. When tunnel_addr is set it
// also accepts enrolled Member connections and routes Run requests according
// to home.projects; projects absent from that map stay on the Local Member.
func runHomeMode(root cliconfig.RootConfig, p *piper.Piper) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	homeID := root.Home.ID
	if homeID == "" {
		homeID = project.LocalHomeID
	}
	memberIDs := make([]string, 0, len(root.Home.Members)+1)
	memberIDs = append(memberIDs, project.LocalMemberID)
	for memberID := range root.Home.Members {
		memberIDs = append(memberIDs, memberID)
	}
	if err := p.SyncFederationMembers(ctx, homeID, memberIDs); err != nil {
		return fmt.Errorf("sync federation members: %w", err)
	}

	opt := piper.ServeOption{Addr: root.Server.HTTPAddr, HomeID: homeID}
	if root.Home.TunnelAddr == "" {
		return p.Serve(ctx, opt)
	}

	listener, err := net.Listen("tcp", root.Home.TunnelAddr)
	if err != nil {
		return fmt.Errorf("listen for member tunnel on %s: %w", root.Home.TunnelAddr, err)
	}
	defer func() { _ = listener.Close() }()

	tunnelServer := membertunnel.NewServer(membertunnel.ServerConfig{
		HomeID: root.Home.ID,
		Tokens: root.Home.Members,
		OnConnectionChanged: func(ctx context.Context, homeID, memberID string, connected bool) error {
			return p.SetFederationMemberConnected(ctx, homeID, memberID, connected)
		},
	})
	var grpcOptions []grpc.ServerOption
	if root.Server.TLS.Enabled {
		certificate, err := tls.LoadX509KeyPair(root.Server.TLS.CertFile, root.Server.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("load member tunnel TLS certificate: %w", err)
		}
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})))
	}
	grpcServer := grpc.NewServer(grpcOptions...)
	agentpb.RegisterMemberTunnelServiceServer(grpcServer, tunnelServer)

	local := piper.NewLocalMemberClient(p)
	opt.Member = &memberclient.RoutingClient{Resolve: func(ref project.ProjectRef) (memberclient.Client, error) {
		if ref.MemberID == project.LocalMemberID {
			return local, nil
		}
		if remote, ok := tunnelServer.Client(ref.MemberID); ok {
			return remote, nil
		}
		return nil, fmt.Errorf("%w: member %q", memberclient.ErrMemberUnavailable, ref.MemberID)
	}}
	opt.ProjectOwner = func(projectID, requested string) (string, error) {
		memberID := requested
		if memberID == "" {
			memberID = root.Home.Projects[projectID]
		}
		if memberID == "" || memberID == project.LocalMemberID {
			return project.LocalMemberID, nil
		}
		if _, ok := root.Home.Members[memberID]; !ok {
			return "", fmt.Errorf("unknown Owner Member %q", memberID)
		}
		return memberID, nil
	}
	opt.ProjectRef = func(projectID string) project.ProjectRef {
		return project.ProjectRef{HomeID: root.Home.ID, MemberID: project.LocalMemberID, ProjectID: projectID}
	}
	for projectID, memberID := range root.Home.Projects {
		if _, err := p.SetProjectOwner(ctx, homeID, projectID, memberID, "config"); err != nil {
			return fmt.Errorf("configure owner for project %q: %w", projectID, err)
		}
	}

	errCh := make(chan error, 2)
	go func() {
		slog.Info("home mode: member tunnel listening", "addr", listener.Addr().String(), "home_id", root.Home.ID)
		errCh <- grpcServer.Serve(listener)
	}()
	go func() { errCh <- p.Serve(ctx, opt) }()

	firstErr := <-errCh
	cancel()
	grpcServer.Stop()
	secondErr := <-errCh
	for _, serveErr := range []error{firstErr, secondErr} {
		if serveErr != nil && serveErr != grpc.ErrServerStopped {
			return serveErr
		}
	}
	return nil
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
