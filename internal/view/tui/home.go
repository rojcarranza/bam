package tui

import (
	"cmp"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// homeState is what the Home screen keeps between frames (home spec §3-§4).
type homeState struct {
	view        homeView                  // which table Home shows
	sort        homeSort                  // the plans table's order
	presetSort  homeSort                  // the presets table's order
	setSort     homeSort                  // the sets table's order
	projectSort homeSort                  // the projects table's order
	live        map[string]provider.Build // builds the UI watches or started, by plan key
	loadedAt    time.Time                 // the last load in which every project succeeded
	stale       bool                      // the last load, or part of it, failed
	plansErr    bool                      // m.err came from the plans stream, not another one
	missing     []string                  // the configured projects missing on the last load

	tickGen int       // the auto-refresh loop's generation (home spec §4.2)
	lastKey time.Time // the last key pressed, for the idle cut-off
	idle    bool      // the loop stopped for idleness; the next key restarts it
}

// homeView is which table Home shows.
type homeView int

const (
	homePlans homeView = iota
	homePresets
	homeSets
	homeProjects
)

// homeCol is a sortable column. The presets table reuses the values: key is
// the target name there, and name is the plan key.
type homeCol int

const (
	colKey homeCol = iota
	colName
	colState
	colAge
)

// homeColNames are the column names :sort accepts, per table, in homeCol order.
var homeColNames = map[homeView][]string{
	homePlans:    {"key", "name", "state", "age"},
	homePresets:  {"target", "plan", "state", "age"},
	homeSets:     {"set", "state", "age"},
	homeProjects: {"key", "name"},
}

// homeSort is a column and a direction. desc reverses the column's natural
// direction (home spec §3.4).
type homeSort struct {
	col  homeCol
	desc bool
}

// next is what s does: the following column, in its natural direction.
func (s homeSort) next() homeSort { return homeSort{col: (s.col + 1) % 4} }

func (s homeSort) label(v homeView) string {
	arrow := "↑"
	if s.desc {
		arrow = "↓"
	}
	return homeColNames[v][s.col] + arrow
}

// parseSort reads a :sort argument: a column of view v, optionally with a
// leading "-" to reverse it.
func parseSort(v homeView, arg string) (homeSort, error) {
	name := strings.TrimPrefix(arg, "-")
	for i, n := range homeColNames[v] {
		if n == name {
			return homeSort{col: homeCol(i), desc: strings.HasPrefix(arg, "-")}, nil
		}
	}
	return homeSort{}, fmt.Errorf("unknown sort column %q; columns: %s", name, strings.Join(homeColNames[v], " "))
}

// stateRank orders the state column: what needs attention first, a plan
// that never built last.
func stateRank(b *provider.BuildSummary) int {
	if b == nil {
		return 8
	}
	switch b.State {
	case provider.StateFailed:
		return 0
	case provider.StateRunning:
		return 1
	case provider.StateQueued:
		return 2
	case provider.StateStopped:
		return 3
	case provider.StateNotBuilt:
		return 4
	case provider.StateSkipped:
		return 5
	case provider.StateSuccess:
		return 7
	}
	return 6
}

func finishedAt(b *provider.BuildSummary) time.Time {
	if b == nil {
		return time.Time{}
	}
	return b.FinishedAt
}

// compareAge puts the newest first and a missing time last.
func compareAge(a, b time.Time) int {
	switch {
	case a.Equal(b):
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	case a.After(b):
		return -1
	}
	return 1
}

func planKeyCompare(a, b provider.Plan) int {
	if c := strings.Compare(a.ProjectKey, b.ProjectKey); c != 0 {
		return c
	}
	return strings.Compare(a.Key, b.Key)
}

// planLess is the plans table's order under s. Ties break on project then
// key, ascending, so the order is total and a refresh cannot shuffle rows.
func planLess(s homeSort) func(a, b provider.Plan) bool {
	return func(a, b provider.Plan) bool {
		var c int
		switch s.col {
		case colName:
			c = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case colState:
			c = cmp.Compare(stateRank(a.LastBuild), stateRank(b.LastBuild))
		case colAge:
			c = compareAge(finishedAt(a.LastBuild), finishedAt(b.LastBuild))
		default:
			c = planKeyCompare(a, b)
		}
		if c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return planKeyCompare(a, b) < 0
	}
}

// planMatchText is what / matches a plan on: key, name, project and the last
// build's state word, so /fail keeps the failed plans.
func planMatchText(p provider.Plan) string {
	txt := p.ProjectKey + " " + p.Key + " " + p.Name
	if p.LastBuild != nil {
		txt += " " + style.Label(p.LastBuild.State)
	}
	return txt
}

// shortBy is the BY column: who or what started a build, from its reason.
func shortBy(reason string) string {
	r := strings.TrimSpace(reason)
	switch {
	case r == "":
		return "–"
	case strings.HasPrefix(r, "Manual run by "):
		return strings.TrimPrefix(r, "Manual run by ")
	case strings.HasPrefix(r, "Scheduled"):
		return "sched"
	case strings.HasPrefix(r, "Changes by"), strings.HasPrefix(r, "Code has changed"):
		return "commit"
	case strings.HasPrefix(r, "Child of"), strings.HasPrefix(r, "Dependant of"):
		return "dep"
	}
	return strings.Fields(r)[0]
}

// ageShort is the AGE column: now, 12m, 3h, 2d.
func ageShort(d time.Duration) string {
	if d < time.Minute {
		return "now"
	}
	return strings.TrimSuffix(view.Ago(d), " ago")
}

// tableCol is one column of a Home table.
type tableCol struct {
	title string
	width int
	right bool
}

// planColumns fits the plans table to width (home spec §3.2). NAME takes
// what is left; BY goes below 100 columns and PROJECT below 80.
func planColumns(width int, rows []provider.Plan) []tableCol {
	keyW, projW := len("KEY"), len("PROJECT")
	for _, p := range rows {
		keyW = max(keyW, lipgloss.Width(p.Key))
		projW = max(projW, lipgloss.Width(p.ProjectKey))
	}
	keyW, projW = min(keyW, 24), min(projW, 10)

	var cols []tableCol
	if width >= 80 {
		cols = append(cols, tableCol{title: "PROJECT", width: projW})
	}
	cols = append(cols,
		tableCol{title: "KEY", width: keyW},
		tableCol{title: "NAME"},
		tableCol{title: "STATE", width: 11},
		tableCol{title: "#", width: 6, right: true},
		tableCol{title: "AGE", width: 5})
	if width >= 100 {
		cols = append(cols, tableCol{title: "BY", width: 12})
	}
	return fillWidth(cols, width, "NAME")
}

// fillWidth gives the flexible column what the others leave of width: two
// columns of cursor, two between columns.
func fillWidth(cols []tableCol, width int, flexible string) []tableCol {
	used := 2 + 2*(len(cols)-1)
	for _, c := range cols {
		used += c.width
	}
	for i := range cols {
		if cols[i].title == flexible {
			cols[i].width = max(width-used, 4)
		}
	}
	return cols
}

// buildCells is STATE, #, AGE and BY for a plan's last build. A live build
// the UI is watching or just started wins while it has not finished (home
// spec §4.4).
func buildCells(last *provider.BuildSummary, live *provider.Build, now time.Time) (state, num, age, by string) {
	switch {
	case live != nil && !live.State.Finished():
		return stateCell(live.State), strconv.Itoa(live.Number), "now", shortBy(live.Reason)
	case last != nil:
		age = "–"
		if !last.FinishedAt.IsZero() {
			age = ageShort(now.Sub(last.FinishedAt))
		}
		return stateCell(last.State), strconv.Itoa(last.Number), age, shortBy(last.Reason)
	}
	return "–", "–", "–", "–"
}

// planCells is one plans-table row, in cols order.
func planCells(cols []tableCol, p provider.Plan, live *provider.Build, now time.Time) []string {
	state, num, age, by := buildCells(p.LastBuild, live, now)
	out := make([]string, len(cols))
	for i, c := range cols {
		switch c.title {
		case "PROJECT":
			out[i] = p.ProjectKey
		case "KEY":
			out[i] = p.Key
		case "NAME":
			out[i] = p.Name
		case "STATE":
			out[i] = state
		case "#":
			out[i] = num
		case "AGE":
			out[i] = age
		case "BY":
			out[i] = by
		}
	}
	return out
}

// tableLines is a header line and one line per row. cursor is the row index
// drawn as the selected bar, behind the ▸ mark; -1 marks none. Home's table
// always has the keys, so its bar is the focused one.
func tableLines(cols []tableCol, cells [][]string, cursor int) []string {
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.title
	}
	lines := []string{"  " + titleStyle.Render(joinCells(cols, head))}
	for r, row := range cells {
		if r == cursor {
			lines = append(lines, cursorStyle.Render("▸")+" "+selectedRow(joinCells(cols, row), rowWidth(cols), true))
			continue
		}
		lines = append(lines, "  "+joinCells(cols, row))
	}
	return lines
}

