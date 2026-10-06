package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

type setAction int

const (
	setActionRun setAction = iota
	setActionResume
	setActionRerun
)

func (a setAction) verb() string {
	return [...]string{"Run", "Resume", "Rerun the failed runs of"}[a]
}

// trainState is the one set this window runs: its context, its events and
// the board they move.
type trainState struct {
	set    string
	cancel context.CancelFunc
	ch     <-chan app.SetEvent
	board  *app.SetBoard
	gen    int
}

func (t trainState) running() bool { return t.ch != nil }

// end forgets the train once its channel closed; the board stays readable
// on the set screen through setView.
func (t *trainState) end() {
	if t.cancel != nil {
		t.cancel()
	}
	t.cancel, t.ch = nil, nil
	t.gen++ // its late messages are stale now
}

type setVarRow struct {
	Name, Value string
	Required    bool // the run was started with it; resume and rerun need it again
}

type setVarsState struct {
	action   setAction
	rows     []setVarRow
	cursor   int
	editing  bool
	editName bool // a new row: its name first, then its value
	input    textinput.Model
	err      error
	busy     bool // a start or dry run is in flight; ctrl-R and d wait for it
	gen      int  // the prompt's start and dry-run generation
}

// flags is the rows as --var flags, in order, skipping rows with no name.
func (s setVarsState) flags() []string {
	var out []string
	for _, r := range s.rows {
		if r.Name != "" {
			out = append(out, r.Name+"="+r.Value)
		}
	}
	return out
}

// missing names a required row with no value, or "" when none.
func (s setVarsState) missing() string {
	for _, r := range s.rows {
		if r.Required && r.Value == "" {
			return r.Name
		}
	}
	return ""
}

type setEventMsg struct {
	Gen   int
	Event app.SetEvent
}

type setClosedMsg struct{ Gen int }

func setEventCmd(ch <-chan app.SetEvent, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return setClosedMsg{Gen: gen}
		}
		return setEventMsg{Gen: gen, Event: e}
	}
}

// openSetVars opens the prompt for a. Resume and rerun ask again for every
// name the record was started with; with none, they act at once.
func (m Model) openSetVars(a setAction) (tea.Model, tea.Cmd) {
	m.setVars = setVarsState{action: a, input: textinput.New(), gen: m.setVars.gen}
	if a != setActionRun && m.setView.rec != nil {
		for _, n := range m.setView.rec.VarNames {
			m.setVars.rows = append(m.setVars.rows, setVarRow{Name: n, Required: true})
		}
		if len(m.setVars.rows) == 0 {
			return m.startTrain()
		}
	}
	m.screen = screenSetVars
	return m, nil
}

func (m Model) handleSetVarsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := &m.setVars
	if v.editing {
		switch msg.Type {
		case tea.KeyEsc:
			v.editing = false
			return m, nil
		case tea.KeyEnter:
			r := &v.rows[v.cursor]
			if v.editName {
				r.Name, v.editName = strings.TrimSpace(v.input.Value()), false
				v.input.SetValue(r.Value)
				v.input.EchoMode = echoFor(r.Name)
				return m, nil
			}
			r.Value, v.editing = v.input.Value(), false
			return m, nil
		}
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, keys.Back):
		m.screen = screenSet
		m.setVars = setVarsState{}
		return m, nil
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Down):
		v.cursor = min(v.cursor+1, max(len(v.rows)-1, 0))
	case key.Matches(msg, keys.Up):
		v.cursor = max(v.cursor-1, 0)
	case key.Matches(msg, keys.AllLogs): // a: add a row
		v.rows = append(v.rows, setVarRow{})
		v.cursor = len(v.rows) - 1
	case key.Matches(msg, keys.RemoveRow):
		if v.cursor < len(v.rows) && !v.rows[v.cursor].Required {
			v.rows = append(v.rows[:v.cursor], v.rows[v.cursor+1:]...)
			v.cursor = max(min(v.cursor, len(v.rows)-1), 0)
		}
	case key.Matches(msg, keys.Enter):
		if v.cursor < len(v.rows) {
			v.editing, v.editName = true, v.rows[v.cursor].Name == ""
			v.input.SetValue("")
			v.input.EchoMode = textinput.EchoNormal
			if !v.editName {
				v.input.SetValue(v.rows[v.cursor].Value)
				v.input.EchoMode = echoFor(v.rows[v.cursor].Name)
			}
			v.input.Focus()
			return m, textinput.Blink
		}
	case key.Matches(msg, keys.DryRun):
		return m.setDryRun()
	case key.Matches(msg, keys.Trigger):
		if n := v.missing(); n != "" {
			v.err = errs.Usagef("%s needs a value", n).WithWhy("the run was started with it, and bam never stores values")
			return m, nil
		}
		return m.startTrain()
	case key.Matches(msg, keys.ExpandErr):
		if v.err != nil {
			m.err, m.overlay = v.err, overlayError
		}
	}
	return m, nil
}

