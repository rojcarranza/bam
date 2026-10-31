package errs

import (
	"maps"
	"slices"
	"strings"
)

// Nearest returns the candidate most similar to input, or "" when none is
// close: substring matches first, shortest first so "log" finds "logs"
// before "login", then names within a small edit distance, nearest first.
// A swap of two neighbouring letters counts as one edit. Input under three
// letters must be a prefix: "go" inside "logout" is chance, not a hint.
// Ties go to the earlier candidate.
func Nearest(input string, candidates []string) string {
	lower := strings.ToLower(input)
	match := strings.Contains
	if len([]rune(input)) < 3 {
		match = strings.HasPrefix
	}
	best := ""
	for _, c := range candidates {
		if match(strings.ToLower(c), lower) && (best == "" || len(c) < len(best)) {
			best = c
		}
	}
	if best != "" {
		return best
	}
	// Two edits would rewrite most of a short word: "del" is two from "set".
	limit := 1
	if l := len([]rune(input)); l > 3 {
		limit = max(2, l/3)
	}
	for _, c := range candidates {
		if d := levenshtein(lower, strings.ToLower(c)); d <= limit {
			best, limit = c, d-1
		}
	}
	return best
}

// NearestKey is Nearest over the keys of m, taken in sorted order so the
// hint is the same on every run.
func NearestKey[V any](input string, m map[string]V) string {
	return Nearest(input, slices.Sorted(maps.Keys(m)))
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	var prev2 []int
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev = prev, cur
	}
	return prev[len(rb)]
}
