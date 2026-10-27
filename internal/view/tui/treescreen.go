package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// treeScreen is one build's tree: what the watch last sent, the cursor over
// its build rows, and where esc returns.
type treeScreen struct {
	root       string
	tree       app.Tree
	have       bool
	refreshing bool             // a tree is drawn (from the session cache, or before r or a rerun) and the watch has not sent a newer one
	failed     bool             // the watch ended in an error; tree is the last one it had
	progress   app.TreeProgress // how far the first refresh got, until have
	cursor     int              // over build rows only
	from       screen
	back       *planReturn     // the plan screen t was pressed on, when from is screenPlan
	restarting map[string]bool // builds a rerun is restarting now
}

type treeEventMsg struct {
	Gen   int
	Event app.TreeEvent
}

type treeClosedMsg struct{ Gen int }

func treeCmd(ch <-chan app.TreeEvent, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return treeClosedMsg{Gen: gen}
		}
		return treeEventMsg{Gen: gen, Event: e}
	}
}

// openTree opens the tree screen on key and starts watching it.
func (m Model) openTree(key string) (tea.Model, tea.Cmd) {
	if m.svc == nil || key == "" {
		return m, nil
	}
	from := m.screen
	var back *planReturn
	switch {
	case from == screenTree:
		from, back = m.buildTree.from, m.buildTree.back
	case from == screenPlan && m.plan.key != "":
		// A build opened on the tree starts a fresh plan screen; esc off the
		// tree draws this one again, stack, cursor and watch included.
		back = &planReturn{frame: m.currentFrame(m.focus), stack: slices.Clone(m.plan.stack),
			fromSet: m.fromSet, fromTree: m.fromTree}
		if m.fromTree {
			prev := m.buildTree
			back.tree = &prev
		}
		m.fromSet, m.fromTree = false, false
	}
	m.stopWatch()
	m.rerunGen++ // a rerun started on another tree reports to that one only
	m.buildTree = treeScreen{root: key, from: from, back: back}
	if t, ok := m.trees[key]; ok {
		m.buildTree.tree, m.buildTree.have = t, true
	}
	m.screen = screenTree
	return m.watchTree()
}

// watchTree starts a fresh watch on the screen's root, so builds a rerun
// restarted are read again.
func (m Model) watchTree() (tea.Model, tea.Cmd) {
	m.stopTree()
	m.buildTree.refreshing, m.buildTree.failed = m.buildTree.have, false
	if m.svc == nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.treeCancel = cancel
	m.treeCh = m.svc.WatchTree(ctx, m.buildTree.root, app.TreeOptions{})
	return m, treeCmd(m.treeCh, m.treeGen)
}

func (m *Model) stopTree() {
	if m.treeCancel != nil {
		m.treeCancel()
		m.treeCancel = nil
	}
	m.treeCh = nil
	m.treeGen++
}

func (m Model) closeTree() (tea.Model, tea.Cmd) {
	m.stopTree()
	m.rerunGen++ // a rerun result for the closed screen is dropped
	m.fromTree, m.logsFromTree = false, false
	if back := m.buildTree.back; back != nil {
		m.buildTree.back = nil
		m.fromSet = back.fromSet
		f := back.frame
		next, cmd := m.enterPlan(f.key, f.buildsPlan, f.wantOf(), f.focus)
		next.plan.stack = back.stack
		next.restoreCursors(f)
		if back.fromTree && back.tree != nil {
			// The plan screen came from an earlier tree screen: esc on it
			// goes back there, so that screen is the current tree again.
			next.fromTree = true
			next.buildTree = *back.tree
			next.buildTree.restarting = nil
		}
		return next, cmd
	}
	m.screen = m.buildTree.from
	switch m.screen {
	case screenHome, screenSet:
		// A build opened on the tree left its plan screen behind; nothing
		// of it may follow the user off the tree.
		m.closePlan()
		if m.screen == screenHome {
			return m.goHome()
		}
	case screenTree:
		m.screen = screenPlan
	}
	return m, nil
}

func (m Model) handleTreeEvent(msg treeEventMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.treeGen {
		return m, nil
	}
	e := msg.Event
	switch e.Type {
	case app.TreeError:
		// A tree already drawn stays, marked: the error is about this refresh.
		m.err = e.Err
		m.buildTree.refreshing, m.buildTree.failed = false, m.buildTree.have
		return m, nil
	case app.TreeLoading:
		m.buildTree.progress = e.Progress
		return m, treeCmd(m.treeCh, m.treeGen)
	case app.TreeUpdate, app.TreeDone:
		selected := ""
		if n, ok := m.treeSelected(); ok {
			selected = n.Build.Key
		}
		m.buildTree.tree, m.buildTree.have, m.buildTree.refreshing = e.Tree, true, false
		m.keepTree(m.buildTree.root, e.Tree)
		m.buildTree.cursor = 0
		for i, l := range m.treeBuildRows() {
			if l.node.Build.Key == selected {
				m.buildTree.cursor = i
			}
		}
		if w := e.Tree.Warnings; len(w) > 0 {
			m.status = fmt.Sprintf("%d warning(s): %s", len(w), w[0])
		}
	}
	if e.Type == app.TreeUpdate {
		return m, treeCmd(m.treeCh, m.treeGen)
	}
	return m, nil
}

