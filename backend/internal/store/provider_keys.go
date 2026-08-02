package store

import (
	"context"
	"fmt"

	"github.com/BeLazy167/argus/backend/internal/crypto"
	"github.com/jackc/pgx/v5"
)

// UpsertProviderKey creates or rotates a provider key. base_url/model use
// PATCH semantics on conflict: omitting them (nil) PRESERVES the stored values
// — a key-only rotation must never silently strip a custom endpoint or its
// declared model (that would flip the row back to platform defaults and
// fragment the embedding space). To clear them, delete and recreate the row.
func (s *Store) UpsertProviderKey(ctx context.Context, installationID int64, repoID *int64, provider, apiKey string, baseURL, model *string) (*ProviderKey, error) {
	// Empty apiKey = keyless row (embeddings custom endpoints only; the API
	// enforces that). Stored as '' so the conflict clause's NULLIF-COALESCE
	// PRESERVES an existing stored key on keyless config updates — updating
	// base_url/model must never destroy a stored credential.
	enc, hint := "", ""
	if apiKey != "" {
		var err error
		enc, err = crypto.Encrypt(apiKey)
		if err != nil {
			return nil, fmt.Errorf("encrypting api key: %w", err)
		}
		if len(apiKey) >= 4 {
			raw := apiKey[len(apiKey)-4:]
			hint, err = crypto.Encrypt(raw)
			if err != nil {
				return nil, fmt.Errorf("encrypting key hint: %w", err)
			}
		}
	}
	var pk ProviderKey
	// Use appropriate ON CONFLICT target: partial index for org-level (repo_id IS NULL),
	// composite unique for repo-level keys.
	var query string
	if repoID == nil {
		query = `
			INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, key_hint, base_url, model)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (installation_id, provider) WHERE repo_id IS NULL DO UPDATE SET
				api_key_enc = COALESCE(NULLIF(EXCLUDED.api_key_enc, ''), provider_keys.api_key_enc),
				key_hint = COALESCE(NULLIF(EXCLUDED.key_hint, ''), provider_keys.key_hint),
				base_url = COALESCE(EXCLUDED.base_url, provider_keys.base_url),
				model = COALESCE(EXCLUDED.model, provider_keys.model),
				updated_at = NOW()
			RETURNING id, installation_id, repo_id, provider, api_key_enc, key_hint, base_url, model, created_at, updated_at`
	} else {
		query = `
			INSERT INTO provider_keys (installation_id, repo_id, provider, api_key_enc, key_hint, base_url, model)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (installation_id, repo_id, provider) DO UPDATE SET
				api_key_enc = COALESCE(NULLIF(EXCLUDED.api_key_enc, ''), provider_keys.api_key_enc),
				key_hint = COALESCE(NULLIF(EXCLUDED.key_hint, ''), provider_keys.key_hint),
				base_url = COALESCE(EXCLUDED.base_url, provider_keys.base_url),
				model = COALESCE(EXCLUDED.model, provider_keys.model),
				updated_at = NOW()
			RETURNING id, installation_id, repo_id, provider, api_key_enc, key_hint, base_url, model, created_at, updated_at`
	}
	err := s.Pool.QueryRow(ctx, query, installationID, repoID, provider, enc, hint, baseURL, model).Scan(
		&pk.ID, &pk.InstallationID, &pk.RepoID, &pk.Provider, &pk.APIKeyEnc, &pk.KeyHint, &pk.BaseURL, &pk.Model, &pk.CreatedAt, &pk.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pk, nil
}

func (s *Store) ListProviderKeys(ctx context.Context, installationID int64) ([]ProviderKey, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, repo_id, provider, api_key_enc, key_hint, base_url, model, created_at, updated_at
		FROM provider_keys WHERE installation_id = $1 ORDER BY provider, repo_id NULLS FIRST
	`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[ProviderKey])
}

func (s *Store) DeleteProviderKey(ctx context.Context, id int64, installationID int64) error {
	ct, err := s.Pool.Exec(ctx, `DELETE FROM provider_keys WHERE id = $1 AND installation_id = $2`, id, installationID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("provider key %d not found", id)
	}
	return nil
}

// ResolveAPIKey resolves an API key for a provider: repo-level → org-level → env fallback.
// Returns decrypted apiKey, baseURL, and whether a DB key was found.
func (s *Store) ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (apiKey string, baseURL string, found bool, err error) {
	// Try repo-level first
	if repoID != nil {
		var pk ProviderKey
		err = s.Pool.QueryRow(ctx, `
			SELECT api_key_enc, base_url FROM provider_keys
			WHERE installation_id = $1 AND repo_id = $2 AND provider = $3
		`, installationID, *repoID, provider).Scan(&pk.APIKeyEnc, &pk.BaseURL)
		if err == nil {
			decrypted, dErr := crypto.Decrypt(pk.APIKeyEnc)
			if dErr != nil {
				return "", "", false, fmt.Errorf("decrypting key: %w", dErr)
			}
			bu := ""
			if pk.BaseURL != nil {
				bu = *pk.BaseURL
			}
			return decrypted, bu, true, nil
		}
		if err != pgx.ErrNoRows {
			return "", "", false, err
		}
	}

	// Try org-level (repo_id IS NULL)
	var pk ProviderKey
	err = s.Pool.QueryRow(ctx, `
		SELECT api_key_enc, base_url FROM provider_keys
		WHERE installation_id = $1 AND repo_id IS NULL AND provider = $2
	`, installationID, provider).Scan(&pk.APIKeyEnc, &pk.BaseURL)
	if err == nil {
		decrypted, dErr := crypto.Decrypt(pk.APIKeyEnc)
		if dErr != nil {
			return "", "", false, fmt.Errorf("decrypting key: %w", dErr)
		}
		bu := ""
		if pk.BaseURL != nil {
			bu = *pk.BaseURL
		}
		return decrypted, bu, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", "", false, err
	}

	return "", "", false, nil
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
