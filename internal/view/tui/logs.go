package tui

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/provider"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// logState is the log screen. It keeps the raw lines as well as the viewport,
// so a search can find a match the viewport has scrolled past.
type logState struct {
	vp        viewport.Model
	title     string
	jobKey    string
	url       string
	all       bool
	multi     bool            // several jobs concatenated; no single job to follow
	build     *provider.Build // the build the logs came from, with its jobs
	offset    int             // the provider's next-read offset for the followed job
	lines     []string
	query     string
	matches   []int
	match     int
	line      int // the line search and next-failure last landed on
	following bool
	ready     bool
}

func (l *logState) setLines(width, height int, lines []string) {
	if !l.ready {
		l.vp = viewport.New(width, height)
		l.ready = true
	}
	l.vp.Width, l.vp.Height = width, height
	l.lines = lines
	l.vp.SetContent(strings.Join(lines, "\n"))
	l.vp.GotoBottom()
}

// resize reflows an already-loaded log for a new terminal size.
func (l *logState) resize(width, height int) {
	if !l.ready {
		return
	}
	l.vp.Width, l.vp.Height = width, height
	l.vp.SetContent(strings.Join(l.lines, "\n"))
}

// appendLines adds streamed lines and keeps the view pinned to the bottom
// only when the reader was already there.
func (l *logState) appendLines(more []string) {
	atBottom := l.vp.AtBottom()
	l.lines = append(l.lines, more...)
	l.vp.SetContent(strings.Join(l.lines, "\n"))
	if atBottom {
		l.vp.GotoBottom()
	}
}

// logsView is the whole terminal: a header, the viewport, a key line.
func (m Model) logsView() string {
	if m.height < 3 {
		return ""
	}
	source := "failed"
	if m.logs.all {
		source = "all"
	}
	shown := m.logs.vp.YOffset + m.logs.vp.Height
	if shown > len(m.logs.lines) {
		shown = len(m.logs.lines)
	}
	pos := fmt.Sprintf("%d/%d", shown, len(m.logs.lines))
	title := truncate(fmt.Sprintf("%s · logs · %s", m.logs.title, source), m.width-lipgloss.Width(pos)-2)
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(pos)
	if gap < 1 {
		gap = 1
	}
	head := titleStyle.Render(title) + strings.Repeat(" ", gap) + dimStyle.Render(pos)

	keyLine := "esc back  / search  G end  f follow  n next failure  o open"
	if m.logs.following {
		keyLine = "following · " + keyLine
	}
	foot := truncate(statusStyle.Render(keyLine), m.width)
	if m.inputFor != inputNone {
		foot = truncate(m.input.View(), m.width)
	}
	if m.err != nil {
		foot = truncate(errorStyle.Render(errorWhat(m.err)), m.width)
	}
	out := []string{head, m.logs.vp.View(), foot}
	if m.height >= 20 {
		planKey := ""
		if b, ok := m.logBuild(); ok {
			planKey = m.masterOf(b.PlanKey)
		}
		out = append([]string{m.nameLine(m.width, planKey)}, out...)
	}
	return strings.Join(out, "\n")
}

// search records every matching line and jumps to the first one. Matching is
// case-insensitive substring, the same rule the panel filters use.
func (l *logState) search(q string) {
	l.query = q
	l.matches = l.matches[:0]
	l.match = 0
	if q == "" {
		return
	}
	needle := strings.ToLower(q)
	for i, line := range l.lines {
		if strings.Contains(strings.ToLower(line), needle) {
			l.matches = append(l.matches, i)
		}
	}
	l.jumpToMatch()
}

// nextMatch walks the matches and wraps. With no search typed it falls back
// to the next failure, which is what the key line promises.
func (l *logState) nextMatch(delta int) {
	if len(l.matches) == 0 {
		l.nextFailure(delta)
		return
	}
	l.match = (l.match + delta + len(l.matches)) % len(l.matches)
	l.jumpToMatch()
}

func (l *logState) jumpToMatch() {
	if len(l.matches) == 0 {
		return
	}
	l.goTo(l.matches[l.match])
}

// goTo records the line landed on and scrolls to it. The line is tracked
// separately because the viewport clamps its offset when the log is shorter
// than the screen, and a search must still know where it is.
func (l *logState) goTo(i int) {
	l.line = i
	l.vp.SetYOffset(i)
}

// failureMarkers are what a failing job's log says. They are deliberately
// broad: the point is to land near the failure, not to parse it.
var failureMarkers = []string{"error", "failed", "failure", "exception", "exit code 1"}

func (l *logState) nextFailure(delta int) {
	n := len(l.lines)
	if n == 0 || delta == 0 {
		return
	}
	start := l.line
	for step := 1; step <= n; step++ {
		i := ((start+step*delta)%n + n) % n
		low := strings.ToLower(l.lines[i])
		for _, marker := range failureMarkers {
			if strings.Contains(low, marker) {
				l.goTo(i)
				return
			}
		}
	}
}
