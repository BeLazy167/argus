// jev_resolver.go: per-installation Jev key resolution shared by every Jev
// consumer (orchestrator, scoring, triage).
//
// Resolution order:
//  1. BYOK — a provider_keys row for provider 'typesafe' (repo-level first,
//     then org-level, the store's built-in cascade). Adding the key in
//     Settings → Providers IS the installation's consent to egress, so BYOK
//     needs no feature flag. A stored base_url overrides the API root.
//  2. Deployment env key — TYPESAFE_API_KEY, which additionally requires the
//     jev_classifier feature flag (default OFF): the shared key alone never
//     egresses tenant code.
package pipeline

import (
	"context"
	"log/slog"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// jevKeyResolver is the one-method store surface resolveJevEvaluator consumes
// (callers convert *store.Store via jevKeyReaderFor); tests substitute a fake.
type jevKeyResolver interface {
	ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (apiKey, baseURL string, found bool, err error)
}

// jevKeyReaderFor maps a possibly-nil *store.Store to a jevKeyResolver. A
// typed nil inside the interface would pass the nil check and panic inside
// the store — same defusal shape as featureFlagReaderFor.
func jevKeyReaderFor(st *store.Store) jevKeyResolver {
	if st == nil {
		return nil
	}
	return st
}

// resolveJevEvaluator picks the evaluator for one installation: a stored
// BYOK 'typesafe' key wins; else the deployment client when jev_classifier
// opted in. nil means "no Jev this run" — callers treat it as
// straight-to-LLM. repoID mirrors store.ResolveAPIKey: nil means no repo
// context (org-level lookup only).
func resolveJevEvaluator(ctx context.Context, keys jevKeyResolver, env jevEvaluator, installationID int64, repoID *int64, flags FeatureFlags) jevEvaluator {
	if keys != nil {
		key, baseURL, found, err := keys.ResolveAPIKey(ctx, installationID, repoID, jev.ProviderName)
		switch {
		case err != nil:
			// A lookup failure must not disable Jev — fall through to the
			// env path.
			var rid int64
			if repoID != nil {
				rid = *repoID
			}
			slog.WarnContext(ctx, "jev key resolution failed, falling back to env client",
				"error", err, "installation_id", installationID, "repo_id", rid, "provider", jev.ProviderName)
		case found && key != "":
			if baseURL != "" {
				if jev.ValidBaseURL(baseURL) {
					return jev.NewClient(key, jev.WithBaseURL(baseURL))
				}
				// A malformed stored endpoint (typo, scheme-less host) would
				// fail every eval — keep the key, fall back to the default
				// API root, and make the bad row visible.
				slog.WarnContext(ctx, "jev stored base_url invalid, using default endpoint",
					"base_url", baseURL, "installation_id", installationID, "provider", jev.ProviderName)
			}
			return jev.NewClient(key)
		}
	}
	if env != nil && flags.JevClassifier {
		return env
	}
	return nil
}

// jevStageTokens converts one eval result into spend for the caller's stage
// bucket. res.Model is the truthful provenance — never hardcode it.
func jevStageTokens(res jev.Result) StageTokens {
	return StageTokens{
		PromptTokens:     res.Usage.InputTokens,
		CompletionTokens: res.Usage.OutputTokens,
		TotalTokens:      res.Usage.InputTokens + res.Usage.OutputTokens,
		Cost:             res.Cost,
		Model:            res.Model,
		Provider:         jev.ProviderName,
	}
}
