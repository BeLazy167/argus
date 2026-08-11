package pipeline

import (
	"strings"
	"testing"
)

// The two halves of the dismissal comparison must produce the SAME text for the
// same finding. This is the round trip: render a comment the way GitHub gets it,
// read it back the way the reaction/reply handlers do, and land on the exact
// statement the enricher queries with.
//
// Without it the write path stored the rendered body while the read path queried
// the statement. Measured on the live corpus, the SAME finding scored 0.7193
// mean / 0.9000 max across that boundary — under the 0.95 drop floor every time,
// which is why 45 dismissals produced 0 suppressions.
func TestFindingTextRoundTripsThroughPostedBody(t *testing.T) {
	cases := []struct {
		name    string
		comment FileComment
	}{
		{"warning with impact", FileComment{
			Severity: SeverityWarning, Category: CategoryBug, Score: 80,
			What: "We send `NaN` to the create-profile API when either CPU field contains non-numeric text.",
			Why:  "Number inputs yield NaN for non-numeric text, and the API rejects the whole request.",
		}},
		{"critical with suggestion block", FileComment{
			Severity: SeverityCritical, Category: CategorySecurity, Score: 90,
			What:       "The body `userId` is trusted when `getUserId(req)` is missing.",
			Why:        "Any caller can act as any user.",
			Suggestion: "const userId = getUserId(req);\nif (!userId) throw new UnauthorizedError();",
		}},
		{"medium confidence wraps in details", FileComment{
			Severity: SeverityWarning, Category: CategoryTypeDesign, Score: 70,
			Confidence: "medium",
			What:       "`instanceId` is used as the cross-document settlement key here, but this schema does not enforce uniqueness on it.",
			Why:        "Duplicate rows let the worker CAS-update the wrong document.",
		}},
		{"praise has no priority label", FileComment{
			Severity: SeverityPraise, Category: CategoryTypeDesign,
			What: "Good addition of a dedicated `AdminPartner` interface.",
		}},
		{"dismissal downgrade note is chrome, not content", FileComment{
			Severity: SeverityWarning, Category: CategoryErrorHandling, Score: 60,
			What:               "We swallow PostHog shutdown failures here and still resolve successfully.",
			Why:                "A failed flush loses the batch silently.",
			DismissedDowngrade: true, DismissedMatchPR: 682,
		}},
		{"category with an underscore", FileComment{
			Severity: SeveritySuggestion, Category: CategoryErrorHandling, Score: 50,
			What: "Send-reply failure is silent — mutation error state is never rendered",
		}},
		// The multi-line `what`. 6 of 200 live findings carry a whole
		// "Context:\ndiff --git …" blob the LLM echoed into `what`, and they are the
		// slice that most needs the shapes to match: the diff dominates the
		// embedding, and one such body scored 0.9455 against an UNRELATED finding on
		// the same file. Both sides must keep the opening statement and drop the
		// blob. Before findingStatement was shared, the read side truncated at 300
		// bytes WITH the newlines while the write side cut at the first one.
		{"multi-line what with a diff blob", FileComment{
			Severity: SeverityWarning, Category: CategoryBug, Score: 75,
			What: "The retry loop never resets the counter\nContext:\n" +
				"diff --git a/internal/worker/retry.go b/internal/worker/retry.go\n" +
				"@@ -18,7 +18,7 @@ func (w *Worker) run(ctx context.Context) error {\n" +
				"-\tattempts = 0\n+\t// attempts intentionally preserved across batches\n",
			Why: "A single poisoned job then exhausts the budget for every later job.",
		}},
		{"multi-line what, medium confidence, wrapped in details", FileComment{
			Severity: SeverityWarning, Category: CategoryErrorHandling, Score: 65,
			Confidence: "medium",
			What: "The context deadline is discarded on the retry path\nContext:\n" +
				"diff --git a/internal/http/client.go b/internal/http/client.go\n" +
				"@@ -42,6 +42,8 @@\n-\tctx, cancel := context.WithTimeout(ctx, d)\n",
			Why: "The retry then runs without any deadline at all.",
		}},
		// A statement whose only sentence boundary sits past the 280-byte window,
		// so it reaches the truncation branch on BOTH sides. Truncation has to be
		// idempotent or the recovered text loses three more bytes than the stored
		// one and the two never compare equal again.
		{"statement long enough to be truncated", FileComment{
			Severity: SeverityCritical, Category: CategoryBug, Score: 95,
			What: strings.Repeat("the reconciler rewrites every row it reads ", 12) + "and never stops",
			Why:  "The table is rewritten in full on every tick.",
		}},
		// Non-ASCII: the header regex is anchored on a 4-byte emoji and truncation
		// walks rune boundaries. A CJK/emoji statement must survive both intact.
		{"non-ASCII statement", FileComment{
			Severity: SeverityWarning, Category: CategoryBug, Score: 70,
			What: "`結算` の instanceId は一意ではない — 重複行が CAS を誤らせる 🚨",
			Why:  "Duplicate rows let the worker update the wrong document.",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := commentTitle(tc.comment)
			got := FindingTextFromPostedBody(formatCommentBody(tc.comment))
			if got != want {
				t.Errorf("round trip lost the statement\n  rendered: %q\n  recovered: %q\n  want:      %q",
					formatCommentBody(tc.comment), got, want)
			}
			// Agreement alone is not enough: both sides agreeing on a 300-byte
			// slab of diff is the failure mode, not the fix. The shared shape is
			// ONE LINE, so pin that directly on the read side.
			if strings.Contains(want, "\n") {
				t.Errorf("the query side kept a multi-line statement: %q", want)
			}
			if strings.Contains(want, "diff --git") {
				t.Errorf("the query side embedded the diff blob: %q", want)
			}
		})
	}

	// Every severity, so a new severityEmoji return that postedBodyHeaderRe does
	// not enumerate fails here instead of quietly leaving those dismissals
	// unparsed — the exact silent-miss shape this whole change is fixing.
	for sev := range ValidSeverities {
		c := FileComment{Severity: sev, Category: CategoryBug, Score: 70, What: "The guard is inverted."}
		if got := FindingTextFromPostedBody(formatCommentBody(c)); got != commentTitle(c) {
			t.Errorf("severity %q: postedBodyHeaderRe does not cover severityEmoji(%q)=%q; got %q",
				sev, sev, severityEmoji(sev), got)
		}
	}
}

