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
	// ShadowsBuiltin marks an installation's row that overrides a built-in of
	// the same slug. It cannot be derived client-side: an installation row
	// always has is_builtin FALSE, so nothing on the row itself distinguishes a
	// brand-new persona from a retuned shipped one — and the difference matters,
	// because deleting a shadow RESTORES the built-in rather than removing a
	// name that repos already select.
	ShadowsBuiltin bool `json:"shadows_builtin"`
}

// ListPersonas returns the built-ins plus this installation's own, built-ins
// first and each group ordered by name.
//
// An installation's persona SHADOWS a built-in with the same slug, which is how
// a team retunes "security_auditor" without losing the name everyone knows.
// Resolution below picks the installation row when both exist.
func (s *Store) ListPersonas(ctx context.Context, installationID int64) ([]Persona, error) {
	// DISTINCT ON collapses a shadow and the built-in it overrides into the ONE
	// row that resolution would pick — matching GetPersona, which already
	// deduplicates the same way. Without it both rows come back: two cards with
	// the same name, one showing the shipped text and one the team's, with
	// nothing to say which one reviews actually use.
	//
	// shadows_builtin is computed here because only the server can see the other
	// row. An installation's row always has is_builtin FALSE, so nothing on the
	// row itself separates a new persona from a retuned built-in.
	rows, err := s.Pool.Query(ctx, `
		SELECT id, installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin, shadows_builtin
		FROM (
			SELECT DISTINCT ON (p.slug)
			       p.id, p.installation_id, p.slug, p.name, p.prompt_overlay,
			       p.specialist_hint, p.is_builtin,
			       (p.installation_id IS NOT NULL AND EXISTS (
			           SELECT 1 FROM personas b WHERE b.installation_id IS NULL AND b.slug = p.slug
			       )) AS shadows_builtin
			FROM personas p
			WHERE p.installation_id IS NULL OR p.installation_id = $1
			ORDER BY p.slug, p.installation_id NULLS LAST
		) resolved
		ORDER BY is_builtin DESC, shadows_builtin DESC, name ASC`, installationID)
	if err != nil {
		return nil, fmt.Errorf("listing personas: %w", err)
	}
	defer rows.Close()

	var out []Persona
	for rows.Next() {
		var p Persona
		if err := rows.Scan(&p.ID, &p.InstallationID, &p.Slug, &p.Name,
			&p.PromptOverlay, &p.SpecialistHint, &p.IsBuiltin, &p.ShadowsBuiltin); err != nil {
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

// SeedBuiltinPersonas reconciles the shipped personas into rows.
//
// Called at startup with the compiled-in set. Without it the table is empty on
// a fresh deployment, ListPersonas returns nothing, and the dashboard shows a
// product with zero personas while reviews quietly keep using the eight in the
// Go switch.
//
// RECONCILE, not insert. It upserts every shipped persona and then deletes any
// built-in row whose slug is no longer shipped. A one-way seed would leave a
// deleted persona selectable forever: its row would still list, a repo could
// still be set to it, and resolution would fall back to an empty overlay — a
// persona that displays a name and does nothing.
//
// Only rows with installation_id IS NULL are touched. An installation's own
// persona, including one that shadows a built-in slug, is never rewritten or
// removed by a deploy.
func (s *Store) SeedBuiltinPersonas(ctx context.Context, builtins []Persona) error {
	if len(builtins) == 0 {
		// Refuse rather than treat it as "nothing is shipped". An empty slice
		// here almost certainly means a caller wiring mistake, and the prune
		// below would read it as "delete every built-in" — silently removing
		// all eight personas from a running installation.
		return fmt.Errorf("seeding personas: refusing to reconcile against an empty built-in set")
	}

	slugs := make([]string, 0, len(builtins))
	for _, b := range builtins {
		slugs = append(slugs, b.Slug)
		_, err := s.Pool.Exec(ctx, `
			INSERT INTO personas (installation_id, slug, name, prompt_overlay, specialist_hint, is_builtin)
			VALUES (NULL, $1, $2, $3, $4, TRUE)
			ON CONFLICT (slug) WHERE installation_id IS NULL
			DO UPDATE SET name = EXCLUDED.name, prompt_overlay = EXCLUDED.prompt_overlay,
			              specialist_hint = EXCLUDED.specialist_hint, updated_at = NOW()`,
			b.Slug, b.Name, b.PromptOverlay, b.SpecialistHint)
		if err != nil {
			return fmt.Errorf("seeding built-in persona %q: %w", b.Slug, err)
		}
	}

	if _, err := s.Pool.Exec(ctx,
		`DELETE FROM personas WHERE installation_id IS NULL AND slug <> ALL($1)`, slugs); err != nil {
		return fmt.Errorf("pruning retired built-in personas: %w", err)
	}
	return nil
}
