package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/stretchr/testify/require"
)

// TestStateStyleCoversEveryState keeps style the single definition: every
// state the provider can report must have a colour here and a glyph there.
func TestStateStyleCoversEveryState(t *testing.T) {
	for _, s := range provider.AllStates() {
		require.NotEmpty(t, stateStyle(s).GetForeground(), "no colour for %s", s)
		require.Equal(t, style.Glyph(s), stateGlyph(s), "glyph for %s must come from style", s)
	}
}

// TestStateCellReadsAsGlyphPlusLabel guards the rule that a state is never
// signalled by colour alone.
func TestStateCellReadsAsGlyphPlusLabel(t *testing.T) {
	cell := stateCell(provider.StateFailed)
	require.Contains(t, cell, style.Glyph(provider.StateFailed))
	require.Contains(t, cell, "failed")
}

// TestUnknownStateFallsBack mirrors style.Glyph's own fallback.
func TestUnknownStateFallsBack(t *testing.T) {
	require.Equal(t, style.Glyph(provider.StateUnknown), stateGlyph(provider.State("nonsense")))
}

// forceColour turns on ANSI output for one test. Tests otherwise render
// without colour, as the goldens do.
func forceColour(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestSelectedRowIsOneSolidBar(t *testing.T) {
	forceColour(t)
	row := stateCell(provider.StateFailed) + "  PROJ-BUILD"
	got := selectedRow(row, 30, true)
	require.Equal(t, 2, strings.Count(got, "\x1b["), "one style start, one reset: no colour of the state's own inside the bar")
	require.Equal(t, 30, lipgloss.Width(got), "the bar spans the width it is given")
	require.True(t, strings.HasPrefix(ansi.Strip(got), "✗ failed  PROJ-BUILD"), "the glyph and label stay")
}

func TestSelectedRowDiffersWithoutFocus(t *testing.T) {
	forceColour(t)
	require.NotEqual(t, selectedRow("PROJ-BUILD", 12, true), selectedRow("PROJ-BUILD", 12, false))
}
