package api

import (
	"io"
	"log/slog"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestListReposIsScopedToCaller(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, repoA := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	tools := &mcpTools{srv: s, scope: tenantScope{userID: "user_a", installationIDs: []int64{installA}, grantedScopes: []string{scopeRead}}}
	_, out, err := tools.listRepos(ctx, nil, listReposInput{})
	if err != nil {
		t.Fatalf("listRepos: %v", err)
	}
	var sawA, sawB bool
	for _, r := range out.Repos {
		if r.RepoID == repoA {
			sawA = true
			if r.InstallationID != installA {
				t.Fatalf("repo %d reported installation %d, want %d", r.RepoID, r.InstallationID, installA)
			}
		}
		sawB = sawB || r.RepoID == repoB
	}
	if !sawA {
		t.Fatal("caller's own repo missing from list_repos")
	}
	if sawB {
		t.Fatalf("list_repos leaked repo %d from installation %d into installation %d's scope", repoB, installB, installA)
	}

	// A token without argus:read cannot even list.
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installA}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.listRepos(ctx, nil, listReposInput{}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: err = %v", err)
	}
}
