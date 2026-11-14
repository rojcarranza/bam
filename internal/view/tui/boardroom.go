package tui

import (
	"fmt"
	"strings"
)

const (
	// roomyRows is the terminal height from which the board gives every panel
	// empty rows and columns around its lines; below it the board lays out
	// tightly, as it always has.
	roomyRows = 40
	// wideCols is the terminal width from which the board uses two columns.
	wideCols = 160
	// roomyPad is the empty columns between a panel's border and its text.
	roomyPad = 2
	// roomyFrame is the rows a panel spends on its border and its two empty
	// rows; a panel of height h shows h-roomyFrame lines.
	roomyFrame = 4
	// roomyMinPanel is the least height of a panel that still shows a line.
	roomyMinPanel = roomyFrame + 1
)

// boardLayout is how the roomy board shares the terminal: heights of the
// four panels, and the widths of its columns (equal in one column).
type boardLayout struct {
	wide                       bool
	leftW, rightW              int
	needs, agents, queue, pins int
}

// boardSizes shares h body rows (the terminal less its header and status bar)
// and w columns. The agents panel takes the rows its lines need. Needs you
// shows all its lines until it fills a third of what is left, then scrolls.
// In one column the queue takes its lines up to a third of that and Pinned
// the rest, never less than a third; in two columns the left column stacks
// Needs you, the agents and the queue, which takes the spare rows, and Pinned
// fills the right column.
func boardSizes(w, h, needsLines, agentLines, queueLines int) boardLayout {
	l := boardLayout{wide: w >= wideCols, leftW: w, rightW: w}
	if l.wide {
		l.leftW = w * 45 / 100
		l.rightW = w - l.leftW
	}
	l.agents = min(agentLines+roomyFrame, h-3*roomyMinPanel)
	rest := h - l.agents
	third := max(rest/3, roomyMinPanel+2)
	l.needs = min(needsLines+roomyFrame, third)
	if l.wide {
		l.queue = rest - l.needs
		l.pins = h
		return l
	}
	l.queue = min(queueLines+roomyFrame, third)
	l.queue = max(l.queue, roomyMinPanel)
	l.pins = rest - l.needs - l.queue
	return l
}

// roomyPanel draws a panel with an empty row above and below its lines and
// roomyPad empty columns inside each side, scrolled so the cursor line shows.
func roomyPanel(title string, focused bool, w, h int, body []string, cursor int) string {
	body = window(body, cursor, h-roomyFrame)
	lines := []string{""}
	for _, l := range body {
		lines = append(lines, strings.Repeat(" ", roomyPad)+truncate(l, w-2-2*roomyPad))
	}
	lines = append(lines, "")
	return panel(title, focused, w, h, strings.Join(lines, "\n"))
}

// roomyBoxes is the roomy board's four panels.
func (m Model) roomyBoxes() []boardBox {
	b := &m.board
	h := m.height - 2
	wide := m.width >= wideCols
	leftW := m.width
	if wide {
		leftW = m.width * 45 / 100
	}
	needs := m.needsLines(leftW - 2 - 2*roomyPad)
	const awayMax = 4
	agents := m.agentLines(leftW-2-2*roomyPad, awayMax)
	queue, queueCursor := m.queueLines(leftW - 2 - 2*roomyPad)
	sz := boardSizes(m.width, h, len(needs), len(agents), len(queue))

	needsTitle := "Needs you"
	if b.have && len(b.data.Needs) > 0 {
		if shown := sz.needs - roomyFrame; shown < len(needs) {
			needsTitle = fmt.Sprintf("Needs you · %d of %d", shown, len(needs))
		} else {
			needsTitle = fmt.Sprintf("Needs you · %d", len(needs))
		}
	}
	pinW := sz.rightW - 2 - 2*roomyPad
	pins, pinCursor, pinInfo := m.pinLinesInfo(pinW, true, sz.pins-roomyFrame)

	boxes := []boardBox{
		{panel: boardNeeds, title: needsTitle, w: sz.leftW, h: sz.needs, lines: needs, cursor: b.cursor[boardNeeds]},
		{panel: boardAgents, title: m.agentsTitle(), w: sz.leftW, h: sz.agents, lines: agents, cursor: m.agentCursorLine()},
		{panel: boardQueue, title: m.queueTitle(), w: sz.leftW, h: sz.queue, lines: queue, cursor: queueCursor},
		{panel: boardPins, title: "Pinned", w: sz.rightW, h: sz.pins, lines: pins, cursor: pinCursor, info: pinInfo},
	}
	y := 0
	for i := range boxes {
		boxes[i].roomy = true
		if i < 3 {
			boxes[i].y = y
			y += boxes[i].h
		}
	}
	if sz.wide {
		boxes[3].x = sz.leftW
	} else {
		boxes[3].y = y
	}
	return boxes
}
