package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// setScreen is the set screen's state: the set, its newest record, the
// board drawn from them, and whether another bam holds the set.
type setScreen struct {
	info      app.SetInfo
	rec       *app.SetRecord
	board     *app.SetBoard
	cursor    int    // over every run, stage after stage
	elsewhere string // "pid 77 on desk, since 09:15" when another bam holds the set
}

type setLoadedMsg struct {
	Gen       int
	Rec       *app.SetRecord
	Elsewhere string
}

// planOf is enough of a plan for a board: the set's runs by name.
func planOf(info app.SetInfo) app.SetPlan {
	p := app.SetPlan{Name: info.Name}
	for _, st := range info.Stages {
		var runs []app.SetRunPlan
		for _, r := range st {
			names := r.Runs
			if len(names) == 0 {
				names = []string{r.Target}
			}
			for _, n := range names {
				runs = append(runs, app.SetRunPlan{Name: n, Target: r.Target})
			}
		}
		p.Stages = append(p.Stages, runs)
	}
	return p
}

func loadSetCmd(svc *app.Service, set string, gen int) tea.Cmd {
	return func() tea.Msg {
		msg := setLoadedMsg{Gen: gen}
		if svc == nil || svc.Sets == nil {
			return msg
		}
		if rec, ok, err := svc.LatestSetRun(set); err == nil && ok {
			msg.Rec = &rec
		}
		if pid, host, since, held := svc.Sets.Holder(set); held {
			msg.Elsewhere = fmt.Sprintf("pid %d on %s, since %s", pid, host, since.Local().Format("15:04"))
		}
		return msg
	}
}

// openSet opens the set screen on the selected set.
func (m Model) openSet() (tea.Model, tea.Cmd) {
	it, ok := m.sets.selected()
	if !ok {
		return m, nil
	}
	m.screen = screenSet
	m.home.tickGen++
	m.setView = setScreen{info: it.Info, rec: it.Last, board: m.boardFor(it.Info, it.Last)}
	m.setGen++
	return m, loadSetCmd(m.svc, it.Info.Name, m.setGen)
}

// boardFor is the live board when this window runs the set, else a board
// seeded from rec.
func (m Model) boardFor(info app.SetInfo, rec *app.SetRecord) *app.SetBoard {
	if m.train.running() && m.train.set == info.Name {
		return m.train.board
	}
	b := app.NewSetBoard(planOf(info))
	if rec != nil {
		b.Seed(*rec)
	}
	return b
}

func (m Model) handleSetLoaded(msg setLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.setGen {
		return m, nil
	}
	m.setView.rec, m.setView.elsewhere = msg.Rec, msg.Elsewhere
	m.setView.board = m.boardFor(m.setView.info, msg.Rec)
	return m, nil
}

// setRows is every run of the board, stage after stage.
func (m Model) setRows() []app.SetBoardRow {
	var out []app.SetBoardRow
	for _, st := range m.setView.board.Stages() {
		out = append(out, st...)
	}
	return out
}

// setActions says which of R, U and F apply to the set now.
func (m Model) setActions() (run, resume, rerun bool) {
	if m.train.running() || m.setView.elsewhere != "" {
		return false, false, false
	}
	run = true
	if r := m.setView.rec; r != nil {
		resume = r.Status == app.SetInterrupted || r.Status == app.SetTimedOut || r.Status == app.SetRunning
		rerun = r.Status == app.SetFailed
	}
	return run, resume, rerun
}

// refuseSetAction says why R, U or F cannot act, or "" when they can.
func (m Model) refuseSetAction() string {
	switch {
	case m.setView.elsewhere != "":
		return "running in another bam (" + m.setView.elsewhere + ")"
	case m.train.running():
		return m.train.set + " is running in this window"
	}
	return ""
}

