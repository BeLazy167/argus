package memory

// SearchRequest describes ONE single-container retrieval. It is a query
// description, not a wire format: reader.go builds it from a MemoryQuery and
// hands it to a runSearchFn, and pgsearch.go compiles it into SQL.
//
// There is deliberately no rerank, query-rewrite or search-mode field: the SQL
// path does none of those, and carrying a knob it ignores makes every call site
// read as though it did.
type SearchRequest struct {
	Query        string
	ContainerTag string
	Limit        int
	// Threshold is the absolute similarity floor. Matches scoring below it are
	// dropped before the caller sees them.
	Threshold float64
	Filters   *SearchFilters
	// Include requests the richer rendering of each match (RichContent).
	Include *SearchInclude
	// PointLookup marks a read whose AND filters UNIQUELY PIN a row, so
	// retrieval is a lookup rather than a ranking problem — the backend may
	// then fall back to a predicate scan when both retrieval legs miss.
	// Never inferred from filter shape: "threshold 0 with some AND filter"
	// also describes ordinary type-filtered ranking reads (scenario search,
	// a shared-patterns leg with specialist_min 0), and a fallback there
	// returns arbitrary rows that briefing rendering copies verbatim into a
	// review prompt with no score gate.
	PointLookup bool
}

// SearchInclude asks for the enriched form of each match. Retrieval cost is
// unchanged; only the rendering differs.
type SearchInclude struct {
	RelatedMemories bool
	Summaries       bool
}

// SearchFilters supports AND/OR metadata filtering. Conditions in AND must all
// hold; when OR is non-empty, at least one of its conditions must also hold.
// The OR group is parenthesized before it is combined with AND.
type SearchFilters struct {
	AND []FilterCondition
	OR  []FilterCondition
}

type FilterCondition struct {
	Key   string
	Value string
	// FilterType selects comparison semantics: "" (string equality),
	// "string_contains", "numeric", or "array_contains".
	FilterType string
	// NumericOperator is ">=", "<=", ">", "<" or "=" and applies only when
	// FilterType is "numeric".
	NumericOperator string
	Negate          bool
}

// FilterNumeric returns a FilterCondition configured for numeric comparison.
// Metadata values are stored as strings, so without FilterType "numeric" the
// comparison is lexicographic — wrong for fields like pr_number, where "99" >
// "100" as text but 99 < 100 as a number.
//
// op is the numeric operator: ">=", "<=", ">", "<", "=".
func FilterNumeric(key, op, value string) FilterCondition {
	return FilterCondition{
		Key:             key,
		Value:           value,
		FilterType:      "numeric",
		NumericOperator: op,
	}
}
