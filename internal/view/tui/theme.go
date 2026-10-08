package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// stateColors repeats style's ANSI numbers as lipgloss colours. style stays
// the definition of what a state looks like; this is the only adapter, and
// theme_test.go fails if a state is added there without one here.
var stateColors = map[provider.State]lipgloss.Color{
	provider.StateSuccess:  lipgloss.Color("2"), // green
	provider.StateFailed:   lipgloss.Color("1"), // red
	provider.StateRunning:  lipgloss.Color("6"), // cyan
	provider.StateQueued:   lipgloss.Color("3"), // yellow
	provider.StateStopped:  lipgloss.Color("8"), // grey
	provider.StateNotBuilt: lipgloss.Color("8"), // dim
	provider.StateSkipped:  lipgloss.Color("8"), // dim
	provider.StateUnknown:  lipgloss.Color("5"), // magenta
}

func stateStyle(s provider.State) lipgloss.Style {
	c, ok := stateColors[s]
	if !ok {
		c = stateColors[provider.StateUnknown]
	}
	return lipgloss.NewStyle().Foreground(c)
}

// stateGlyph defers to style so there is one glyph table in the binary.
func stateGlyph(s provider.State) string { return style.Glyph(s) }

// stateCell is glyph plus label. A state is never signalled by colour alone.
func stateCell(s provider.State) string {
	return stateStyle(s).Render(style.Glyph(s) + " " + style.Label(s))
}

// panelEdge is how a panel's border looks. The focused panel is drawn with a
// thick border rather than only a brighter one, so focus is visible on a
// terminal with no colour and in the goldens, which lipgloss renders
// without colour.
func panelEdge(focused bool) (lipgloss.Border, lipgloss.Color) {
	if focused {
		return lipgloss.ThickBorder(), lipgloss.Color("4")
	}
	return lipgloss.RoundedBorder(), lipgloss.Color("8")
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))

	hintKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)

// selectedRowStyle is the cursor row's bar, as k9s draws it: black on cyan
// where the keys go, light on grey in a panel without focus, so the user
// still sees where they were.
func selectedRowStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("8"))
}

// selectedRow draws s as the cursor row's bar, width cells wide. Any colour
// s carries is dropped first: a state's own colour ends in a reset, which
// would cut the bar in two.
func selectedRow(s string, width int, focused bool) string {
	plain := ansi.Strip(s)
	if pad := width - lipgloss.Width(plain); pad > 0 {
		plain += strings.Repeat(" ", pad)
	}
	return selectedRowStyle(focused).Render(plain)
}
