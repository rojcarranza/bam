package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// planScreen is the screen about one plan: its builds, the stages of the
// selected build, and what that build triggered (plan screen spec §3).
type planScreen struct {
	key   string          // the plan the screen is about; the header names it
	want  *provider.Build // the build to put the cursor on when the builds load
	older string          // a build pinned on top because it is not among the newest

	restGen int // bumped by every Builds cursor move

	// stack holds the plan screens Enter on a Tree row came from; cache and
	// trees hold finished builds and their trees by key.
	stack []planFrame
	cache map[string]provider.Build
	trees map[string]app.Tree

	tree       treePreview
	treeCursor int

	stagesLoading bool // the open build's stages are being read
}

// planFrame is one plan screen on the drill stack: enough to draw it again
// with the cursor where it was.
type planFrame struct {
	key, buildsPlan string
	build           provider.Build
	focus           focus
}

// treePreview is the Tree panel: the direct children of one build.
type treePreview struct {
	key     string
	tree    app.Tree
	have    bool
	loading bool
	err     error
}

// openPlan shows the plan screen on key, listing buildsPlan's builds (the
// master or a branch plan). want, when set, is shown at once and the cursor
// lands on it once the list arrives. The drill stack and cache survive, so
// Enter on a Tree row and esc back keep them.
func (m Model) openPlan(key, buildsPlan string, want *provider.Build, f focus) (Model, tea.Cmd) {
	m.screen = screenPlan
	m.home.tickGen++ // Home's auto-refresh stops while it is not visible
	m.leaveBuild()
	m.plan.key, m.plan.want, m.plan.older = key, want, ""
	m.plan.restGen++
	m.plan.tree, m.plan.treeCursor = treePreview{}, 0
	m.treePreviewGen++
	m.focus = f
	m.builds.setQuery("")
	cmds := []tea.Cmd{m.loadBuilds(buildsPlan, true)}
	if want != nil {
		var show tea.Cmd
		m, show = m.showBuild(*want)
		cmds = append(cmds, show)
	}
	return m, tea.Batch(cmds...)
}

// closePlan forgets the plan screen: its build, its watch, its stack and
// its cache. Every way off the plan screen to Home, and a server switch,
// calls it.
func (m *Model) closePlan() {
	m.leaveBuild()
	m.plan = planScreen{}
	m.treePreviewGen++
}

// showBuild makes b the open build: Stages shows it, a running build is
// watched, a finished one is read once for its stages.
func (m Model) showBuild(b provider.Build) (Model, tea.Cmd) {
	m.stopWatch()
	m.detailGen++
	m.cancelGen++
	m.progress = provider.Progress{}
	m.treeCursor = 0
	m.dropOlder(b.Key)
	if c, ok := m.plan.cache[b.Key]; ok {
		m.detail = &c
		m.expanded = defaultExpanded(c)
		m.plan.stagesLoading = false
		return m.loadTreePreview(b.Key, false)
	}
	bb := b
	m.detail = &bb
	m.expanded = defaultExpanded(b)
	m.plan.stagesLoading = true
	var read tea.Cmd
	if b.State.Finished() {
		read = reloadBuildCmd(m.baseCtx(), m.svc, b.Key, m.detailGen)
	} else {
		var next tea.Model
		next, read = m.startWatch(b.Key)
		m = next.(Model)
	}
	m, tree := m.loadTreePreview(b.Key, false)
	return m, tea.Batch(read, tree)
}

// dropOlder removes the pinned older build once the cursor opens another.
func (m *Model) dropOlder(key string) {
	if m.plan.older == "" || m.plan.older == key {
		return
	}
	older := m.plan.older
	m.plan.older = ""
	items := make([]provider.Build, 0, len(m.builds.items))
	for _, b := range m.builds.items {
		if b.Key != older {
			items = append(items, b)
		}
	}
	m.builds.setItems(items)
	m.builds.selectFirst(func(b provider.Build) bool { return b.Key == key })
}

// planBuildsLoaded places the cursor once the Builds list arrives: on the
// wanted build (pinned on top when it is older than the list), on the open
// build after a refresh, or on the newest build, which then opens.
func (m Model) planBuildsLoaded() (Model, tea.Cmd) {
	if w := m.plan.want; w != nil {
		m.plan.want = nil
		if !m.builds.selectFirst(func(b provider.Build) bool { return b.Key == w.Key }) && w.Number != 0 {
			m.builds.setItems(append([]provider.Build{*w}, m.builds.items...))
			m.builds.top()
			m.plan.older = w.Key
		}
		return m, nil
	}
	if m.detail != nil {
		if o := m.plan.older; o != "" && m.detail.Key == o {
			// A refresh brings the newest builds only; the pinned build
			// stays on top until the user moves off it.
			if !m.builds.selectFirst(func(b provider.Build) bool { return b.Key == o }) {
				m.builds.setItems(append([]provider.Build{*m.detail}, m.builds.items...))
				m.builds.top()
			}
			return m, nil
		}
		m.builds.selectFirst(func(b provider.Build) bool { return b.Key == m.detail.Key })
		return m, nil
	}
	b, ok := m.builds.selected()
	if !ok || m.svc == nil || m.screen != screenPlan {
		// Off the plan screen (esc went back before the list landed) the
		// newest build must not open a watch behind another screen.
		return m, nil
	}
	return m.showBuild(b)
}

