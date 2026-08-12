package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// Bounds on what one persona may contribute to a prompt.
//
// These are a spend control, not cosmetics. The overlay is appended to the
// review system prompt on EVERY review a repo runs, and the hint is appended
// once per specialist on the deep-review path — four times per file. The budget
// limits in internal/admission measure the DIFF, so neither of these is visible
// to them: an unbounded overlay is an unbounded per-review cost that no existing
// gate catches.
//
// Generous enough for a real house style; small enough that a paste accident
// cannot quietly triple a review's prompt.
const (
	maxPersonaSlug    = 64
	maxPersonaName    = 80
	maxPersonaOverlay = 8000
	maxPersonaHint    = 600
)

// personaSlugPattern is the identity format. Lowercase only, because a slug is
// compared exactly: "HouseStyle" and "housestyle" would be two rows that look
// like one in the dashboard, and a repo pointing at the wrong one would resolve
// to no persona at all and silently review with none.
var personaSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// personaInput is the request body for creating or updating a persona.
type personaInput struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	PromptOverlay  string `json:"prompt_overlay"`
	SpecialistHint string `json:"specialist_hint"`
}

// validatePersonaInput rejects a persona that would be unusable or unbounded.
//
// Pure, so the rules are testable without a request or a database — which is
// what lets the bounds above be asserted rather than assumed.
func validatePersonaInput(in personaInput) error {
	if strings.TrimSpace(in.Slug) == "" {
		return fmt.Errorf("slug is required")
	}
	if len(in.Slug) > maxPersonaSlug {
		return fmt.Errorf("slug must be %d characters or fewer", maxPersonaSlug)
	}
	// Anchored pattern, so this also refuses "../admin" and anything else that
	// would misbehave as a URL path segment on the delete route.
	if !personaSlugPattern.MatchString(in.Slug) {
		return fmt.Errorf("slug must be lowercase letters, digits, hyphen or underscore")
	}
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(in.Name) > maxPersonaName {
		return fmt.Errorf("name must be %d characters or fewer", maxPersonaName)
	}
	// An empty overlay would store a persona that displays a name and changes
	// nothing — indistinguishable, to the person who wrote it, from one that is
	// not being applied.
	if strings.TrimSpace(in.PromptOverlay) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(in.PromptOverlay) > maxPersonaOverlay {
		return fmt.Errorf("prompt must be %d characters or fewer", maxPersonaOverlay)
	}
	if len(in.SpecialistHint) > maxPersonaHint {
		return fmt.Errorf("specialist hint must be %d characters or fewer", maxPersonaHint)
	}
	return nil
}

// listPersonas returns the built-ins plus this installation's own.
func (s *Server) listPersonas(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.listPersonas")
	defer op.Finish(w)
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation"})
		return
	}
	personas, err := s.store.ListPersonas(r.Context(), ids[0])
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list personas", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	if personas == nil {
		// An empty table must serialize as [] rather than null, so the client
		// can map over it without a nil check.
		personas = []store.Persona{}
	}
	writeJSON(w, http.StatusOK, personas)
}

// upsertPersona creates or replaces one of the installation's own personas.
//
// Always writes an installation-scoped row. A built-in is never edited in place:
// writing a built-in's slug creates a SHADOW, so the shipped text stays intact
// and deleting the shadow restores it.
func (s *Server) upsertPersona(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.upsertPersona")
	defer op.Finish(w)
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation"})
		return
	}

	var in personaInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// The path segment is authoritative on update, so a body carrying a
	// different slug cannot rename a row out from under the URL it was PUT to.
	if slug := chi.URLParam(r, "slug"); slug != "" {
		in.Slug = slug
	}
	if err := validatePersonaInput(in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	saved, err := s.store.UpsertPersona(r.Context(), ids[0], store.Persona{
		Slug:           in.Slug,
		Name:           in.Name,
		PromptOverlay:  in.PromptOverlay,
		SpecialistHint: in.SpecialistHint,
	})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "upsert persona", "error", err, "slug", in.Slug)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save persona"})
		return
	}
	if err := s.store.LogActivity(r.Context(), &ids[0], "persona_saved", "", "persona:"+in.Slug, nil); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to log activity", "error", err, "action", "persona_saved")
	}
	writeJSON(w, http.StatusOK, saved)
}

// deletePersona removes one of the installation's own personas.
//
// Deleting a shadow reveals the built-in of the same slug again, so a repo that
// selected that slug keeps working rather than resolving to nothing.
func (s *Server) deletePersona(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.deletePersona")
	defer op.Finish(w)
	ids := getInstallationIDs(r.Context())
	if len(ids) == 0 {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation"})
		return
	}
	slug := chi.URLParam(r, "slug")
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "slug required"})
		return
	}
	// DeletePersona is scoped to installation_id, so this can only ever remove
	// the caller's own row — a built-in (installation_id IS NULL) and another
	// installation's row are both unreachable from here.
	if err := s.store.DeletePersona(r.Context(), ids[0], slug); err != nil {
		s.logger.ErrorContext(r.Context(), "delete persona", "error", err, "slug", slug)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete persona"})
		return
	}
	if err := s.store.LogActivity(r.Context(), &ids[0], "persona_deleted", "", "persona:"+slug, nil); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to log activity", "error", err, "action", "persona_deleted")
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
