package tui

import (
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
	stack []planFrame               //nolint:unused // filled by a later task
	cache map[string]provider.Build //nolint:unused // filled by a later task
	trees map[string]app.Tree       //nolint:unused // filled by a later task

	tree       treePreview
	treeCursor int

	stagesLoading bool // the open build's stages are being read
}

// planFrame is one plan screen on the drill stack: enough to draw it again
// with the cursor where it was.
type planFrame struct { //nolint:unused // used by a later task
	key, buildsPlan string
	build           provider.Build
	focus           focus
}

// treePreview is the Tree panel: the direct children of one build.
type treePreview struct {
	key     string   //nolint:unused // filled by a later task
	tree    app.Tree //nolint:unused // filled by a later task
	have    bool     //nolint:unused // filled by a later task
	loading bool     //nolint:unused // filled by a later task
	err     error    //nolint:unused // filled by a later task
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
	bb := b
	m.detail = &bb
	m.expanded = defaultExpanded(b)
	m.plan.stagesLoading = true
	if b.State.Finished() {
		return m, reloadBuildCmd(m.baseCtx(), m.svc, b.Key, m.detailGen)
	}
	next, cmd := m.startWatch(b.Key)
	return next.(Model), cmd
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
		m.builds.selectFirst(func(b provider.Build) bool { return b.Key == m.detail.Key })
		return m, nil
	}
	b, ok := m.builds.selected()
	if !ok || m.svc == nil {
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