func (m Model) handleSetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.setRows()
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.Down):
		m.setView.cursor = min(m.setView.cursor+1, max(len(rows)-1, 0))
	case key.Matches(msg, keys.Up):
		m.setView.cursor = max(m.setView.cursor-1, 0)
	case key.Matches(msg, keys.Enter):
		return m.openSetBuild()
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Refresh):
		m.setGen++
		return m, loadSetCmd(m.svc, m.setView.info.Name, m.setGen)
	case key.Matches(msg, keys.Run), key.Matches(msg, keys.Resume), key.Matches(msg, keys.RerunFailed):
		if why := m.refuseSetAction(); why != "" {
			m.status = why
			return m, nil
		}
		run, resume, rerun := m.setActions()
		switch {
		case key.Matches(msg, keys.Run) && run:
			return m.openSetVars(setActionRun)
		case key.Matches(msg, keys.Resume) && resume:
			return m.openSetVars(setActionResume)
		case key.Matches(msg, keys.RerunFailed) && rerun:
			return m.openSetVars(setActionRerun)
		}
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
	}
	return m, nil
}

// openSetBuild opens the run under the cursor in the panels; esc there
// returns here.
func (m Model) openSetBuild() (tea.Model, tea.Cmd) {
	rows := m.setRows()
	if m.setView.cursor >= len(rows) || rows[m.setView.cursor].Build.Key == "" || m.svc == nil {
		return m, nil
	}
	b := rows[m.setView.cursor].Build
	m.fromSet = true
	m.screen = screenColumns
	m.focus = focusMain
	m.detail = &b
	m.progress = provider.Progress{}
	m.expanded = defaultExpanded(b)
	m.treeCursor = 0
	return m.startWatch(b.Key)
}

func (m Model) setScreenView() string {
	head := []string{m.setView.info.Name}
	switch {
	case m.train.running() && m.train.set == m.setView.info.Name:
		head = append(head, m.train.board.ID(), "running")
	case m.setView.rec != nil:
		head = append(head, m.setView.rec.ID, strings.ReplaceAll(string(m.setView.rec.Status), "_", " "))
	default:
		head = append(head, "never run")
	}
	lines := []string{" " + strings.Join(head, " · ")}
	if m.setView.elsewhere != "" {
		lines = append(lines, " running in another bam ("+m.setView.elsewhere+")")
	}
	stages := m.setView.board.Stages()
	idx := 0
	for i, st := range stages {
		lines = append(lines, fmt.Sprintf(" stage %d/%d", i+1, len(stages)))
		cols := []tableCol{{title: "RUN"}, {title: "BUILD", width: 16}, {title: "STATE", width: 18}, {title: "TIME", width: 8, right: true}}
		cols = fillWidth(cols, m.width-1, "RUN")
		var cells [][]string
		for _, r := range st {
			cells = append(cells, m.setRowCells(r))
		}
		for _, l := range tableLines(cols, cells, m.setView.cursor-idx)[1:] {
			lines = append(lines, " "+l)
		}
		idx += len(st)
	}
	body := m.height - 1
	if len(lines) > body {
		lines = lines[:body]
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(append(lines, m.statusBar(m.width)), "\n")
}

// setRowCells is one run row: glyph and name, build key, state (with its
// rerun count), and its duration or progress.
func (m Model) setRowCells(r app.SetBoardRow) []string {
	state, key := "pending", "–"
	if r.Build.Key != "" || r.Err != nil {
		state = stateCell(r.Build.State)
	}
	if r.Build.Key != "" {
		key = r.Build.Key
	}
	if r.Rerun > 0 {
		state += fmt.Sprintf(" (rerun %d)", r.Rerun)
	}
	when := ""
	switch {
	case r.Build.State == provider.StateRunning && r.Progress.Valid:
		when = fmt.Sprintf("%d%%", int(r.Progress.Percent*100))
	case r.Build.Duration > 0:
		when = view.Duration(r.Build.Duration)
	}
	return []string{r.Name, key, state, when}
}

// setSelectionURL is the URL of the run under the set screen's cursor.
func (m Model) setSelectionURL() string {
	rows := m.setRows()
	if m.setView.cursor >= len(rows) || rows[m.setView.cursor].Build.Key == "" {
		return ""
	}
	if u := rows[m.setView.cursor].Build.URL; u != "" {
		return u
	}
	if m.svc != nil {
		return m.svc.P.URL(rows[m.setView.cursor].Build.Key)
	}
	return ""
}

type setAction int

const (
	setActionRun setAction = iota
	setActionResume
	setActionRerun
)

// openSetVars is replaced in a later step.
func (m Model) openSetVars(a setAction) (tea.Model, tea.Cmd) { return m, nil }
