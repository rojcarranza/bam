package view

import (
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/view/style"
)

const gap = "  "

// Table aligns columns by visible width. On a TTY, Flex columns shrink (and
// their cells end in …) until the table fits the terminal. Other columns,
// including keys, are never cut.
type Table struct {
	Headers []string
	Rows    [][]string
	Flex    []int
	Indent  string
}

func (t Table) Render(o Out) error {
	n := len(t.Headers)
	widths := make([]int, n)
	all := append([][]string{t.Headers}, t.Rows...)
	for _, row := range all {
		for i := 0; i < n && i < len(row); i++ {
			if w := style.Width(row[i]); w > widths[i] {
				widths[i] = w
			}
		}
	}
	if o.TTY && o.Width > 0 {
		total := len(t.Indent) + len(gap)*(n-1)
		for _, w := range widths {
			total += w
		}
		for _, c := range t.Flex {
			if total <= o.Width {
				break
			}
			floor := max(style.Width(t.Headers[c]), 8)
			shrink := min(total-o.Width, widths[c]-floor)
			if shrink > 0 {
				widths[c] -= shrink
				total -= shrink
			}
		}
	}
	for _, row := range all {
		var b strings.Builder
		b.WriteString(t.Indent)
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			if style.Width(cell) > widths[i] {
				cell = cut(style.Strip(cell), widths[i])
			}
			b.WriteString(cell)
			if i < n-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-style.Width(cell)))
				b.WriteString(gap)
			}
		}
		if _, err := fmt.Fprintln(o.W, strings.TrimRight(b.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

func cut(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w < 1 {
		return ""
	}
	return string(r[:w-1]) + "…"
}