// rowWidth is how wide a row of cols is: the columns and the two spaces
// between each pair.
func rowWidth(cols []tableCol) int {
	w := 2 * (len(cols) - 1)
	for _, c := range cols {
		w += c.width
	}
	return w
}

func joinCells(cols []tableCol, cells []string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		s := cut(cells[i], c.width)
		pad := strings.Repeat(" ", max(c.width-lipgloss.Width(s), 0))
		if c.right {
			parts[i] = pad + s
		} else {
			parts[i] = s + pad
		}
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

// cut shortens s to w columns, ending in … when it had to cut.
func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return truncate(s, w-1) + "…"
}

// homeFocus is the panel whose list Home shows. While Home is visible, focus
// points at it, so the shared key handling (movement, /, R, o, y) acts on
// the table's rows.
func (m Model) homeFocus() focus {
	if m.home.view == homeSets {
		return focusSets
	}
	if m.home.view == homeProjects {
		return focusProjects
	}
	if m.home.view == homePresets {
		return focusPresets
	}
	return focusPlans
}

// goHome shows Home again.
func (m Model) goHome() (tea.Model, tea.Cmd) {
	if m.screen == screenPlan {
		m.closePlan()
	}
	m.screen = screenHome
	m.fromSet, m.fromTree, m.logsFromTree = false, false, false // Home leaves a set's or tree's build behind; esc must not return to it
	m.focus = m.homeFocus()
	// restartHomeTick has a pointer receiver and mutates m; calling it inline
	// as a return operand would rely on Go's unspecified evaluation order
	// between the two operands, so its mutation is applied here first.
	cmd := m.restartHomeTick()
	return m, cmd
}

