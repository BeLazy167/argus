package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestRepointPatternsTreatsEveryNonLiveMemoryAsOrphaned(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := &store.Store{Pool: pool}

	var install int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'archive-lifecycle-test')
		RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, install)
	})

	var replacementID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content)
		VALUES ($1, 'api', 'replacement', 'pattern', 'replacement') RETURNING id`, install).Scan(&replacementID); err != nil {
		t.Fatal(err)
	}
	states := []struct {
		name     string
		customID string
	}{
		{"deleted", "archive-deleted"},
		{"invalidated", "archive-invalidated"},
		{"superseded", "archive-superseded"},
	}
	for _, tc := range states {
		var memoryID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content)
			VALUES ($1, 'api', $2, 'pattern', $2) RETURNING id`, install, tc.customID).Scan(&memoryID); err != nil {
			t.Fatal(err)
		}
		switch tc.name {
		case "deleted":
			_, err = pool.Exec(ctx, `UPDATE memories SET deleted_at = now() WHERE id = $1`, memoryID)
		case "invalidated":
			_, err = pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE id = $1`, memoryID)
		case "superseded":
			_, err = pool.Exec(ctx, `UPDATE memories SET superseded_by = $2 WHERE id = $1`, memoryID, replacementID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO patterns (installation_id, content, memory_doc_id)
			VALUES ($1, $2, $2)`, install, tc.customID); err != nil {
			t.Fatal(err)
		}
	}

	mapped, orphaned, err := repointPatterns(ctx, st, install)
	if err != nil {
		t.Fatalf("repointPatterns: %v", err)
	}
	if mapped != 0 || orphaned != len(states) {
		t.Fatalf("mapped/orphaned = %d/%d, want 0/%d", mapped, orphaned, len(states))
	}
	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM patterns WHERE installation_id = $1 AND memory_doc_id IS NOT NULL`, install).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d non-live archive pointers remain", remaining)
	}
}

func TestArchiveBackfillUsesCurrentEmbeddingSpaceAfterCachedRotation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := &store.Store{Pool: pool}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, memory.StorageDimensions)
			vec[0] = 1
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()

	var install int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'archive-current-space-test')
		RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_export_archive WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM provider_keys WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, install)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO provider_keys (installation_id, provider, api_key_enc, base_url, model)
		VALUES ($1, 'embeddings', '', $2, 'space-A')`, install, server.URL); err != nil {
		t.Fatal(err)
	}
	embeds := memory.NewEmbedderRegistry(st, memory.PlatformEmbeddings{
		Dimensions: memory.StorageDimensions,
	}, slog.New(slog.DiscardHandler))
	capturedA, _ := embeds.GetEmbedder(ctx, install)
	if capturedA == nil || capturedA.Model() != "space-A" {
		t.Fatalf("cached initial embedder = %v, want space-A", capturedA)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE provider_keys SET model = 'space-B', updated_at = now()
		WHERE installation_id = $1 AND provider = 'embeddings'`, install); err != nil {
		t.Fatal(err)
	}

	payload, err := json.Marshal(map[string]any{
		"content":  "late archive write belongs to B",
		"metadata": map[string]string{"type": string(memory.TypePattern)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memory_export_archive
			(installation_id, container_tag, doc_id, custom_id, payload)
		VALUES ($1, 'api', 'archive-doc-B', 'archive-custom-B', $2)`, install, payload); err != nil {
		t.Fatal(err)
	}

	registry := memory.NewRegistry(slog.New(slog.DiscardHandler)).WithPostgresBackend(pool, embeds)
	imported, skipped, err := backfillInstallation(ctx, slog.New(slog.DiscardHandler), st, embeds, registry, install, runConfig{})
	if err != nil {
		t.Fatalf("backfillInstallation: %v", err)
	}
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported/skipped = %d/%d, want 1/0", imported, skipped)
	}

	wantSpace := memory.NewEmbedder("", server.URL, "space-B", memory.StorageDimensions).SpaceID()
	var gotSpace string
	if err := pool.QueryRow(ctx, `
		SELECT embedding_space FROM live_memories
		WHERE installation_id = $1 AND custom_id = 'archive-custom-B'`, install).Scan(&gotSpace); err != nil {
		t.Fatal(err)
	}
	if gotSpace != wantSpace {
		t.Fatalf("archive row space = %q, want current B %q", gotSpace, wantSpace)
	}

	// The --reembed path must not use a cached clean-looking B plan as the
	// authority after PostgreSQL has moved to C. It must still enter the
	// registry repair, take the tenant lock, and resolve C durably.
	embedsB := memory.NewEmbedderRegistry(st, memory.PlatformEmbeddings{
		Dimensions: memory.StorageDimensions,
	}, slog.New(slog.DiscardHandler))
	cachedB, _ := embedsB.GetEmbedder(ctx, install)
	if cachedB == nil || cachedB.Model() != "space-B" {
		t.Fatalf("cached reembed planner = %v, want space-B", cachedB)
	}
	registryB := memory.NewRegistry(slog.New(slog.DiscardHandler)).WithPostgresBackend(pool, embedsB)
	if _, err := pool.Exec(ctx, `
		UPDATE provider_keys SET model = 'space-C', updated_at = now()
		WHERE installation_id = $1 AND provider = 'embeddings'`, install); err != nil {
		t.Fatal(err)
	}
	if err := runReembed(ctx, slog.New(slog.DiscardHandler), st, embedsB, registryB, runConfig{installation: install, reembed: true}); err != nil {
		t.Fatalf("runReembed after B-to-C rotation: %v", err)
	}

	wantSpace = memory.NewEmbedder("", server.URL, "space-C", memory.StorageDimensions).SpaceID()
	if err := pool.QueryRow(ctx, `
		SELECT embedding_space FROM live_memories
		WHERE installation_id = $1 AND custom_id = 'archive-custom-B'`, install).Scan(&gotSpace); err != nil {
		t.Fatal(err)
	}
	if gotSpace != wantSpace {
		t.Fatalf("--reembed row space = %q, want durable current C %q", gotSpace, wantSpace)
	}
}
