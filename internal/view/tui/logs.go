package tui

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"

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
	multi     bool            // several jobs: every line carries its job's prefix
	stage     string          // the stage a stage screen follows; empty otherwise
	live      provider.State  // the followed stage's (or build's) state; empty when not live
	build     *provider.Build // the build the logs came from, with its jobs
	offsets   map[string]int  // the provider's next-read offset per job on screen
	lines     []string
	rendered  []string // lines with the search highlighted, one per line
	query     string
	matches   []int
	match     int
	line      int // the line search and next-failure last landed on
	following bool
	pinned    bool // the view stays at the bottom as lines arrive
	ready     bool
}

// waitingLine fills a followed log that has no line yet. It is drawn, not
// stored, so search never matches it.
const waitingLine = "waiting for jobs to start"

func (l *logState) setLines(width, height int, lines []string) {
	if !l.ready {
		l.vp = viewport.New(width, height)
		l.ready = true
	}
	l.vp.Width, l.vp.Height = width, height
	l.lines = lines
	l.rerender()
	l.pinned = true
	l.vp.GotoBottom()
}

// resize reflows an already-loaded log for a new terminal size.
func (l *logState) resize(width, height int) {
	if !l.ready {
		return
	}
	l.vp.Width, l.vp.Height = width, height
	l.refresh()
	if l.pinned {
		l.vp.GotoBottom()
	}
}

// rerender highlights every line again, for a new query or a new log.
func (l *logState) rerender() {
	l.rendered = make([]string, len(l.lines))
	for i, line := range l.lines {
		l.rendered[i] = highlight(line, l.query)
	}
	l.refresh()
}

// refresh hands the rendered lines to the viewport.
func (l *logState) refresh() {
	if !l.ready {
		return
	}
	if len(l.lines) == 0 && l.following {
		l.vp.SetContent(dimStyle.Render(waitingLine))
		return
	}
	l.vp.SetContent(strings.Join(l.rendered, "\n"))
}

// appendLines adds streamed lines, matches them against the search, and
// keeps the view at the bottom only while it is pinned.
func (l *logState) appendLines(more []string) {
	start := len(l.lines)
	l.lines = append(l.lines, more...)
	for i, line := range more {
		l.rendered = append(l.rendered, highlight(line, l.query))
		if l.query != "" && matchesQuery(line, l.query) {
			l.matches = append(l.matches, start+i)
		}
	}
	l.refresh()
	if l.pinned {
		l.vp.GotoBottom()
	}
}

// logsView is the whole terminal: a header, the viewport, a key line.
func (m Model) logsView() string {
	if m.height < 3 {
		return ""
	}
	shown := m.logs.vp.YOffset + m.logs.vp.Height
	if shown > len(m.logs.lines) {
		shown = len(m.logs.lines)
	}
	pos := fmt.Sprintf("%d/%d", shown, len(m.logs.lines))
	parts := []string{m.logs.title}
	if m.logs.stage != "" {
		parts = append(parts, m.logs.stage)
	} else {
		source := "failed"
		if m.logs.all {
			source = "all"
		}
		parts = append(parts, "logs", source)
	}
	if m.logs.live != "" {
		parts = append(parts, style.Label(m.logs.live))
	}
	if m.logs.query != "" {
		cur := 0
		if len(m.logs.matches) > 0 {
			cur = m.logs.match + 1
		}
		parts = append(parts, fmt.Sprintf("match %d/%d", cur, len(m.logs.matches)))
	}
	title := truncate(strings.Join(parts, " · "), m.width-lipgloss.Width(pos)-2)
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(pos)
	if gap < 1 {
		gap = 1
	}
	head := titleStyle.Render(title) + strings.Repeat(" ", gap) + dimStyle.Render(pos)

	nKey := "n next failure"
	if m.logs.query != "" {
		nKey = "n next match"
	}
	keyLine := "esc back  / search  G end  f follow  " + nKey + "  o open"
	if m.logs.following {
		keyLine = "following · " + keyLine
	}
	foot := statusStyle.Render(fitHints(keyLine, m.width))
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
	if q != "" {
		for i, line := range l.lines {
			if matchesQuery(line, q) {
				l.matches = append(l.matches, i)
			}
		}
	}
	l.rerender()
	l.jumpToMatch()
}

func matchesQuery(line, q string) bool {
	return strings.Contains(strings.ToLower(line), strings.ToLower(q))
}

// nextMatch walks the matches and wraps. With no search typed it falls back
// to the next failure, which is what the key line promises.
func (l *logState) nextMatch(delta int) {
	if l.query != "" && len(l.matches) == 0 {
		return
	}
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
// than the screen, and a search must still know where it is. Landing anywhere
// unpins: the next chunk must not scroll the reader away from it.
func (l *logState) goTo(i int) {
	l.line = i
	l.vp.SetYOffset(i)
	l.pinned = false
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

// highlightSpans is where q appears in line, as byte ranges of line. They are
// found with an ASCII-only case fold, which keeps the offsets valid for line
// itself. When the matching rule finds the line but the fold cannot place it
// (a non-ASCII case difference), the whole line is the span.
func highlightSpans(line, q string) [][2]int {
	if q == "" || !matchesQuery(line, q) {
		return nil
	}
	hay, needle := asciiLower(line), asciiLower(q)
	var out [][2]int
	for from := 0; ; {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		out = append(out, [2]int{from + i, from + i + len(needle)})
		from += i + len(needle)
	}
	if len(out) == 0 {
		return [][2]int{{0, len(line)}}
	}
	return out
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// highlight draws line with every match of q in matchStyle.
func highlight(line, q string) string {
	spans := highlightSpans(line, q)
	if len(spans) == 0 {
		return line
	}
	var sb strings.Builder
	last := 0
	for _, s := range spans {
		sb.WriteString(line[last:s[0]])
		sb.WriteString(matchStyle.Render(line[s[0]:s[1]]))
		last = s[1]
	}
	sb.WriteString(line[last:])
	return sb.String()
}

// apply folds one follow event into the screen.
func (l *logState) apply(ev app.LogEvent) {
	switch ev.Kind {
	case app.LogStatus:
		l.setBuild(ev.Build)
	case app.StageStart:
		l.stage = ev.Stage
		l.setBuild(ev.Build)
		if l.offsets == nil {
			l.offsets = map[string]int{}
		}
		for _, st := range ev.Build.Stages {
			if st.Name == ev.Stage {
				for _, j := range st.Jobs {
					l.offsets[j.Key] = 0
				}
			}
		}
		l.appendLines([]string{"", "== " + ev.Stage + " =="})
	case app.LogLines:
		if l.offsets == nil {
			l.offsets = map[string]int{}
		}
		l.offsets[ev.Job.Key] = ev.Next
		lines := ev.Lines
		if l.multi && l.build != nil {
			prefix := "[" + app.ShortJobKey(*l.build, ev.Job) + "] "
			lines = make([]string, len(ev.Lines))
			for i, s := range ev.Lines {
				lines[i] = prefix + s
			}
		}
		l.appendLines(lines)
	}
}

// setBuild keeps the newest snapshot and the state the header shows: the
// followed stage's on a stage screen, the build's otherwise.
func (l *logState) setBuild(b provider.Build) {
	l.build = &b
	l.live = b.State
	if l.stage == "" {
		return
	}
	for _, st := range b.Stages {
		if st.Name == l.stage {
			l.live = st.State
		}
	}
}
