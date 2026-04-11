package piper

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/piper/piper/pkg/ui"
)

// ServeOption customizes the behavior of Serve
type ServeOption struct {
	// Extra is an additional http.Handler injected by the caller.
	// It is invoked before the piper API (auth, custom routes, etc.).
	//
	//   p.Serve(ctx, piper.ServeOption{
	//       Extra: myRouter,  // chi, gin, echo, etc.
	//   })
	Extra http.Handler

	// Addr overrides Config.Server.Addr when non-empty.
	Addr string
}

// Serve runs the piper HTTP server.
// Supports both HTTP and HTTPS. Library users can call this directly or
// mount it on their own server using Handler().
func (p *Piper) Serve(ctx context.Context, opt ServeOption) error {
	// Combined API + UI handler
	mux := http.NewServeMux()
	mux.Handle("/runs", p.Handler(opt.Extra))
	mux.Handle("/runs/", p.Handler(opt.Extra))
	mux.Handle("/api/", p.Handler(opt.Extra))
	mux.Handle("/health", p.Handler(opt.Extra))
	mux.Handle("/", ui.Handler()) // UI handles all remaining paths

	// Apply middleware chain (Config.Hooks.Middleware)
	var handler http.Handler = mux
	for i := len(p.cfg.Hooks.Middleware) - 1; i >= 0; i-- {
		handler = p.cfg.Hooks.Middleware[i](handler)
	}

	addr := p.cfg.Server.Addr
	if opt.Addr != "" {
		addr = opt.Addr
	}
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	tlsCfg := p.cfg.Server.TLS
	if tlsCfg.Enabled {
		if tlsCfg.CertFile == "" || tlsCfg.KeyFile == "" {
			return fmt.Errorf("TLS enabled but cert_file or key_file not set")
		}
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		slog.Info("piper server starting (HTTPS)", "addr", srv.Addr)
		if err := srv.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			return err
		}
		return nil
	}

	slog.Info("piper server starting (HTTP)", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
