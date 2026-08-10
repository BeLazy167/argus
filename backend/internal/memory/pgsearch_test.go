package memory

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// filterSQL and searchPredicates are pure functions, but every assertion about
// them used to live in the PG-backed suite, which t.Skips without
// TEST_DATABASE_URL — so the operator whitelist, LIKE escaping, the guarded
// ::numeric casts, and the placeholder arithmetic were unverifiable on a
// developer machine and exercised in CI only end-to-end through a live query.
//
// These tests prefer INVARIANTS over rendered SQL text wherever the invariant
// is expressible: a golden-string test over the fragments would fail on every
// harmless rewording and teach people to update the expectation instead of
// thinking. Where a test must look at the fragment, it matches the smallest
// thing that carries the meaning.

// ordinals extracts the placeholder set a fragment references. Set comparison
// (not substring matching) is the point: `strings.Contains(frag, "$8")` is
// satisfied by a fragment that only ever mentions $80.
func ordinals(frag string) []int {
	var out []int
	for _, m := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(frag, -1) {
		n, _ := strconv.Atoi(m[1]) // the regex matched digits
		out = append(out, n)
	}
	sort.Ints(out)
	// dedupe: a branch may legitimately reference one ordinal twice
	uniq := out[:0]
	for i, n := range out {
		if i == 0 || n != uniq[len(uniq)-1] {
			uniq = append(uniq, n)
		}
	}
	return uniq
}

func wantOrdinals(base, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = base + 1 + i
	}
	return out
}

// TestFilterSQLOperatorWhitelist: the numeric operator is the only element of
// a FilterCondition that reaches SQL outside the parameter protocol.
func TestFilterSQLOperatorWhitelist(t *testing.T) {
	hostile := []string{
		"; DROP TABLE memories--",
		">= 0 OR true",
		"=/**/",
		">=; SELECT pg_sleep(10)",
		"junk",
		"=>",
		"false", // the rejection fragment is literally "false" — a substring
		// matcher would fail here on CORRECT code, which is why this test
		// asserts a biconditional instead.
	}
	allowed := map[string]bool{">=": true, "<=": true, ">": true, "<": true, "=": true, "": true}
	for _, op := range append(hostile, ">=", "<=", ">", "<", "=", "") {
		t.Run("op="+op, func(t *testing.T) {
			frag, args := filterSQL(FilterCondition{
				Key: "score", Value: "0.5", FilterType: "numeric", NumericOperator: op,
			}, 0)
			accepted := frag != "false"
			if accepted != allowed[op] {
				t.Fatalf("operator %q: accepted=%v want %v (fragment %q)", op, accepted, allowed[op], frag)
			}
			if accepted && len(args) != 2 {
				t.Errorf("operator %q: %d args, want key+value", op, len(args))
			}
			// Identity, not just acceptance: collapsing every whitelisted
			// operator to "=" renders the hardcoded >= 0.30 confidence floor
			// as `= 0.30`, so briefing and suppression legs match only rows
			// whose stored confidence is exactly that string — they go
			// near-empty, findings read as novel, dismissed findings repost.
			if accepted {
				want := op
				if want == "" {
					want = "="
				}
				if !strings.Contains(frag, " "+want+" ") {
					t.Errorf("operator %q was accepted but rendered as something else: %q", op, frag)
				}
			}
			if !accepted && len(args) != 0 {
				t.Errorf("failed-closed fragment bound args: %v", args)
			}
		})
	}
}

