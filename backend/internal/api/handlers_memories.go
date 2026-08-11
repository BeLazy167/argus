package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

const (
	defaultMemoryListLimit = 50
	maxMemoryListLimit     = 100
)

type memoryListStore interface {
	GetRepoScoped(ctx context.Context, id int64, installationIDs []int64) (*store.Repo, error)
	ListMemories(ctx context.Context, filter store.MemoryListFilter) (store.MemoryListResult, error)
}

type memoryListParams struct {
	installationID int64
	repoID         *int64
	scope          string
	memoryType     string
	query          string
	limit          int
	offset         int
}

func parseMemoryListParams(r *http.Request) (memoryListParams, error) {
	q := r.URL.Query()
	rawInstallationID := q.Get("installation_id")
	if rawInstallationID == "" {
		return memoryListParams{}, fmt.Errorf("installation_id is required")
	}
	installationID, err := strconv.ParseInt(rawInstallationID, 10, 64)
	if err != nil || installationID <= 0 {
		return memoryListParams{}, fmt.Errorf("invalid installation_id")
	}

	params := memoryListParams{installationID: installationID, scope: q.Get("scope"), limit: defaultMemoryListLimit}
	if params.scope == "" {
		params.scope = "all"
	}
	if params.scope != "all" && params.scope != "shared" && params.scope != "repo" {
		return memoryListParams{}, fmt.Errorf("scope must be all, shared, or repo")
	}
	if rawRepoID := q.Get("repo_id"); rawRepoID != "" {
		repoID, parseErr := strconv.ParseInt(rawRepoID, 10, 64)
		if parseErr != nil || repoID <= 0 {
			return memoryListParams{}, fmt.Errorf("invalid repo_id")
		}
		params.repoID = &repoID
	}
	if params.scope == "repo" && params.repoID == nil {
		return memoryListParams{}, fmt.Errorf("repo_id is required for repo scope")
	}
	if params.scope == "shared" && params.repoID != nil {
		return memoryListParams{}, fmt.Errorf("repo_id cannot be used with shared scope")
	}

	if rawLimit := q.Get("limit"); rawLimit != "" {
		limit, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || limit < 1 || limit > maxMemoryListLimit {
			return memoryListParams{}, fmt.Errorf("limit must be between 1 and 100")
		}
		params.limit = limit
	}
	if rawOffset := q.Get("offset"); rawOffset != "" {
		offset, parseErr := strconv.Atoi(rawOffset)
		if parseErr != nil || offset < 0 {
			return memoryListParams{}, fmt.Errorf("offset must be zero or greater")
		}
		params.offset = offset
	}
	params.memoryType = strings.TrimSpace(q.Get("type"))
	params.query = strings.TrimSpace(q.Get("q"))
	return params, nil
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	params, err := parseMemoryListParams(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ids := getInstallationIDs(r.Context())
	if !containsID(ids, params.installationID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "access denied"})
		return
	}

	filter := store.MemoryListFilter{InstallationID: params.installationID, Type: params.memoryType, Query: params.query, Limit: params.limit, Offset: params.offset}
	if params.scope == "shared" {
		filter.ContainerTags = []string{memory.SharedTag}
	} else if params.repoID != nil {
		repo, repoErr := s.memoryLister.GetRepoScoped(r.Context(), *params.repoID, []int64{params.installationID})
		if repoErr != nil || repo.InstallationID != params.installationID {
			if repoErr != nil && !errors.Is(repoErr, pgx.ErrNoRows) {
				s.logger.Error("resolve memory repo", "error", repoErr)
			}
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "repo not found"})
			return
		}
		parts := strings.SplitN(repo.FullName, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			s.logger.Error("repo has invalid full name", "repo_id", repo.ID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		repoTag := memory.RepoTagNew(parts[1])
		if params.scope == "repo" {
			filter.ContainerTags = []string{repoTag}
		} else {
			filter.ContainerTags = []string{memory.SharedTag, repoTag}
		}
	}

	result, err := s.memoryLister.ListMemories(r.Context(), filter)
	if err != nil {
		s.logger.Error("list memories", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Memories []store.MemoryAPI `json:"memories"`
		Total    int               `json:"total"`
		Limit    int               `json:"limit"`
		Offset   int               `json:"offset"`
	}{result.Memories, result.Total, params.limit, params.offset})
}
