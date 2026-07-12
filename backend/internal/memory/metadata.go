package memory

import (
	"fmt"
	"time"
)

// MemoryType classifies a memory document by the kind of information it carries.
// Readers use this (via metadata.type filter) to pull only the relevant slice of
// the unified {repo} / _shared container.
type MemoryType string

const (
	TypePattern   MemoryType = "pattern"
	TypeScenario  MemoryType = "scenario"
	TypeTrace     MemoryType = "trace"
	TypeFeedback  MemoryType = "feedback"
	TypeSynthesis MemoryType = "synthesis"
	TypePRSummary MemoryType = "pr_summary"
	TypeReview    MemoryType = "review"
	TypeTopology  MemoryType = "topology"
	TypeRule      MemoryType = "rule"
)

// Polarity disambiguates feedback documents: positive (confirmed good) vs
// negative (dismissed / false-positive suppression).
type Polarity string

const (
	PolarityPositive Polarity = "positive"
	PolarityNegative Polarity = "negative"
)

// ContainerScope selects which unified container(s) a read or write targets:
// ScopeRepo → {repo}; ScopeShared → "_shared" (cross-repo under this
// installation); ScopeBoth → both, merged best-first (reads only). It is the
// single container-selection enum shared by the write path and the deep Search
// reader (MemoryQuery.Scope).
type ContainerScope string

const (
	ScopeRepo   ContainerScope = "repo"
	ScopeShared ContainerScope = "shared"
	ScopeBoth   ContainerScope = "both"
)

// Metadata models the typed metadata that accompanies every Supermemory write.
// Callers construct Metadata, then ToMap() validates the type-specific required
// fields and emits the flat map[string]string that the REST API expects.
//
// Illegal combinations (e.g. type=feedback without polarity) fail fast at index
// time rather than silently returning wrong results at query time.
type Metadata struct {
	// SchemaVersion is always emitted so readers can migrate shape on their
	// side. Always 1 for now; bump + add migration logic when we evolve the
	// field set. Zero-value is normalized to CurrentSchemaVersion in ToMap so
	// callers don't have to remember to set it.
	SchemaVersion int

	Type       MemoryType
	Subtype    string
	FilePath   string
	Category   string
	Severity   string
	Polarity   Polarity // required iff Type == TypeFeedback
	Action     string   // required iff Type == TypeFeedback: "confirmed" | "dismissed"
	PRNumber   int      // 0 means absent
	PRAuthor   string
	ScenarioID int64 // 0 means absent
	Score      int   // 0 means absent
	Source     string
	CreatedAt  time.Time         // zero-value means absent
	Extra      map[string]string // escape hatch; copied verbatim
}

// CurrentSchemaVersion is the version number every new write emits. Bump when
// adding fields with semantic meaning so downstream migrators can distinguish
// old docs from new.
const CurrentSchemaVersion = 1

// reservedMetadataKeys lists the flat-map keys that Metadata's typed fields
// own. Extra may not use these — otherwise the caller would clobber typed
// values silently.
var reservedMetadataKeys = map[string]struct{}{
	"schema_version": {},
	"type":           {},
	"subtype":        {},
	"file_path":      {},
	"category":       {},
	"severity":       {},
	"polarity":       {},
	"action":         {},
	"pr_number":      {},
	"pr_author":      {},
	"scenario_id":    {},
	"score":          {},
	"source":         {},
	"created_at":     {},
}

// validMemoryTypes enumerates every MemoryType constant for membership checks.
var validMemoryTypes = map[MemoryType]struct{}{
	TypePattern:   {},
	TypeScenario:  {},
	TypeTrace:     {},
	TypeFeedback:  {},
	TypeSynthesis: {},
	TypePRSummary: {},
	TypeReview:    {},
	TypeTopology:  {},
	TypeRule:      {},
}

