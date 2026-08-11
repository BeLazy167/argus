package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// UpsertProviderKey creates or rotates a provider key. base_url/model use
// PATCH semantics on conflict: omitting them (nil) PRESERVES the stored values
// — a key-only rotation must never silently strip a custom endpoint or its
// declared model (that would flip the row back to platform defaults and
// fragment the embedding space). To clear them, delete and recreate the row.
func (s *Store) UpsertProviderKey(ctx context.Context, installationID int64, repoID *int64, provider, apiKey string, baseURL, model *string) (*ProviderKey, error) {
	// Empty apiKey updates endpoint/model metadata without destroying a stored key.
	enc, hint := "", ""
	if apiKey != "" {
		var err error
		enc, err = crypto.Encrypt(apiKey)
		if err != nil {
			return nil, fmt.Errorf("encrypting api key: %w", err)
		}
		if len(apiKey) >= 4 {
			hint, err = crypto.Encrypt(apiKey[len(apiKey)-4:])
			if err != nil {
				return nil, fmt.Errorf("encrypting key hint: %w", err)
			}
		}
	}

	if repoID == nil {
		row, err := s.q.UpsertProviderKeyOrgLevel(ctx, db.UpsertProviderKeyOrgLevelParams{InstallationID: installationID, Provider: provider, APIKeyEnc: enc, BaseURL: baseURL, KeyHint: &hint, Model: model})
		if err != nil {
			return nil, err
		}
		key := providerKeyFromValues(row.ID, row.InstallationID, row.RepoID, row.Provider, row.APIKeyEnc, row.BaseURL, row.Model, row.KeyHint, row.CreatedAt, row.UpdatedAt)
		return &key, nil
	}
	row, err := s.q.UpsertProviderKeyRepoLevel(ctx, db.UpsertProviderKeyRepoLevelParams{InstallationID: installationID, RepoID: repoID, Provider: provider, APIKeyEnc: enc, BaseURL: baseURL, KeyHint: &hint, Model: model})
	if err != nil {
		return nil, err
	}
	key := providerKeyFromValues(row.ID, row.InstallationID, row.RepoID, row.Provider, row.APIKeyEnc, row.BaseURL, row.Model, row.KeyHint, row.CreatedAt, row.UpdatedAt)
	return &key, nil
}

func (s *Store) ListProviderKeys(ctx context.Context, installationID int64) ([]ProviderKey, error) {
	rows, err := s.q.ListProviderKeys(ctx, installationID)
	if err != nil {
		return nil, err
	}
	keys := make([]ProviderKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, providerKeyFromValues(row.ID, row.InstallationID, row.RepoID, row.Provider, row.APIKeyEnc, row.BaseURL, row.Model, row.KeyHint, row.CreatedAt, row.UpdatedAt))
	}
	return keys, nil
}

func (s *Store) DeleteProviderKey(ctx context.Context, id int64, installationID int64) error {
	_, err := s.DeleteProviderKeyReturningProvider(ctx, id, installationID)
	return err
}

// DeleteProviderKeyReturningProvider atomically deletes a tenant-scoped key and
// returns its provider slot so callers can trigger provider-specific cleanup.
func (s *Store) DeleteProviderKeyReturningProvider(ctx context.Context, id int64, installationID int64) (string, error) {
	provider, err := s.q.DeleteProviderKey(ctx, db.DeleteProviderKeyParams{ID: id, InstallationID: installationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("provider key %d not found", id)
	}
	if err != nil {
		return "", err
	}
	return provider, nil
}

// ResolveAPIKey resolves an API key for a provider: repo-level → org-level → env fallback.
// Returns decrypted apiKey, baseURL, and whether a DB key was found.
func (s *Store) ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (apiKey string, baseURL string, found bool, err error) {
	var enc string
	var configuredBaseURL *string
	resolved := false
	if repoID != nil {
		row, rowErr := s.q.ResolveAPIKeyRepoLevel(ctx, db.ResolveAPIKeyRepoLevelParams{InstallationID: installationID, RepoID: repoID, Provider: provider})
		if rowErr == nil {
			enc, configuredBaseURL = row.APIKeyEnc, row.BaseURL
			resolved = true
		} else if !errors.Is(rowErr, pgx.ErrNoRows) {
			return "", "", false, rowErr
		}
	}
	if !resolved {
		row, rowErr := s.q.ResolveAPIKeyOrgLevel(ctx, db.ResolveAPIKeyOrgLevelParams{InstallationID: installationID, Provider: provider})
		if errors.Is(rowErr, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		if rowErr != nil {
			return "", "", false, rowErr
		}
		enc, configuredBaseURL = row.APIKeyEnc, row.BaseURL
	}
	decrypted, err := crypto.Decrypt(enc)
	if err != nil {
		return "", "", false, fmt.Errorf("decrypting key: %w", err)
	}
	if configuredBaseURL != nil {
		baseURL = *configuredBaseURL
	}
	return decrypted, baseURL, true, nil
}

// ResolveEmbeddingsKey resolves the installation-wide "embeddings" BYOK slot.
// Embeddings are deliberately NOT repo-scoped: an installation has exactly one
// embedding space (memories.embedding_model + the similarity floors are
// calibrated per space), so per-repo keys would fragment retrieval. The API
// rejects repo-scoped embeddings rows; this reads only the org-level row.
// model is "" when the row leaves it NULL (caller applies the platform model).
func (s *Store) ResolveEmbeddingsKey(ctx context.Context, installationID int64) (apiKey, baseURL, model string, found bool, err error) {
	var enc string
	var bu, m *string
	err = s.Pool.QueryRow(ctx, `
		SELECT api_key_enc, base_url, model FROM provider_keys
		WHERE installation_id = $1 AND repo_id IS NULL AND provider = 'embeddings'
	`, installationID).Scan(&enc, &bu, &m)
	if err == pgx.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	decrypted := ""
	if enc != "" {
		var dErr error
		decrypted, dErr = crypto.Decrypt(enc)
		if dErr != nil {
			return "", "", "", false, fmt.Errorf("decrypting embeddings key: %w", dErr)
		}
	}
	if bu != nil {
		baseURL = *bu
	}
	if m != nil {
		model = *m
	}
	return decrypted, baseURL, model, true, nil
}