// showPlans and showPresets pick Home's table (:plans, :presets).
func (m Model) showPlans() (tea.Model, tea.Cmd) {
	m.home.view = homePlans
	return m.goHome()
}

func (m Model) showPresets() (tea.Model, tea.Cmd) {
	m.fromProjects = false
	m.home.view = homePresets
	m.sortPresets()
	return m.goHome()
}

// lastBuildOf is the last build of planKey from the plans load, or nil when
// the plan is not in the configured projects.
func (m Model) lastBuildOf(planKey string) *provider.BuildSummary {
	for _, p := range m.plans.items {
		if p.Key == planKey {
			return p.LastBuild
		}
	}
	return nil
}

// planNameOf is the name of planKey from the plans load. A branch plan is
// named after its master plan; when two masters match, the longer key is the
// closer one. It is empty when the plan is not loaded.
func (m Model) planNameOf(planKey string) string {
	name, best := "", 0
	for _, p := range m.plans.items {
		switch {
		case p.Key == planKey:
			return p.Name
		case len(p.Key) > best && isBranchOf(planKey, p.Key):
			name, best = p.Name, len(p.Key)
		}
	}
	return name
}

// sortPresets orders the presets in place by the presets sort. It sorts the
// items rather than setting a list order, because the state and age columns
// read the plans load, which changes under the list.
func (m *Model) sortPresets() {
	s := m.home.presetSort
	keep, had := m.presets.selected()
	items := m.presets.items
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.col {
		case colName:
			c = strings.Compare(a.Plan, b.Plan)
		case colState:
			c = cmp.Compare(stateRank(m.lastBuildOf(a.Plan)), stateRank(m.lastBuildOf(b.Plan)))
		case colAge:
			c = compareAge(finishedAt(m.lastBuildOf(a.Plan)), finishedAt(m.lastBuildOf(b.Plan)))
		default:
			c = strings.Compare(a.Name, b.Name)
		}
		if c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return a.Name < b.Name
	})
	m.presets.refilter()
	if had {
		m.presets.selectFirst(func(t app.TargetInfo) bool { return t.Name == keep.Name })
	}
}

