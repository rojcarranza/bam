package app

import (
	"slices"
	"strings"
)

// Closest returns up to n candidates similar to input: substring matches
// first, shortest first so "log" finds "logs" before "login", then names
// within a small edit distance, nearest first. A swap of two neighbouring
// letters counts as one edit. Input under three letters must be a prefix:
// "go" inside "logout" is chance, not a hint.
func Closest(input string, candidates []string, n int) []string {
	lower := strings.ToLower(input)
	var out []string
	seen := map[string]bool{}
	match := strings.Contains
	if len([]rune(input)) < 3 {
		match = strings.HasPrefix
	}
	for _, c := range candidates {
		if match(strings.ToLower(c), lower) {
			out = append(out, c)
			seen[c] = true
		}
	}
	slices.SortStableFunc(out, func(a, b string) int { return len(a) - len(b) })
	if len(out) >= n {
		return out[:n]
	}
	// Two edits would rewrite most of a short word: "del" is two from "set".
	limit := 1
	if l := len([]rune(input)); l > 3 {
		limit = max(2, l/3)
	}
	type near struct {
		name string
		dist int
	}
	var nears []near
	for _, c := range candidates {
		if d := levenshtein(lower, strings.ToLower(c)); !seen[c] && d <= limit {
			nears = append(nears, near{c, d})
		}
	}
	slices.SortStableFunc(nears, func(a, b near) int { return a.dist - b.dist })
	for _, c := range nears {
		if len(out) == n {
			break
		}
		out = append(out, c.name)
	}
	return out
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
