package credential

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

func (s *Store) Test(ctx context.Context, projectID, name string, req TestRequest) (TestResult, error) {
	meta, err := s.repo.Get(ctx, projectID, name)
	if err != nil {
		return TestResult{}, err
	}
	if meta == nil {
		return TestResult{}, ErrNotFound
	}
	if meta.Kind != KindGit {
		return TestResult{}, fmt.Errorf("%w: credential kind %q is not testable", ErrInvalid, meta.Kind)
	}

	value, err := s.resolve(ctx, projectID, name, "", req.Repo)
	if err != nil {
		return TestResult{}, err
	}

	result := testGit(ctx, req.Repo, value)

	msg := result.Message
	if result.OK {
		msg = ""
	}
	_ = s.repo.RecordTestResult(ctx, projectID, name, result.OK, msg)
	return result, nil
}

func testGit(ctx context.Context, repo string, value Value) TestResult {
	if repo == "" {
		return TestResult{OK: false, Message: "repo URL is required for git credential test"}
	}
	token := firstNonEmpty(value.Data["token"], value.Data["password"])
	if token == "" {
		return TestResult{OK: false, Message: "credential has no token"}
	}
	authedURL := injectGitCredentials(repo, firstNonEmpty(value.Data["username"], value.Data["user"]), token)

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// "--" stops git from interpreting authedURL as a flag if it happens to
	// start with "-" (argument injection, since it's built from a user-supplied repo URL).
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "--", authedURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return TestResult{OK: false, Message: scrubCredentials(msg, token)}
	}
	return TestResult{OK: true, Message: "credential successful"}
}

func injectGitCredentials(repoURL, username, token string) string {
	if username == "" {
		username = "x-token"
	}
	u, err := url.Parse(repoURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return repoURL
	}
	u.User = url.UserPassword(username, token)
	return u.String()
}

func scrubCredentials(msg, token string) string {
	if token == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, token, "***")
	return strings.ReplaceAll(msg, escapedPassword(token), "***")
}

func escapedPassword(token string) string {
	const marker = "user:"
	escaped := url.UserPassword("user", token).String()
	return strings.TrimPrefix(escaped, marker)
}