// setDryMsg carries a planned dry run back to Update.
type setDryMsg struct {
	Gen   int
	Lines []string
	Err   error
}

// setDryRun plans the set with the rows in a command, since planning talks
// to Bamboo, and shows every run, masked, when it comes back.
func (m Model) setDryRun() (tea.Model, tea.Cmd) {
	if m.svc == nil || m.setVars.busy {
		return m, nil
	}
	m.setVars.gen++
	gen, svc, ctx := m.setVars.gen, m.svc, m.baseCtx()
	set, o := m.setView.info.Name, app.SetOptions{Vars: m.setVars.flags(), Server: m.server}
	return m, func() tea.Msg {
		p, err := svc.PlanSet(ctx, set, o)
		if err != nil {
			return setDryMsg{Gen: gen, Err: err}
		}
		return setDryMsg{Gen: gen, Lines: setDryRunText(p)}
	}
}

func (m Model) handleSetDry(msg setDryMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.setVars.gen || m.screen != screenSetVars {
		return m, nil
	}
	if msg.Err != nil {
		m.setVars.err = msg.Err
		return m, nil
	}
	m.setVars.err = nil
	m.setDry = msg.Lines
	m.overlay = overlayDryRun
	return m, nil
}

// setDryRunText is the dry run as lines: every run of every stage with its
// plan, branch and changed variables, secrets masked.
func setDryRunText(p app.SetPlan) []string {
	var out []string
	for i, st := range p.Stages {
		out = append(out, fmt.Sprintf("stage %d/%d", i+1, len(p.Stages)))
		for _, r := range st {
			branch := r.Ref.Branch
			if branch == "" {
				branch = "default"
			}
			out = append(out, fmt.Sprintf("  %s  %s  branch %s", app.PlanRunName(r), r.Ref.PlanKey, branch))
			changed := r.Vars.Changed()
			for _, n := range sortedVarNames(changed) {
				v, _ := r.Vars.Get(n)
				out = append(out, fmt.Sprintf("      %s = %s", n, v.Display()))
			}
		}
	}
	return out
}

// trainStartedMsg carries a started train, or why it could not start, back
// to Update.
type trainStartedMsg struct {
	Gen    int
	Ch     <-chan app.SetEvent
	Cancel context.CancelFunc
	Err    error
}

// startTrain starts the action with the rows as --var flags, in a command:
// planning and locking the set talk to Bamboo and the disk, and Update must
// not wait on them. An error before the first event comes back to the
// prompt; nothing triggered.
func (m Model) startTrain() (tea.Model, tea.Cmd) {
	if m.svc == nil || m.setVars.busy {
		return m, nil
	}
	if why := m.refuseSetAction(); why != "" {
		m.status = why
		m.screen = screenSet
		return m, nil
	}
	m.setVars.gen++
	m.setVars.busy = true
	m.status = "starting " + m.setView.info.Name + "…"
	gen, svc, base := m.setVars.gen, m.svc, m.baseCtx()
	action, set := m.setVars.action, m.setView.info.Name
	o := app.SetOptions{Vars: m.setVars.flags(), Server: m.server}
	id := ""
	if m.setView.rec != nil {
		id = m.setView.rec.ID
	}
	return m, func() tea.Msg {
		ctx, cancel := context.WithCancel(base)
		var (
			ch  <-chan app.SetEvent
			err error
		)
		switch action {
		case setActionRun:
			var p app.SetPlan
			if p, err = svc.PlanSet(ctx, set, o); err == nil {
				_, ch, err = svc.RunSet(ctx, p, app.SetLockOptions{})
			}
		case setActionResume:
			ch, err = svc.ResumeSet(ctx, id, o, app.SetLockOptions{})
		case setActionRerun:
			ch, err = svc.RerunSet(ctx, id, o, app.SetLockOptions{})
		}
		if err != nil {
			cancel()
			return trainStartedMsg{Gen: gen, Err: err}
		}
		return trainStartedMsg{Gen: gen, Ch: ch, Cancel: cancel}
	}
}

