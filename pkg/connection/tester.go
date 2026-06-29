package connection

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Test verifies connectivity for a connection and stores the result.
func (s *Store) Test(ctx context.Context, projectID, name string, req TestRequest) (TestResult, error) {
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return TestResult{}, err
	}
	if meta == nil {
		return TestResult{}, ErrNotFound
	}

	value, err := s.Resolve(ctx, projectID, name, req.Repo)
	if err != nil {
		return TestResult{}, err
	}

	var result TestResult
	switch meta.Type {
	case TypeGit:
		result = testGit(ctx, req.Repo, value, meta.Endpoint)
	case TypeRegistry:
		result = testRegistry(ctx, meta.Endpoint, value)
	default:
		return TestResult{}, fmt.Errorf("unknown connection type %q", meta.Type)
	}

	msg := result.Message
	if result.OK {
		msg = ""
	}
	_ = s.repo.RecordTestResult(ctx, projectID, name, result.OK, msg)
	return result, nil
}

func testGit(ctx context.Context, repo string, value Value, endpoint string) TestResult {
	if repo == "" {
		return TestResult{OK: false, Message: "repo URL is required for git connection test"}
	}
	token := firstNonEmpty(value.Data["token"], value.Data["password"])
	if token == "" {
		return TestResult{OK: false, Message: "connection has no token"}
	}

	// Build authenticated URL: inject credentials into the URL
	authedURL := injectGitCredentials(repo, firstNonEmpty(value.Data["username"], value.Data["user"]), token)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", authedURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		// Scrub credentials from error message before returning
		msg = scrubCredentials(msg, token)
		return TestResult{OK: false, Message: msg}
	}
	return TestResult{OK: true, Message: "connection successful"}
}

func testRegistry(_ context.Context, host string, value Value) TestResult {
	// Minimal check: ensure we have credentials. Full registry ping requires
	// docker client or registry API — implement when registry driver is added.
	if host == "" {
		return TestResult{OK: false, Message: "host is required for registry connection"}
	}
	if firstNonEmpty(value.Data["password"]) == "" {
		return TestResult{OK: false, Message: "connection has no password"}
	}
	return TestResult{OK: true, Message: "credentials present (registry ping not implemented)"}
}

// injectGitCredentials returns the repo URL with credentials embedded:
// https://user:token@github.com/org/repo
func injectGitCredentials(repoURL, username, token string) string {
	if username == "" {
		username = "x-token"
	}
	// Find "://" and insert credentials after it
	idx := strings.Index(repoURL, "://")
	if idx < 0 {
		return repoURL
	}
	scheme := repoURL[:idx+3]
	rest := repoURL[idx+3:]
	return scheme + username + ":" + token + "@" + rest
}

func scrubCredentials(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "***")
}
