package pipeline

import (
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
)

// ---------- Layer 1: Canonical Vulnerability Type Fingerprint ----------

// VulnType is a normalized vulnerability classification for dedup grouping.
type VulnType string

const (
	VulnSQLInjection    VulnType = "sql_injection"
	VulnXSS             VulnType = "xss"
	VulnPathTraversal   VulnType = "path_traversal"
	VulnResourceLeak    VulnType = "resource_leak"
	VulnWeakRandomness  VulnType = "weak_randomness"
	VulnRaceCondition   VulnType = "race_condition"
	VulnInputValidation VulnType = "input_validation"
	VulnAuthBypass      VulnType = "auth_bypass"
	VulnErrorSwallowing VulnType = "error_swallowing"
	VulnInsecureHash    VulnType = "insecure_hash"
	VulnInsecureCrypto  VulnType = "insecure_crypto"
	VulnOpenRedirect    VulnType = "open_redirect"
	VulnHeaderInjection VulnType = "header_injection"
	VulnDOSAmplify      VulnType = "dos_amplification"
	VulnHardcodedSecret VulnType = "hardcoded_secret"
	VulnNone            VulnType = ""
)

// vulnPattern maps lowercase substring keywords to a VulnType.
// Checked in order — first match wins. More specific patterns before general ones.
type vulnPattern struct {
	Type     VulnType
	Keywords []string
}

// canonicalPatterns is the ordered list of vulnerability classifiers.
// Each keyword is matched as a case-insensitive substring of What+Body.
var canonicalPatterns = []vulnPattern{
	// Injection
	{VulnSQLInjection, []string{"sql injection", "sql inject", "string interpolat", "parameterized quer", "query concat", "unsanitized query"}},
	{VulnXSS, []string{"cross-site script", " xss ", "innerhtml", "dangerouslysetinnerhtml", "unsanitized html", "reflected input"}},
	{VulnHeaderInjection, []string{"header injection", "response splitting", "crlf injection"}},

	// Path/redirect
	{VulnPathTraversal, []string{"path traversal", "directory traversal", "file path inject"}},
	{VulnOpenRedirect, []string{"open redirect", "unvalidated redirect", "redirect inject"}},

	// Resource management
	{VulnResourceLeak, []string{"unbounded growth", "unbounded array", "unbounded map", "setinterval", "clearinterval", "memory leak", "not cleared", "not cleaned", "grows without bound", "never freed", "event listener leak"}},

	// Crypto
	{VulnWeakRandomness, []string{"math.random", "weak random", "predictable random", "crypto.getrandomvalues"}},
	{VulnInsecureHash, []string{"md5 hash", "sha1 hash", "weak hash", "insecure hash", "md5 password", "sha1 password"}},
	{VulnInsecureCrypto, []string{"weak cipher", "insecure cipher", "ecb mode", "no hmac"}},
	{VulnHardcodedSecret, []string{"hardcoded secret", "hardcoded password", "hardcoded key", "api key in source", "secret in code"}},

	// Concurrency
	{VulnRaceCondition, []string{"race condition", "data race", "concurrent access", "thread safe", "mutex", "toctou"}},

	// Auth/input
	{VulnAuthBypass, []string{"authentication bypass", "auth bypass", "missing auth", "unauthenticated access"}},
	{VulnInputValidation, []string{"input validation", "missing validation", "unsanitized input", "unvalidated input"}},

	// Error handling
	{VulnErrorSwallowing, []string{"error swallow", "empty catch", "silently ignor", "error ignored", "catch block empty"}},

	// Availability
	{VulnDOSAmplify, []string{"denial of service", "regex dos", "redos", "exponential backtrack", "amplification"}},
}

// classifyVulnType returns the canonical VulnType for a finding.
// Matches lowercased (What + " " + Body[:200]) against canonicalPatterns.
func classifyVulnType(what, body string) VulnType {
	text := strings.ToLower(what)
	if body != "" {
		b := body
		if len(b) > 200 {
			b = b[:200]
		}
		text += " " + strings.ToLower(b)
	}
	for _, p := range canonicalPatterns {
		for _, kw := range p.Keywords {
			if strings.Contains(text, kw) {
				return p.Type
			}
		}
	}
	return VulnNone
}

