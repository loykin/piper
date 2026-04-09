package piper

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
)

// ServeOption은 Serve 동작을 커스터마이징한다
type ServeOption struct {
	// Extra는 사용자가 주입하는 추가 http.Handler.
	// piper API 앞단에서 먼저 처리된다 (인증, 커스텀 라우트 등).
	//
	//   p.Serve(ctx, piper.ServeOption{
	//       Extra: myRouter,  // chi, gin, echo 등
	//   })
	Extra http.Handler
}

// Serve는 piper HTTP 서버를 실행한다.
// HTTP/HTTPS 모두 지원. 라이브러리 사용자는 직접 호출하거나
// Handler()로 자신의 서버에 마운트할 수 있다.
func (p *Piper) Serve(ctx context.Context, opt ServeOption) error {
	// 미들웨어 체인 적용 (Config.Hooks.Middleware)
	var handler http.Handler = p.Handler(opt.Extra)
	for i := len(p.cfg.Hooks.Middleware) - 1; i >= 0; i-- {
		handler = p.cfg.Hooks.Middleware[i](handler)
	}

	srv := &http.Server{
		Addr:    p.cfg.Server.Addr,
		Handler: handler,
	}

	// graceful shutdown
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
