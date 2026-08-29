package memory

import (
	"context"
	"strings"
	"testing"
)

func lifecycleDoc(id, content string) Doc {
	return Doc{
		ContainerTag: "api",
		CustomID:     id,
		Type:         string(TypePattern),
		Content:      content,
		Metadata:     map[string]string{"type": string(TypePattern)},
	}
}

func TestPGIndexerMemoryLifecycleOperations(t *testing.T) {
	tests := []struct {
		name        string
		apply       func(context.Context, *PGIndexer) error
		source      string
		replacement string
	}{
		{
			name:   "invalidate",
			source: "lifecycle-invalid",
			apply: func(ctx context.Context, idx *PGIndexer) error {
				return idx.InvalidateDocument(ctx, "lifecycle-invalid")
			},
		},
		{
			name:        "supersede",
			source:      "lifecycle-old",
			replacement: "lifecycle-new",
			apply: func(ctx context.Context, idx *PGIndexer) error {
				return idx.SupersedeDocument(ctx, "lifecycle-old", "lifecycle-new")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, install := pgTestPool(t)
			ctx := context.Background()
			idx := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())

			docs := []Doc{lifecycleDoc(tc.source, "obsolete lifecycle claim")}
			if tc.replacement != "" {
				docs = append(docs, lifecycleDoc(tc.replacement, "current replacement knowledge"))
			}
			if err := idx.ImportDocs(ctx, docs); err != nil {
				t.Fatalf("seed memories: %v", err)
			}
			if err := tc.apply(ctx, idx); err != nil {
				t.Fatalf("first transition: %v", err)
			}
			if err := tc.apply(ctx, idx); err != nil {
				t.Fatalf("idempotent transition: %v", err)
			}

			var live int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM live_memories WHERE installation_id = $1 AND custom_id = $2`,
				install, tc.source).Scan(&live); err != nil {
				t.Fatalf("read live view: %v", err)
			}
			if live != 0 {
				t.Fatalf("transitioned source remains live: %d row(s)", live)
			}

			matches, err := idx.Search(ctx, MemoryQuery{
				Query: "obsolete lifecycle claim", Repo: "api", Scope: ScopeRepo,
				Type: TypePattern, Limit: 10, Threshold: 0,
			})
			if err != nil {
				t.Fatalf("search after transition: %v", err)
			}
			for _, match := range matches {
				if strings.Contains(match.Content, "obsolete lifecycle claim") {
					t.Fatalf("search returned transitioned memory: %+v", match)
				}
			}

			// A deterministic writer may re-emit the same custom ID. That is
			// not an authorization to reverse a lifecycle decision.
			if err := idx.ImportDocs(ctx, []Doc{lifecycleDoc(tc.source, "obsolete lifecycle claim rewritten")}); err != nil {
				t.Fatalf("re-upsert source: %v", err)
			}
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM live_memories WHERE installation_id = $1 AND custom_id = $2`,
				install, tc.source).Scan(&live); err != nil {
				t.Fatalf("read live view after re-upsert: %v", err)
			}
			if live != 0 {
				t.Fatal("deterministic re-upsert resurrected a transitioned memory")
			}
		})
	}
}

func TestPGIndexerSupersessionRejectsInvalidTargets(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	if err := idx.ImportDocs(ctx, []Doc{lifecycleDoc("source", "source")}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, source, replacement string
	}{
		{"self supersession", "source", "source"},
		{"missing replacement", "source", "missing"},
		{"missing source", "missing", "source"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := idx.SupersedeDocument(ctx, tc.source, tc.replacement); err == nil {
				t.Fatal("SupersedeDocument succeeded, want an explicit error")
			}
		})
	}
}

func TestPGIndexerReembedOnlyLiveRows(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	blind := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	if err := blind.ImportDocs(ctx, []Doc{
		lifecycleDoc("dead-invalid", "invalidated"),
		lifecycleDoc("dead-old", "superseded"),
		lifecycleDoc("live-new", "replacement"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := blind.InvalidateDocument(ctx, "dead-invalid"); err != nil {
		t.Fatal(err)
	}
	if err := blind.SupersedeDocument(ctx, "dead-old", "live-new"); err != nil {
		t.Fatal(err)
	}

	repair := NewPGIndexer(pool, &stubEmbedder{dims: StorageDimensions}, install, StorageDimensions, discardLogger())
	repaired, err := repair.ReembedMissing(ctx, 10)
	if err != nil {
		t.Fatalf("ReembedMissing: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want the one live row", repaired)
	}

	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"dead-invalid", false}, {"dead-old", false}, {"live-new", true},
	} {
		if got := readRow(t, pool, install, tc.id).hasEmbedding; got != tc.want {
			t.Errorf("%s embedded = %v, want %v", tc.id, got, tc.want)
		}
	}
}
