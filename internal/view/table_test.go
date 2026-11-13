package view

import (
	"strings"
	"testing"

	"github.com/rojcarranza/bam/internal/view/style"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableAlignsByVisibleWidth(t *testing.T) {
	o, buf := testOut(true)
	o.Style = style.Mode{Color: true}
	tb := Table{Headers: []string{"A", "B", "C"}, Rows: [][]string{
		{style.Paint(o.Style, "32", "✓ ok"), "x", "last"},
		{"longer", "yy", "z"},
	}}
	require.NoError(t, tb.Render(o))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "A       B   C", style.Strip(lines[0]))
	assert.Equal(t, "✓ ok    x   last", style.Strip(lines[1]))
	assert.Equal(t, "longer  yy  z", style.Strip(lines[2]))
}

func TestTableTruncatesFlexColumnToWidth(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 30
	tb := Table{Headers: []string{"KEY", "REASON"}, Flex: []int{1}, Rows: [][]string{
		{"PROJ-PLAN-1", "Manual run by someone with a very long name indeed"},
	}}
	require.NoError(t, tb.Render(o))
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		assert.LessOrEqual(t, style.Width(l), 30, l)
	}
	assert.Contains(t, buf.String(), "…")
	assert.Contains(t, buf.String(), "PROJ-PLAN-1", "keys are never cut")
}

func TestTableNeverTruncatesOnPipe(t *testing.T) {
	o, buf := testOut(false)
	o.Width = 30
	long := "Manual run by someone with a very long name indeed"
	require.NoError(t, Table{Headers: []string{"KEY", "REASON"}, Flex: []int{1}, Rows: [][]string{{"PROJ-PLAN-1", long}}}.Render(o))
	assert.Contains(t, buf.String(), long)
}

// A Flex cell that is a hyperlink stays a hyperlink to the same URL when the
// table cuts it to the terminal width, so a cut name is still clickable.
func TestTableKeepsHyperlinkOnCutCell(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 30
	o.Style = style.Mode{Links: true}
	url := "https://bamboo.example.com/browse/PROJ-LONG"
	cell := style.Link(o.Style, url, "A really long pipeline name that goes on and on")
	require.NoError(t, Table{Headers: []string{"PIPELINE", "USUAL"}, Flex: []int{0}, Rows: [][]string{{cell, "3m"}}}.Render(o))
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 2)
	assert.LessOrEqual(t, style.Width(lines[1]), 30, lines[1])
	assert.Contains(t, style.Strip(lines[1]), "…")
	assert.True(t, strings.HasPrefix(lines[1], "\x1b]8;;"+url+"\x1b\\A really"), "cut cell opens the plan link: %q", lines[1])
	assert.Contains(t, lines[1], "…\x1b]8;;\x1b\\", "the link closes after the cut text")
}