// presetColumns fits the presets table to width; BRANCH takes what is left.
func presetColumns(width int, rows []app.TargetInfo) []tableCol {
	nameW, planW := len("TARGET"), len("PLAN")
	for _, t := range rows {
		nameW = max(nameW, lipgloss.Width(t.Name))
		planW = max(planW, lipgloss.Width(t.Plan))
	}
	cols := []tableCol{
		{title: "TARGET", width: min(nameW, 24)},
		{title: "PLAN", width: min(planW, 24)},
		{title: "BRANCH"},
		{title: "STATE", width: 11},
		{title: "#", width: 6, right: true},
		{title: "AGE", width: 5},
	}
	return fillWidth(cols, width, "BRANCH")
}

// presetCells is one presets-table row. The state is the preset's plan's
// (its master plan, not its branch).
func presetCells(cols []tableCol, t app.TargetInfo, last *provider.BuildSummary, live *provider.Build, now time.Time) []string {
	state, num, age, _ := buildCells(last, live, now)
	branch := t.Branch
	if branch == "" {
		branch = "default"
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		switch c.title {
		case "TARGET":
			out[i] = t.Name
		case "PLAN":
			out[i] = t.Plan
		case "BRANCH":
			out[i] = branch
		case "STATE":
			out[i] = state
		case "#":
			out[i] = num
		case "AGE":
			out[i] = age
		}
	}
	return out
}

func (m Model) presetTable(width, height int) []string {
	rows := m.presets.rows()
	cols := presetColumns(width, rows)
	start, end := m.presets.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, t := range rows[start:end] {
		cells = append(cells, presetCells(cols, t, m.lastBuildOf(t.Plan), m.liveFor(t.Plan), m.now()))
	}
	return tableLines(cols, cells, m.presets.cursor-start)
}

// handleHomeKey takes the keys that mean something else, or nothing, on
// Home. It reports handled=false for every other key, which then goes
// through handleKey's shared switch with focus on Home's list.
func (m Model) handleHomeKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.home.view == homeProjects {
		if next, cmd, handled := m.handleProjectsKey(msg); handled {
			return next, cmd, true
		}
	}
	if m.home.view == homeSets {
		if next, cmd, handled := m.handleSetsKey(msg); handled {
			return next, cmd, true
		}
	}
	switch {
	case key.Matches(msg, keys.Enter):
		next, cmd := m.drillFromHome(focusBuilds)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Panel1):
		next, cmd := m.drillFromHome(focusBuilds)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Panel2):
		next, cmd := m.drillFromHome(focusStages)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Panel3):
		next, cmd := m.drillFromHome(focusTree)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Sort):
		m.setHomeSort(m.currentHomeSort().next())
		return m, nil, true
	case key.Matches(msg, keys.Refresh):
		next, cmd := m.refreshHome()
		return next.(Model), cmd, true
	case key.Matches(msg, keys.NextPanel), key.Matches(msg, keys.PrevPanel),
		key.Matches(msg, keys.Logs), key.Matches(msg, keys.AllLogs), key.Matches(msg, keys.Follow),
		key.Matches(msg, keys.Cancel), key.Matches(msg, keys.Branch),
		key.Matches(msg, keys.NextMatch), key.Matches(msg, keys.PrevMatch):
		return m, nil, true // panel keys; Home has no panel for them
	}
	return m, nil, false
}

