package tui

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// planScreen is the screen about one plan: its builds, the stages of the
// selected build, and what that build triggered (plan screen spec §3).
type planScreen struct {
	key   string          // the plan the screen is about; the header names it
	want  *provider.Build // the build to put the cursor on when the builds load
	older string          // a build pinned on top because it is not among the newest

	restGen int // bumped by every Builds cursor move

	// stack holds the plan screens Enter on a Tree row came from; cache
	// holds finished builds by key.
	stack []planFrame
	cache map[string]provider.Build

	tree       treePreview
	treeCursor int

	stagesLoading bool // the open build's stages are being read
	buildsStale   bool // the last Builds load failed; the rows are older
}

// planFrame is one plan screen on the drill stack: enough to draw it again
// with the cursor where it was.
type planFrame struct {
	key, buildsPlan string
	build           provider.Build
	focus           focus

	stageCursor int             // Stages: the row under the cursor
	expanded    map[string]bool // Stages: which stages were open
	treeCursor  int             // Tree: the row under the cursor
}

// planReturn is the plan screen t was pressed on: closing the tree screen
// draws it again, with its drill stack and its way back to a set or to the
// tree screen it was itself opened from.
type planReturn struct {
	frame    planFrame
	stack    []planFrame
	fromSet  bool
	fromTree bool
	tree     *treeScreen // the tree screen the plan screen came from, when fromTree
}

// currentFrame is the plan screen as it stands, with focus f.
func (m Model) currentFrame(f focus) planFrame {
	frame := planFrame{key: m.plan.key, buildsPlan: m.buildsPlan, focus: f,
		stageCursor: m.treeCursor, expanded: maps.Clone(m.expanded), treeCursor: m.plan.treeCursor}
	if m.detail != nil {
		frame.build = *m.detail
	}
	return frame
}

// restoreCursors puts the Stages and Tree cursors back where the frame had
// them, once the frame's plan screen is open again. A tree still loading
// keeps the Tree cursor and clamps it when it arrives.
func (m *Model) restoreCursors(f planFrame) {
	if m.detail == nil || m.detail.Key != f.build.Key {
		return
	}
	if f.expanded != nil {
		m.expanded = maps.Clone(f.expanded)
	}
	m.treeCursor = f.stageCursor
	m.clampTree()
	m.plan.treeCursor = f.treeCursor
	m.clampTreePreview()
}

// clampTreePreview keeps the Tree cursor on a row, once the rows are known.
func (m *Model) clampTreePreview() {
	if !m.plan.tree.have {
		return
	}
	m.plan.treeCursor = min(m.plan.treeCursor, max(len(m.treePreviewRows())-1, 0))
}

// wantOf is the build a frame opens on, or nil when it had none open.
func (f planFrame) wantOf() *provider.Build {
	if f.build.Key == "" {
		return nil
	}
	b := f.build
	return &b
}

// treePreview is the Tree panel: the direct children of one build.
type treePreview struct {
	key      string
	tree     app.Tree
	have     bool
	loading  bool
	progress app.TreeProgress // how far the read got, while loading
	err      error
	// refreshing: tree is shown while it is read again; failed: that read
	// failed, so the tree shown may be out of date.
	refreshing, failed bool
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
	m.stopTreePreview()
	m.focus = f
	m.builds.setQuery("")
	// A branch plan's result list does not name its branch; the build it
	// was opened on may, so remember that for this and later loads.
	if want != nil && want.Branch != "" && want.PlanKey == buildsPlan && buildsPlan != key {
		if m.branchNames == nil {
			m.branchNames = map[string]string{}
		}
		m.branchNames[buildsPlan] = want.Branch
	}
	cmds := []tea.Cmd{m.loadBuilds(buildsPlan, true)}
	if want != nil {
		var show tea.Cmd
		m, show = m.showBuild(*want)
		cmds = append(cmds, show)
	}
	return m, tea.Batch(cmds...)
}

