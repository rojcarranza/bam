package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// panel draws one bordered box with its title set into the top border, the
// way the spec's layout draws it. lipgloss v1 has no border title, so the top
// edge is drawn by hand and the box is rendered without one.
func panel(title string, focused bool, width, height int, body string) string {
	if width < 6 || height < 3 {
		return ""
	}
	edge, color := panelEdge(focused)
	inner := width - 2

	label := truncate(title, inner-2)
	fill := inner - 1 - lipgloss.Width(label)
	if fill < 0 {
		fill = 0
	}
	top := lipgloss.NewStyle().Foreground(color).Render(
		edge.TopLeft+edge.Top) + titleStyle.Render(label) +
		lipgloss.NewStyle().Foreground(color).Render(strings.Repeat(edge.Top, fill)+edge.TopRight)

	lines := strings.Split(body, "\n")
	for len(lines) < height-2 {
		lines = append(lines, "")
	}
	lines = lines[:height-2]
	for i, l := range lines {
		lines[i] = lipgloss.NewStyle().Width(inner).MaxWidth(inner).Render(l)
	}
	box := lipgloss.NewStyle().
		Border(edge, false, true, true, true).
		BorderForeground(color).
		Width(inner).
		Render(strings.Join(lines, "\n"))

	return top + "\n" + box
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// cursorFor marks the selected row, and marks it differently when its panel
// does not have focus, so there is never more than one live cursor.
func (m Model) cursorFor(focused, selected bool) string {
	switch {
	case selected && focused:
		return cursorStyle.Render("▸") + " "
	case selected:
		return dimStyle.Render("▸") + " "
	}
	return "  "
}

// ago is relative time against the model's clock, so goldens are stable.
func (m Model) ago(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return view.Ago(m.now().Sub(t))
}

func branchOrDefault(b provider.Build) string {
	if b.Branch == "" {
		return "default"
	}
	return b.Branch
}

// keyLine is the reminder on the right of the status bar. It follows the
// screen, because the keys do.
func (m Model) keyLine() string {
	if m.screen == screenForm {
		// The f hint replaces "tab next" and "d dry-run" rather than adding
		// to them: a key line longer than the bar's free space is dropped
		// whole, and at 80 columns the longer line would not fit.
		if m.form.onRevision() && m.form.fromRevision != "" && !m.form.loading {
			return "f use " + app.BuildLabel(m.form.fromBuild) + "'s revision  enter edit  ^R run  esc cancel"
		}
		return "enter edit  tab next  ^R run  d dry-run  esc cancel"
	}
	if m.screen == screenSetVars {
		return "enter edit  a add  x remove  d dry-run  ^R " + strings.ToLower(m.setVars.action.verb()) + "  esc cancel"
	}
	if m.screen == screenSet {
		// o and y work here too but stay in ? help: with them the longest
		// line does not fit an 80-column bar, which drops the line whole.
		parts := []string{"enter build"}
		run, resume, rerun := m.setActions()
		if run {
			parts = append(parts, "R run")
		}
		if resume {
			parts = append(parts, "U resume")
		}
		if rerun {
			parts = append(parts, "F rerun failed")
		}
		return strings.Join(append(parts, "r refresh", "esc back"), "  ")
	}
	if m.screen == screenTree {
		parts := []string{"enter build", "l logs"}
		if m.treeRerunnable() {
			parts = append(parts, "F rerun")
		}
		return strings.Join(append(parts, "r refresh", "esc back"), "  ")
	}
	if m.screen == screenHome {
		if m.home.view == homeProjects {
			return "enter plans  / filter  s sort  : cmd  ?help  q quit"
		}
		return "enter open  / filter  s sort  : cmd  R run  ?help  q quit"
	}
	switch m.focus {
	case focusStages:
		return "enter expand  l logs  R run  t tree  esc builds"
	case focusTree:
		return "enter open  t tree  l logs  esc builds"
	}
	return "enter stages  R run  l logs  t tree  / filter  esc back"
}

// statusBar is one line: where we are on the left, what to press on the
// right. An error takes over the left half until it is dismissed.
func (m Model) statusBar(width int) string {
	if m.inputFor != inputNone {
		line := m.input.View()
		if m.inputFor == inputCommand {
			if cands := m.complete(m.input.Value()); len(cands) > 0 {
				line += "   " + dimStyle.Render(strings.Join(cands[:min(len(cands), 5)], "  "))
			}
		}
		return truncate(line, width)
	}
	left := fmt.Sprintf("%s · %s · %s", m.server, m.info.Version, m.user.Name)
	switch {
	case m.err != nil:
		left = errorStyle.Render(errorWhat(m.err))
	case m.status != "":
		left = m.status
	}
	right := m.keyLine()
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return truncate(left, width)
	}
	return left + strings.Repeat(" ", gap) + statusStyle.Render(right)
}