// drillFromHome opens the plan screen on the row's plan, or on a preset's
// plan and branch, with focus on want.
func (m Model) drillFromHome(want focus) (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	if m.home.view == homePresets {
		t, ok := m.presets.selected()
		if !ok || t.Plan == "" {
			return m, nil
		}
		m, _ = m.enterPlan(t.Plan, t.Plan, nil, want)
		// A preset may name a branch, and its builds live under the branch
		// plan. resolveTargetBuildsCmd resolves it exactly as bam run does,
		// under the builds generation openPlan just bumped.
		return m, resolveTargetBuildsCmd(m.baseCtx(), m.svc, t, m.buildsGen)
	}
	p, ok := m.plans.selected()
	if !ok {
		return m, nil
	}
	return m.enterPlan(p.Key, p.Key, nil, want)
}

// currentHomeSort is the order of the table Home is currently showing.
func (m Model) currentHomeSort() homeSort {
	if m.home.view == homeProjects {
		return m.home.projectSort
	}
	if m.home.view == homeSets {
		return m.home.setSort
	}
	if m.home.view == homePresets {
		return m.home.presetSort
	}
	return m.home.sort
}

// setHomeSort re-sorts the active table and keeps the cursor on its row.
func (m *Model) setHomeSort(s homeSort) {
	if m.home.view == homeProjects {
		if int(s.col) >= len(homeColNames[homeProjects]) {
			s.col = 0 // key and name only
		}
		keep, had := m.projects.selected()
		m.home.projectSort = s
		m.projects.setOrder(projectLess(s))
		if had {
			m.projects.selectFirst(func(p projectItem) bool { return p.Key == keep.Key })
		}
		return
	}
	if m.home.view == homeSets {
		if int(s.col) >= len(homeColNames[homeSets]) {
			s.col = 0 // the sets table has three columns, not four
		}
		m.home.setSort = s
		m.sortSets()
		return
	}
	if m.home.view == homePresets {
		m.home.presetSort = s
		m.sortPresets()
		return
	}
	keep, had := m.plans.selected()
	m.home.sort = s
	m.plans.setOrder(planLess(s))
	if had {
		m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == keep.Key })
	}
}

// refreshHome is r on Home: reload the plans now, and the presets too when
// the presets table is showing.
func (m Model) refreshHome() (tea.Model, tea.Cmd) {
	m.err = nil
	if m.svc == nil {
		return m, nil
	}
	if m.home.view == homePresets && m.deps.Targets != nil {
		m.presetsGen++
		presets := loadPresetsCmd(m.deps, m.presetsGen)
		load := m.loadPlans()
		tick := m.restartHomeTick()
		return m, tea.Batch(presets, load, tick)
	}
	load := m.loadPlans()
	tick := m.restartHomeTick()
	return m, tea.Batch(load, tick)
}

// liveFor is the unfinished build the UI is watching or just started on
// planKey or one of its branches, or nil. An exact key match always counts;
// the branch match only counts when the live build itself names a branch —
// otherwise a plan whose key merely looks like a branch of another (PROJ-B2
// of PROJ-B) would mark the wrong row.
func (m Model) liveFor(planKey string) *provider.Build {
	for k, b := range m.home.live {
		if b.State.Finished() {
			continue
		}
		if k == planKey || (b.Branch != "" && isBranchOf(k, planKey)) {
			return &b
		}
	}
	return nil
}

