package credential

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/loykin/piper/pkg/pipeline"
)

// ResolvePipelineEnv resolves per-step credential-derived environment
// variables for every step of pl: git credentials (credentialRef explicit,
// or endpoint auto-match) for git-sourced steps, plus options.env resolution
// for every step. Steps with no resolved env are omitted from the result.
func (s *Store) ResolvePipelineEnv(ctx context.Context, projectID, runID string, pl *pipeline.Pipeline) (map[string][]string, error) {
	envByStep := map[string][]string{}
	for _, step := range pl.Spec.Steps {
		var env []string

		// Git credential resolution: credentialRef > auto-match by endpoint.
		if strings.TrimSpace(step.Run.Source) == "git" && strings.TrimSpace(step.Run.Repo) != "" {
			gitEnv, err := s.resolveGitEnv(ctx, projectID, runID, step.Name, step.Run.CredentialRef, step.Run.Repo)
			if err != nil {
				return nil, fmt.Errorf("step %q git credential: %w", step.Name, err)
			}
			env = append(env, gitEnv...)
		}

		// options.env: plain values + credentialRef resolution.
		if len(step.Options.Env) > 0 {
			optEnv, err := s.ResolveEnv(ctx, projectID, step.Options.Env)
			if err != nil {
				return nil, fmt.Errorf("step %q env: %w", step.Name, err)
			}
			env = append(env, optEnv...)
		}

		if len(env) > 0 {
			envByStep[step.Name] = env
		}
	}
	return envByStep, nil
}

// resolveGitEnv resolves git credentials for a step using priority:
// credentialRef (explicit) > endpoint auto-match (lowest).
// Returns nil env (no error) when no credential is configured.
func (s *Store) resolveGitEnv(ctx context.Context, projectID, runID, stepName, credentialRef, repoURL string) ([]string, error) {
	if strings.TrimSpace(credentialRef) != "" {
		env, err := s.GitEnv(ctx, projectID, credentialRef, repoURL)
		if err == nil {
			slog.Info("git credential resolved",
				"project_id", projectID,
				"run_id", runID,
				"step", stepName,
				"repo", repoURL,
				"credential", credentialRef,
				"source", "explicit",
			)
		}
		return env, err
	}
	// Auto-match: find credential whose endpoint covers repoURL.
	best, err := s.FindGitByRepo(ctx, projectID, repoURL)
	if err != nil {
		return nil, err
	}
	if best != nil {
		env, envErr := s.GitEnv(ctx, projectID, best.Name, repoURL)
		if envErr == nil {
			slog.Info("git credential resolved",
				"project_id", projectID,
				"run_id", runID,
				"step", stepName,
				"repo", repoURL,
				"credential", best.Name,
				"source", "endpoint-auto-match",
			)
		}
		return env, envErr
	}
	return nil, nil
}
