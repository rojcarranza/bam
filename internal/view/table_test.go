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