// masterOf is the plan a branch plan belongs to, when the plans list has
// it; otherwise planKey itself.
func (m Model) masterOf(planKey string) string {
	best, n := planKey, 0
	for _, p := range m.plans.items {
		if p.Key == planKey {
			return planKey
		}
		if len(p.Key) > n && isBranchOf(planKey, p.Key) {
			best, n = p.Key, len(p.Key)
		}
	}
	return best
}

// planRestDelay is how long the Builds cursor rests before its build loads.
// A variable only so tests can make it fire at once.
var planRestDelay = 250 * time.Millisecond

// restCmd schedules loading the build under the Builds cursor, unless it is
// the one already open. Every call abandons the previous schedule.
func (m *Model) restCmd() tea.Cmd {
	if m.screen != screenPlan || m.focus != focusBuilds {
		return nil
	}
	b, ok := m.builds.selected()
	if !ok || (m.detail != nil && m.detail.Key == b.Key) {
		return nil
	}
	m.plan.restGen++
	gen, key := m.plan.restGen, b.Key
	return tea.Tick(planRestDelay, func(time.Time) tea.Msg { return planRestMsg{Gen: gen, Key: key} })
}

// planRest opens the build the cursor rested on, if it is still there.
func (m Model) planRest(msg planRestMsg) (Model, tea.Cmd) {
	if msg.Gen != m.plan.restGen || m.screen != screenPlan {
		return m, nil
	}
	b, ok := m.builds.selected()
	if !ok || b.Key != msg.Key {
		return m, nil
	}
	return m.showBuild(b)
}

// cacheBuild keeps a finished build with its stages, so moving back to it
// draws at once. A running build changes under the cache, so it never
// enters it.
func (m Model) cacheBuild(b provider.Build) Model {
	if m.screen != screenPlan || !b.State.Finished() || len(b.Stages) == 0 {
		return m
	}
	if m.plan.cache == nil {
		m.plan.cache = map[string]provider.Build{}
	}
	m.plan.cache[b.Key] = b
	return m
}

// loadTreePreview reads key's tree, or takes it from the cache unless fresh.
func (m Model) loadTreePreview(key string, fresh bool) (Model, tea.Cmd) {
	m.treePreviewGen++
	m.plan.treeCursor = 0
	if t, ok := m.plan.trees[key]; ok && !fresh {
		m.plan.tree = treePreview{key: key, tree: t, have: true}
		return m, nil
	}
	m.plan.tree = treePreview{key: key, loading: true}
	if m.svc == nil {
		return m, nil
	}
	return m, treePreviewCmd(m.baseCtx(), m.svc, key, m.treePreviewGen)
}

// treePreviewLoaded keeps the tree for the build it was read for. A finished
// tree is cached: Bamboo queues children when the parent ends, so a tree is
// final once nothing in it runs.
func (m Model) treePreviewLoaded(msg treePreviewMsg) (Model, tea.Cmd) {
	if msg.Gen != m.treePreviewGen || m.screen != screenPlan || msg.Key != m.plan.tree.key {
		return m, nil
	}
	m.plan.tree = treePreview{key: msg.Key, tree: msg.Tree, have: true, err: msg.Err}
	if msg.Err == nil && msg.Tree.Root != nil && msg.Tree.State != provider.StateRunning {
		if m.plan.trees == nil {
			m.plan.trees = map[string]app.Tree{}
		}
		m.plan.trees[msg.Key] = msg.Tree
	}
	return m, nil
}

// treePreviewRows is the Tree panel's rows: the open build's direct children.
func (m Model) treePreviewRows() []*app.TreeNode {
	if !m.plan.tree.have || m.plan.tree.tree.Root == nil {
		return nil
	}
	return m.plan.tree.tree.Root.Children
}

// drillTree opens the plan screen of the child under the Tree cursor, and
// remembers this one so esc comes back to it.
func (m Model) drillTree() (Model, tea.Cmd) {
	rows := m.treePreviewRows()
	if m.plan.treeCursor >= len(rows) || m.svc == nil {
		return m, nil
	}
	n := rows[m.plan.treeCursor]
	if n.Loop {
		return m, nil
	}
	frame := planFrame{key: m.plan.key, buildsPlan: m.buildsPlan, focus: focusTree}
	if m.detail != nil {
		frame.build = *m.detail
	}
	stack := append(m.plan.stack, frame)
	var want *provider.Build
	if !n.NotRun {
		b := n.Build
		want = &b
	}
	m, cmd := m.openPlan(m.masterOf(n.PlanKey), n.PlanKey, want, focusStages)
	m.plan.stack = stack
	return m, cmd
}

// popPlan goes back to the plan screen the last Tree drill came from.
func (m Model) popPlan() (Model, tea.Cmd) {
	last := len(m.plan.stack) - 1
	f := m.plan.stack[last]
	stack := m.plan.stack[:last]
	var want *provider.Build
	if f.build.Key != "" {
		b := f.build
		want = &b
	}
	m, cmd := m.openPlan(f.key, f.buildsPlan, want, f.focus)
	m.plan.stack = stack
	return m, cmd
}
