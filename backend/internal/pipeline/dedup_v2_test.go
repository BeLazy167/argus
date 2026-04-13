package pipeline

import (
	"math"
	"strings"
	"testing"
)

func TestClassifyVulnType(t *testing.T) {
	tests := []struct {
		name string
		what string
		body string
		want VulnType
	}{
		{"sql injection direct", "SQL injection in where()", "", VulnSQLInjection},
		{"sql injection indirect", "unsanitized field interpolation in ORDER BY via string interpolation", "", VulnSQLInjection},
		{"sql injection body match", "query issue", "this uses string interpolation to build queries", VulnSQLInjection},
		{"xss innerHTML", "cross-site scripting via innerHTML", "", VulnXSS},
		{"path traversal", "path traversal via user-controlled filename", "", VulnPathTraversal},
		{"resource leak setinterval", "setInterval not cleared on destroy", "", VulnResourceLeak},
		{"resource leak unbounded", "Map grows without bound", "", VulnResourceLeak},
		{"weak random", "uses Math.random for session tokens", "", VulnWeakRandomness},
		{"race condition", "data race on shared map without mutex", "", VulnRaceCondition},
		{"error swallowing", "error silently ignored in catch block", "", VulnErrorSwallowing},
		{"open redirect", "unvalidated redirect to user-supplied URL", "", VulnOpenRedirect},
		{"header injection", "CRLF injection in response header", "", VulnHeaderInjection},
		{"hardcoded secret", "API key in source code", "", VulnHardcodedSecret},
		{"no match", "use builder pattern for composability", "", VulnNone},
		{"no match style", "variable naming is inconsistent", "", VulnNone},
		{"empty what", "", "some body text about nothing specific", VulnNone},
		{"empty both", "", "", VulnNone},
		{"long body truncated", "check this", longString("sql injection detected at ", 500), VulnSQLInjection},
		{"body only match", "", "this function has sql injection vulnerability", VulnSQLInjection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyVulnType(tt.what, tt.body)
			if got != tt.want {
				t.Errorf("classifyVulnType(%q, %q) = %q, want %q", tt.what, truncStr(tt.body, 50), got, tt.want)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b tfidfVector
		want float64
		tol  float64
	}{
		{"identical", tfidfVector{"sql": 1, "injection": 1}, tfidfVector{"sql": 1, "injection": 1}, 1.0, 0.001},
		{"orthogonal", tfidfVector{"sql": 1}, tfidfVector{"xss": 1}, 0.0, 0.001},
		{"partial overlap", tfidfVector{"sql": 1, "injection": 1, "where": 1}, tfidfVector{"sql": 1, "injection": 1, "orderby": 1}, 0.667, 0.01},
		{"empty a", tfidfVector{}, tfidfVector{"sql": 1}, 0.0, 0.001},
		{"empty b", tfidfVector{"sql": 1}, tfidfVector{}, 0.0, 0.001},
		{"both empty", tfidfVector{}, tfidfVector{}, 0.0, 0.001},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > tt.tol {
				t.Errorf("cosineSimilarity = %f, want %f (tol %f)", got, tt.want, tt.tol)
			}
		})
	}
}

func TestSmartDedup_QueryBuilderExample(t *testing.T) {
	reviews := []FileReview{{
		Path: "query-builder.ts",
		Comments: []FileComment{
			{Line: 12, What: "SQL injection in where(): field and value are string-interpolated", Severity: SeverityCritical, Category: CategorySecurity, Specialist: SpecialistSecurity},
			{Line: 12, What: "SQL injection via unsanitized string value interpolation in WHERE clause", Severity: SeverityCritical, Category: CategoryBug, Specialist: SpecialistBugHunter},
			{Line: 35, What: "SQL injection via unsanitized field interpolation in orderBy()", Severity: SeverityCritical, Category: CategorySecurity, Specialist: SpecialistSecurity},
			{Line: 35, What: "SQL injection via unsanitized field interpolation in ORDER BY clause", Severity: SeverityCritical, Category: CategoryBug, Specialist: SpecialistBugHunter},
			{Line: 58, What: "SQL injection in build(): table and fields are interpolated", Severity: SeverityCritical, Category: CategorySecurity, Specialist: SpecialistSecurity},
			{Line: 12, What: "query builder uses string concat instead of parameterized queries", Severity: SeverityWarning, Category: CategoryBug, Specialist: SpecialistArchitecture},
			{Line: 80, What: "new insertMany() doesn't escape column names", Severity: SeverityWarning, Category: CategoryBug, Specialist: SpecialistRegression},
			{Line: 92, What: "missing input validation on limit parameter", Severity: SeverityWarning, Category: CategorySecurity, Specialist: SpecialistSecurity},
			{Line: 1, What: "query-builder should use builder pattern for composability", Severity: SeveritySuggestion, Category: CategoryStyle, Specialist: SpecialistArchitecture},
		},
	}}

	result := SmartDedup(reviews, 5, 0.7)

	total := 0
	for _, fr := range result {
		total += len(fr.Comments)
	}

	// Layer 1 merges [0-6] (all sql_injection on same file) → 1 group
	// [7] input_validation → standalone
	// [8] no match → standalone
	// Layer 3 may merge [7]+something if within 5 lines+same category, but unlikely here
	// Expected: 3-4 findings
	if total != 4 {
		t.Errorf("SmartDedup produced %d findings, expected 4 (was 9 input)", total)
	}

	for _, fr := range result {
		for _, c := range fr.Comments {
			vt := classifyVulnType(c.What, c.Body)
			if vt == VulnSQLInjection {
				// pickBest should choose a Critical over Warning
				if c.Severity != SeverityCritical {
					t.Errorf("sql_injection representative severity = %q, want critical", c.Severity)
				}
				if c.DedupCount < 5 {
					t.Errorf("sql_injection group DedupCount = %d, want ≥5", c.DedupCount)
				}
				// Should have line references from merged findings
				if !strings.Contains(c.Why, "also at") {
					t.Errorf("sql_injection Why missing merged line refs, got: %q", c.Why)
				}
			}
		}
	}
}

