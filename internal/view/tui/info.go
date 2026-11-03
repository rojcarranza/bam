package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// statsBuilds is how many of a plan's newest builds :stats reads, the
// default of bam plan stats.
const statsBuilds = 20

// historyRuns is how many past runs H lists, the default of bam set history.
const historyRuns = 20

// infoState is the read-only text overlay that :stats and H fill: a title
// over a scrollable body, drawn by the same renderers the commands print with.
type infoState struct {
	title string
	vp    helpState
}

type infoLoadedMsg struct {
	Gen   int
	Title string
	Body  string
}

// infoOut is where a view renderer writes for the overlay: plain text the
// width of the box, relative times as on a terminal.
func (m Model) infoOut(buf *bytes.Buffer, width int) view.Out {
	return view.Out{W: buf, TTY: true, Width: width, Style: style.Mode{}, Now: m.now}
}

// openInfo shows title over body.
func (m Model) openInfo(title, body string) Model {
	m.overlay = overlayInfo
	m.infoPane.title = title
	w := overlayWidth(m.width, helpMaxWidth) - 2
	m.infoPane.vp.set(w, m.helpHeight(), strings.TrimRight(body, "\n"))
	return m
}

// loadStatsCmd reads a plan's stats exactly as bam plan stats does: arg is a
// plan key or a preset name.
func loadStatsCmd(ctx context.Context, svc *app.Service, o func(*bytes.Buffer) view.Out, arg string, gen int) tea.Cmd {
	return func() tea.Msg {
		ref, err := svc.ResolvePlan(ctx, arg, "")
		if err != nil {
			return errMsg{Err: err, Where: "stats", Stream: streamInfo, Gen: gen}
		}
		st, err := svc.PlanStats(ctx, ref, statsBuilds)
		if err != nil {
			return errMsg{Err: err, Where: "stats", Stream: streamInfo, Gen: gen}
		}
		var buf bytes.Buffer
		if len(st.Warnings) > 0 {
			fmt.Fprintln(&buf, strings.Join(st.Warnings, "\n"))
			fmt.Fprintln(&buf)
		}
		if err := view.PlanStatsView(o(&buf), st); err != nil {
			return errMsg{Err: err, Where: "stats", Stream: streamInfo, Gen: gen}
		}
		return infoLoadedMsg{Gen: gen, Title: "Stats · " + arg, Body: buf.String()}
	}
}

// commandStats is :stats [PLAN]. Without an argument it reads the plan or
// preset under the cursor, or the plan the plan screen is on.
func (m Model) commandStats(arg string) (tea.Model, tea.Cmd, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		arg = m.statsSubject()
	}
	if arg == "" {
		return m, nil, fmt.Errorf("no plan selected; :stats PLAN")
	}
	if m.svc == nil {
		return m, nil, fmt.Errorf("not connected; :stats needs a server")
	}
	m.infoGen++
	m = m.openInfo("Stats · "+arg, "reading the last "+fmt.Sprint(statsBuilds)+" builds…")
	width := overlayWidth(m.width, helpMaxWidth) - 2
	out := func(b *bytes.Buffer) view.Out { return m.infoOut(b, width) }
	return m, loadStatsCmd(m.baseCtx(), m.svc, out, arg, m.infoGen), nil
}

// statsSubject is what :stats reads when it is given no name.
func (m Model) statsSubject() string {
	switch m.screen {
	case screenHome:
		switch m.home.view {
		case homePlans:
			if p, ok := m.plans.selected(); ok {
				return p.Key
			}
		case homePresets:
			if t, ok := m.presets.selected(); ok {
				return t.Name
			}
		}
	case screenPlan:
		return m.buildsPlan
	}
	return ""
}

// openSetHistory is H: the past runs of set, newest first, as bam set history
// lists them. The records are files, so it needs no server.
func (m Model) openSetHistory(set string) (tea.Model, tea.Cmd) {
	if set == "" || m.svc == nil || m.svc.Sets == nil {
		return m, nil
	}
	recs, err := m.svc.SetHistory(set, historyRuns)
	if err != nil {
		m.err = err
		return m, nil
	}
	var buf bytes.Buffer
	if len(recs) == 0 {
		fmt.Fprintf(&buf, "no runs of %s recorded in this repository\n", set)
	} else if err := view.SetHistory(m.infoOut(&buf, overlayWidth(m.width, helpMaxWidth)-2), recs); err != nil {
		m.err = err
		return m, nil
	}
	return m.openInfo("History · "+set, buf.String()), nil
}