// TestFilterSQLFragmentIsValueIndependent is the property that actually proves
// non-interpolation, and it holds for ALL inputs rather than a sampled few: for
// a fixed base and FilterType the rendered SQL must not vary with the key or
// the value. Anything inlining either would change the fragment.
func TestFilterSQLFragmentIsValueIndependent(t *testing.T) {
	shapes := map[string]func(k, v string) FilterCondition{
		"equality": func(k, v string) FilterCondition { return FilterCondition{Key: k, Value: v} },
		"contains": func(k, v string) FilterCondition {
			return FilterCondition{Key: k, Value: v, FilterType: "string_contains"}
		},
		"numeric": func(k, _ string) FilterCondition {
			return FilterCondition{Key: k, Value: "0.5", FilterType: "numeric", NumericOperator: ">="}
		},
	}
	pairs := [][2]string{
		{"category", "bug_risk"},
		{"'; DROP TABLE memories--", "' OR 1=1 --"},
		{"%_\\", "\\_%"},
	}
	for name, mk := range shapes {
		t.Run(name, func(t *testing.T) {
			want, _ := filterSQL(mk(pairs[0][0], pairs[0][1]), 3)
			for _, p := range pairs[1:] {
				if got, _ := filterSQL(mk(p[0], p[1]), 3); got != want {
					t.Errorf("fragment varies with input (%q vs %q) — a key or value is being inlined", got, want)
				}
			}
		})
	}
}

// TestFilterSQLOrdinalsMatchArgs pins the placeholder arithmetic for EVERY
// branch at a non-zero base. Set equality closes both directions: a branch
// that emits more placeholders than it binds (a pgx bind mismatch, or a silent
// read of a neighbouring caller's argument) and one that binds more than it
// emits. Mutating `base+2` to `base+1` in the numeric branch — which renders
// the confidence floor as `... >= $key::numeric` and raises 22P02 in
// production — previously left the entire suite green.
func TestFilterSQLOrdinalsMatchArgs(t *testing.T) {
	const base = 7
	cases := map[string]FilterCondition{
		"type_column":    {Key: "type", Value: string(TypePattern)},
		"metadata_equal": {Key: "category", Value: "bug_risk"},
		"contains":       {Key: "file_path", Value: "a.go", FilterType: "string_contains"},
		"numeric":        {Key: "confidence", Value: "0.5", FilterType: "numeric", NumericOperator: ">="},
		"negated_equal":  {Key: "category", Value: "bug_risk", Negate: true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			frag, args := filterSQL(c, base)
			// Roles, not just cardinality: set equality is blind to a
			// key/value transposition. Swapping them renders
			// `metadata->>'a.go' ILIKE '%file_path%'` — a read of a
			// nonexistent metadata key that yields NULL, and under Negate
			// becomes NOT COALESCE(NULL,false) = TRUE, matching every row.
			//
			// This must assert the ARGS, not the fragment. Every branch binds
			// the key at $base+1 by construction, so a fragment-only check is
			// invariant under `args = []any{f.Value, f.Key}` and can never
			// fail for the defect it names.
			if len(args) == 2 && strings.Contains(frag, "metadata->>") {
				keyOrd := fmt.Sprintf("metadata->>$%d", base+1)
				if !strings.Contains(frag, keyOrd) {
					t.Errorf("fragment %q does not bind the KEY at %s; key and value look transposed", frag, keyOrd)
				}
				if args[0] != c.Key {
					t.Errorf("args[0] = %v, want the KEY %q — key and value are transposed", args[0], c.Key)
				}
				// string_contains escapes the value before binding it, so the
				// arg is not required to equal c.Value verbatim — only to not
				// be the key.
				if args[1] == c.Key {
					t.Errorf("args[1] = %v, which is the KEY — key and value are transposed", args[1])
				}
			}
			got, want := ordinals(frag), wantOrdinals(base, len(args))
			if len(got) != len(want) {
				t.Fatalf("fragment %q references %v but binds %d args (want ordinals %v)", frag, got, len(args), want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("fragment %q references %v, want exactly %v", frag, got, want)
				}
			}
		})
	}
}