func TestSmartDedup_EmptyInput(t *testing.T) {
	result := SmartDedup(nil, 5, 0.7)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil input, got %d", len(result))
	}

	result = SmartDedup([]FileReview{}, 5, 0.7)
	if len(result) != 0 {
		t.Errorf("expected empty result for empty input, got %d", len(result))
	}
}

func TestSmartDedup_SingleFinding(t *testing.T) {
	reviews := []FileReview{{
		Path: "foo.go",
		Comments: []FileComment{
			{Line: 10, What: "something wrong", Severity: SeverityWarning, Category: CategoryBug},
		},
	}}
	result := SmartDedup(reviews, 5, 0.7)
	total := 0
	for _, fr := range result {
		total += len(fr.Comments)
	}
	if total != 1 {
		t.Errorf("single finding should pass through unchanged, got %d", total)
	}
}

func TestSmartDedup_CrossFileSameVuln(t *testing.T) {
	reviews := []FileReview{
		{Path: "auth.go", Comments: []FileComment{
			{Line: 10, What: "SQL injection in query", Severity: SeverityCritical, Category: CategorySecurity},
		}},
		{Path: "db.go", Comments: []FileComment{
			{Line: 20, What: "SQL injection in query", Severity: SeverityCritical, Category: CategorySecurity},
		}},
	}
	result := SmartDedup(reviews, 5, 0.7)
	total := 0
	for _, fr := range result {
		total += len(fr.Comments)
	}
	if total != 2 {
		t.Errorf("cross-file findings should not merge, got %d findings (want 2)", total)
	}
}

func TestLayer2TFIDFCluster(t *testing.T) {
	// These two findings use nearly identical wording — high cosine
	findings := []taggedComment{
		{filePath: "a.ts", comment: FileComment{What: "SQL injection in query builder where clause allows arbitrary input", Line: 10}},
		{filePath: "a.ts", comment: FileComment{What: "SQL injection in query builder orderBy clause allows arbitrary input", Line: 15}},
		{filePath: "a.ts", comment: FileComment{What: "missing error boundary for React component tree rendering", Line: 50}},
		{filePath: "a.ts", comment: FileComment{What: "CSS z-index stacking context is wrong on modal overlay", Line: 80}},
	}

	result := layer2TFIDFCluster(findings, 0.5)

	// First two share most words and should cluster → 3 results
	if len(result) > 3 {
		t.Errorf("expected ≤3 findings after TF-IDF cluster, got %d", len(result))
	}
	if len(result) < 3 {
		t.Errorf("expected ≥3 findings (shouldn't over-merge), got %d", len(result))
	}
}

func TestLayer3LineProximity(t *testing.T) {
	findings := []taggedComment{
		{filePath: "a.ts", comment: FileComment{Line: 10, Category: CategorySecurity, What: "issue A"}},
		{filePath: "a.ts", comment: FileComment{Line: 12, Category: CategorySecurity, What: "issue B"}},
		{filePath: "a.ts", comment: FileComment{Line: 50, Category: CategorySecurity, What: "issue C"}},
		{filePath: "a.ts", comment: FileComment{Line: 10, Category: CategoryBug, What: "issue D"}},
	}

	result := layer3LineProximity(findings, 5)

	// [0]+[1] merge (same file, same category, 2 lines apart)
	// [2] separate (38 lines away)
	// [3] separate (same line but different category)
	if len(result) != 3 {
		t.Errorf("expected 3 findings after proximity merge, got %d", len(result))
	}
}

func TestValidateJudgeOutput(t *testing.T) {
	tests := []struct {
		name   string
		groups []judgeGroup
		total  int
		wantOK bool
	}{
		{
			"valid complete",
			[]judgeGroup{
				{Representative: 0, Score: 90, Duplicates: []int{1, 2}},
				{Representative: 3, Score: 70, Duplicates: nil},
			},
			4, true,
		},
		{
			"out of range index",
			[]judgeGroup{
				{Representative: 99, Score: 90, Duplicates: nil},
			},
			5, false,
		},
		{
			"negative representative",
			[]judgeGroup{
				{Representative: -1, Score: 90, Duplicates: nil},
			},
			5, false,
		},
		{
			"negative duplicate",
			[]judgeGroup{
				{Representative: 0, Score: 90, Duplicates: []int{-1}},
			},
			5, false,
		},
		{
			"duplicate index across groups",
			[]judgeGroup{
				{Representative: 0, Score: 90, Duplicates: []int{1}},
				{Representative: 1, Score: 80, Duplicates: nil},
			},
			3, false,
		},
		{
			"low coverage",
			[]judgeGroup{
				{Representative: 0, Score: 90, Duplicates: nil},
			},
			10, false,
		},
		{
			"empty groups",
			[]judgeGroup{},
			5, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJudgeOutput(tt.groups, tt.total)
			if tt.wantOK && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

// helpers

func longString(prefix string, targetLen int) string {
	var sb strings.Builder
	for sb.Len() < targetLen {
		sb.WriteString(prefix)
	}
	return sb.String()
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