// treeLine is one row of the screen: a build, a stage, or a not-run child.
type treeLine struct {
	text string
	node *app.TreeNode // nil for a stage row
}

// treeLines is the tree as the CLI draws it, one row per build and stage.
func (m Model) treeLines() []treeLine {
	t := m.buildTree.tree
	if t.Root == nil {
		return nil
	}
	out := []treeLine{{text: m.treeNodeText(t.Root, false), node: t.Root}}
	var add func(n *app.TreeNode, indent string)
	node := func(k *app.TreeNode, indent, branch, more string, underStage bool) {
		out = append(out, treeLine{text: indent + branch + m.treeNodeText(k, underStage), node: k})
		add(k, indent+more)
	}
	add = func(n *app.TreeNode, indent string) {
		if n.NotRun || n.Loop || n.Superseded {
			return
		}
		items := view.TreeItems(n, true)
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.Stage == nil {
				node(it.Node, indent, branch, more, false)
				continue
			}
			out = append(out, treeLine{text: indent + branch + stageText(*it.Stage)})
			for j, k := range it.Under {
				b, mo := "├─ ", "│  "
				if j == len(it.Under)-1 {
					b, mo = "└─ ", "   "
				}
				node(k, indent+more, b, mo, true)
			}
		}
	}
	add(t.Root, "")
	return out
}

func (m Model) treeNodeText(n *app.TreeNode, underStage bool) string {
	switch {
	case n.NotRun:
		return dimStyle.Render("· " + n.PlanKey + "  not run")
	case n.Loop:
		return "↺ " + n.Build.Key + view.TreePlanName(n.Build) + "  already shown above"
	case n.Superseded:
		return dimStyle.Render(stateGlyph(n.Build.State) + " " + n.Build.Key + "  earlier run")
	}
	b := n.Build
	d := b.Duration
	if !app.TreeFinished(b.State) && !b.StartedAt.IsZero() {
		d = m.now().Sub(b.StartedAt)
	}
	text := b.Key + view.TreePlanName(b) + "  " + stateCell(b.State) + "  " + view.Duration(d)
	if note := view.TreeNodeNote(n, underStage); note != "" {
		text += "  " + note
	}
	if m.buildTree.restarting[b.Key] {
		text += "  restarting…"
	}
	return text
}

func stageText(s provider.Stage) string {
	text := stateStyle(s.State).Render(stateGlyph(s.State)) + " stage " + s.Name + "  " + view.Duration(s.Duration)
	if s.State == provider.StateFailed {
		var names []string
		for _, j := range s.Jobs {
			if j.State == provider.StateFailed {
				names = append(names, j.Name)
			}
		}
		if len(names) > 0 {
			text += "  " + strings.Join(names, ", ")
		}
	}
	return text
}

// treeBuildRows is the rows the cursor moves over: every build, no stage,
// no not-run child.
func (m Model) treeBuildRows() []treeLine {
	var out []treeLine
	for _, l := range m.treeLines() {
		if l.node != nil && !l.node.NotRun {
			out = append(out, l)
		}
	}
	return out
}

func (m Model) treeSelected() (*app.TreeNode, bool) {
	rows := m.treeBuildRows()
	if m.buildTree.cursor >= len(rows) {
		return nil, false
	}
	return rows[m.buildTree.cursor].node, true
}

func (m Model) treeSelectionURL() string {
	n, ok := m.treeSelected()
	if !ok {
		return ""
	}
	if n.Build.URL != "" {
		return n.Build.URL
	}
	if m.svc != nil {
		return m.svc.P.URL(n.Build.Key)
	}
	return ""
}

// treeRerunnable reports whether F would act: a finished tree with a
// failure to restart, and no rerun in flight.
func (m Model) treeRerunnable() bool {
	t := m.buildTree.tree
	return m.buildTree.have && len(m.buildTree.restarting) == 0 && t.State != provider.StateRunning && len(app.RerunCandidates(t)) > 0
}

func (m Model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.treeBuildRows()
	switch {
	case key.Matches(msg, keys.Quit), key.Matches(msg, keys.Back):
		return m.closeTree()
	case key.Matches(msg, keys.Down):
		m.buildTree.cursor = min(m.buildTree.cursor+1, max(len(rows)-1, 0))
	case key.Matches(msg, keys.Up):
		m.buildTree.cursor = max(m.buildTree.cursor-1, 0)
	case key.Matches(msg, keys.Top):
		m.buildTree.cursor = 0
	case key.Matches(msg, keys.Bottom):
		m.buildTree.cursor = max(len(rows)-1, 0)
	case key.Matches(msg, keys.Enter):
		return m.openTreeBuild(false, false)
	case key.Matches(msg, keys.Logs):
		return m.openTreeBuild(true, false)
	case key.Matches(msg, keys.AllLogs):
		return m.openTreeBuild(true, true)
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Refresh):
		return m.watchTree()
	case key.Matches(msg, keys.RerunFailed):
		return m.askTreeRerun()
	case key.Matches(msg, keys.Server):
		return m.openServerPicker()
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
	}
	return m, nil
}