// ToMap validates Metadata's type-specific required fields and flattens every
// non-zero field into the string-keyed map the Supermemory API consumes.
//
// Returns an error when:
//   - Type is empty or not a known MemoryType
//   - TypeFeedback without Polarity or Action (action must be "confirmed" or "dismissed")
//   - TypeScenario with ScenarioID <= 0
//   - TypeSynthesis with empty FilePath
//   - TypePRSummary with PRNumber <= 0
//   - Extra contains a key reserved for a typed field
//
// Zero-value numeric fields and zero-value CreatedAt are omitted from the map.
func (m Metadata) ToMap() (map[string]string, error) {
	if m.Type == "" {
		return nil, fmt.Errorf("metadata: Type is required")
	}
	if _, ok := validMemoryTypes[m.Type]; !ok {
		return nil, fmt.Errorf("metadata: unknown Type %q", m.Type)
	}

	// Reject negatives across the board — metadata values are serialized as
	// strings, so a negative integer would flow through to filter comparisons
	// where lexicographic vs numeric mismatches cause silent-wrong results.
	if m.PRNumber < 0 {
		return nil, fmt.Errorf("metadata: PRNumber must be >= 0, got %d", m.PRNumber)
	}
	if m.ScenarioID < 0 {
		return nil, fmt.Errorf("metadata: ScenarioID must be >= 0, got %d", m.ScenarioID)
	}
	if m.Score < 0 {
		return nil, fmt.Errorf("metadata: Score must be >= 0, got %d", m.Score)
	}
	// SchemaVersion is a monotonic counter — readers compare it against
	// CurrentSchemaVersion to decide migration paths. A negative value would
	// serialize into metadata, then sort before every legitimate version,
	// breaking version-gated reader branches. Zero is fine here: ToMap's
	// default-assignment below replaces it with CurrentSchemaVersion.
	if m.SchemaVersion < 0 {
		return nil, fmt.Errorf("metadata: SchemaVersion must be >= 0, got %d", m.SchemaVersion)
	}

	switch m.Type {
	case TypeFeedback:
		if m.Polarity == "" {
			return nil, fmt.Errorf("metadata: type=feedback requires Polarity")
		}
		if m.Polarity != PolarityPositive && m.Polarity != PolarityNegative {
			return nil, fmt.Errorf("metadata: invalid Polarity %q", m.Polarity)
		}
		if m.Action != "confirmed" && m.Action != "dismissed" && m.Action != "ignored" {
			return nil, fmt.Errorf("metadata: type=feedback requires Action in {confirmed,dismissed,ignored}, got %q", m.Action)
		}
	case TypeScenario:
		if m.ScenarioID <= 0 {
			return nil, fmt.Errorf("metadata: type=scenario requires ScenarioID > 0")
		}
	case TypeSynthesis:
		if m.FilePath == "" {
			return nil, fmt.Errorf("metadata: type=synthesis requires FilePath")
		}
	case TypePRSummary:
		if m.PRNumber <= 0 {
			return nil, fmt.Errorf("metadata: type=pr_summary requires PRNumber > 0")
		}
	}

	for k := range m.Extra {
		if _, reserved := reservedMetadataKeys[k]; reserved {
			return nil, fmt.Errorf("metadata: Extra key %q collides with a typed field", k)
		}
	}

	version := m.SchemaVersion
	if version == 0 {
		version = CurrentSchemaVersion
	}
	out := make(map[string]string, 16)
	out["schema_version"] = fmt.Sprintf("%d", version)
	out["type"] = string(m.Type)
	if m.Subtype != "" {
		out["subtype"] = m.Subtype
	}
	if m.FilePath != "" {
		out["file_path"] = m.FilePath
	}
	if m.Category != "" {
		out["category"] = m.Category
	}
	if m.Severity != "" {
		out["severity"] = m.Severity
	}
	if m.Polarity != "" {
		out["polarity"] = string(m.Polarity)
	}
	if m.Action != "" {
		out["action"] = m.Action
	}
	if m.PRNumber > 0 {
		out["pr_number"] = fmt.Sprintf("%d", m.PRNumber)
	}
	if m.PRAuthor != "" {
		out["pr_author"] = m.PRAuthor
	}
	if m.ScenarioID > 0 {
		out["scenario_id"] = fmt.Sprintf("%d", m.ScenarioID)
	}
	if m.Score > 0 {
		out["score"] = fmt.Sprintf("%d", m.Score)
	}
	if m.Source != "" {
		out["source"] = m.Source
	}
	if !m.CreatedAt.IsZero() {
		out["created_at"] = m.CreatedAt.UTC().Format(time.RFC3339)
	}
	for k, v := range m.Extra {
		out[k] = v
	}
	return out, nil
}