// layer1CanonicalGroup groups findings by (file, canonical vuln type).
// Findings with VulnNone are returned as ungrouped.
// Within each group, pickBest selects the best representative; the rest are merged.
func layer1CanonicalGroup(all []taggedComment) (grouped, ungrouped []taggedComment) {
	type fingerprint struct {
		file     string
		vulnType VulnType
	}
	type group struct {
		key     fingerprint
		members []taggedComment
	}

	groups := make(map[fingerprint]*group)
	var order []fingerprint

	for _, tc := range all {
		vt := classifyVulnType(tc.comment.What, tc.comment.Body)
		if vt == VulnNone {
			ungrouped = append(ungrouped, tc)
			continue
		}
		fp := fingerprint{file: tc.filePath, vulnType: vt}
		if g, ok := groups[fp]; ok {
			g.members = append(g.members, tc)
		} else {
			groups[fp] = &group{key: fp, members: []taggedComment{tc}}
			order = append(order, fp)
		}
	}

	for _, fp := range order {
		g := groups[fp]
		if len(g.members) == 1 {
			grouped = append(grouped, g.members[0])
			continue
		}
		best := pickBest(g.members)
		// Collect line references from merged findings
		var otherLines []string
		for _, m := range g.members {
			if m.comment.Line != best.comment.Line {
				otherLines = append(otherLines, fmt.Sprintf("L%d", m.comment.Line))
			}
		}
		if len(otherLines) > 0 {
			best.comment.Why += fmt.Sprintf(" (same pattern also at %s)", strings.Join(otherLines, ", "))
		}
		best.comment.DedupCount = len(g.members)
		grouped = append(grouped, best)
	}
	return grouped, ungrouped
}

// ---------- Layer 2: TF-IDF Cosine Similarity ----------

// tfidfVector is a sparse TF-IDF vector: term → weight.
type tfidfVector map[string]float64

// tokenize splits text into lowercase tokens, filtering short words and stop words.
func tokenize(text string) []string {
	words := strings.Fields(strings.ToLower(text))
	tokens := make([]string, 0, len(words))
	for _, w := range words {
		// Strip common punctuation from edges
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		if len(w) <= 3 || isStopWord(w) {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// findingText returns the text used for TF-IDF: What + first 200 chars of Body.
func findingText(c FileComment) string {
	text := c.What
	if c.Body != "" {
		b := c.Body
		if len(b) > 200 {
			b = b[:200]
		}
		text += " " + b
	}
	return text
}

// buildTFIDFVectors computes TF-IDF vectors for a set of findings.
// Returns one vector per finding and the document frequency map.
func buildTFIDFVectors(findings []taggedComment) []tfidfVector {
	n := len(findings)
	if n == 0 {
		return nil
	}

	// Tokenize all findings
	docTokens := make([][]string, n)
	docFreq := make(map[string]int)
	for i, tc := range findings {
		tokens := tokenize(findingText(tc.comment))
		docTokens[i] = tokens
		// Count unique terms per document for IDF
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				docFreq[t]++
				seen[t] = true
			}
		}
	}

	// Compute TF-IDF vectors
	vectors := make([]tfidfVector, n)
	for i, tokens := range docTokens {
		vec := make(tfidfVector)
		// Term frequency: count occurrences
		tf := make(map[string]int)
		for _, t := range tokens {
			tf[t]++
		}
		for term, count := range tf {
			// TF: raw count normalized by doc length
			tfVal := float64(count) / float64(len(tokens))
			// IDF: log(N / df)
			idfVal := math.Log(float64(n) / float64(docFreq[term]))
			vec[term] = tfVal * idfVal
		}
		vectors[i] = vec
	}
	return vectors
}

// cosineSimilarity computes the cosine between two sparse TF-IDF vectors.
// Returns 0 if either vector is zero.
func cosineSimilarity(a, b tfidfVector) float64 {
	var dot, normA, normB float64
	for term, wa := range a {
		normA += wa * wa
		if wb, ok := b[term]; ok {
			dot += wa * wb
		}
	}
	for _, wb := range b {
		normB += wb * wb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// layer2TFIDFCluster clusters ungrouped findings by TF-IDF cosine similarity.
// Uses union-find to group findings with cosine > threshold.
// Returns one representative per cluster via pickBest.
func layer2TFIDFCluster(ungrouped []taggedComment, threshold float64) []taggedComment {
	n := len(ungrouped)
	if n <= 1 {
		return ungrouped
	}

	vectors := buildTFIDFVectors(ungrouped)

	// Union-find
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// Pairwise cosine — only merge findings on the same file
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if ungrouped[i].filePath != ungrouped[j].filePath {
				continue
			}
			if cosineSimilarity(vectors[i], vectors[j]) > threshold {
				union(i, j)
			}
		}
	}

	// Collect clusters
	clusters := make(map[int][]taggedComment)
	for i, tc := range ungrouped {
		root := find(i)
		clusters[root] = append(clusters[root], tc)
	}

	var result []taggedComment
	for _, cluster := range clusters {
		if len(cluster) == 1 {
			result = append(result, cluster[0])
			continue
		}
		best := pickBest(cluster)
		best.comment.DedupCount = len(cluster)
		result = append(result, best)
	}
	return result
}

// ---------- Layer 3: Line Proximity ----------

// layer3LineProximity merges findings on the same file within lineThreshold lines
// that share the same category. Uses union-find.
func layer3LineProximity(findings []taggedComment, lineThreshold int) []taggedComment {
	n := len(findings)
	if n <= 1 {
		return findings
	}

	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if findings[i].filePath != findings[j].filePath {
				continue
			}
			if findings[i].comment.Category != findings[j].comment.Category {
				continue
			}
			d := findings[i].comment.Line - findings[j].comment.Line
			if d < 0 {
				d = -d
			}
			if d <= lineThreshold {
				union(i, j)
			}
		}
	}

	clusters := make(map[int][]taggedComment)
	for i, tc := range findings {
		root := find(i)
		clusters[root] = append(clusters[root], tc)
	}

	var result []taggedComment
	for _, cluster := range clusters {
		if len(cluster) == 1 {
			result = append(result, cluster[0])
			continue
		}
		best := pickBest(cluster)
		if best.comment.DedupCount < len(cluster) {
			best.comment.DedupCount = len(cluster)
		}
		result = append(result, best)
	}
	return result
}

