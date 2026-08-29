package admission

import "fmt"

// Outcome is what Admission decided.
type Outcome string

const (
	// OutcomeAllow runs the review in full.
	OutcomeAllow Outcome = "allow"
	// OutcomeReduce runs the review at lower depth and over fewer files.
	OutcomeReduce Outcome = "reduce"
	// OutcomeRefuse does not run the review.
	OutcomeRefuse Outcome = "refuse"
	// OutcomeSignal does not run the review either, but the event is worth
	// surfacing rather than dropping: auto-review is off, so the pull request
	// gets a one-shot "Trigger review" checkbox instead of silence.
	//
	// Distinct from refuse because the affordance is the point. A refusal says
	// no; a signal says "not automatically — here is how".
	OutcomeSignal Outcome = "signal"
)

// Severity orders the outcomes so the most severe answer can win when several
// gates disagree. A bare Outcome comparison would depend on string ordering,
// which is not the ordering that matters.
func (o Outcome) severity() int {
	switch o {
	case OutcomeRefuse:
		return 3
	case OutcomeSignal:
		return 2
	case OutcomeReduce:
		return 1
	default:
		return 0
	}
}

// Verdict is the answer, and why.
//
// The reason is not decoration. A refusal with no reason is indistinguishable
// from a fault: the contributor ticks a box, nothing happens, and they report a
// bug. On an open-source repo they have no dashboard to check, so the reason on
// the pull request is the only thing they will ever see.
type Verdict struct {
	Outcome Outcome
	// Reason is one sentence, written for the person who asked.
	Reason string

	// MaxFiles caps how many files a reduced review may look at. Zero means no
	// cap. Only meaningful when Outcome is OutcomeReduce.
	MaxFiles int
	// ForceShallow drops the multi-specialist path, so a reduced review costs
	// one call per file instead of four. Only meaningful when Outcome is
	// OutcomeReduce.
	ForceShallow bool
}

// Allowed reports whether the review runs at all.
func (v Verdict) Allowed() bool {
	return v.Outcome == OutcomeAllow || v.Outcome == OutcomeReduce
}

// worst returns the more severe of two verdicts.
//
// Ties keep the receiver, so the first gate to reach a severity owns the reason
// a reader will see. That matters: "refused: you do not have write access" is a
// more useful message than a second refusal for size on the same request.
func (v Verdict) worst(other Verdict) Verdict {
	if other.Outcome.severity() > v.Outcome.severity() {
		return other
	}
	if other.Outcome.severity() == v.Outcome.severity() && v.Outcome == OutcomeReduce {
		// Two reduce verdicts: keep the tighter caps from either side, so a
		// file cap from one measure is not lost to a shallow flag from another.
		merged := v
		if other.MaxFiles > 0 && (merged.MaxFiles == 0 || other.MaxFiles < merged.MaxFiles) {
			merged.MaxFiles = other.MaxFiles
		}
		merged.ForceShallow = merged.ForceShallow || other.ForceShallow
		return merged
	}
	return v
}

// Allow is the verdict for a review with nothing against it.
func Allow() Verdict { return Verdict{Outcome: OutcomeAllow} }

// Reduce builds a reduced verdict.
func Reduce(reason string, maxFiles int, forceShallow bool) Verdict {
	return Verdict{Outcome: OutcomeReduce, Reason: reason, MaxFiles: maxFiles, ForceShallow: forceShallow}
}

// Signal builds the "auto-review is off, offer the trigger" verdict.
func Signal(reason string) Verdict {
	return Verdict{Outcome: OutcomeSignal, Reason: reason}
}

// Refuse builds a refusal.
func Refuse(format string, args ...any) Verdict {
	return Verdict{Outcome: OutcomeRefuse, Reason: fmt.Sprintf(format, args...)}
}