// A body that was never rendered is already the statement. The reaction and
// reply handlers cannot tell a rendered row from a legacy one, so the function
// must be a no-op on clean input or applying it uniformly would corrupt the
// bodies it was not meant to touch.
func TestFindingTextIsIdempotentOnStatements(t *testing.T) {
	statements := []string{
		"We swallow PostHog shutdown failures here and still resolve successfully.",
		"Click-target advancement recursively dispatches the same click",
		"`instanceId` is used as the cross-document settlement key here.",
		"Both **halves** of the guard are inverted",    // bold prose, no header colon
		"Fix the **timeout:** it is unset",             // bold ending in a colon, mid-sentence
		"`結算` の instanceId は一意ではない — 重複行が CAS を誤らせる 🚨", // non-ASCII
		"",
		// Already truncated by a previous pass. Truncation counts the ellipsis
		// against the budget precisely so this is a fixed point; a budget that
		// excluded it would shave three more bytes on every re-normalisation and
		// the stored text would drift away from the queried text.
		strings.Repeat("x", statementMaxBytes) + "...",
		// A fenced block inside the statement: the parser reads one line, so a
		// ``` that never opens a real fence must not confuse it.
		"The ```suggestion``` fence is emitted without a language tag",
	}
	for _, s := range statements {
		if got := FindingTextFromPostedBody(s); got != s {
			t.Errorf("statement rewritten\n  in:  %q\n  out: %q", s, got)
		}
		if twice := FindingTextFromPostedBody(FindingTextFromPostedBody(s)); twice != s {
			t.Errorf("not idempotent for %q: %q", s, twice)
		}
	}
}

// Bodies the header parser cannot recognise still get NORMALISED — same first
// line, same sentence rule, same budget — because a raw multi-line body stored
// next to one-line statements is the exact mismatch this path exists to remove.
// What must never happen is emptying one: dismissalSearch short-circuits on an
// empty query, so a dismissal written with no content would be recorded and
// then be unreachable forever.
func TestFindingTextNormalizesUnparseableBodies(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "details wrapper with no severity header",
			in:   "<details><summary>no header here</summary>\n\nbody\n</details>",
			want: "no header here",
		},
		{
			name: "header present but the title is empty",
			in:   "🟡 **P1 (7/10) · Bug:**",
			want: "🟡 **P1 (7/10) · Bug:**",
		},
		{
			name: "no header at all, single line",
			in:   "plain prose with no markup at all",
			want: "plain prose with no markup at all",
		},
		{
			// A pre-#252 dismissal body, or one a developer hand-edited so the
			// emoji is gone. The header line is unrecognisable, so it is kept —
			// but the impact prose and footer below it are still dropped.
			name: "no header, multi-line legacy body",
			in:   "**P1 (8/10) · Bug:** The guard is inverted\n\nEvery request is admitted.\n\n---\n<sub>React 👎 to dismiss · Argus learns from feedback</sub>",
			want: "**P1 (8/10) · Bug:** The guard is inverted",
		},
		{
			name: "empty body stays empty",
			in:   "",
			want: "",
		},
		{
			name: "whitespace-only body",
			in:   "   \n\t\n",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindingTextFromPostedBody(tc.in)
			if got != tc.want {
				t.Errorf("FindingTextFromPostedBody(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
			if tc.in != "" && strings.TrimSpace(tc.in) != "" && got == "" {
				t.Errorf("non-empty body %q normalised to empty — the dismissal doc would be unsearchable", tc.in)
			}
		})
	}
}
