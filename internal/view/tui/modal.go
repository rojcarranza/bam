package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// helpState is the scrollable key table.
type helpState struct {
	vp    viewport.Model
	ready bool
}

func (h *helpState) set(width, height int, body string) {
	if !h.ready {
		h.vp = viewport.New(width, height)
		h.ready = true
	}
	h.vp.Width, h.vp.Height = width, height
	h.vp.SetContent(body)
	h.vp.GotoTop()
}

// pickerItem is one row of any overlay list. Value is what choosing it means:
// a server alias, a project key or a branch plan key.
type pickerItem struct {
	Label  string
	Value  string
	Detail string
}

// A picker is narrow; the help table is as wide as the terminal allows, up
// to a readable line, and wraps the descriptions that are longer still.
const (
	pickerMaxWidth = 60
	helpMaxWidth   = 100
)

// overlayWidth is the outside width of the modal, never wider than the
// terminal.
func overlayWidth(termWidth, maxWidth int) int {
	w := termWidth - 8
	if w > maxWidth {
		w = maxWidth
	}
	if w < 12 {
		w = termWidth - 2
	}
	return w
}

// overlayBox draws the modal frame. body must already be sized to w-2.
func overlayBox(title string, w int, body string) string {
	if w < 4 {
		return ""
	}
	edge, color := panelEdge(true)
	return lipgloss.NewStyle().Border(edge).BorderForeground(color).Width(w - 2).
		Render(titleStyle.Render(truncate(title, w-2)) + "\n" + body)
}

func (m Model) pickerRows(width, height int) string {
	rows := m.picker.rows()
	start, end := m.picker.window(height)
	var b strings.Builder
	for i := start; i < end; i++ {
		// Truncate to the box's inner width: a long alias or URL would
		// otherwise make the modal wider than the terminal.
		row := rows[i].Label
		if rows[i].Detail != "" {
			row += "  " + dimStyle.Render(rows[i].Detail)
		}
		b.WriteString(m.cursorFor(true, m.picker.cursor == i))
		b.WriteString(truncate(row, width-2))
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m Model) overlayTitle() string {
	switch m.overlay {
	case overlayServers:
		return "Servers"
	case overlayProjects:
		return "Projects"
	case overlayBranches:
		return "Branches"
	case overlayHelp:
		return "Keys"
	case overlayError:
		return "Error"
	case overlayDryRun:
		return "Would send"
	case overlayConfirm:
		return "Confirm"
	}
	return ""
}

// helpBody is generated from the binding set, so a key cannot ship
// undocumented and the README table cannot drift. The group is a column
// rather than a heading row, because the whole table has to fit 24 rows.
func (m Model) helpBody(width, height int) string {
	const groupW, keysW = 5, 16
	var b strings.Builder
	group := ""
	for _, r := range append(keys.helpRows(), commandHelpRows()...) {
		label := ""
		if r.Group != group {
			label, group = r.Group, r.Group
		}
		// A long description wraps under its own column: cutting it would
		// hide the end of rows like esc's, wider than any help box.
		for i, d := range strings.Split(ansi.Wrap(r.Desc, width-groupW-keysW-3, ""), "\n") {
			if i > 0 {
				label, r.Keys = "", ""
			}
			fmt.Fprintf(&b, "%s %-*s %s\n",
				dimStyle.Render(fmt.Sprintf("%-*s", groupW, label)), keysW, r.Keys, d)
		}
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// helpView renders the key table through a viewport, because the table is
// taller than a 24-row terminal and the last rows must still be reachable.
func (m Model) helpView(width int) string {
	if !m.help.ready {
		return m.helpBody(width, 0)
	}
	return m.help.vp.View()
}

// helpHeight is the rows the overlay gives the table: the terminal minus the
// box's border, its title and the room lipgloss.Place needs.
func (m Model) helpHeight() int {
	h := m.height - 5
	if h < 1 {
		h = 1
	}
	return h
}

// errorBody is the What, the Why and the Try of an errs.Error. An auth error
// always names bam login, because the UI cannot exit 4 to say so.
func (m Model) errorBody(width int) string {
	if m.err == nil {
		return ""
	}
	lines := []string{errorStyle.Render(truncate(errorWhat(m.err), width-4))}
	var e *errs.Error
	if errors.As(m.err, &e) {
		// Why and Try wrap: the reason is the part worth reading, and a
		// Bamboo refusal is often longer than the box is wide.
		wrap := lipgloss.NewStyle().Width(width - 4)
		if e.Why != "" {
			lines = append(lines, "", dimStyle.Render(wrap.Render(e.Why)))
		}
		try := e.Try
		if try == "" && e.Kind == errs.KindAuth {
			try = "run bam login for this server"
		}
		if try != "" {
			lines = append(lines, "", wrap.Render(try))
		}
	}
	return strings.Join(lines, "\n")
}

// errorWhat is the one-line What of an errs.Error, or the plain message.
func errorWhat(err error) string {
	var e *errs.Error
	if errors.As(err, &e) && e.What != "" {
		return e.What
	}
	return err.Error()
}

// overlayView centres the modal over the columns. It replaces the base rather
// than drawing on top of it, so no line can end up wider than the terminal.
func (m Model) overlayView(base string) string {
	if m.overlay == overlayNone {
		return base
	}
	maxW := pickerMaxWidth
	if m.overlay == overlayHelp {
		maxW = helpMaxWidth
	}
	w := overlayWidth(m.width, maxW)

	var body string
	switch m.overlay {
	case overlayHelp:
		body = m.helpView(w - 2)
	case overlayError:
		body = m.errorBody(w - 2)
	case overlayDryRun:
		body = m.dryRunBody(w - 2)
	case overlayConfirm:
		body = m.confirmBody(w - 2)
	default:
		body = m.pickerRows(w-2, m.height/2)
	}
	box := overlayBox(m.overlayTitle(), w, body)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// indexOf is where value sits in items, or 0 when it is not there.
func indexOf(items []pickerItem, value string) int {
	for i, it := range items {
		if it.Value == value {
			return i
		}
	}
	return 0
}
