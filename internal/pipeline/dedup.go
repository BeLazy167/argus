package pipeline

import (
	"sort"
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

	var all []taggedComment
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			all = append(all, taggedComment{filePath: fr.Path, comment: c})
		}
	}

	// Group by file + line proximity using union-find to avoid bucket-boundary misses.
	// Fixed-width bucketing would split lines 9 and 10 (threshold=5) into different
	// buckets despite being only 1 line apart.
	parent := make([]int, len(all))
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
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[i].filePath == all[j].filePath {
				d := all[i].comment.Line - all[j].comment.Line
				if d < 0 {
					d = -d
				}
				if d <= lineThreshold {
					union(i, j)
				}
			}
		}
	}
	groups := make(map[int][]taggedComment)
	for i, tc := range all {
		groups[find(i)] = append(groups[find(i)], tc)
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

	// Rebuild FileReview list (sorted by path for deterministic output)
	paths := make([]string, 0, len(kept))
	for path := range kept {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var result []FileReview
	for _, path := range paths {
		result = append(result, FileReview{Path: path, Comments: kept[path]})
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
