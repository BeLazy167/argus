package admission

import (
	"fmt"
	"log/slog"
	"time"
)

// Limits bound what one review may cost.
//
// Two measures, because either alone leaves a shape of pull request unbounded.
// Size misses a ten-file request in a repo where every file is deep-reviewed by
// four specialists. Tokens miss a six-hundred-file request in a repo whose
// average is small — and miss everything on a repo with no history at all.
//
// Each measure carries a soft limit, which reduces the review, and a hard
// limit, which refuses it. A zero limit is off, so an operator can disable one
// measure without disabling the other.
type Limits struct {
	SoftFiles int
	HardFiles int
	SoftLines int
	HardLines int
	// Token limits apply only when the repo has review history. A repo with no
	// completed reviews yields no estimate, and this measure then contributes
	// nothing rather than allowing everything — which is why size is checked
	// too, and why size is the measure that never abstains.
	SoftTokens int64
	HardTokens int64

	// ReducedMaxFiles caps the file count of a reduced review. Reducing depth
	// alone does not bound a large request: six hundred files at one call each
	// is still six hundred calls.
	ReducedMaxFiles int
}

// DefaultLimits are deliberately generous. This is a safety limit, not a tuning
// knob, and a limit that fires on ordinary work would be removed rather than
// respected.
//
// MEASURED against 383 completed reviews across 18 repos rather than guessed:
//
//	files per review   p50 2    p90 8     p99 26     max 50
//	tokens per repo    p50 322k p90 882k  max avg 1.43M
//
// The file limits below would have fired zero times on that history. The token
// limits are set above the worst observed repo average, not near it: a first
// draft used 400k/1.5M, which would have permanently reduced 7 of the 18 repos
// and left the worst one a rounding error from refusal. A limit that fires on
// two fifths of ordinary traffic is not a safety limit, it is a bug with a
// settings page.
//
// They apply to every deployment, self-hosted included. The auto_run bypass for
// self-hosted exists because there is no bill to gate; that argument does not
// carry here. A two-thousand-call fan-out still exhausts provider rate limits
// and still produces a review nobody reads, whoever pays for it. Self-hosters
// run the settings page and can raise these per repo.
var DefaultLimits = Limits{
	SoftFiles:       60,
	HardFiles:       400,
	SoftLines:       1500,
	HardLines:       20000,
	SoftTokens:      3_000_000,
	HardTokens:      10_000_000,
	ReducedMaxFiles: 40,
}

// Size is what a review is about to look at, taken from the fetched diff.
type Size struct {
	Files int
	Lines int
}

// TokenEstimate is the repo's recent average. Samples is zero when the repo has
// no completed reviews, and the estimate then abstains.
type TokenEstimate struct {
	AvgTokens int64
	Samples   int
}

// known reports whether the estimate carries a usable signal.
func (t TokenEstimate) known() bool { return t.Samples > 0 && t.AvgTokens > 0 }

// Budget returns the verdict for one review's cost.
//
// Exported because the cost gate genuinely runs later than the others: size is
// only exact once the diff is fetched, which happens after the launch site has
// already decided permission and the rate limit. The pipeline calls this
// directly rather than pretending to re-cross the whole seam.
//
// Every measure is evaluated and the most severe answer wins. Stopping at the
// first hit would let a request that merely reduces on size slip past a token
// count that should refuse it.
func Budget(size Size, est TokenEstimate, lim Limits) Verdict {
	started := time.Now()
	v := Allow()

	if lim.HardFiles > 0 && size.Files >= lim.HardFiles {
		v = v.worst(Refuse("this pull request changes %d files, over the limit of %d — split it, or raise the limit in settings", size.Files, lim.HardFiles))
	} else if lim.SoftFiles > 0 && size.Files >= lim.SoftFiles {
		v = v.worst(Reduce(
			fmt.Sprintf("%d files changed, over the full-review limit of %d — reviewing the highest-risk files at reduced depth", size.Files, lim.SoftFiles),
			lim.ReducedMaxFiles, true))
	}

	if lim.HardLines > 0 && size.Lines >= lim.HardLines {
		v = v.worst(Refuse("this pull request changes %d lines, over the limit of %d — split it, or raise the limit in settings", size.Lines, lim.HardLines))
	} else if lim.SoftLines > 0 && size.Lines >= lim.SoftLines {
		v = v.worst(Reduce(
			fmt.Sprintf("%d lines changed, over the full-review limit of %d — reviewing at reduced depth", size.Lines, lim.SoftLines),
			lim.ReducedMaxFiles, true))
	}

	if est.known() {
		if lim.HardTokens > 0 && est.AvgTokens >= lim.HardTokens {
			v = v.worst(Refuse("recent reviews of this repo averaged %d tokens, over the limit of %d — raise the limit in settings", est.AvgTokens, lim.HardTokens))
		} else if lim.SoftTokens > 0 && est.AvgTokens >= lim.SoftTokens {
			v = v.worst(Reduce(
				fmt.Sprintf("recent reviews of this repo averaged %d tokens — reviewing at reduced depth", est.AvgTokens),
				lim.ReducedMaxFiles, true))
		}
	}

	slog.Info("admission budget decided", "outcome", v.Outcome, "reason", v.Reason,
		"files", size.Files, "lines", size.Lines, "estimated_tokens", est.AvgTokens,
		"token_samples", est.Samples, "max_files", v.MaxFiles, "force_shallow", v.ForceShallow,
		"duration_ms", time.Since(started).Milliseconds())
	return v
}
