// Package ui는 빌드된 React 앱을 Go 바이너리에 임베딩한다.
//
// 사용:
//
//	mux.Handle("/", ui.Handler())
//
// SPA 라우팅을 위해 존재하지 않는 경로는 index.html로 fallback한다.
// API 경로(/runs, /api, /health)는 통과시킨다.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var dist embed.FS

// Handler는 React SPA를 서빙하는 http.Handler를 반환한다.
// API 경로가 아닌 모든 요청은 index.html로 fallback한다.
// dist가 비어 있으면(make ui 전) 503을 반환한다.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("ui: embed dist not found: " + err.Error())
	}
	// index.html이 없으면 UI 미빌드 상태
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "UI not built — run 'make ui'", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API 경로는 통과 (이 핸들러로 오면 안 되지만 방어적으로)
		if isAPIPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		// 실제 파일이 있으면 서빙, 없으면 SPA index.html로 fallback
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// SPA fallback
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func isAPIPath(path string) bool {
	for _, prefix := range []string{"/runs", "/api/", "/health"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