// enterPlan opens a plan screen afresh. Every entry but a Tree drill and
// its esc back goes through it, so no drill stack, cache, wanted build or
// pin survives from an earlier visit (§4.1, §4.3).
func (m Model) enterPlan(key, buildsPlan string, want *provider.Build, f focus) (Model, tea.Cmd) {
	m.closePlan()
	return m.openPlan(key, buildsPlan, want, f)
}

// closePlan forgets the plan screen: its build, its watch, its builds, its
// stack and its cache. Every way off the plan screen to Home, and a server
// switch, calls it.
func (m *Model) closePlan() {
	m.leaveBuild()
	m.plan = planScreen{}
	m.stopTreePreview()
	// The builds are the plan's: left behind, t on Home would fall back to
	// one of them. A load still in flight for them is dropped.
	m.buildsGen++
	m.builds.setItems(nil)
	m.builds.setQuery("")
	m.builds.loading = false
	m.buildsPlan = ""
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
// wanted build (pinned on top when the list does not have it), on the open
// build after a refresh, or on the newest build, which then opens.
func (m Model) planBuildsLoaded() (Model, tea.Cmd) {
	newest := m.newestListed()
	if w := m.plan.want; w != nil {
		m.plan.want = nil
		if !m.builds.selectFirst(func(b provider.Build) bool { return b.Key == w.Key }) && w.Number != 0 {
			m.builds.setItems(append([]provider.Build{*w}, m.builds.items...))
			m.builds.top()
			// Bamboo's listing leaves out queued and running builds, so a
			// build missing from it is older only when the list goes past it;
			// a just-triggered build is the newest, not "(older)".
			if w.Number < newest {
				m.plan.older = w.Key
			}
		}
		return m, nil
	}
	if m.detail != nil {
		d := *m.detail
		pinned := m.plan.older == d.Key
		m.plan.older = ""
		if m.builds.selectFirst(func(b provider.Build) bool { return b.Key == d.Key }) {
			return m, nil // listed now: no pin, no label
		}
		// A refresh brings the newest finished builds only: a pinned older
		// build stays on top until the user moves off it, and the open build
		// still running above the list stays on top as the newest.
		if pinned || (d.Number > newest && d.PlanKey == m.buildsPlan) {
			m.builds.setItems(append([]provider.Build{d}, m.builds.items...))
			m.builds.top()
			if pinned {
				m.plan.older = d.Key
			}
		}
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

// newestListed is the highest build number among the listed builds.
func (m Model) newestListed() int {
	n := 0
	for _, b := range m.builds.items {
		n = max(n, b.Number)
	}
	return n
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
	if m.detail != nil && m.detail.Key == msg.Key {
		return m, nil // enter opened it before the rest ended
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
	m.stopTreePreview()
	m.plan.treeCursor = 0
	if t, ok := m.trees[key]; ok && !fresh {
		m.plan.tree = treePreview{key: key, tree: t, have: true}
		return m, nil
	}
	if old := m.plan.tree; old.key == key && old.have && old.err == nil && old.tree.Root != nil {
		// Keep the tree on screen while it is read again (#82).
		m.plan.tree = treePreview{key: key, tree: old.tree, have: true, refreshing: true}
	} else if t, ok := m.savedTree(key); ok && !fresh {
		m.plan.tree = treePreview{key: key, tree: t, have: true, refreshing: true}
	} else {
		m.plan.tree = treePreview{key: key, loading: true}
	}
	if m.svc == nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.treePreviewCancel = cancel
	return m, treePreviewCmd(ctx, m.svc, key, m.treePreviewGen, fresh)
}

// stopTreePreview moves the Tree panel on: a read in flight is cancelled,
// so it stops reading Bamboo, and its result is dropped.
func (m *Model) stopTreePreview() {
	m.treePreviewGen++
	if m.treePreviewCancel != nil {
		m.treePreviewCancel()
		m.treePreviewCancel = nil
	}
}

// treePreviewLoaded keeps the tree for the build it was read for. A finished
// tree is cached: Bamboo queues children when the parent ends, so a tree is
// final once nothing in it runs.
func (m Model) treePreviewLoaded(msg treePreviewMsg) (Model, tea.Cmd) {
	// The logs or the form may be on top of the plan screen; the preview is
	// still its own. Closing the plan screen bumps the generation.
	if msg.Gen != m.treePreviewGen || m.plan.key == "" || msg.Key != m.plan.tree.key {
		return m, nil
	}
	if msg.Err != nil && m.plan.tree.refreshing {
		m.plan.tree.refreshing, m.plan.tree.failed = false, true
		return m, nil
	}
	m.plan.tree = treePreview{key: msg.Key, tree: msg.Tree, have: true, err: msg.Err}
	m.clampTreePreview()
	if msg.Err == nil {
		m.keepTree(msg.Key, msg.Tree)
	}
	return m, nil
}

// savedTree is the tree saved on disk for key by an earlier session (#82).
// It may be out of date, so it is drawn marked refreshing and read again.
func (m Model) savedTree(key string) (app.Tree, bool) {
	if m.svc == nil {
		return app.Tree{}, false
	}
	return m.svc.CachedTree(key)
}

// keepTree caches t for the session, and on disk, when it is final, and
// otherwise drops any cached tree for key: a rerun makes a cached tree stale.
func (m *Model) keepTree(key string, t app.Tree) {
	if m.svc != nil {
		m.svc.KeepTree(key, t)
	}
	if !app.TreeFinal(t) {
		delete(m.trees, key)
		return
	}
	if m.trees == nil {
		m.trees = map[string]app.Tree{}
	}
	m.trees[key] = t
}

// treePreviewProgressed shows how far the Tree panel's read has got, if it
// is still this panel's read.
func (m Model) treePreviewProgressed(msg treePreviewProgressMsg) (Model, tea.Cmd) {
	if msg.Gen != m.treePreviewGen || m.plan.key == "" || msg.Key != m.plan.tree.key || !m.plan.tree.loading {
		return m, nil
	}
	m.plan.tree.progress = msg.Progress
	return m, msg.next
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
	stack := append(m.plan.stack, m.currentFrame(focusTree))
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
	m, cmd := m.openPlan(f.key, f.buildsPlan, f.wantOf(), f.focus)
	m.plan.stack = stack
	m.restoreCursors(f)
	return m, cmd
}

// planCompact is the small-terminal form: name line only, one panel.
func (m Model) planCompact() bool { return m.width < 60 || m.height < 20 }

// planLeftWidth is the Builds column: two fifths of the terminal, at least
// 30 and at most 60 columns, leaving the right side at least 40.
func planLeftWidth(total int) int {
	w := min(max(total*2/5, 30), 60)
	return max(min(w, total-40), 0)
}

// nameLine is the plan's full name, bold, or its key when the name is not
// known; the line the plan screen, the run form and the log screen share.
func (m Model) nameLine(width int, planKey string) string {
	name := m.planNameOf(planKey)
	if name == "" {
		name = planKey
	}
	return " " + titleStyle.Render(cut(name, width-1))
}

// projectOf is the plan's project key from the plans list, or the key's
// first part.
func (m Model) projectOf(planKey string) string {
	for _, p := range m.plans.items {
		if p.Key == planKey && p.ProjectKey != "" {
			return p.ProjectKey
		}
	}
	project, _, _ := strings.Cut(planKey, "-")
	return project
}

// planHeader is §3.2: the name, then project › key, branch and last build.
func (m Model) planHeader(width int) []string {
	lines := []string{m.nameLine(width, m.plan.key)}
	if m.planCompact() {
		return lines
	}
	left := " " + m.projectOf(m.plan.key) + " › " + m.plan.key
	if m.buildsPlan != "" && m.buildsPlan != m.plan.key {
		branch := m.buildsPlan
		if len(m.builds.items) > 0 && m.builds.items[0].Branch != "" {
			branch = m.builds.items[0].Branch
		}
		left += " @ " + branch
	}
	right := ""
	if len(m.builds.items) > 0 {
		lb := m.builds.items[0]
		if lb.Key == m.plan.older && len(m.builds.items) > 1 {
			lb = m.builds.items[1]
		}
		right = stateCell(lb.State) + dimStyle.Render(fmt.Sprintf(" last build #%d %s", lb.Number, m.ago(lb.FinishedAt)))
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if right == "" || gap < 2 {
		lines = append(lines, dimStyle.Render(cut(left, width)))
	} else {
		lines = append(lines, dimStyle.Render(left)+strings.Repeat(" ", gap)+right)
	}
	title := "builds"
	if q := m.builds.query; q != "" {
		title += "</" + q + ">"
	}
	title += fmt.Sprintf("[%d]", m.builds.len())
	marker := ""
	switch {
	case m.builds.loading:
		marker = "refreshing…"
	case m.plan.buildsStale:
		marker = "stale"
	}
	return append(lines, titleRule(width, title, marker))
}

// planBuildRows is the Builds panel: glyph, number, branch, age, who. Who
// goes first, then branch, when the panel is narrow.
func (m Model) planBuildRows(width, height int) string {
	rows := m.builds.rows()
	if len(rows) == 0 {
		if m.builds.loading {
			return dimStyle.Render("loading…")
		}
		return dimStyle.Render("no builds yet")
	}
	start, end := m.builds.window(height)
	inner := width - 4
	var b strings.Builder
	for i := start; i < end; i++ {
		bd := rows[i]
		focused := m.focus == focusBuilds
		b.WriteString(m.cursorFor(focused, m.builds.cursor == i))
		// A dim row is drawn plain and dimmed once: a coloured glyph inside
		// it would end the dim at its reset.
		older := bd.Key == m.plan.older
		glyph := stateGlyph(bd.State)
		if !older {
			glyph = stateStyle(bd.State).Render(glyph)
		}
		head := glyph + fmt.Sprintf(" #%-4d", bd.Number)
		branch, age, by := branchOrDefault(bd), ageShort(m.now().Sub(bd.FinishedAt)), shortBy(bd.Reason)
		if bd.FinishedAt.IsZero() {
			age = "–"
		}
		if older {
			by = "(older)"
		}
		// Who goes first, then branch, when the panel is narrow (§3.3).
		line := head + "  " + branch + "  " + age + "  " + by
		if lipgloss.Width(line) > inner {
			line = head + "  " + branch + "  " + age
		}
		if lipgloss.Width(line) > inner {
			line = head + "  " + age
		}
		line = truncate(line, inner)
		if older {
			line = dimStyle.Render(line)
		}
		if m.builds.cursor == i {
			line = selectedRow(line, inner, focused)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// treeBody is the Tree panel: one row per child of the open build.
func (m Model) treeBody(width, height int) string {
	t := m.plan.tree
	switch {
	case m.detail == nil:
		return dimStyle.Render("no builds yet")
	case t.loading:
		text := "reading the tree…"
		if t.progress != (app.TreeProgress{}) {
			text += " " + view.TreeProgressText(t.progress)
		}
		return dimStyle.Render(cut(text, width-4))
	case t.err != nil:
		// Cut to the panel, as every other row is: a wrapped error would
		// push the screen past the terminal's bottom.
		return errorStyle.Render(cut("could not read the tree: "+errorWhat(t.err), width-4))
	}
	rows := m.treePreviewRows()
	if len(rows) == 0 {
		return dimStyle.Render(fmt.Sprintf("#%d triggered nothing", m.detail.Number))
	}
	start, end := treeWindow(m.plan.treeCursor, len(rows), height)
	inner := width - 4
	focused := m.focus == focusTree
	names := make([]string, len(rows))
	nameW := 0
	for i, n := range rows {
		if names[i] = m.planNameOf(n.PlanKey); names[i] == "" {
			names[i] = n.PlanKey
		}
		nameW = max(nameW, lipgloss.Width(names[i]))
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		n, name := rows[i], names[i]
		var line string
		switch {
		case n.NotRun:
			line = dimStyle.Render("– " + name + "  not run")
		case n.Loop:
			line = dimStyle.Render("↺ " + name + "  already shown")
		default:
			// The glyph leads the row, so the state after the number is its
			// label alone; names are padded so the numbers line up.
			// A superseded row is drawn plain and dimmed once: a coloured
			// glyph inside it would end the dim at its reset.
			st := stateStyle(n.Build.State)
			if n.Superseded {
				st = lipgloss.NewStyle()
			}
			tail := fmt.Sprintf("  #%-4d %s", n.Build.Number, st.Render(style.Label(n.Build.State)))
			w := min(nameW, inner-2-lipgloss.Width(tail))
			line = st.Render(stateGlyph(n.Build.State)) + " " +
				lipgloss.NewStyle().Width(w).Render(cut(name, w)) + tail
			if n.Superseded {
				line = dimStyle.Render(truncate(line, inner))
			}
		}
		line = truncate(line, inner)
		b.WriteString(m.cursorFor(focused, m.plan.treeCursor == i))
		if m.plan.treeCursor == i {
			line = selectedRow(line, inner, focused)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// stagesTitle names the build Stages shows.
func (m Model) stagesTitle() string {
	if m.detail == nil {
		return "Stages"
	}
	t := fmt.Sprintf("Stages · #%d", m.detail.Number)
	if m.plan.stagesLoading {
		t += " · loading"
	}
	return t
}

func (m Model) treeTitle() string {
	if m.detail == nil {
		return "Tree"
	}
	t := fmt.Sprintf("Tree · what #%d triggered", m.detail.Number)
	switch {
	case m.plan.tree.refreshing:
		t += " · refreshing…"
	case m.plan.tree.failed:
		t += " · refresh failed"
	}
	return t
}

func (m Model) stagesPanel(width, height int) string {
	body := dimStyle.Render("no builds yet")
	if m.detail != nil {
		body = m.stagesBody(width-2, height-2)
	}
	return panel(m.stagesTitle(), m.focus == focusStages, width, height, body)
}

// rightHeights splits the right column (§3.3): Stages gets its rows up to
// 60%, at least 5; Tree the rest, at least 3; an empty tree takes 3.
func (m Model) rightHeights(body int) (stages, tree int) {
	if m.plan.tree.have && m.plan.tree.err == nil && len(m.treePreviewRows()) == 0 {
		return body - 3, 3
	}
	want := 2 + 2 // border and the reason line with its blank
	if m.detail != nil {
		want += len(buildTree(*m.detail, m.expanded)) + failedTestLines(*m.detail)
	}
	stages = min(max(want, 5), body*3/5)
	tree = body - stages
	if tree < 3 {
		tree, stages = 3, body-3
	}
	return stages, tree
}

// planView is the whole plan screen: header, panels, status bar.
func (m Model) planView() string {
	head := m.planHeader(m.width)
	body := max(m.height-len(head)-1, 0)
	var frame string
	if m.planCompact() {
		switch m.focus {
		case focusStages:
			frame = m.stagesPanel(m.width, body)
		case focusTree:
			frame = panel(m.treeTitle(), true, m.width, body, m.treeBody(m.width, body-2))
		default:
			frame = panel("Builds", true, m.width, body, m.planBuildRows(m.width, body-2))
		}
	} else {
		lw := planLeftWidth(m.width)
		rw := m.width - lw
		sh, th := m.rightHeights(body)
		frame = lipgloss.JoinHorizontal(lipgloss.Top,
			panel("Builds", m.focus == focusBuilds, lw, body, m.planBuildRows(lw, body-2)),
			lipgloss.JoinVertical(lipgloss.Left,
				m.stagesPanel(rw, sh),
				panel(m.treeTitle(), m.focus == focusTree, rw, th, m.treeBody(rw, th-2))))
	}
	out := append(append([]string{}, head...), frame, m.statusBar(m.width))
	return m.overlayView(strings.Join(out, "\n"))
}
