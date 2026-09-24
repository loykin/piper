package piper

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	storemod "github.com/loykin/piper/internal/store"
	sqlitestore "github.com/loykin/piper/internal/store/sqlite"
	"github.com/loykin/piper/pkg/auth"
	"github.com/loykin/piper/pkg/viewer"
)

// TestOpenAPIMatchesRegisteredRoutes keeps docs/openapi.yaml honest: every
// JSON API route the server registers must be documented, and every
// documented operation must still exist. The router is built the way
// `piper server` builds it (built-in auth, Home directory routes enabled) so
// the comparison covers the full production route set.
func TestOpenAPIMatchesRegisteredRoutes(t *testing.T) {
	p := newTestPiper(t, Config{
		OutputDir: t.TempDir(),
		Runtime:   RuntimeConfig{Type: RuntimeBaremetal},
		Auth: AuthConfig{Factory: func(deps AuthDependencies) (AuthConfig, error) {
			users := sqlitestore.NewUserRepo(deps.Executor, storemod.PrimarySource)
			members := sqlitestore.NewMemberRepo(deps.Executor, storemod.PrimarySource)
			sessions := sqlitestore.NewSessionRepo(deps.Executor, storemod.PrimarySource)
			provider := auth.New(auth.Config{SigningKey: []byte("test-signing-key-0123456789abcdef")}, users, members, sessions)
			return AuthConfig{LoginRoutes: auth.NewHandler(provider, provider, false), Authenticator: provider, Authorizer: provider, UserDirectory: provider, UserManager: provider, ProjectMemberManager: provider}, nil
		}},
	})
	mgr := viewer.NewManager(p.repos.Viewer, p.store, p.cfg.OutputDir)
	engine := p.newRouterWithFederation(nil, mgr, nil, nil, nil, nil, "home-test").(*gin.Engine)

	registered := map[string]bool{}
	for _, r := range engine.Routes() {
		// Only the JSON API is contract-documented. Reverse proxies,
		// the workload /store, the SPA, and scrape/health endpoints are not.
		if !strings.HasPrefix(r.Path, "/api/") && r.Path != "/events" && r.Path != "/health" {
			continue
		}
		registered[r.Method+" "+r.Path] = true
	}

	raw, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	param := regexp.MustCompile(`\{([^}]+)\}`)
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		ginPath := param.ReplaceAllString(path, ":$1")
		// OpenAPI can't express gin's catch-all; document it as {path}.
		ginPath = strings.Replace(ginPath, "/artifacts/:path", "/artifacts/*path", 1)
		ginPath = strings.Replace(ginPath, "/storage/objects/:key", "/storage/objects/*key", 1)
		for method := range ops {
			switch method {
			case "get", "post", "put", "patch", "delete":
				documented[strings.ToUpper(method)+" "+ginPath] = true
			}
		}
	}

	var undocumented, stale []string
	for k := range registered {
		if !documented[k] {
			undocumented = append(undocumented, k)
		}
	}
	for k := range documented {
		if !registered[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)
	if len(undocumented) > 0 {
		t.Errorf("routes missing from docs/openapi.yaml:\n  %s", strings.Join(undocumented, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("docs/openapi.yaml documents routes the server no longer registers:\n  %s", strings.Join(stale, "\n  "))
	}
}
