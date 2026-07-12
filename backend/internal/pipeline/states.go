package pipeline

// PipelineState represents the current state of a review pipeline run.
type PipelineState string

const (
	StatePending      PipelineState = "pending"
	StateTriaging     PipelineState = "triaging"
	StateBriefing     PipelineState = "briefing"
	StateReviewing    PipelineState = "reviewing"
	StateDeduping     PipelineState = "deduping"
	StateValidating   PipelineState = "validating"
	StateScoring      PipelineState = "scoring"
	StatePass2        PipelineState = "pass2"
	StateSynthesizing PipelineState = "synthesizing"
	StatePosting      PipelineState = "posting"
	StateCompleted    PipelineState = "completed"
	StateFailed       PipelineState = "failed"
	StateCancelled    PipelineState = "cancelled"

	// Deprecated: kept for in-flight migration
	StateEnriching PipelineState = "enriching"
)

// transitions defines the valid next state after each stage succeeds.
func transitions() map[PipelineState]PipelineState {
	return map[PipelineState]PipelineState{
		StatePending:      StateTriaging,
		StateTriaging:     StateBriefing,
		StateBriefing:     StateReviewing,
		StateReviewing:    StateDeduping,
		StateDeduping:     StateValidating,
		StateValidating:   StateScoring,
		StateScoring:      StatePass2,
		StatePass2:        StateSynthesizing,
		StateSynthesizing: StatePosting,
		StatePosting:      StateCompleted,
		// Migration from old pipeline
		StateEnriching: StateValidating,
	}
}

// IsTerminal returns true if the state is a final state.
func (s PipelineState) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed || s == StateCancelled
}