// handleTrainStarted installs the started train, or puts its error back in
// the prompt. A start the user has walked away from is still a running
// train: it is installed all the same, so it can be watched and quit.
func (m Model) handleTrainStarted(msg trainStartedMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.setVars.gen {
		if msg.Cancel != nil {
			msg.Cancel() // superseded: end it rather than leak it
		}
		return m, nil
	}
	m.setVars.busy = false
	if msg.Err != nil {
		m.status = ""
		m.setVars.err = msg.Err
		if m.screen != screenSetVars {
			m.err = msg.Err
		}
		return m, nil
	}
	board := app.NewSetBoard(planOf(m.setView.info))
	if m.setVars.action != setActionRun && m.setView.rec != nil {
		board.Seed(*m.setView.rec)
	}
	m.train = trainState{set: m.setView.info.Name, cancel: msg.Cancel, ch: msg.Ch, board: board, gen: m.train.gen + 1}
	m.setView.board = board
	m.setVars = setVarsState{gen: m.setVars.gen}
	if m.screen == screenSetVars {
		m.screen = screenSet
	}
	m.status = m.train.set + " started"
	return m, setEventCmd(msg.Ch, m.train.gen)
}

// handleSetEvent moves the train's board on and reads the next event. The
// end of the set goes to the status bar.
func (m Model) handleSetEvent(e app.SetEvent) (tea.Model, tea.Cmd) {
	m.train.board.Apply(e, m.now())
	switch e.Type {
	case app.SetDone:
		if e.State == provider.StateSuccess {
			m.status = m.train.set + " succeeded"
		} else {
			m.status = fmt.Sprintf("%s failed: stage %d", m.train.set, e.Stage+1)
		}
	case app.SetError:
		m.err = e.Err
	}
	return m, setEventCmd(m.train.ch, m.train.gen)
}

func (m Model) setVarsView() string {
	v := m.setVars
	title := " " + v.action.verb() + " " + m.setView.info.Name
	if v.action != setActionRun && m.setView.rec != nil {
		title += " " + m.setView.rec.ID
	}
	lines := []string{title}
	for i, r := range v.rows {
		cur := "  "
		if i == v.cursor {
			cur = "▸ "
		}
		name, val := r.Name, r.Value
		if val != "" && app.IsSecretName(r.Name) {
			val = app.MaskedDisplay
		}
		if v.editing && i == v.cursor {
			if v.editName {
				name = v.input.View()
			} else {
				val = v.input.View()
			}
		}
		if name == "" {
			name = dimStyle.Render("name")
		}
		note := ""
		if r.Required {
			note = dimStyle.Render("required: the run was started with it")
		}
		lines = append(lines, fmt.Sprintf(" %s%-16s %-20s %s", cur, name, val, note))
	}
	if len(v.rows) == 0 {
		lines = append(lines, dimStyle.Render("   no variables; a adds one"))
	}
	if v.err != nil {
		lines = append(lines, "", errorStyle.Render(" "+errorWhat(v.err)))
	}
	body := m.height - 1
	for len(lines) < body {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(append(lines[:body], m.statusBar(m.width)), "\n")
}

// sortedVarNames is the names of a variable map in order.
func sortedVarNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// echoFor is how the prompt's input shows a value of name: hidden for a
// secret-looking name, as the run form hides one.
func echoFor(name string) textinput.EchoMode {
	if app.IsSecretName(name) {
		return textinput.EchoPassword
	}
	return textinput.EchoNormal
}