// ---------- Orchestrator ----------

// SmartDedup applies 3 deterministic layers to deduplicate findings.
//
//	Layer 1: Canonical vuln type fingerprint (file + vuln type)
//	Layer 2: TF-IDF cosine similarity for ungrouped findings
//	Layer 3: Line proximity for remaining same-file/same-category
func SmartDedup(reviews []FileReview, lineThreshold int, cosineThreshold float64) []FileReview {
	// Flatten
	var all []taggedComment
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			all = append(all, taggedComment{filePath: fr.Path, comment: c})
		}
	}
	if len(all) <= 1 {
		return reviews
	}

	totalBefore := len(all)

	// Layer 1: canonical type grouping
	grouped, ungrouped := layer1CanonicalGroup(all)
	afterL1 := len(grouped) + len(ungrouped)
	slog.Info("[dedup] layer1 canonical", "before", totalBefore, "after", afterL1,
		"grouped", len(grouped), "ungrouped", len(ungrouped))

	// Layer 2: TF-IDF cosine for ungrouped
	clustered := layer2TFIDFCluster(ungrouped, cosineThreshold)
	slog.Info("[dedup] layer2 tfidf", "ungrouped_in", len(ungrouped), "clustered_out", len(clustered))

	// Combine for Layer 3
	combined := make([]taggedComment, 0, len(grouped)+len(clustered))
	combined = append(combined, grouped...)
	combined = append(combined, clustered...)

	// Layer 3: line proximity
	final := layer3LineProximity(combined, lineThreshold)
	slog.Info("[dedup] layer3 proximity", "before", len(combined), "after", len(final))
	slog.Info("[dedup] SmartDedup complete", "total_before", totalBefore, "total_after", len(final),
		"removed", totalBefore-len(final))

	// Rebuild FileReview list sorted by path
	kept := make(map[string][]FileComment)
	for _, tc := range final {
		kept[tc.filePath] = append(kept[tc.filePath], tc.comment)
	}
	paths := make([]string, 0, len(kept))
	for p := range kept {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	result := make([]FileReview, 0, len(paths))
	for _, p := range paths {
		result = append(result, FileReview{Path: p, Comments: kept[p]})
	}
	return result
}
