package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// blastTenant is one seeded installation with two repositories in it. Two
// repositories are the point: the whole reason the traversal boundary moved from
// repo_id to installation_id is that an API repo and the web repo that calls it
// live in one installation and legitimately depend on each other.
type blastTenant struct {
	installID int64
	repoA     int64
	repoB     int64
}

// seedBlastTenant creates installation + two repos and returns their ids.
func seedBlastTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org string) blastTenant {
	t.Helper()

	var tenant blastTenant
	err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, $1)
		RETURNING id`, org).Scan(&tenant.installID)
	if err != nil {
		t.Fatalf("seed installation %s: %v", org, err)
	}

	newRepo := func(name string) int64 {
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO repos (installation_id, github_id, full_name)
			VALUES ($1, (random() * 1000000000)::bigint, $2)
			RETURNING id`, tenant.installID, org+"/"+name).Scan(&id)
		if err != nil {
			t.Fatalf("seed repo %s/%s: %v", org, name, err)
		}
		return id
	}
	tenant.repoA = newRepo("api")
	tenant.repoB = newRepo("web")

	t.Cleanup(func() {
		bg := context.Background()
		// code_nodes/code_edges cascade off repos; repos and installations do not.
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE installation_id = $1`, tenant.installID)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, tenant.installID)
	})
	return tenant
}

// seedNode writes one code_nodes row through the production upsert, so the test
// also proves installation_id is populated by the writer rather than by the
// test's own INSERT. A test that stamped the column itself would keep passing
// after the writer stopped setting it.
func seedNode(t *testing.T, ctx context.Context, st *Store, repoID int64, name, filePath string) int64 {
	t.Helper()
	id, err := st.UpsertCodeNode(ctx, repoID, "function", name, filePath, 1, 10, "go", 0)
	if err != nil {
		t.Fatalf("upsert node %s: %v", name, err)
	}
	return id
}

// seedEdge writes a dependency edge: dependent CALLS target, so a blast radius
// from target's file must reach dependent.
func seedEdge(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID, dependentID, targetID int64) {
	t.Helper()
	// Written directly rather than via UpsertCodeEdge because a cross-tenant edge
	// is not something any production writer would produce — it is precisely the
	// corrupt state the traversal boundary has to survive.
	_, err := pool.Exec(ctx, `
		INSERT INTO code_edges (repo_id, source_id, target_id, kind, updated_at)
		VALUES ($1, $2, $3, 'calls', NOW())
		ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING`,
		repoID, dependentID, targetID)
	if err != nil {
		t.Fatalf("seed edge %d->%d: %v", dependentID, targetID, err)
	}
}

// TestBlastRadiusScopesByInstallation pins the tenant boundary of blast radius.
//
// Two things must hold at once, and they pull in opposite directions:
//   - A repository inside the installation that depends on the changed file MUST
//     appear. Scoping the walk to repo_id (the pre-#221 behaviour) hides it, and
//     hiding it is what made cross-repo blast radius impossible.
//   - A node belonging to ANOTHER installation must NEVER appear, even when a
//     code_edges row physically connects the two graphs. That row is the leak:
//     code_edges carries no tenant predicate of its own, so the node-side filter
//     is the only thing between one customer's code structure and another's.
//
// Widening the boundary in either direction fails this test: dropping the
// installation predicate returns the foreign node, and narrowing it back to
// repo_id drops the sibling repo's dependent.
func TestBlastRadiusScopesByInstallation(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}

	mine := seedBlastTenant(t, ctx, pool, "acme")
	theirs := seedBlastTenant(t, ctx, pool, "initech")

	const seedPath = "api/handlers/jobs.go"

	// acme/api: the changed file.
	target := seedNode(t, ctx, st, mine.repoA, "ListJobs", seedPath)
	// acme/api: a same-repo dependent, reachable before and after this change.
	sameRepo := seedNode(t, ctx, st, mine.repoA, "Router", "api/router.go")
	seedEdge(t, ctx, pool, mine.repoA, sameRepo, target)
	// acme/web: a SIBLING-REPO dependent. Only reachable once the walk is bounded
	// by installation instead of repo.
	crossRepo := seedNode(t, ctx, st, mine.repoB, "JobsPage", "web/pages/jobs.tsx")
	seedEdge(t, ctx, pool, mine.repoB, crossRepo, target)

	// initech: another customer entirely, wired to acme's node by a corrupt edge.
	foreign := seedNode(t, ctx, st, theirs.repoA, "SecretPipeline", "initech/secret.go")
	seedEdge(t, ctx, pool, theirs.repoA, foreign, target)

	// A file path that collides across tenants. Seeding must not resolve it in
	// the wrong installation.
	foreignCollision := seedNode(t, ctx, st, theirs.repoA, "ListJobs", seedPath)
	_ = foreignCollision

	wantNames := []string{"JobsPage", "ListJobs", "Router"}

	// Every read path is asserted, because a boundary enforced on one and not the
	// other means the answer changes with which extensions the database happens
	// to have installed.
	paths := map[string]func(context.Context) ([]CodeNode, error){
		"cte": func(c context.Context) ([]CodeNode, error) {
			return st.blastRadiusCTE(c, mine.installID, mine.repoA, []string{seedPath}, 2)
		},
		"public": func(c context.Context) ([]CodeNode, error) {
			return st.GetBlastRadius(c, mine.installID, mine.repoA, []string{seedPath}, 2)
		},
	}
	if st.pgGraphAvailable(ctx) {
		paths["pggraph"] = func(c context.Context) ([]CodeNode, error) {
			return st.blastRadiusPGGraph(c, mine.installID, mine.repoA, []string{seedPath}, 2)
		}
	} else {
		t.Log("pgGraph extension absent: traversal path not exercised, only the recursive CTE")
	}

	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			nodes, err := run(ctx)
			if err != nil {
				t.Fatalf("blast radius: %v", err)
			}

			got := make([]string, 0, len(nodes))
			byName := make(map[string]CodeNode, len(nodes))
			for _, n := range nodes {
				got = append(got, n.Name)
				byName[n.Name] = n
			}
			slices.Sort(got)

			if slices.Contains(got, "SecretPipeline") {
				t.Fatalf("TENANT LEAK: blast radius returned another installation's node; got %v", got)
			}
			if _, ok := byName["JobsPage"]; !ok {
				t.Fatalf("cross-repo dependent inside the installation was not reached; got %v", got)
			}
			if !slices.Equal(got, wantNames) {
				t.Fatalf("wrong dependent set:\n got %v\nwant %v", got, wantNames)
			}
			// The foreign node is one hop from the seed, so a leak would surface at
			// depth 1 exactly like the legitimate dependents — depth cannot be used
			// to tell them apart, and the predicate is the only defence.
			if d := byName["JobsPage"].Depth; d != 1 {
				t.Errorf("cross-repo dependent at depth %d, want 1", d)
			}

			// Every consumer resolves a dependent's path against the PULL
			// REQUEST's repository at its head SHA. Without repo_id on the row
			// they cannot tell a sibling repository's `src/index.ts` from their
			// own, so they fetch the wrong file and present it as the dependent's
			// source. Returning the column is what makes that distinguishable.
			if got := byName["JobsPage"].RepoID; got != mine.repoB {
				t.Errorf("cross-repo dependent carries repo_id %d, want the sibling repo %d", got, mine.repoB)
			}
			if got := byName["Router"].RepoID; got != mine.repoA {
				t.Errorf("same-repo dependent carries repo_id %d, want the PR's repo %d", got, mine.repoA)
			}
		})
	}

	// A caller that pairs one tenant's installation with another tenant's repo
	// must get nothing, not a walk seeded in one tenant and bounded by another.
	t.Run("mismatched installation and repo fail closed", func(t *testing.T) {
		nodes, err := st.GetBlastRadius(ctx, theirs.installID, mine.repoA, []string{seedPath}, 2)
		if err != nil {
			t.Fatalf("blast radius: %v", err)
		}
		if len(nodes) != 0 {
			t.Fatalf("mismatched (installation, repo) pair returned %d nodes; want 0", len(nodes))
		}
	})
}

// TestBlastRadiusKeepsOwnRepoWithinRowBudget pins the row budget against the
// case that widening the walk introduced.
//
// The walk went from one repository to a whole installation; the 50-row cap did
// not move. Ordering the result on bare file_path therefore lets an
// alphabetically earlier sibling repository ("admin-ui/..." before "api/...")
// fill every slot and evict the dependents in the repository the pull request is
// actually in — dependents the repo-scoped query was guaranteed to return. The
// caller sees exactly 50 rows and cannot tell that from a complete answer, so
// nothing anywhere reports the loss.
func TestBlastRadiusKeepsOwnRepoWithinRowBudget(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}

	tenant := seedBlastTenant(t, ctx, pool, "budget")
	const seedPath = "api/handlers/jobs.go"
	target := seedNode(t, ctx, st, tenant.repoA, "ListJobs", seedPath)

	// One same-repo dependent, on a path that sorts AFTER every sibling path.
	sameRepo := seedNode(t, ctx, st, tenant.repoA, "Router", "api/router.go")
	seedEdge(t, ctx, pool, tenant.repoA, sameRepo, target)

	// Enough sibling-repo dependents to consume the whole budget on their own.
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("AdminWidget%02d", i)
		dep := seedNode(t, ctx, st, tenant.repoB, name, fmt.Sprintf("admin-ui/w%02d.tsx", i))
		seedEdge(t, ctx, pool, tenant.repoB, dep, target)
	}

	nodes, err := st.blastRadiusCTE(ctx, tenant.installID, tenant.repoA, []string{seedPath}, 2)
	if err != nil {
		t.Fatalf("blast radius: %v", err)
	}
	for _, n := range nodes {
		if n.Name == "Router" {
			return
		}
	}
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Name)
	}
	t.Fatalf("the PR's own repo dependent was evicted by sibling-repo nodes; got %d rows: %v", len(nodes), names)
}

// TestCodeNodeInstallationFilledForWriterThatOmitsIt covers the rolling deploy.
//
// The migration runs as the Fly release_command, so it completes BEFORE the
// rolling update starts and the previous binary keeps serving for a minute or
// two. That binary's UpsertCodeNode does not know the column exists. Without the
// fill trigger every node it writes in that window is rejected by NOT NULL, the
// indexer logs and skips the symbol, and its orphan sweep still runs — leaving a
// partially emptied graph for the files it touched.
func TestCodeNodeInstallationFilledForWriterThatOmitsIt(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	tenant := seedBlastTenant(t, ctx, pool, "pre-deploy-binary")

	var id, got int64
	err := pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, updated_at)
		VALUES ($1, 'function', 'OldBinary', 'svc/old.go', 1, 5, 'go', NOW())
		RETURNING id`, tenant.repoA).Scan(&id)
	if err != nil {
		t.Fatalf("write as the pre-deploy binary would: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT installation_id FROM code_nodes WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatalf("read installation_id: %v", err)
	}
	if got != tenant.installID {
		t.Fatalf("installation_id %d, want %d", got, tenant.installID)
	}
}

// TestUpsertCodeNodeStampsInstallationFromRepo proves the denormalised tenant
// column is derived from repos on every write, including the ON CONFLICT branch.
//
// If a writer ever left installation_id stale or unset, the row would either
// violate NOT NULL or, worse, carry the wrong tenant — and every read path
// filters on exactly that column, so a wrong value is a leak and a missing one
// makes the node invisible to its own owner's blast radius.
func TestUpsertCodeNodeStampsInstallationFromRepo(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	tenant := seedBlastTenant(t, ctx, pool, "stamp-test")

	readInstallation := func(id int64) int64 {
		var got int64
		if err := pool.QueryRow(ctx,
			`SELECT installation_id FROM code_nodes WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("read installation_id: %v", err)
		}
		return got
	}

	writers := []struct {
		name  string
		write func(repoID int64) (int64, error)
	}{
		{"UpsertCodeNode", func(repoID int64) (int64, error) {
			return st.UpsertCodeNode(ctx, repoID, "function", "Handle", "svc/handle.go", 1, 5, "go", 0)
		}},
		{"UpsertCodeNodeFullWithHash", func(repoID int64) (int64, error) {
			return st.UpsertCodeNodeFullWithHash(ctx, repoID, "function", "HandleHash", "svc/hash.go", 1, 5, "go", 0,
				"error", "ctx context.Context", "public", false, "", "package", "deadbeef")
		}},
	}

	for _, w := range writers {
		t.Run(w.name, func(t *testing.T) {
			id, err := w.write(tenant.repoA)
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
			if got := readInstallation(id); got != tenant.installID {
				t.Fatalf("INSERT stamped installation_id %d, want %d", got, tenant.installID)
			}

			// Second write takes the ON CONFLICT branch. It must re-derive the
			// column, not leave whatever was there.
			if _, err := pool.Exec(ctx,
				`UPDATE code_nodes SET installation_id = $1 WHERE id = $2`, tenant.installID+999999, id); err != nil {
				t.Fatalf("corrupt installation_id: %v", err)
			}
			again, err := w.write(tenant.repoA)
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if again != id {
				t.Fatalf("second write created a new row %d, expected upsert onto %d", again, id)
			}
			if got := readInstallation(id); got != tenant.installID {
				t.Fatalf("ON CONFLICT left installation_id %d, want %d", got, tenant.installID)
			}
		})
	}

	// Non-existent repo: the derived subquery yields NULL and NOT NULL rejects
	// the row. Silently writing a tenant-less node would hide it from every
	// traversal instead.
	t.Run("unknown repo is rejected, not written NULL", func(t *testing.T) {
		_, err := st.UpsertCodeNode(ctx, -1, "function", "Ghost", "svc/ghost.go", 1, 5, "go", 0)
		if err == nil {
			t.Fatal("upsert against an unknown repo succeeded; a NULL installation_id would make the node invisible to every blast radius")
		}
		if !strings.Contains(err.Error(), "installation_id") {
			t.Fatalf("unexpected failure mode: %v", err)
		}
	})
}
