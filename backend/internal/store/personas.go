package store

import (
	"context"
	"fmt"
)

// Persona is a review style: a prompt overlay appended to the review system
// prompt, plus a shorter hint for the specialist path.
//
// It is not a prompt override. prompt_templates REPLACES a stage's system
// prompt and is unique per (repo, stage); a persona is appended, carries two
// strings, and is chosen by name from a set.
type Persona struct {
	ID             int64  `json:"id"`
	InstallationID *int64 `json:"installation_id,omitempty"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	PromptOverlay  string `json:"prompt_overlay"`
	SpecialistHint string `json:"specialist_hint"`
	IsBuiltin      bool   `json:"is_builtin"`
}

// ListPersonas returns the built-ins plus this installation's own, built-ins
// first and each group ordered by name.
//
// An installation's persona SHADOWS a built-in with the same slug, which is how
// a team retunes "security_auditor" without losing the name everyone knows.
// Resolution below picks the installation row when both exist.
func (s *Store) ListPersonas(ctx context.Context, installationID int64) ([]Persona, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin
		FROM personas
		WHERE installation_id IS NULL OR installation_id = $1
		ORDER BY is_builtin DESC, name ASC`, installationID)
	if err != nil {
		return nil, fmt.Errorf("listing personas: %w", err)
	}
	defer rows.Close()

	var out []Persona
	for rows.Next() {
		var p Persona
		if err := rows.Scan(&p.ID, &p.InstallationID, &p.Slug, &p.Name,
			&p.PromptOverlay, &p.SpecialistHint, &p.IsBuiltin); err != nil {
			return nil, fmt.Errorf("scanning persona: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPersona resolves one slug for an installation.
//
// Returns (nil, nil) when the slug is unknown — a miss, not a failure. The
// caller falls back to the compiled-in overlay, so a review never fails because
// a persona was renamed or deleted mid-flight.
//
// ORDER BY installation_id NULLS LAST makes an installation's own row win over
// the built-in of the same slug.
func (s *Store) GetPersona(ctx context.Context, installationID int64, slug string) (*Persona, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin
		FROM personas
		WHERE slug = $2 AND (installation_id IS NULL OR installation_id = $1)
		ORDER BY installation_id NULLS LAST
		LIMIT 1`, installationID, slug)
	if err != nil {
		return nil, fmt.Errorf("getting persona %q: %w", slug, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, rows.Err()
	}
	var p Persona
	if err := rows.Scan(&p.ID, &p.InstallationID, &p.Slug, &p.Name,
		&p.PromptOverlay, &p.SpecialistHint, &p.IsBuiltin); err != nil {
		return nil, fmt.Errorf("scanning persona %q: %w", slug, err)
	}
	return &p, nil
}

// UpsertPersona creates or updates one of an installation's own personas.
//
// Built-ins are not writable through this path: installationID is required, so
// a write always lands on an installation row. Shadowing a built-in slug is
// allowed and is the supported way to retune one.
func (s *Store) UpsertPersona(ctx context.Context, installationID int64, p Persona) (*Persona, error) {
	if installationID == 0 {
		return nil, fmt.Errorf("upsert persona: installation is required")
	}
	var out Persona
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO personas (installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin)
		VALUES ($1, $2, $3, $4, $5, FALSE)
		ON CONFLICT (installation_id, slug) WHERE installation_id IS NOT NULL
		DO UPDATE SET name = EXCLUDED.name, prompt_overlay = EXCLUDED.prompt_overlay,
		              specialist_hint = EXCLUDED.specialist_hint, updated_at = NOW()
		RETURNING id, installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin`,
		installationID, p.Slug, p.Name, p.PromptOverlay, p.SpecialistHint).
		Scan(&out.ID, &out.InstallationID, &out.Slug, &out.Name,
			&out.PromptOverlay, &out.SpecialistHint, &out.IsBuiltin)
	if err != nil {
		return nil, fmt.Errorf("upserting persona %q: %w", p.Slug, err)
	}
	return &out, nil
}

// DeletePersona removes one of an installation's own personas.
//
// Scoped to installation_id, so a built-in cannot be deleted by any caller and
// one installation cannot delete another's. Deleting a shadow reveals the
// built-in of the same slug again rather than breaking repos that selected it.
func (s *Store) DeletePersona(ctx context.Context, installationID int64, slug string) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM personas WHERE installation_id = $1 AND slug = $2`, installationID, slug)
	if err != nil {
		return fmt.Errorf("deleting persona %q: %w", slug, err)
	}
	return nil
}
