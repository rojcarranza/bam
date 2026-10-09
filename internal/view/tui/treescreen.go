package tui

import (
	"context"
	"fmt"
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
	cursor     int // over build rows only
	from       screen
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
	if from == screenTree {
		from = m.buildTree.from
	}
	m.stopWatch()
	m.buildTree = treeScreen{root: key, from: from}
	m.screen = screenTree
	return m.watchTree()
}

// watchTree starts a fresh watch on the screen's root, so builds a rerun
// restarted are read again.
func (m Model) watchTree() (tea.Model, tea.Cmd) {
	m.stopTree()
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
	m.screen = m.buildTree.from
	if m.screen == screenTree {
		m.screen = screenColumns
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
		m.err = e.Err
		return m, nil
	case app.TreeUpdate, app.TreeDone:
		selected := ""
		if n, ok := m.treeSelected(); ok {
			selected = n.Build.Key
		}
		m.buildTree.tree, m.buildTree.have = e.Tree, true
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
	out := []treeLine{{text: m.treeNodeText(t.Root), node: t.Root}}
	var add func(n *app.TreeNode, indent string)
	add = func(n *app.TreeNode, indent string) {
		type item struct {
			stage *provider.Stage
			node  *app.TreeNode
		}
		var items []item
		if !n.NotRun && !n.Loop && !n.Superseded {
			for i := range n.Build.Stages {
				items = append(items, item{stage: &n.Build.Stages[i]})
			}
			for _, c := range n.Children {
				items = append(items, item{node: c})
			}
		}
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.stage != nil {
				out = append(out, treeLine{text: indent + branch + stageText(*it.stage)})
				continue
			}
			out = append(out, treeLine{text: indent + branch + m.treeNodeText(it.node), node: it.node})
			add(it.node, indent+more)
		}
	}
	add(t.Root, "")
	return out
}

func (m Model) treeNodeText(n *app.TreeNode) string {
	switch {
	case n.NotRun:
		return dimStyle.Render("· " + n.PlanKey + "  not run")
	case n.Loop:
		return "↺ " + n.Build.Key + "  already shown above"
	case n.Superseded:
		return dimStyle.Render(stateGlyph(n.Build.State) + " " + n.Build.Key + "  earlier run")
	}
	b := n.Build
	d := b.Duration
	if !app.TreeFinished(b.State) && !b.StartedAt.IsZero() {
		d = m.now().Sub(b.StartedAt)
	}
	text := b.Key + "  " + stateCell(b.State) + "  " + view.Duration(d)
	if c := len(b.FailedTests); c > 0 {
		text += fmt.Sprintf("  %d tests failed", c)
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
	m.fromTree = true
	m.screen = screenColumns
	m.focus = focusMain
	m.detail = &b
	m.progress = provider.Progress{}
	m.expanded = defaultExpanded(b)
	m.treeCursor = 0
	if logs {
		return m.openLogs("", all)
	}
	return m.startWatch(b.Key)
}

func (m Model) treeScreenView() string {
	t := m.buildTree.tree
	head := " " + m.buildTree.root
	if m.buildTree.have {
		head += " · " + stateCell(t.State)
	} else {
		head += " · loading"
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

// askTreeRerun is F; Task 6 fills it in.
func (m Model) askTreeRerun() (tea.Model, tea.Cmd) { return m, nil }

type treeRerunMsg struct {
	Gen     int
	Results []app.RerunResult
}

func (m Model) handleTreeRerun(treeRerunMsg) (tea.Model, tea.Cmd) { return m, nil }