// openTreeBuild shows the selected build in the panels, or its logs; esc
// there comes back to the tree. Watching the tree pauses meanwhile.
func (m Model) openTreeBuild(logs, all bool) (tea.Model, tea.Cmd) {
	n, ok := m.treeSelected()
	if !ok || m.svc == nil {
		return m, nil
	}
	b := n.Build
	m.stopTree()
	m.fromTree, m.fromSet = true, false
	next, cmd := m.enterPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
	if !logs {
		return next, cmd
	}
	next.logsFromTree = true
	after, logsCmd := next.openLogs("", all)
	return after, tea.Batch(cmd, logsCmd)
}

func (m Model) treeScreenView() string {
	t := m.buildTree.tree
	head := " " + m.buildTree.root
	if m.buildTree.have {
		head += " · " + stateCell(t.State)
		switch {
		case m.buildTree.refreshing:
			head += " · refreshing…"
		case m.buildTree.failed:
			head += " · refresh failed"
		}
	} else {
		head += " · " + view.TreeProgressText(m.buildTree.progress)
	}
	lines := []string{head, " " + m.treeCounts()}
	rows := m.treeLines()
	sel := -1
	if n, ok := m.treeSelected(); ok {
		for i, l := range rows {
			if l.node == n {
				sel = i
			}
		}
	}
	for i, l := range rows {
		text := " " + l.text
		if i == sel {
			text = selectedRow(text, m.width, true)
		}
		lines = append(lines, text)
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

// treeCounts is "2 failed · 1 running · 4 succeeded", over current builds.
func (m Model) treeCounts() string {
	var failed, running, ok, notRun int
	var walk func(n *app.TreeNode)
	walk = func(n *app.TreeNode) {
		if n.Superseded {
			return
		}
		switch {
		case n.NotRun:
			notRun++
		case n.Loop:
		case !app.TreeFinished(n.Build.State):
			running++
		case n.Build.State == provider.StateSuccess || n.Build.State == provider.StateSkipped:
			ok++
		default:
			failed++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if m.buildTree.tree.Root != nil {
		walk(m.buildTree.tree.Root)
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{failed, "failed"}, {running, "running"}, {ok, "succeeded"}, {notRun, "not run"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return strings.Join(parts, " · ")
}

type treeRerunMsg struct {
	Gen     int
	Results []app.RerunResult
}

// askTreeRerun is F: it says why it cannot act, or asks before restarting
// the tree's topmost failures.
func (m Model) askTreeRerun() (tea.Model, tea.Cmd) {
	t := m.buildTree.tree
	if !m.buildTree.have || len(m.buildTree.restarting) > 0 || m.svc == nil {
		return m, nil
	}
	cands := app.RerunCandidates(t)
	switch {
	case len(cands) == 0:
		m.status = "nothing to rerun"
		return m, nil
	case t.State == provider.StateRunning:
		m.status = "the tree is still running; rerun when it has finished"
		return m, nil
	}
	var keys []string
	for _, n := range cands {
		keys = append(keys, n.Build.Key)
	}
	noun := "builds"
	if len(cands) == 1 {
		noun = "build"
	}
	prompt := fmt.Sprintf("Rerun the failed jobs of %d %s? %s", len(cands), noun, strings.Join(keys, ", "))
	return m.ask(prompt, func(m Model) (tea.Model, tea.Cmd) {
		m.buildTree.restarting = map[string]bool{}
		for _, k := range keys {
			m.buildTree.restarting[k] = true
		}
		m.stopTree()
		gen, svc, ctx := m.rerunGen, m.svc, m.baseCtx()
		return m, func() tea.Msg { return treeRerunMsg{Gen: gen, Results: svc.RerunTree(ctx, cands)} }
	})
}

// handleTreeRerun reports the restarts and watches the tree afresh, so the
// restarted builds are read again. A result for a screen since closed or
// reopened is dropped; one that lands while a build of the tree is open is
// reported, and the tree is watched again on the way back.
func (m Model) handleTreeRerun(msg treeRerunMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.rerunGen {
		return m, nil
	}
	m.buildTree.restarting = nil
	restarted := 0
	var problems []string
	for _, r := range msg.Results {
		switch {
		case r.Err != nil:
			problems = append(problems, r.Key+": "+errorWhat(r.Err))
		case r.Restarted:
			restarted++
		default:
			problems = append(problems, r.Key+" was not restarted")
		}
	}
	m.status = fmt.Sprintf("restarted %d build(s)", restarted)
	if len(problems) > 0 {
		m.status += "; " + strings.Join(problems, "; ")
	}
	if m.screen != screenTree {
		return m, nil
	}
	return m.watchTree()
}
