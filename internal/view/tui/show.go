package tui

import tea "github.com/charmbracelet/bubbletea"

// show is the only writer of the screen. It also owns each screen's refresh
// loops: leaving a screen ends its loops at once, by bumping the generation
// their beats carry, and showing a screen starts the loops it owns. The
// commands returned are those loops' first beats; the caller batches them
// with its own. A screen with no loop returns nil, so a caller may ignore it.
//
//	Home   the plans refresh, every homeRefreshEvery
//	Board  the read beat and the redraw beat
//	Set    the poll of a set another bam holds
//
// The board's first read and the set's load belong to whoever opens the
// screen: they read what that opening wants, which is not always everything.
func (m *Model) show(s screen) tea.Cmd {
	if m.screen != s {
		m.endLoops(m.screen)
	}
	m.screen = s
	switch s {
	case screenHome:
		return m.restartHomeTick()
	case screenBoard:
		m.board.tickGen++
		return boardBeats(m.board.tickGen)
	case screenSet:
		return m.restartSetPoll()
	}
	return nil
}

// endLoops stops the loops screen s owns: their next beat is dropped.
func (m *Model) endLoops(s screen) {
	switch s {
	case screenHome:
		m.home.tickGen++
	case screenBoard:
		m.board.tickGen++
	case screenSet:
		m.setPollGen++
	}
}