// isBranchOf reports whether key is a branch plan of master: the master key
// followed by digits only (PROJ-PROV12 of PROJ-PROV).
func isBranchOf(key, master string) bool {
	rest, ok := strings.CutPrefix(key, master)
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// homeHeader is the line above the table: where, what and how it is sorted.
func (m Model) homeHeader() string {
	project := m.project
	if project == "" {
		project = "all"
		if m.svc != nil && len(m.svc.ProjectKeys()) == 0 {
			project = "all (none configured)"
		}
	}
	count, sortLabel, query := countOf(m.plans.len(), "plan"), m.home.sort.label(homePlans), m.plans.query
	if m.home.view == homePresets {
		count, sortLabel, query = countOf(m.presets.len(), "preset"), m.home.presetSort.label(homePresets), m.presets.query
	}
	if m.home.view == homeSets {
		count, sortLabel, query = countOf(m.sets.len(), "set"), m.home.setSort.label(homeSets), m.sets.query
	}
	if m.home.view == homeProjects {
		count, sortLabel, query = countOf(m.projectCount(), "project"), m.home.projectSort.label(homeProjects), m.projects.query
	}
	bam := "bam"
	if m.deps.Version != "" {
		bam += " " + m.deps.Version
	}
	parts := []string{bam, m.server, m.info.Version, m.user.Name, "project " + project,
		count, "sort " + sortLabel}
	if m.home.view == homeSets || m.home.view == homeProjects {
		// Sets are not filtered by project.
		parts = append(parts[:4], count, "sort "+sortLabel)
	}
	if query != "" {
		parts = append(parts, "filter /"+query)
	}
	if s := m.homeLoadMarker(); s != "" {
		parts = append(parts, s)
	}
	return " " + strings.Join(parts, " · ")
}

// homeLoadMarker is "refreshing…", "stale 3m", "stale" or "": what both
// header forms say about the last plans load.
func (m Model) homeLoadMarker() string {
	switch {
	case m.plans.loading && len(m.plans.items) > 0:
		return "refreshing…"
	case m.home.stale && !m.home.loadedAt.IsZero():
		return "stale " + ageShort(m.now().Sub(m.home.loadedAt))
	case m.home.stale:
		return "stale"
	}
	return ""
}

// homeTitle is the title set into the rule above the table, k9s style:
// view(scope)</query>[count], and what goes at the rule's right end.
func (m Model) homeTitle() (title, right string) {
	scope := m.project
	if scope == "" {
		scope = "all"
	}
	name, scope, n, query, sortLabel := "plans", "("+scope+")", m.plans.len(), m.plans.query, m.home.sort.label(homePlans)
	switch m.home.view {
	case homePresets:
		name, n, query, sortLabel = "presets", m.presets.len(), m.presets.query, m.home.presetSort.label(homePresets)
	case homeSets:
		name, scope, n, query, sortLabel = "sets", "", m.sets.len(), m.sets.query, m.home.setSort.label(homeSets)
	case homeProjects:
		name, scope, n, query, sortLabel = "projects", "", m.projectCount(), m.projects.query, m.home.projectSort.label(homeProjects)
	}
	title = name + scope
	if query != "" {
		title += "</" + query + ">"
	}
	title += fmt.Sprintf("[%d]", n)
	right = "sort " + sortLabel
	if s := m.homeLoadMarker(); s != "" {
		right += " · " + s
	}
	return title, right
}

// homeHints is the key hints the header shows for view, most useful first.
// They are the bindings themselves, so a hint cannot name a key that is not
// bound.
func homeHints(v homeView) []key.Binding {
	switch v {
	case homePresets:
		return []key.Binding{keys.Enter, keys.Run, keys.Project, keys.Filter, keys.Sort, keys.Command, keys.Help}
	case homeProjects:
		return []key.Binding{keys.Enter, keys.Open, keys.Filter, keys.Sort, keys.Copy, keys.Command, keys.Help}
	case homeSets:
		return []key.Binding{keys.Enter, keys.Run, keys.Filter, keys.Sort, keys.Command, keys.Help}
	}
	return []key.Binding{keys.Enter, keys.Run, keys.Open, keys.Project, keys.Filter, keys.Sort, keys.Copy, keys.Command, keys.Help}
}

// serverHost is the host of the server the UI is on, from the configuration:
// never a path or a query.
func (m Model) serverHost() string {
	for _, s := range m.deps.Servers {
		if s.Alias == m.server {
			if u, err := url.Parse(s.URL); err == nil {
				return u.Host
			}
		}
	}
	return ""
}

// hintWidth is one key-hint column: "<o> open in the browser" and a gap.
const hintWidth = 26

// homeHeaderBlock is the three lines of the k9s-style header: who and where
// on the left, key hints in as many columns as fit beside them.
func (m Model) homeHeaderBlock(width int) []string {
	server := m.server
	if h := m.serverHost(); h != "" {
		server += " [" + h + "]"
	}
	version := m.info.Version
	if m.deps.Version != "" {
		version = "bam " + m.deps.Version + " · Bamboo " + m.info.Version
	}
	ctx := [3][2]string{{"Server:", server}, {"Version:", version}, {"User:", m.user.Name}}
	leftW := 0
	for _, c := range ctx {
		leftW = max(leftW, 9+lipgloss.Width(c[1]))
	}
	hints := homeHints(m.home.view)
	// One column whenever a short hint fits, even if a long one is cut:
	// between 60 and 80 columns the header would otherwise show no keys.
	cols := max((width-leftW-4)/hintWidth, 0)
	if cols == 0 && width-leftW-4 >= 16 {
		cols = 1
	}
	lines := make([]string, 3)
	for r, c := range ctx {
		var b strings.Builder
		b.WriteString(" " + dimStyle.Render(fmt.Sprintf("%-9s", c[0])) + titleStyle.Render(c[1]))
		b.WriteString(strings.Repeat(" ", leftW-9-lipgloss.Width(c[1])+3))
		for col := 0; col < cols; col++ {
			i := col*3 + r
			if i >= len(hints) {
				break
			}
			h := hints[i].Help()
			cell := hintKeyStyle.Render("<"+h.Key+">") + " " + h.Desc
			b.WriteString(cell + strings.Repeat(" ", max(hintWidth-lipgloss.Width(cell), 1)))
		}
		lines[r] = truncate(strings.TrimRight(b.String(), " "), width)
	}
	return lines
}

// titleRule is a rule width cells wide with title centred in it and right
// near its end. right is left out when it does not fit.
func titleRule(width int, title, right string) string {
	t := " " + title + " "
	tw := lipgloss.Width(t)
	if tw >= width {
		return truncate(t, width)
	}
	left := (width - tw) / 2
	rest := width - left - tw
	r, tail := "", ""
	if right != "" && lipgloss.Width(right)+3 <= rest {
		r, tail = " "+right+" ", "─"
	}
	mid := rest - lipgloss.Width(r) - lipgloss.Width(tail)
	return dimStyle.Render(strings.Repeat("─", left)) + titleStyle.Render(t) +
		dimStyle.Render(strings.Repeat("─", mid)+r+tail)
}

// homeChrome is what sits above Home's table: the k9s-style block and title
// rule when the terminal has room, else the one-line header.
func (m Model) homeChrome() []string {
	if m.width < 60 || m.height < 20 {
		return []string{truncate(m.homeHeader(), m.width)}
	}
	title, right := m.homeTitle()
	return append(m.homeHeaderBlock(m.width), titleRule(m.width, title, right))
}

// planTable is the column header and the rows that fit in height lines.
func (m Model) planTable(width, height int) []string {
	rows := m.plans.rows()
	cols := planColumns(width, rows)
	start, end := m.plans.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, p := range rows[start:end] {
		cells = append(cells, planCells(cols, p, m.liveFor(p.Key), m.now()))
	}
	return tableLines(cols, cells, m.plans.cursor-start)
}

// homeView is the whole Home screen: header, table, status bar.
func (m Model) homeView() string {
	head := m.homeChrome()
	body := max(m.height-len(head)-1, 0)
	lines := m.planTable(m.width, body)
	if m.home.view == homePresets {
		lines = m.presetTable(m.width, body)
	}
	if m.home.view == homeSets {
		lines = m.setTable(m.width, body)
	}
	if m.home.view == homeProjects {
		lines = m.projectTable(m.width, body)
	}
	if len(lines) > body {
		lines = lines[:body]
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	out := make([]string, 0, m.height)
	out = append(out, head...)
	for _, l := range lines {
		out = append(out, truncate(l, m.width))
	}
	out = append(out, m.statusBar(m.width))
	return m.overlayView(strings.Join(out, "\n"))
}

// countOf is "1 plan", "3 plans": the header's row count.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
