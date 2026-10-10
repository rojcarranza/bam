package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

type treeKind int

const (
	rowStage treeKind = iota
	rowJob
)

// treeRow is one visible line of the stage and job tree. Stage and Job are
// indexes into the build, so a row survives a refresh that reorders nothing.
type treeRow struct {
	Kind     treeKind
	Stage    int
	Job      int
	Name     string
	State    provider.State
	Duration time.Duration
	Key      string // job result key; empty for a stage
	URL      string
}

// buildTree flattens the build into the rows to draw, honouring which stages
// are expanded.
func buildTree(b provider.Build, expanded map[string]bool) []treeRow {
	var out []treeRow
	for si, st := range b.Stages {
		out = append(out, treeRow{Kind: rowStage, Stage: si, Name: st.Name, State: st.State, Duration: st.Duration})
		if !expanded[st.Name] {
			continue
		}
		for ji, j := range st.Jobs {
			out = append(out, treeRow{Kind: rowJob, Stage: si, Job: ji, Name: j.Name,
				State: j.State, Duration: j.Duration, Key: j.Key, URL: j.URL})
		}
	}
	return out
}

// defaultExpanded opens the stages that failed or are still running, because
// that is what the user opened the build to read.
func defaultExpanded(b provider.Build) map[string]bool {
	out := map[string]bool{}
	for _, st := range b.Stages {
		if st.State == provider.StateFailed || st.State == provider.StateRunning {
			out[st.Name] = true
		}
	}
	return out
}

// stagesBody is the Stages panel for the open build: the reason line, the
// stage and job tree, and the failed-test count when the server reported
// one. State, number, branch and duration are in the Builds row and the
// header, so the panel does not repeat them.
func (m Model) stagesBody(width, height int) string {
	b := *m.detail
	// Cut to the panel: the panel wraps an over-long line, which would push
	// Tree and the status bar off the screen.
	first := dimStyle.Render(cut(strings.TrimSpace(b.Reason+" "+revisionShort(b)), width))
	// The estimate bar stays: it is the one thing about a running build the
	// Builds row and the header do not say. It takes the columns the reason
	// leaves, and is dropped when they cannot hold a readable one.
	if bar := view.BarWithin(style.Mode{}, m.progress, width-lipgloss.Width(first)-3); bar != "" {
		first += "   " + bar
	}
	lines := []string{first, ""}

	rows := buildTree(b, m.expanded)
	// Window the tree around the cursor rather than truncating the body: the
	// cursor can walk past the bottom, and enter or o on a row nobody can see
	// acts on the wrong thing.
	start, end := treeWindow(m.treeCursor, len(rows), height-len(lines)-failedTestLines(b))
	nameWidth := maxNameWidth(width)
	for i := start; i < end; i++ {
		r := rows[i]
		indent := ""
		if r.Kind == rowJob {
			indent = "  "
		}
		dur := ""
		if r.Duration > 0 {
			dur = view.Duration(r.Duration)
		}
		// The glyph is indented with its job, and the whole cell is padded by
		// lipgloss rather than by %-*s, because the glyph carries ANSI and
		// fmt would count those bytes as width. Every duration then lands in
		// the same column.
		cell := indent + stateStyle(r.State).Render(stateGlyph(r.State)) + " " +
			truncate(r.Name, nameWidth-len(indent))
		focused := m.focus == focusStages
		line := strings.TrimRight(lipgloss.NewStyle().Width(nameWidth+2).Render(cell)+" "+dur, " ")
		if m.treeCursor == i {
			line = selectedRow(line, width-2, focused)
		}
		lines = append(lines, m.cursorFor(focused, m.treeCursor == i)+line)
	}
	if n := len(b.FailedTests); n > 0 {
		lines = append(lines, "", errorStyle.Render(fmt.Sprintf("%d failed tests", n)))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// failedTestLines is the room the failed-test footer needs, so the window
// does not claim it.
func failedTestLines(b provider.Build) int {
	if len(b.FailedTests) > 0 {
		return 2
	}
	return 0
}

// treeWindow keeps the cursor inside the rows that are drawn.
func treeWindow(cursor, n, height int) (int, int) {
	if height <= 0 || n == 0 {
		return 0, 0
	}
	if height >= n {
		return 0, n
	}
	start := cursor - height + 1
	if start < 0 {
		start = 0
	}
	if maxStart := n - height; start > maxStart {
		start = maxStart
	}
	return start, start + height
}

// maxNameWidth leaves room for the cursor, the glyph and the duration, and
// caps the column so durations stay readable next to their names.
func maxNameWidth(panelWidth int) int {
	w := panelWidth - 12
	switch {
	case w < 8:
		w = 8
	case w > 26:
		w = 26
	}
	return w
}

func revisionShort(b provider.Build) string {
	if len(b.Revisions) == 0 {
		return ""
	}
	return "· " + b.Revisions[0].Short()
}