// TestFilterSQLNumericValueGuard covers the ARGUMENT side of the ::numeric
// cast: an unparseable filter value would raise 22P02 and fail the whole leg —
// and via the fan-out's single-error policy, the whole read.
func TestFilterSQLNumericValueGuard(t *testing.T) {
	// "1.2.3" and friends are the load-bearing rejects: they are what a
	// one-character loosening of the pattern (`?` -> `*`) admits, and what the
	// realistic "simplification" to ^[0-9.-]+$ admits. Both compile, both pass
	// a reject list built only from obviously-hostile strings, and both let a
	// legacy _shared row reach ::numeric and 22P02 the whole leg.
	for _, v := range []string{"0 OR true", "high", "", "1e5", "0x10", "NaN", "--1", "1.2.3", "1.", ".5", "1-2"} {
		frag, args := filterSQL(FilterCondition{
			Key: "confidence", Value: v, FilterType: "numeric", NumericOperator: ">=",
		}, 0)
		if frag != "false" || len(args) != 0 {
			t.Errorf("non-numeric value %q rendered %q with args %v; must match nothing", v, frag, args)
		}
	}
	for _, v := range []string{"0", "0.5", "-1", "-0.30", "1.00"} {
		if frag, _ := filterSQL(FilterCondition{
			Key: "confidence", Value: v, FilterType: "numeric", NumericOperator: ">=",
		}, 0); frag == "false" {
			t.Errorf("numeric literal %q was rejected", v)
		}
	}
}

// TestFilterSQLNumericGuardsStoredValue covers the OTHER half of the cast: the
// stored metadata value must be regex-guarded in SQL so one malformed legacy
// row (confidence "high" from a backfill) fails to MATCH rather than erroring
// the whole leg with 22P02. Deleting that guard previously left every DB-free
// test green — it was caught only by the PG-backed suite, i.e. never on a
// developer machine. Asserted as an invariant, not a golden string: the key's
// ordinal must appear inside a CASE guard before it is cast.
func TestFilterSQLNumericGuardsStoredValue(t *testing.T) {
	// A stored-value numeric key. NOT confidence: that one is computed from
	// age now (see TestFilterSQLConfidenceIsComputed) and never reads its
	// stored value, so it cannot exercise the guard this test protects.
	frag, args := filterSQL(FilterCondition{
		Key: "score", Value: "0.5", FilterType: "numeric", NumericOperator: ">=",
	}, 0)
	if len(args) != 2 {
		t.Fatalf("numeric filter bound %d args, want key+value", len(args))
	}
	if !strings.Contains(frag, "CASE WHEN") {
		t.Errorf("stored value is cast without a guard: %q", frag)
	}
	guard := frag[:strings.Index(frag, "THEN")+4]
	if !strings.Contains(guard, "~") || !strings.Contains(guard, "$1") {
		t.Errorf("guard does not regex-check the key's own ordinal before casting: %q", guard)
	}
	// The guard and the cast must read the SAME column. Checking only the text
	// before THEN leaves the cast side free: `CASE WHEN metadata->>$1 ~ ...
	// THEN (metadata->>$2)::numeric END >= $2::numeric` guards the key and
	// casts the VALUE, so the hardcoded 0.30 confidence floor renders as
	// `(metadata->>'0.30')::numeric >= 0.30` — NULL, and every confidence-
	// floored read (briefing legs, dismissal suppression) silently returns
	// nothing. The key's ordinal appears exactly twice: guard and cast.
	if n := strings.Count(frag, "metadata->>$1"); n != 2 {
		t.Errorf("key ordinal $1 read %d times, want 2 (guard and cast must read the same column): %q", n, frag)
	}
	// Shape is not enough — the guard's CONTENT decides what reaches ::numeric.
	// Loosening the regex to '.*', or "simplifying" it to '^[0-9.-]+$' (which
	// accepts "1.2.3"), keeps every assertion above true while restoring the
	// exact 22P02 whole-leg failure the guard exists to prevent. Asserting the
	// twin invariant instead of a golden string: the SQL guard must be the same
	// pattern the Go-side check uses, so the two cannot drift apart.
	if want := "~ '" + numericLiteral.String() + "'"; !strings.Contains(frag, want) {
		t.Errorf("SQL guard is not the Go-side pattern %s — the twins have drifted: %q", want, frag)
	}
	// Polarity. `!~ '<pattern>'` satisfies every assertion above (it contains
	// "~", contains the pattern, reads the key twice) while inverting the
	// guard: well-formed values yield NULL and drop out, and malformed ones
	// reach ::numeric and raise the 22P02 the guard exists to prevent.
	if !strings.Contains(frag, "$1 ~ '") {
		t.Errorf("guard polarity is inverted (matches NON-numeric values): %q", frag)
	}
	// Operand order. The floor is `stored >= bound`; flipping the operands
	// renders `0.30 >= confidence`, silently turning the confidence FLOOR into
	// a CEILING — briefing legs and dismissal suppression would read the
	// lowest-confidence memories instead of the highest. The ordinal set, the
	// operator substring, and the guard all survive that swap.
	if !strings.HasPrefix(frag, "CASE WHEN") {
		t.Errorf("stored value is not the left operand — the floor is inverted into a ceiling: %q", frag)
	}
}

