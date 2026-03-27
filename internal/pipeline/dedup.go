package pipeline

import (
	"strings"
)

type taggedComment struct {
	filePath string
	comment  FileComment
}

// dedupFindings removes duplicate comments across specialists.
// Groups by file + line proximity (within lineThreshold lines) + similar content.
// Keeps the comment with highest severity, then longest explanation.
func dedupFindings(reviews []FileReview, lineThreshold int) []FileReview {
	if lineThreshold == 0 {
		lineThreshold = 5
	}

	type dedupKey struct {
		path string
		line int // normalized to nearest lineThreshold bucket
	}

	var all []taggedComment
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			all = append(all, taggedComment{filePath: fr.Path, comment: c})
		}
	}

	// Group by file + line bucket
	groups := make(map[dedupKey][]taggedComment)
	for _, tc := range all {
		key := dedupKey{
			path: tc.filePath,
			line: (tc.comment.Line / lineThreshold) * lineThreshold,
		}
		groups[key] = append(groups[key], tc)
	}

	// For each group, find duplicates by content similarity and keep the best
	kept := make(map[string][]FileComment) // file path → comments
	for _, group := range groups {
		if len(group) == 1 {
			kept[group[0].filePath] = append(kept[group[0].filePath], group[0].comment)
			continue
		}

		// Within the group, cluster by similar content
		// Simple approach: check if What/Body overlap significantly
		var clusters [][]taggedComment
		used := make([]bool, len(group))
		for i := range group {
			if used[i] {
				continue
			}
			cluster := []taggedComment{group[i]}
			used[i] = true
			for j := i + 1; j < len(group); j++ {
				if used[j] {
					continue
				}
				if isSimilarFinding(group[i].comment, group[j].comment) {
					cluster = append(cluster, group[j])
					used[j] = true
				}
			}
			clusters = append(clusters, cluster)
		}

		// For each cluster, keep the best comment
		for _, cluster := range clusters {
			best := pickBest(cluster)
			best.comment.DedupCount = len(cluster)
			kept[best.filePath] = append(kept[best.filePath], best.comment)
		}
	}

	// Rebuild FileReview list
	var result []FileReview
	for path, comments := range kept {
		result = append(result, FileReview{Path: path, Comments: comments})
	}
	return result
}

// isSimilarFinding checks if two comments are about the same issue.
// Uses simple heuristics: same category + overlapping keywords in What/Body.
func isSimilarFinding(a, b FileComment) bool {
	// Same category is a strong signal
	if a.Category != "" && a.Category == b.Category {
		aText := strings.ToLower(a.What + " " + a.Body)
		bText := strings.ToLower(b.What + " " + b.Body)
		// Check for significant word overlap
		aWords := strings.Fields(aText)
		overlap := 0
		for _, w := range aWords {
			if len(w) > 3 && strings.Contains(bText, w) {
				overlap++
			}
		}
		// If >40% of significant words overlap, likely the same finding
		if len(aWords) > 0 && float64(overlap)/float64(len(aWords)) > 0.4 {
			return true
		}
	}

	// Different category but very similar What field
	if a.What != "" && b.What != "" {
		aLower := strings.ToLower(a.What)
		bLower := strings.ToLower(b.What)
		if strings.Contains(aLower, bLower) || strings.Contains(bLower, aLower) {
			return true
		}
	}

	return false
}

// pickBest selects the best comment from a cluster of duplicates.
// Priority: highest severity, then longest explanation.
func pickBest(cluster []taggedComment) taggedComment {
	severityRank := map[Severity]int{
		SeverityCritical: 4, SeverityWarning: 3, SeveritySuggestion: 2, SeverityPraise: 1,
	}
	best := cluster[0]
	for _, tc := range cluster[1:] {
		tcRank := severityRank[tc.comment.Severity]
		bestRank := severityRank[best.comment.Severity]
		if tcRank > bestRank {
			best = tc
		} else if tcRank == bestRank {
			// Same severity — prefer longer explanation
			tcLen := len(tc.comment.What) + len(tc.comment.Why) + len(tc.comment.Body)
			bestLen := len(best.comment.What) + len(best.comment.Why) + len(best.comment.Body)
			if tcLen > bestLen {
				best = tc
			}
		}
	}
	return best
}
