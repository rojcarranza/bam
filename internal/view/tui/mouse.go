package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// wheelRows is how many rows one wheel notch moves a panel's cursor. Every
// panel's window follows its cursor, so scrolling is moving it.
const wheelRows = 3

// panelHit is what sits under the pointer: a panel and, when the pointer is
// on one of its rows, that row's index in the panel's list.
type panelHit struct {
	focus focus
	row   int // -1 when the pointer is on the panel but not on a row
}

// handleMouse is the mouse: a left click selects the row under the pointer
// and focuses its panel; the wheel scrolls the panel under the pointer,
// whichever panel has focus, and focuses it. Anything that already owns the keys (a prompt,
// an overlay, the form and the other screens) ignores the mouse, so a click
// never reaches through it to a table behind. The log screen takes the wheel.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.inputFor != inputNone {
		return m, nil
	}
	if m.overlay == overlayHelp || m.overlay == overlayInfo {
		return m.wheelOverlay(msg)
	}
	if m.overlay != overlayNone {
		return m, nil
	}
	wheel := 0
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelUp:
		wheel = -wheelRows
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonWheelDown:
		wheel = wheelRows
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
	default:
		return m, nil
	}
	switch m.screen {
	case screenLogs:
		if wheel == 0 {
			return m, nil
		}
		before := m.logs.vp.YOffset
		var cmd tea.Cmd
		m.logs.vp, cmd = m.logs.vp.Update(msg)
		if m.logs.vp.YOffset != before || wheel > 0 {
			m.logs.pinned = m.logs.vp.AtBottom()
		}
		return m, cmd
	case screenBoard:
		return m.boardMouse(msg, wheel)
	case screenHome, screenPlan:
	default:
		return m, nil
	}
	hit, ok := m.panelAt(msg.X, msg.Y)
	if !ok {
		return m, nil
	}
	// Both focus the panel: the keys then act on what the pointer just did,
	// and the Builds panel's cursor move opens its build as a key would.
	m.focus = hit.focus
	switch {
	case wheel != 0:
		m.movePanel(hit.focus, wheel)
	case hit.row >= 0:
		m.selectRow(hit.focus, hit.row)
	default:
		return m, nil // a click on a border or an empty line only focuses
	}
	rest := m.restCmd()
	return m, rest
}

// boardMouse is the mouse on the live board: the wheel moves the cursor of
// the panel under the pointer, a click selects the line under it, and a click
// on an underlined name or build number of Pinned also opens that page in
// Bamboo, through the same opener o uses. The board writes no terminal link
// itself, so a terminal that opens links on its own cannot open it twice.
func (m Model) boardMouse(msg tea.MouseMsg, wheel int) (tea.Model, tea.Cmd) {
	if m.boardTooSmall() {
		return m, nil
	}
	bx, line, col, ok := m.boardHit(msg.X, msg.Y)
	if !ok || (bx.panel == boardAgents && m.board.data.Mine == nil) {
		return m, nil
	}
	b := &m.board
	b.panel = bx.panel
	if wheel != 0 {
		b.move(wheel)
		return m, nil
	}
	if line < 0 {
		return m, nil // a border, an empty row or the space under the lines only focuses
	}
	row := -1
	switch bx.panel {
	case boardNeeds:
		if b.have && line < len(b.data.Needs) {
			row = line
		}
	case boardAgents:
		_, selLine := m.mineLines(bx.w - 2 - 2*roomyPad)
		if i := slices.Index(selLine, line); i >= 0 {
			row = i
		}
	case boardPins:
		if line < len(bx.info) {
			row = bx.info[line].ord
		}
	}
	if row < 0 {
		return m, nil
	}
	b.cursor[bx.panel] = row
	b.clamp()
	if bx.panel == boardPins {
		for _, sp := range bx.info[line].spans {
			if col >= sp.x0 && col < sp.x1 {
				return m, m.openURLCmd(m.pageURL(sp.key))
			}
		}
	}
	return m, nil
}

// boardHit finds the panel under cell (x, y) of the board, the index of the
// line of that panel there (-1 when it is on a border, an empty row or past
// the last line) and the column within that line.
func (m Model) boardHit(x, y int) (bx boardBox, line, col int, ok bool) {
	for _, bx = range m.boardBoxes() {
		if x < bx.x || x >= bx.x+bx.w || y-1 < bx.y || y-1 >= bx.y+bx.h {
			continue
		}
		tx, ty, n := bx.text()
		col, rel := x-tx, y-1-ty
		if col < 0 || rel < 0 || rel >= n || rel >= len(bx.lines) {
			return bx, -1, 0, true
		}
		return bx, windowStart(len(bx.lines), bx.cursor, n) + rel, col, true
	}
	return boardBox{}, -1, 0, false
}

