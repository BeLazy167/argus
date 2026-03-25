package pipeline

// PipelineState represents the current state of a review pipeline run.
type PipelineState string

const (
	StatePending      PipelineState = "pending"
	StateTriaging     PipelineState = "triaging"
	StateReviewing    PipelineState = "reviewing"
	StateEnriching    PipelineState = "enriching"
	StateScoring      PipelineState = "scoring"
	StatePass2        PipelineState = "pass2"
	StateSynthesizing PipelineState = "synthesizing"
	StatePosting      PipelineState = "posting"
	StateCompleted    PipelineState = "completed"
	StateFailed       PipelineState = "failed"
)

// transitions defines the valid next state after each stage succeeds.
func transitions() map[PipelineState]PipelineState {
	return map[PipelineState]PipelineState{
		StatePending:      StateTriaging,
		StateTriaging:     StateReviewing,
		StateReviewing:    StateEnriching,
		StateEnriching:    StateScoring,
		StateScoring:      StatePass2,
		StatePass2:        StateSynthesizing,
		StateSynthesizing: StatePosting,
		StatePosting:      StateCompleted,
	}
}

// IsTerminal returns true if the state is a final state.
func (s PipelineState) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed
}
