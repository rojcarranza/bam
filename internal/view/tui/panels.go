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

// leftWidth is a quarter of the terminal, clamped so the panels stay
// readable on a narrow terminal and do not swallow a wide one.
func leftWidth(total int) int {
	w := total / 4
	switch {
	case w < 20:
		w = 20
	case w > 40:
		w = 40
	}
	if w > total-20 {
		w = total - 20
	}
	if w < 0 {
		w = 0
	}
	return w
}

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

func (m Model) planRows(width, height int) string {
	rows := m.plans.rows()
	start, end := m.plans.window(height)
	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(m.cursorFor(m.focus == focusPlans, m.plans.cursor == i))
		// Keys differ by a letter or two and say nothing about the plan, so
		// the name follows whenever the panel has room for some of it.
		p := rows[i]
		label := p.Key
		if w := width - 6 - lipgloss.Width(label); w > 6 && p.Name != "" {
			label += dimStyle.Render("  " + truncate(p.Name, w))
		}
		label = truncate(label, width-4)
		if m.plans.cursor == i {
			label = selectedRow(label, width-4, m.focus == focusPlans)
		}
		b.WriteString(label)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m Model) buildRows(width, height int) string {
	rows := m.builds.rows()
	start, end := m.builds.window(height)
	var b strings.Builder
	for i := start; i < end; i++ {
		bd := rows[i]
		b.WriteString(m.cursorFor(m.focus == focusBuilds, m.builds.cursor == i))
		line := stateStyle(bd.State).Render(stateGlyph(bd.State)) +
			fmt.Sprintf(" #%d ", bd.Number) + dimStyle.Render(m.ago(bd.FinishedAt))
		if m.builds.cursor == i {
			line = selectedRow(line, width-4, m.focus == focusBuilds)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// ago is relative time against the model's clock, so goldens are stable.
func (m Model) ago(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return view.Ago(m.now().Sub(t))
}

func (m Model) presetRows(width, height int) string {
	rows := m.presets.rows()
	start, end := m.presets.window(height)
	var b strings.Builder
	for i := start; i < end; i++ {
		t := rows[i]
		b.WriteString(m.cursorFor(m.focus == focusPresets, m.presets.cursor == i))
		// Name, plan and branch: without the branch, two presets on the same
		// plan look identical and the user cannot tell what enter will open.
		detail := t.Plan
		if t.Branch != "" {
			detail += " @" + t.Branch
		}
		label := t.Name
		if w := width - 6 - lipgloss.Width(label); w > 6 && detail != "" {
			label += dimStyle.Render("  " + truncate(detail, w))
		}
		label = truncate(label, width-4)
		if m.presets.cursor == i {
			label = selectedRow(label, width-4, m.focus == focusPresets)
		}
		b.WriteString(label)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m Model) leftColumn(width, height int) string {
	each := height / 3
	rest := height - 2*each
	return lipgloss.JoinVertical(lipgloss.Left,
		panel("1 Plans", m.focus == focusPlans, width, each, m.planRows(width, each-2)),
		panel(m.namedTitle("2 Builds", m.buildsPlan), m.focus == focusBuilds, width, each, m.buildRows(width, each-2)),
		panel("3 Presets", m.focus == focusPresets, width, rest, m.presetRows(width, rest-2)),
	)
}

// mainPanel shows what the focused left panel points at: the open build's
// detail, otherwise the plan the Plans cursor is on, otherwise a hint about
// the build the Builds cursor is on.
func (m Model) mainPanel(width, height int) string {
	if m.detail != nil {
		return panel(m.namedTitle(m.detail.Key, m.detail.PlanKey), m.focus == focusMain, width, height,
			m.detailBody(width-2, height-2))
	}
	if p, ok := m.plans.selected(); ok && m.focus == focusPlans {
		return panel(m.namedTitle(p.Key, p.Key), false, width, height, m.planSummary(p, width-2))
	}
	if b, ok := m.builds.selected(); ok {
		return panel(m.namedTitle(b.Key, b.PlanKey), m.focus == focusMain, width, height,
			dimStyle.Render("press enter to open this build"))
	}
	return panel("bam", m.focus == focusMain, width, height, dimStyle.Render("select a plan"))
}

// namedTitle follows a panel title with the name of the plan it is about:
// the key alone rarely says which plan that is.
func (m Model) namedTitle(title, planKey string) string {
	if name := m.planNameOf(planKey); name != "" {
		return title + " · " + name
	}
	return title
}

// planSummary is the main panel while the Plans cursor moves. Moving it loads
// nothing, so this says which plan enter would open, with the whole name.
func (m Model) planSummary(p provider.Plan, width int) string {
	var lines []string
	if p.Name != "" {
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(p.Name))
	}
	last := "no builds yet"
	if lb := p.LastBuild; lb != nil {
		last = fmt.Sprintf("last build %s #%d %s",
			stateCell(lb.State), lb.Number, m.ago(lb.FinishedAt))
	}
	if p.ProjectKey != "" {
		last = p.ProjectKey + " · " + last
	}
	lines = append(lines, last, "", dimStyle.Render("press enter to show its builds"))
	return strings.Join(lines, "\n")
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
	if m.screen == screenHome {
		return "enter open  / filter  s sort  : cmd  R run  ?help  q quit"
	}
	return "?help  : cmd  tab focus  l logs  o open  q quit"
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