// wheelOverlay scrolls the help or stats overlay when the wheel is over its
// box. A click, or a wheel outside the box, does nothing: the screen behind
// stays out of reach, as it is for keys.
func (m Model) wheelOverlay(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress ||
		(msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown) {
		return m, nil
	}
	pane := &m.help
	if m.overlay == overlayInfo {
		pane = &m.infoPane.vp
	}
	// The box is drawn centred by overlayView; find its rectangle the same way.
	w := overlayWidth(m.width, helpMaxWidth)
	box := overlayBox(m.overlayTitle(), w, pane.vp.View())
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	x0, y0 := (m.width-bw+1)/2, (m.height-bh+1)/2
	if msg.X < x0 || msg.X >= x0+bw || msg.Y < y0 || msg.Y >= y0+bh {
		return m, nil
	}
	var cmd tea.Cmd
	pane.vp, cmd = pane.vp.Update(msg)
	return m, cmd
}

// panelAt finds the panel under cell (x, y), laid out as homeView and
// planView lay it out.
func (m Model) panelAt(x, y int) (panelHit, bool) {
	if x < 0 || y < 0 || x >= m.width || y >= m.height {
		return panelHit{}, false
	}
	if m.screen == screenHome {
		return m.homeHit(y)
	}
	return m.planHit(x, y)
}

// rowAt is the list row index for a pointer rel lines below a window's first
// row, or -1 when that is past the last row.
func rowAt(start, rel, n int) int {
	if rel < 0 || start+rel >= n {
		return -1
	}
	return start + rel
}

func (m Model) homeHit(y int) (panelHit, bool) {
	head := len(m.homeChrome())
	body := max(m.height-head-1, 0)
	if y < head || y >= head+body {
		return panelHit{}, false
	}
	// The first body line is the column header, so rows start one below it.
	rel := y - head - 1
	h := body - 1
	f := m.homeFocus()
	var start, n int
	switch f {
	case focusSets:
		start, _ = m.sets.window(h)
		n = m.sets.len()
	case focusProjects:
		start, _ = m.projects.window(h)
		n = m.projects.len()
	case focusPresets:
		start, _ = m.presets.window(h)
		n = m.presets.len()
	default:
		start, _ = m.plans.window(h)
		n = m.plans.len()
	}
	return panelHit{f, rowAt(start, rel, n)}, true
}

func (m Model) planHit(x, y int) (panelHit, bool) {
	head := len(m.planHeader(m.width))
	body := max(m.height-head-1, 0)
	if y < head || y >= head+body {
		return panelHit{}, false
	}
	top, bottom := head, head+body
	// A panel's first and last lines are its borders, so its rows start one
	// below its top.
	f := focusBuilds
	switch {
	case m.planCompact():
		if m.focus == focusStages || m.focus == focusTree {
			f = m.focus
		}
	case x >= planLeftWidth(m.width):
		sh, _ := m.rightHeights(body)
		f = focusStages
		if y >= head+sh {
			f, top = focusTree, head+sh
		} else {
			bottom = head + sh
		}
	}
	rel, h := y-top-1, bottom-top-2
	if rel < 0 || rel >= h {
		return panelHit{f, -1}, true
	}
	switch f {
	case focusStages:
		if m.detail == nil {
			return panelHit{f, -1}, true
		}
		// The reason line and its blank line come first, the failed tests last.
		rows := len(m.treeRows())
		start, _ := treeWindow(m.treeCursor, rows, h-2-failedTestLines(*m.detail))
		return panelHit{f, rowAt(start, rel-2, rows)}, true
	case focusTree:
		rows := len(m.treePreviewRows())
		if m.detail == nil || m.plan.tree.loading || m.plan.tree.err != nil {
			rows = 0
		}
		start, _ := treeWindow(m.plan.treeCursor, rows, h)
		return panelHit{f, rowAt(start, rel, rows)}, true
	}
	start, _ := m.builds.window(h)
	return panelHit{f, rowAt(start, rel, m.builds.len())}, true
}

// selectRow puts panel f's cursor on row.
func (m *Model) selectRow(f focus, row int) {
	switch f {
	case focusPlans:
		m.plans.cursor = row
	case focusBuilds:
		m.builds.cursor = row
	case focusPresets:
		m.presets.cursor = row
	case focusSets:
		m.sets.cursor = row
	case focusProjects:
		m.projects.cursor = row
	case focusStages:
		m.treeCursor = row
		m.clampTree()
	case focusTree:
		m.plan.treeCursor = row
		m.clampTreePreview()
	}
}
