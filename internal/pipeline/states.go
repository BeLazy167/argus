package pipeline

// PipelineState represents the current state of a review pipeline run.
type PipelineState string

const (
	StatePending       PipelineState = "pending"
	StateTriaging      PipelineState = "triaging"
	StateBriefing      PipelineState = "briefing"
	StateReviewing     PipelineState = "reviewing"
	StateBroadcasting  PipelineState = "broadcasting"
	StateCrossChecking PipelineState = "cross_checking"
	StatePass2         PipelineState = "pass2"
	StateSynthesizing  PipelineState = "synthesizing"
	StatePosting       PipelineState = "posting"
	StateCompleted     PipelineState = "completed"
	StateFailed        PipelineState = "failed"

	// Deprecated: kept for in-flight migration from old pipeline
	StateEnriching PipelineState = "enriching"
	StateScoring   PipelineState = "scoring"
)

// transitions defines the valid next state after each stage succeeds.
func transitions() map[PipelineState]PipelineState {
	return map[PipelineState]PipelineState{
		StatePending:       StateTriaging,
		StateTriaging:      StateBriefing,
		StateBriefing:      StateReviewing,
		StateReviewing:     StateBroadcasting,
		StateBroadcasting:  StateCrossChecking,
		StateCrossChecking: StatePass2,
		StatePass2:         StateSynthesizing,
		StateSynthesizing:  StatePosting,
		StatePosting:       StateCompleted,
		// Migration: in-flight runs from old pipeline
		StateEnriching: StateBroadcasting,
		StateScoring:   StateCrossChecking,
	}
}

// IsTerminal returns true if the state is a final state.
func (s PipelineState) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed
}