// TestNumericLiteralPatternIsSQLSafe: the pattern is spliced into a
// single-quoted SQL literal by filterSQL. Nothing escapes it — safety rests on
// the pattern's content. A later widening to something like `^-?[0-9]+([.,']
// [0-9]+)?$` compiles in Go, passes vet, and passes every assertion here
// (the twin check derives its expectation from the same const), failing only
// at query time with a 42601 unterminated string on every confidence-floored
// read.
func TestNumericLiteralPatternIsSQLSafe(t *testing.T) {
	if strings.Contains(numericLiteralPattern, "'") {
		t.Errorf("pattern %q contains a single quote — it would terminate the SQL literal early", numericLiteralPattern)
	}
}

// TestFilterSQLStringContainsEscapesWildcards: the value must be a literal
// substring. Unescaped, "%" matches every row. (Backslash is Postgres's
// default LIKE escape with no ESCAPE clause, and the value arrives as a bound
// parameter, so standard_conforming_strings — which governs string literals —
// does not apply.)
func TestFilterSQLStringContainsEscapesWildcards(t *testing.T) {
	cases := []struct{ in, want string }{
		{`%`, `\%`},
		{`_`, `\_`},
		{`\`, `\\`},
		{`100%_done`, `100\%\_done`},
	}
	for _, c := range cases {
		frag, args := filterSQL(FilterCondition{
			Key: "file_path", Value: c.in, FilterType: "string_contains",
		}, 0)
		if len(args) != 2 {
			t.Fatalf("string_contains %q: %d args, want key+value", c.in, len(args))
		}
		// Pin the operator too: backslash escaping is meaningful to LIKE/ILIKE
		// and meaningless to `~`. Swapping in a regex match would keep every
		// assertion below green while regex metacharacters in the value go
		// live against the whole container.
		if !strings.Contains(frag, " ILIKE ") {
			t.Errorf("string_contains does not use ILIKE, so LIKE escaping is inert: %q", frag)
		}
		got, ok := args[1].(string)
		if !ok {
			t.Fatalf("value arg is %T, want string", args[1])
		}
		if got != c.want {
			t.Errorf("value %q escaped to %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFilterSQLFailClosedSurvivesNegation covers EVERY fail-closed path, not
// just array_contains. All three return the bare constant "false" BEFORE the
// Negate wrapper runs. A tempting cleanup unifying them into
// `frag = "false"; break` yields `NOT COALESCE(false,false)` = TRUE — a filter
// matching every live row in the container. That mutation previously escaped
// on the two numeric sites while being caught on array_contains.
func TestFilterSQLFailClosedSurvivesNegation(t *testing.T) {
	cases := map[string]FilterCondition{
		"array_contains":  {Key: "files", Value: "x.go", FilterType: "array_contains"},
		"bad_operator":    {Key: "confidence", Value: "0.5", FilterType: "numeric", NumericOperator: "; DROP TABLE memories--"},
		"bad_numeric_arg": {Key: "confidence", Value: "0 OR true", FilterType: "numeric", NumericOperator: ">="},
	}
	for name, base := range cases {
		for _, negate := range []bool{false, true} {
			c := base
			c.Negate = negate
			frag, args := filterSQL(c, 0)
			if frag != "false" {
				t.Errorf("%s (negate=%v) rendered %q; fail-closed must be unnegatable", name, negate, frag)
			}
			if len(args) != 0 {
				t.Errorf("%s bound args: %v", name, args)
			}
		}
	}
}

// TestFilterSQLNegateWrapsMatchingFragments: the positive path — a real filter
// under Negate must still bind its values and must not collapse to a constant.
func TestFilterSQLNegateWrapsMatchingFragments(t *testing.T) {
	for _, c := range []FilterCondition{
		{Key: "category", Value: "bug_risk", Negate: true},
		{Key: "file_path", Value: "a.go", FilterType: "string_contains", Negate: true},
		{Key: "confidence", Value: "0.5", FilterType: "numeric", NumericOperator: ">=", Negate: true},
	} {
		frag, args := filterSQL(c, 0)
		if frag == "false" || !strings.HasPrefix(frag, "NOT ") {
			t.Errorf("negated %+v rendered %q; want a wrapped fragment", c, frag)
		}
		// The COALESCE is the whole point of the wrapper, not decoration. A row
		// missing the key yields NULL: NOT COALESCE(NULL,false) is TRUE (the
		// row matches a negated filter, which is what "not equal to v" means),
		// while a bare NOT (NULL) is NULL and silently excludes it.
		// The default matters as much as the call: COALESCE(NULL, true) under
		// NOT is FALSE, which excludes the missing-key row just as silently as
		// dropping the COALESCE entirely.
		if !strings.Contains(frag, "COALESCE(") || !strings.Contains(frag, ", false)") {
			t.Errorf("negated %+v is not wrapped in COALESCE(..., false): %q", c, frag)
		}
		if len(args) == 0 {
			t.Errorf("negated %+v bound no args", c)
		}
	}
}

// TestFilterSQLTypeUsesIndexedColumn: `type` reads the indexed column, not the
// metadata map — memories_scope_idx covers it. Asserted as "does not read the
// metadata map" rather than by prefix-matching the rendered SQL, so qualifying
// the column with the query's table alias stays a harmless rewording.
func TestFilterSQLTypeUsesIndexedColumn(t *testing.T) {
	frag, args := filterSQL(FilterCondition{Key: "type", Value: string(TypePattern)}, 0)
	if strings.Contains(frag, "metadata->>") {
		t.Errorf("type filter went through the metadata map: %q", frag)
	}
	if !strings.Contains(frag, "type") {
		t.Errorf("type filter does not reference the type column: %q", frag)
	}
	if len(args) != 1 || args[0] != string(TypePattern) {
		t.Errorf("type filter args = %v, want just the value", args)
	}
	// With an explicit FilterType it is an ordinary metadata key again.
	if frag, _ := filterSQL(FilterCondition{
		Key: "type", Value: "x", FilterType: "string_contains",
	}, 0); !strings.Contains(frag, "metadata->>") {
		t.Errorf("an explicitly-typed filter on key 'type' must read the metadata map: %q", frag)
	}
}

// TestSearchPredicatesOrdinalsAreDense is the altitude the per-filter tests
// cannot reach: correctness rests on searchPredicates re-reading base from
// len(args) AFTER each append. Hoisting `base := len(args)` out of the loop,
// or reordering those two statements, silently produces overlapping ordinals —
// a filter would then read installation_id as its metadata key, which is a
// cross-tenant read rather than a crash. Nothing referenced searchPredicates
// from any test before this; it needs no database, only the installation id.
func TestSearchPredicatesOrdinalsAreDense(t *testing.T) {
	idx := &PGIndexer{installationID: 42}
	req := SearchRequest{
		ContainerTag: "argus_api",
		Filters: &SearchFilters{
			AND: []FilterCondition{
				{Key: "type", Value: string(TypePattern)},
				{Key: "files", Value: "x.go", FilterType: "array_contains"},                           // binds nothing
				{Key: "confidence", Value: "0 OR true", FilterType: "numeric", NumericOperator: ">="}, // binds nothing
				{Key: "file_path", Value: "a.go", FilterType: "string_contains"},
				{Key: "confidence", Value: "0.30", FilterType: "numeric", NumericOperator: ">="},
			},
			OR: []FilterCondition{
				{Key: "severity", Value: "high"},
				{Key: "severity", Value: "critical"},
			},
		},
	}
	where, args := idx.searchPredicates(req)

	got := ordinals(where)
	want := wantOrdinals(0, len(args)) // $1..$len(args), dense, no gaps
	if len(got) != len(want) {
		t.Fatalf("WHERE references %v but %d args are bound\n%s", got, len(args), where)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("WHERE references %v, want exactly %v (dense from $1)\n%s", got, want, where)
		}
	}
	// The tenant predicate must be first and bound, never interpolated.
	if args[0] != int64(42) || args[1] != "argus_api" {
		t.Errorf("args[0:2] = %v, %v; want the installation id and container tag", args[0], args[1])
	}
	if !strings.Contains(where, "installation_id = $1") {
		t.Errorf("tenant predicate missing or not bound: %s", where)
	}

	// The OR group MUST be parenthesized. SQL binds AND tighter than OR, so
	// an unparenthesized group turns the whole predicate into
	//   (tenant AND container AND tombstones AND sev=high) OR (sev=critical)
	// — every matching row in the entire memories table, from ANY
	// installation and ANY container, including soft-deleted and invalidated
	// ones. This is the hottest production read: the specialist Repo leg
	// builds Filters.OR{pattern, scenario, feedback} on every review, and
	// briefing rendering copies matched content verbatim into the prompt.
	if topLevelOR.MatchString(stripParens(where)) {
		t.Errorf("OR group is not parenthesized — this is a cross-tenant read:\n%s", where)
	}
	// ...and it must actually BE a disjunction. Parenthesization alone is
	// satisfied by an AND-joined group, which for the real production filter
	// (type = pattern AND type = scenario AND type = feedback) is three
	// mutually-exclusive equalities on one column: unsatisfiable, zero rows,
	// no error. The specialist Repo leg would return empty on every review,
	// findings would all read as novel, and dismissal suppression would stop.
	if n := strings.Count(where, " OR "); n != len(req.Filters.OR)-1 {
		t.Errorf("OR group has %d disjunctions, want %d — the conditions are not OR-joined:\n%s",
			n, len(req.Filters.OR)-1, where)
	}

	// Tombstones bind no args, so the ordinal check cannot see them: deleting
	// either one silently resurfaces soft-deleted and invalidated memories,
	// reposting findings the developer already dismissed.
	for _, pred := range []string{"deleted_at IS NULL", "invalidated_at IS NULL"} {
		if !strings.Contains(where, pred) {
			t.Errorf("missing tombstone predicate %q:\n%s", pred, where)
		}
	}
}

// TestSearchPredicatesORGroupsWithNestedFragments guards the OR check against
// itself. Negate renders `NOT COALESCE(..., false)` and the numeric branch
// renders `CASE WHEN ... THEN (...)::numeric END`, both of which nest inside
// the OR group's own parentheses. A paren-stripper that does not iterate to a
// fixpoint leaves the group's parens standing and its inner " OR " exposed,
// failing correct code with a cross-tenant-read accusation — and the cheapest
// way out of that red build is to delete the only assertion guarding OR
// grouping. Both shapes are realistic: the specialist Repo leg already builds
// Filters.OR, and a confidence floor inside it is one line away.
func TestSearchPredicatesORGroupsWithNestedFragments(t *testing.T) {
	idx := &PGIndexer{installationID: 42}
	groups := map[string][]FilterCondition{
		"negated": {
			{Key: "severity", Value: "high", Negate: true},
			{Key: "severity", Value: "critical", Negate: true},
		},
		"numeric": {
			{Key: "severity", Value: "high"},
			{Key: "confidence", Value: "0.9", FilterType: "numeric", NumericOperator: ">="},
		},
		"contains": {
			{Key: "file_path", Value: "a.go", FilterType: "string_contains"},
			{Key: "severity", Value: "critical"},
		},
		// A one-condition group binds one arg, so dropping the whole group
		// drops its arg too and leaves the ordinals dense — invisible to every
		// density check. Only the rendered predicate shows it survived.
		"single": {
			{Key: "severity", Value: "high"},
		},
	}
	for name, or := range groups {
		t.Run(name, func(t *testing.T) {
			where, args := idx.searchPredicates(SearchRequest{
				ContainerTag: "argus_api",
				Filters:      &SearchFilters{OR: or},
			})
			if topLevelOR.MatchString(stripParens(where)) {
				t.Errorf("false alarm: this OR group IS parenthesized\n%s\nstripped: %q", where, stripParens(where))
			}
			if n := strings.Count(where, " OR "); n != len(or)-1 {
				t.Errorf("group has %d disjunctions, want %d:\n%s", n, len(or)-1, where)
			}
			// The group must be emitted at all: the tenant predicate binds
			// installation_id and container_tag, so anything beyond those two
			// args is the group itself.
			if len(args) <= 2 {
				t.Errorf("OR group emitted no predicate — it was dropped entirely:\n%s", where)
			}
		})
	}
}

// topLevelOR matches an " OR " that is not enclosed in parentheses — i.e. one
// that would rebind the whole WHERE clause. Apply it to stripParens output.
var topLevelOR = regexp.MustCompile(`\sOR\s`)

var innermostParens = regexp.MustCompile(`\([^()]*\)`)

// stripParens removes every parenthesized span, innermost-first, so that what
// remains is exactly the top level. It must iterate to a fixpoint: filterSQL's
// fragments DO nest — Negate wraps any fragment in `NOT COALESCE(..., false)`
// and the numeric branch emits `CASE WHEN ... THEN (...)::numeric END` — so a
// single pass leaves the OR group's own parens intact and its inner " OR "
// exposed, reporting a cross-tenant read on correct code. A false alarm here
// is worse than no check: the cheapest way out of a red build alleging a
// nonexistent leak is to weaken the only assertion guarding OR grouping.
// Each span collapses to a space, never to "()": substituting a paren pair
// would re-match on the next pass and pin the string at a premature fixpoint
// with the outer levels never stripped. A space also keeps neighbouring tokens
// from fusing. Every replacement shortens the string, so this terminates with
// no parenthesis left.
func stripParens(s string) string {
	for {
		next := innermostParens.ReplaceAllString(s, " ")
		if next == s {
			return s
		}
		s = next
	}
}

// TestFilterSQLConfidenceIsComputed pins the decay contract. metadata.confidence
// is written as "1.00" on EVERY shared write and the nightly sweep that moved it
// is gone, so a filter that reads the stored value compares against a constant
// and can exclude nothing. The fragment must therefore compute confidence from
// age instead of reading it.
//
// It must also bind exactly one argument: the computed expression references no
// metadata key, and an argument bound but never referenced shifts every later
// placeholder and silently mis-binds the rest of the WHERE clause.
func TestFilterSQLConfidenceIsComputed(t *testing.T) {
	frag, args := filterSQL(FilterCondition{
		Key: "confidence", Value: "0.30", FilterType: "numeric", NumericOperator: ">=",
	}, 0)

	// The distinction is which value is COMPARED, not whether metadata is
	// touched at all. metadata->>'confidence' must still appear as a presence
	// and regex GUARD -- dropping it let rows with no confidence, and rows
	// with a malformed one, pass a floor meant for `_shared` (caught by the
	// PG-backed suite). What must not appear is the stored value cast as the
	// comparand.
	if strings.Contains(frag, "(metadata->>'confidence')::numeric") {
		t.Errorf("confidence must be computed from age, not compared as its stored value: %q", frag)
	}
	if !strings.Contains(frag, "metadata->>'confidence' ~") {
		t.Errorf("the presence/regex guard on the stored value must survive: %q", frag)
	}
	if !strings.Contains(frag, "EXTRACT(EPOCH") {
		t.Errorf("confidence must be derived from elapsed time: %q", frag)
	}
	if !strings.Contains(frag, "decay_anchor") || !strings.Contains(frag, "updated_at") {
		t.Errorf("confidence must anchor on decay_anchor then updated_at: %q", frag)
	}
	if len(args) != 1 {
		t.Errorf("computed confidence binds only the threshold; got %d args", len(args))
	}
	// A generic numeric key keeps the stored-value path.
	other, otherArgs := filterSQL(FilterCondition{
		Key: "score", Value: "0.5", FilterType: "numeric", NumericOperator: ">=",
	}, 0)
	if !strings.Contains(other, "metadata->>") || len(otherArgs) != 2 {
		t.Errorf("non-confidence numeric keys must still read stored metadata: %q (%d args)", other, len(otherArgs))
	}
}
