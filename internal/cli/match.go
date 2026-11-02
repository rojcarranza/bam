package cli

import (
	"slices"
	"strings"
)

// matching keeps the items with a field that contains text, ignoring case.
func matching[T any](items []T, text string, fields func(T) []string) []T {
	text = strings.ToLower(text)
	var out []T
	for _, it := range items {
		if slices.ContainsFunc(fields(it), func(f string) bool { return strings.Contains(strings.ToLower(f), text) }) {
			out = append(out, it)
		}
	}
	return out
}
