package tui

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// inputMode says what the text input is collecting, if anything.
type inputMode int

const (
	inputNone inputMode = iota
	inputFilter
	inputSearch
	inputCommand
	inputPreset
)

// screen is what fills the terminal. The log screen takes the whole width.
type screen int

const (
	screenPlan screen = iota
	screenLogs
	screenForm
	screenHome // the plans table bam opens on (home spec §2)
	screenSet  // one run set: its stages and runs
	screenSetVars
	screenTree // one build's tree: every build it triggered
)

// focus is which panel or table takes keys. Plans, Presets, Sets and
// Projects are Home's tables; Builds, Stages and Tree are the plan screen's
// panels.
type focus int

const (
	focusPlans focus = iota
	focusBuilds
	focusPresets
	focusStages // the plan screen's stage and job tree of the open build
	focusSets   // Home's sets table; not in the plan screen's tab order
	focusProjects
	focusTree // the plan screen's list of what the open build triggered
)

// overlay is the modal on top, if any. At most one is open.
type overlay int

const (
	overlayNone overlay = iota
	overlayServers
	overlayProjects
	overlayBranches
	overlayHelp
	overlayError
	overlayDryRun
	overlayConfirm
)

var tabOrder = []focus{focusBuilds, focusStages, focusTree}

// next and prev look the focus up rather than indexing by its value: Home's
// tables are not in the tab order, and from one of them tab lands on Builds.
func (f focus) next() focus {
	return tabOrder[(slices.Index(tabOrder, f)+1)%len(tabOrder)]
}

func (f focus) prev() focus {
	i := max(slices.Index(tabOrder, f), 0)
	return tabOrder[(i+len(tabOrder)-1)%len(tabOrder)]
}

// Model is the whole UI. Update is a pure function of (Model, tea.Msg).
type Model struct {
	deps Deps

	// ctx is the UI's lifetime. Every watch, follow and load hangs off it, so
	// when bubbletea returns because its context was cancelled — which can
	// happen without any key reaching quit — no goroutine outlives the UI.
	ctx context.Context

	width, height int

	screen  screen
	focus   focus
	overlay overlay

	server string
	info   provider.ServerInfo
	user   provider.User
	svc    *app.Service

	project    string // the project filter; empty means every project
	buildsPlan string // the plan (or branch plan) the Builds panel holds
	// branchNames maps branch plan keys to their short names, learned from
	// the branch picker: a branch plan's result list does not name its branch.
	branchNames map[string]string

	detail     *provider.Build
	progress   provider.Progress // the server's estimate for detail, if any
	expanded   map[string]bool
	treeCursor int

	plans    listState[provider.Plan]
	builds   listState[provider.Build]
	presets  listState[app.TargetInfo]
	sets     listState[setItem]
	projects listState[projectItem]
	picker   listState[pickerItem]

	confirm  confirmState
	help     helpState
	form     formState
	logs     logState
	input    textinput.Model
	inputFor inputMode
	// presetBuild is the build the preset-name prompt will save.
	presetBuild string

	// One generation per asynchronous stream. Bumping a stream's generation
	// abandons every request already in flight on it, so a slow response for
	// a server, project, branch, build or job the user has left cannot land
	// on the one they are looking at now.
	plansGen, buildsGen, logsGen int
	watchGen, followGen, formGen int
	connGen, detailGen           int
	pickerGen, presetsGen        int
	cancelGen                    int
	setsGen, setGen              int
	projectsGen                  int
	treeGen                      int
	treePreviewGen               int
	rerunGen                     int // the tree screen opened now; only opening or closing it moves on

	watchCancel context.CancelFunc
	watchCh     <-chan app.Event
	treeCancel  context.CancelFunc
	treeCh      <-chan app.TreeEvent

	treePreviewCancel context.CancelFunc // the Tree panel's read in flight

	// trees holds finished trees by root key for the session, so leaving the
	// plan screen does not read them again; a server switch clears it.
	trees map[string]app.Tree

	// revisionRun is the build the form last triggered with a revision, so a
	// not-built result can say which revision Bamboo could not build.
	revisionRun struct{ Key, Rev string }

	followCancel context.CancelFunc
	followEvents <-chan app.LogEvent
	followDone   <-chan error
	followFrom   map[string]int // the offsets the current follow resumed at

	now func() time.Time

	err error
	// cmdErr marks err as the : bar's own mistake, which the next command
	// clears; an error from anywhere else is not the bar's to clear.
	cmdErr bool
	status string
	// projectStarted is set once Deps.Project has been applied, so a later
	// connection opens on its own server's default_project.
	projectStarted bool

	home homeState

	train        trainState
	setView      setScreen
	setVars      setVarsState
	setDry       []string // the set dry run the overlay shows on the vars prompt
	buildTree    treeScreen
	plan         planScreen
	fromTree     bool // the panels were opened from the tree screen; esc returns there
	logsFromTree bool // the log screen was opened from the tree screen; esc returns there
	fromProjects bool // Home's plans were reached from the projects view; esc returns there
	fromSet      bool // the panels were opened from a set screen run; esc returns there

	formFrom screen // where esc from the run form returns
}

// New builds the initial model. It starts no work; Init does that.
func New(d Deps) Model {
	m := Model{
		deps:     d,
		ctx:      context.Background(),
		server:   d.Initial,
		screen:   screenHome,
		focus:    focusPlans,
		plans:    newList(planMatchText),
		builds:   newList(func(b provider.Build) string { return b.Key + " " + b.Branch + " " + b.Reason }),
		presets:  newList(func(t app.TargetInfo) string { return t.Name + " " + t.Plan + " " + t.Branch }),
		sets:     newList(func(s setItem) string { return s.Info.Name }),
		projects: newList(projectMatchText),
		picker:   newList(func(p pickerItem) string { return p.Label + " " + p.Detail }),
		now:      time.Now,
		input:    textinput.New(),
	}
	m.plans.setOrder(planLess(m.home.sort))
	m.projects.setOrder(projectLess(homeSort{}))
	m.home.lastKey = m.now()
	return m
}

// loadPlans, loadBuilds and loadLogs own their stream's generation bump, so
// no call site can issue a request without invalidating the older ones.
func (m *Model) loadPlans() tea.Cmd {
	m.plansGen++
	m.plans.loading = true
	return loadPlansCmd(m.baseCtx(), m.svc, m.project, m.plansGen)
}

// clear empties the panel first, for a switch to a different plan or branch:
// the rows on screen belong to what is being left, and Enter on one of them
// would open a build from the wrong plan. A plain refresh keeps them, so the
// cursor does not jump. The panel names planKey from the request on, so its
// title never names the plan being left, even if the load fails.
func (m *Model) loadBuilds(planKey string, clear bool) tea.Cmd {
	m.buildsGen++
	m.builds.loading = true
	m.buildsPlan = planKey
	if clear {
		m.builds.setItems(nil)
	}
	return loadBuildsCmd(m.baseCtx(), m.svc, planKey, buildsPerPlan, m.buildsGen)
}

// presetsForServer keeps the panel to the presets that belong to the server
// the UI is connected to. A preset may name a server of its own, and the
// panel loads every layer of the configuration: acting on one bound to
// another alias would resolve its plan against the wrong Bamboo and show
// unrelated builds. Switching server with S reloads the panel.
func presetsForServer(ts []app.TargetInfo, alias string) []app.TargetInfo {
	out := make([]app.TargetInfo, 0, len(ts))
	for _, t := range ts {
		if t.Server == "" || t.Server == alias {
			out = append(out, t)
		}
	}
	return out
}

// currentStream says whether a message belongs to the request the model is
// waiting on. An untagged message (streamNone) is always current.
func (m Model) currentStream(s stream, gen int) bool {
	switch s {
	case streamConnect:
		return gen == m.connGen
	case streamPlans:
		return gen == m.plansGen
	case streamBuilds:
		return gen == m.buildsGen
	case streamLogs:
		return gen == m.logsGen
	case streamDetail:
		return gen == m.detailGen
	case streamPicker:
		return gen == m.pickerGen
	case streamPresets:
		return gen == m.presetsGen
	case streamRun:
		return gen == m.formGen
	case streamCancel:
		return gen == m.cancelGen
	case streamSets:
		return gen == m.setsGen
	case streamProjects:
		return gen == m.projectsGen
	}
	return true
}

// baseCtx is the context every command runs under. A zero Model built by a
// test still gets a usable one.
func (m Model) baseCtx() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// withContext roots the model in the UI's lifetime. Run calls it before the
// program starts.
func (m Model) withContext(ctx context.Context) Model {
	m.ctx = ctx
	return m
}

// connected folds the start-up handshake's result into the model, so Init
// does not dial a second time.
func (m Model) connected(msg connectedMsg) Model {
	m.svc, m.info, m.user, m.server = msg.Svc, msg.Info, msg.User, msg.Alias
	m.startProject()
	// Init loads the plans of this connection, so the panel is already
	// waiting on them here. Init's receiver is a copy, so it cannot say so.
	m.plans.loading = true
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{}
	switch {
	case m.svc != nil:
		// Run's handshake already connected; go straight to the panels.
		// loadPlans is not usable here: Init's receiver is a copy, so the
		// generation it bumped would be lost and every plansLoadedMsg the
		// request produced would be dropped as stale.
		cmds = append(cmds, loadPlansCmd(m.baseCtx(), m.svc, m.project, m.plansGen))
	case m.deps.Connect != nil:
		cmds = append(cmds, connectCmd(m.baseCtx(), m.deps, m.server, m.connGen))
	}
	if m.deps.Targets != nil {
		cmds = append(cmds, loadPresetsCmd(m.deps, m.presetsGen))
	}
	if m.screen == screenHome {
		cmds = append(cmds, homeTickCmd(m.home.tickGen))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// The viewport holds its own width and height, so a resize has to
		// reach it or the log stays wrapped for the old terminal.
		m.logs.resize(m.width, m.logHeight())
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case homeTickMsg:
		return m.homeTick(msg)
	case connectedMsg:
		if msg.Gen != m.connGen {
			return m, nil
		}
		m.svc, m.info, m.user, m.server = msg.Svc, msg.Info, msg.User, msg.Alias
		m.startProject()
		m.err = nil
		cmd := m.loadPlans()
		if m.home.view == homeProjects {
			projects := m.loadProjects()
			return m, tea.Batch(cmd, projects)
		}
		return m, cmd
	case plansLoadedMsg:
		if msg.Gen != m.plansGen {
			return m, nil
		}
		return m.plansLoaded(msg), nil
	case buildsLoadedMsg:
		if msg.Gen != m.buildsGen {
			return m, nil
		}
		m.builds.loading = false
		m.plan.buildsStale = false
		m.buildsPlan = msg.PlanKey
		app.WithBranch(msg.Builds, m.branchNames[msg.PlanKey])
		m.builds.setItems(msg.Builds)
		if m.plan.key != "" {
			return m.planBuildsLoaded()
		}
		return m, nil
	case presetsLoadedMsg:
		if msg.Gen != m.presetsGen {
			return m, nil
		}
		m.presets.setItems(presetsForServer(msg.Targets, m.server))
		m.sortPresets()
		return m, nil
	case projectsLoadedMsg:
		if msg.Stream == streamProjects {
			if msg.Gen != m.projectsGen {
				return m, nil
			}
			return m.projectsLoaded(msg), nil
		}
		if msg.Stream != streamPicker || msg.Gen != m.pickerGen {
			return m, nil
		}
		// The empty value is the "no filter" row, so one list both sets and
		// clears the filter.
		items := []pickerItem{{Label: "all projects", Value: ""}}
		for _, p := range msg.Projects {
			items = append(items, pickerItem{Label: p.Key, Value: p.Key, Detail: p.Name})
		}
		m.picker.setItems(items)
		m.picker.cursor = indexOf(items, m.project)
		return m, nil
	case branchesLoadedMsg:
		if msg.Gen != m.pickerGen {
			return m, nil
		}
		// The first row is the master plan itself, so one list both sets and
		// clears the branch.
		items := []pickerItem{{Label: "default branch", Value: msg.MasterKey}}
		if m.branchNames == nil {
			m.branchNames = map[string]string{}
		}
		for _, br := range msg.Branches {
			items = append(items, pickerItem{Label: br.ShortName, Value: br.Key, Detail: view.MarkDisabled(br.Name, br.Disabled)})
			m.branchNames[br.Key] = br.ShortName
		}
		m.picker.setItems(items)
		m.picker.cursor = indexOf(items, m.buildsPlan)
		return m, nil
	case copiedMsg:
		m.status = "copied to clipboard (osc 52)"
		return m, nil
	case presetSavedMsg:
		m.status = "added preset " + msg.Name + " to " + msg.Path
		return m, nil
	case logEventMsg:
		if msg.Gen != m.followGen {
			return m, nil
		}
		m.logs.apply(msg.Event)
		return m, drainFollowCmd(m.followEvents, m.followDone, m.followGen)
	case followEndedMsg:
		if msg.Gen != m.followGen {
			return m, nil
		}
		m.stopFollow()
		if msg.Err != nil {
			m.err = msg.Err
		}
		return m, nil
	case logsLoadedMsg:
		if msg.Gen != m.logsGen {
			return m, nil
		}
		m.logs.title, m.logs.jobKey, m.logs.url, m.logs.all = msg.Title, msg.JobKey, msg.URL, msg.All
		m.logs.multi, m.logs.offsets, m.logs.stage = msg.Multi, msg.Offsets, msg.Stage
		// Keep the build the logs were read from. A row straight from the
		// Builds panel carries no stages, so without this f could not find
		// the job it is meant to follow.
		if msg.Build.Key != "" {
			b := msg.Build
			m.logs.build = &b
		}
		m.logs.setLines(m.width, m.logHeight(), msg.Lines)
		if msg.Live && m.screen == screenLogs {
			return m.startFollow()
		}
		return m, nil
	case formLoadedMsg:
		if msg.Gen != m.formGen {
			return m, nil
		}
		m.form.loading = false
		m.form.ref, m.form.target, m.form.base = msg.Ref, msg.Target, msg.Base
		m.form.fromBuild, m.form.fromRevision = msg.FromBuild, msg.FromRevision
		m.form.fields = buildFields(msg.Base, msg.Ref)
		m.form.cursor = 0
		// A stray enter struck while loading (now ignored by handleFormKey,
		// but a stale build or an older session could still reach here with
		// editing left on) must not carry into the freshly loaded form.
		m.form.editing = false
		m.form.input.Blur()
		m.revalidate()
		return m, nil
	case cancelledMsg:
		// The build was stopped either way; this only decides whether the
		// screen the user is on now is the one that should say so.
		if msg.Gen != m.cancelGen {
			return m, nil
		}
		// An already-finished build is not an error: it is a fact about
		// timing, and the status bar is where facts go.
		if msg.AlreadyFinished {
			m.status = msg.Build.Key + " had already finished"
			return m, nil
		}
		m.status = "cancelled " + msg.Build.Key
		if msg.Build.Key != "" && m.detail != nil && m.detail.Key == msg.Build.Key {
			b := msg.Build
			m.detail = &b
			m.clampTree()
		}
		return m, nil
	case triggeredMsg:
		if msg.Gen != m.formGen {
			return m, nil
		}
		return m.openTriggered(msg.Build)
	case planRestMsg:
		return m.planRest(msg)
	case buildLoadedMsg:
		if msg.Gen != m.detailGen {
			return m, nil
		}
		b := msg.Build
		m.detail = &b
		m.progress = msg.Progress
		m.clampTree()
		m.plan.stagesLoading = false
		m = m.cacheBuild(b)
		return m, nil
	case setsLoadedMsg:
		if msg.Gen != m.setsGen {
			return m, nil
		}
		m.sets.loading = false
		m.sets.setItems(msg.Items)
		m.sortSets()
		return m, nil
	case setEventMsg:
		if msg.Gen != m.train.gen {
			return m, nil
		}
		return m.handleSetEvent(msg.Event)
	case setClosedMsg:
		if msg.Gen != m.train.gen {
			return m, nil
		}
		// The train wrote its record as it ended: re-read it so the set
		// screen and the sets table show the run that just finished.
		set := m.train.set
		m.train.end()
		if m.setView.info.Name != set {
			return m, m.loadSets()
		}
		m.setGen++
		return m, tea.Batch(loadSetCmd(m.baseCtx(), m.svc, set, m.setGen), m.loadSets())
	case setPollMsg:
		if msg.Gen != m.setGen || m.screen != screenSet {
			return m, nil
		}
		m.setGen++
		return m, loadSetCmd(m.baseCtx(), m.svc, m.setView.info.Name, m.setGen)
	case switchServerMsg:
		return m.switchServer(msg.Alias)
	case trainStartedMsg:
		return m.handleTrainStarted(msg)
	case setDryMsg:
		return m.handleSetDry(msg)
	case treeEventMsg:
		return m.handleTreeEvent(msg)
	case treePreviewMsg:
		return m.treePreviewLoaded(msg)
	case treePreviewProgressMsg:
		return m.treePreviewProgressed(msg)
	case treeClosedMsg:
		return m, nil
	case treeRerunMsg:
		return m.handleTreeRerun(msg)
	case setLoadedMsg:
		return m.handleSetLoaded(msg)
	case watchEventMsg:
		if msg.Gen != m.watchGen {
			return m, nil
		}
		return m.handleWatchEvent(msg.Event)
	case watchClosedMsg:
		// A closed channel from an abandoned watch must not stop the current
		// one: that is a different watch, on a different build.
		if msg.Gen != m.watchGen {
			return m, nil
		}
		m.stopWatch()
		return m, nil
	case errMsg:
		// A failure from a request the user has abandoned must not replace
		// the status of the one they are waiting on.
		if !m.currentStream(msg.Stream, msg.Gen) {
			return m, nil
		}
		m.err = msg.Err
		m.status = ""
		m.plans.loading, m.builds.loading = false, false
		if msg.Stream == streamDetail {
			m.plan.stagesLoading = false
		}
		if msg.Stream == streamBuilds && m.plan.key != "" {
			m.plan.buildsStale = true
		}
		if msg.Stream == streamProjects {
			m.projects.loading = false
		}
		if msg.Stream == streamSets {
			m.sets.loading = false
		}
		if msg.Stream == streamRun && m.form.loading {
			// loading stays true: with no fields, keys must stay locked.
			m.form.err = msg.Err
		}
		// plansErr tracks whether the error now showing came from the plans
		// stream, so a later successful plans load knows whether it is the
		// one that gets to clear it (finding 5).
		m.home.plansErr = msg.Stream == streamPlans
		m.cmdErr = false
		if msg.Stream == streamPlans {
			m.home.stale = true
		}
		return m, nil
	}
	return m, nil
}

// handleKey notes the key for Home's idle cut-off, then dispatches it.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A terminal may deliver several typed characters in one message (a fast
	// typist over ssh, a paste outside bracketed paste). Outside a text field
	// they are keys, so each one is handled on its own, in order; once one of
	// them opens a field, the rest become its text.
	if msg.Type == tea.KeyRunes && !msg.Paste && len(msg.Runes) > 1 && m.inputFor == inputNone && !m.form.editing {
		var next tea.Model = m
		var cmds []tea.Cmd
		for _, r := range msg.Runes {
			var cmd tea.Cmd
			next, cmd = next.(Model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
			cmds = append(cmds, cmd)
		}
		return next, tea.Batch(cmds...)
	}
	resume := m.noteKey()
	next, cmd := m.dispatchKey(msg)
	if resume == nil {
		return next, cmd
	}
	return next, tea.Batch(resume, cmd)
}

func (m Model) dispatchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inputFor != inputNone {
		return m.handleInputKey(msg)
	}
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	if m.screen == screenForm {
		return m.handleFormKey(msg)
	}
	if m.screen == screenLogs {
		return m.handleLogKey(msg)
	}
	if m.screen == screenSet {
		return m.handleSetKey(msg)
	}
	if m.screen == screenSetVars {
		return m.handleSetVarsKey(msg)
	}
	if m.screen == screenTree {
		return m.handleTreeKey(msg)
	}
	if m.screen == screenHome {
		if next, cmd, handled := m.handleHomeKey(msg); handled {
			return next, cmd
		}
	}
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.NextPanel):
		m.focus = m.focus.next()
	case key.Matches(msg, keys.PrevPanel):
		m.focus = m.focus.prev()
	case key.Matches(msg, keys.Panel1):
		m.focus = focusBuilds
	case key.Matches(msg, keys.Panel2):
		m.focus = focusStages
	case key.Matches(msg, keys.Panel3):
		m.focus = focusTree
	case key.Matches(msg, keys.Down):
		m.moveFocused(1)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Up):
		m.moveFocused(-1)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Top):
		m.jumpFocused(true)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Bottom):
		m.jumpFocused(false)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Enter):
		return m.drill()
	case key.Matches(msg, keys.Filter):
		if m.screen == screenPlan && m.focus != focusBuilds {
			return m, nil // Stages and Tree have no filter
		}
		return m.startInput()
	case key.Matches(msg, keys.Command):
		return m.startCommand()
	case key.Matches(msg, keys.NextMatch):
		m.logs.nextMatch(1)
	case key.Matches(msg, keys.PrevMatch):
		m.logs.nextMatch(-1)
	case key.Matches(msg, keys.Run):
		return m.openForm()
	case key.Matches(msg, keys.Cancel):
		return m.askCancel()
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Logs):
		return m.openSelectionLogs()
	case key.Matches(msg, keys.AllLogs):
		return m.openLogs("", true)
	case key.Matches(msg, keys.SavePreset):
		return m.startSavePreset()
	case key.Matches(msg, keys.Tree):
		if b, ok := m.currentBuild(); ok {
			return m.openTree(b.Key)
		}
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err == nil {
			return m, nil
		}
		m.overlay = overlayError
	case key.Matches(msg, keys.Refresh):
		return m.refresh()
	case key.Matches(msg, keys.Server):
		return m.openServerPicker()
	case key.Matches(msg, keys.Project):
		if m.svc == nil {
			return m, nil
		}
		m.overlay = overlayProjects
		m.pickerGen++
		m.picker.setQuery("")
		m.picker.setItems(nil)
		return m, loadProjectsCmd(m.baseCtx(), m.svc, streamPicker, m.pickerGen)
	case key.Matches(msg, keys.Branch):
		if m.screen != screenPlan || m.plan.key == "" || m.svc == nil {
			return m, nil
		}
		m.overlay = overlayBranches
		m.pickerGen++
		m.picker.setQuery("")
		m.picker.setItems(nil)
		return m, loadBranchesCmd(m.baseCtx(), m.svc, m.plan.key, m.pickerGen)
	}
	return m, nil
}

// handleInputKey owns every key while the user is typing, so q, l and j are
// characters rather than commands.
func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab:
		if m.inputFor == inputCommand {
			return m.completeInput(), nil
		}
	case tea.KeyEsc:
		if m.inputFor == inputSearch {
			m.logs.search("") // esc drops the search and its highlight
		}
		m.inputFor = inputNone
		m.input.SetValue("")
		m.input.Blur()
		return m, nil
	case tea.KeyEnter:
		mode, q := m.inputFor, m.input.Value()
		m.inputFor = inputNone
		m.input.Blur()
		if mode == inputCommand {
			return m.runCommandLine(q)
		}
		if mode == inputSearch {
			m.logs.search(q)
			return m, nil
		}
		if mode == inputPreset {
			m.input.SetValue("")
			return m.savePreset(strings.TrimSpace(q))
		}
		m.applyFilter(q)
		// The filter may move the Builds cursor; Stages follows it as it
		// follows a cursor move.
		rest := m.restCmd()
		return m, rest
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// startInput opens the text input for the filter or the log search.
func (m Model) startInput() (tea.Model, tea.Cmd) {
	m.inputFor = inputFilter
	if m.screen == screenLogs {
		m.inputFor = inputSearch
	}
	m.input.SetValue("")
	m.input.Prompt = "/"
	m.input.Focus()
	return m, textinput.Blink
}

// applyFilter narrows whichever list has focus.
func (m *Model) applyFilter(q string) {
	switch m.focus {
	case focusPlans:
		m.plans.setQuery(q)
	case focusBuilds:
		m.builds.setQuery(q)
	case focusPresets:
		m.presets.setQuery(q)
	case focusSets:
		m.sets.setQuery(q)
	case focusProjects:
		m.projects.setQuery(q)
	}
}

// openHelp sizes the help viewport for the terminal it is about to fill.
// handleConfirmKey answers the question and nothing else. Any other key is
// ignored rather than guessed at.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		return m.answerYes()
	case tea.KeyEsc:
		m.overlay, m.confirm = overlayNone, confirmState{}
		return m, nil
	}
	switch strings.ToLower(msg.String()) {
	case "y":
		return m.answerYes()
	case "n", "q":
		m.overlay, m.confirm = overlayNone, confirmState{}
		return m, nil
	}
	return m, nil
}

func (m Model) answerYes() (tea.Model, tea.Cmd) {
	action := m.confirm.Action
	m.overlay, m.confirm = overlayNone, confirmState{}
	if action == nil {
		return m, nil
	}
	return action(m)
}

func (m Model) openHelp() (tea.Model, tea.Cmd) {
	m.overlay = overlayHelp
	w := overlayWidth(m.width, helpMaxWidth) - 2
	m.help.set(w, m.helpHeight(), m.helpBody(w, 0))
	return m, nil
}

// handleOverlayKey keeps overlay keys from reaching the panels underneath.
// The help overlay scrolls; the pickers move a cursor.
func (m Model) handleOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay == overlayConfirm {
		return m.handleConfirmKey(msg)
	}
	if m.overlay == overlayHelp {
		switch {
		case key.Matches(msg, keys.Back):
			return m.back()
		case key.Matches(msg, keys.Quit):
			return m.quit()
		case key.Matches(msg, keys.Top):
			m.help.vp.GotoTop()
			return m, nil
		case key.Matches(msg, keys.Bottom):
			m.help.vp.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.help.vp, cmd = m.help.vp.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Down):
		m.picker.move(1)
	case key.Matches(msg, keys.Up):
		m.picker.move(-1)
	case key.Matches(msg, keys.Top):
		m.picker.top()
	case key.Matches(msg, keys.Bottom):
		m.picker.bottom()
	case key.Matches(msg, keys.Enter):
		return m.chooseOverlay()
	}
	return m, nil
}

// refresh reloads whichever panel has focus. It is also the documented retry
// after a watch error, so it clears the error first. Presets is handled before
// the connection check, because rereading .bam.yaml needs no server.
func (m Model) refresh() (tea.Model, tea.Cmd) {
	m.err = nil
	if m.focus == focusPresets {
		if m.deps.Targets == nil {
			return m, nil
		}
		m.presetsGen++
		return m, loadPresetsCmd(m.deps, m.presetsGen)
	}
	if m.svc == nil {
		return m, nil
	}
	switch m.focus {
	case focusPlans:
		cmd := m.loadPlans()
		return m, cmd
	case focusBuilds:
		if m.buildsPlan == "" {
			return m, nil
		}
		cmd := m.loadBuilds(m.buildsPlan, false)
		return m, cmd
	case focusStages:
		if m.detail == nil {
			return m, nil
		}
		m.detailGen++
		return m, reloadBuildCmd(m.baseCtx(), m.svc, m.detail.Key, m.detailGen)
	case focusTree:
		if m.detail == nil {
			return m, nil
		}
		return m.loadTreePreview(m.detail.Key, true)
	}
	return m, nil
}

func (m Model) openServerPicker() (tea.Model, tea.Cmd) {
	items := make([]pickerItem, 0, len(m.deps.Servers))
	for _, s := range m.deps.Servers {
		items = append(items, pickerItem{Label: s.Alias, Value: s.Alias, Detail: s.URL})
	}
	m.overlay = overlayServers
	m.pickerGen++
	m.picker.setQuery("")
	m.picker.setItems(items)
	m.picker.cursor = indexOf(items, m.server)
	return m, nil
}

func (m Model) chooseOverlay() (tea.Model, tea.Cmd) {
	it, ok := m.picker.selected()
	if !ok {
		m.overlay = overlayNone
		return m, nil
	}
	switch m.overlay {
	case overlayServers:
		m.overlay = overlayNone
		if it.Value == m.server {
			return m, nil
		}
		return m.switchServer(it.Value)
	case overlayProjects:
		m.overlay = overlayNone
		return m.setProject(it.Value)
	case overlayBranches:
		m.overlay = overlayNone
		m.leaveBuild()
		m.plan.want, m.plan.older = nil, "" // the pinned build belonged to the branch being left
		m.focus = focusBuilds
		cmd := m.loadBuilds(it.Value, true)
		return m, cmd
	}
	m.overlay = overlayNone
	return m, nil
}

// startProject sets the project a new connection opens on: Deps.Project on
// the first connection, then the server's default_project.
func (m *Model) startProject() {
	if !m.projectStarted {
		m.projectStarted = true
		switch p := strings.ToUpper(m.deps.Project); p {
		case "":
		case "ALL":
			m.project = ""
			return
		default:
			m.project = p
			return
		}
	}
	m.project = m.svc.DefaultProject()
}

// setProject narrows the plans to one project, or to all with "". The
// plans, the builds and the open build all belonged to the old filter, so
// they go before the new list loads.
func (m Model) setProject(key string) (tea.Model, tea.Cmd) {
	m.fromProjects = false // a project picked any other way ends the trail
	if key == m.project {
		return m, nil
	}
	m.project = key
	m.leaveBuild()
	m.buildsPlan = ""
	m.buildsGen++
	m.builds.setItems(nil)
	m.plans.setItems(nil)
	if m.screen == screenPlan {
		m.closePlan()
		m.screen = screenHome
		m.focus = m.homeFocus()
	}
	load := m.loadPlans()
	tick := m.restartHomeTick()
	return m, tea.Batch(load, tick)
}

// switchServer drops everything that belonged to the old server: its build,
// its watch, its panels. Nothing keyed to one origin is shown under another.
func (m Model) switchServer(alias string) (tea.Model, tea.Cmd) {
	if m.train.running() {
		set := m.train.set
		return m.ask(set+" is running in this window. Switch server and stop watching it? Its builds keep running.",
			func(m Model) (tea.Model, tea.Cmd) {
				// As quitting does: end the train, wait for it to write its
				// record and release its lock, then switch.
				ch := m.train.ch
				m.train.end()
				return m, func() tea.Msg {
					for range ch {
					}
					return switchServerMsg{Alias: alias}
				}
			})
	}
	m.leaveBuild()
	m.fromSet, m.fromTree, m.logsFromTree = false, false, false
	if m.screen == screenTree {
		// The tree is the old server's; a build key means another build here.
		m.stopTree()
		m.screen = m.buildTree.from
		m.buildTree.back = nil // the plan screen to return to is the old server's
	}
	// The plan, its builds and its cache are the old server's, also when
	// the plan screen is not the one on top.
	m.closePlan()
	m.trees = nil
	if m.screen == screenPlan {
		m.screen = screenHome
		m.focus = m.homeFocus()
	}
	m.server, m.svc = alias, nil
	m.buildsPlan = ""
	// The project filter names a project of the server being left; keeping it
	// would hide every plan on the new one.
	m.project = ""
	m.fromProjects = false
	m.projectsGen++ // a projects load from the old server is dropped
	m.projects.setItems(nil)
	m.plans.setItems(nil)
	m.builds.setItems(nil)
	m.presets.setItems(nil)
	m.err = nil
	// Clearing the rows is not enough: a request already in flight for the
	// server being left would still match these generations and repopulate
	// the panels under the new one.
	m.plansGen++
	m.buildsGen++
	m.detailGen++
	m.logsGen++
	m.presetsGen++
	m.cancelGen++
	m.home.live = nil

	cmds := []tea.Cmd{}
	cmds = append(cmds, m.restartHomeTick())
	if m.deps.Targets != nil {
		cmds = append(cmds, loadPresetsCmd(m.deps, m.presetsGen))
	}
	if m.deps.Connect != nil {
		m.connGen++
		cmds = append(cmds, connectCmd(m.baseCtx(), m.deps, alias, m.connGen))
	}
	return m, tea.Batch(cmds...)
}

// moveFocused moves exactly one cursor: the focused panel's.
func (m *Model) moveFocused(delta int) {
	switch m.focus {
	case focusPlans:
		m.plans.move(delta)
	case focusBuilds:
		m.builds.move(delta)
	case focusPresets:
		m.presets.move(delta)
	case focusSets:
		m.sets.move(delta)
	case focusProjects:
		m.projects.move(delta)
	case focusStages:
		m.treeCursor += delta
		m.clampTree()
	case focusTree:
		m.plan.treeCursor = min(max(m.plan.treeCursor+delta, 0), max(len(m.treePreviewRows())-1, 0))
	}
}

func (m *Model) jumpFocused(top bool) {
	switch m.focus {
	case focusPlans:
		jump(&m.plans, top)
	case focusBuilds:
		jump(&m.builds, top)
	case focusPresets:
		jump(&m.presets, top)
	case focusSets:
		jump(&m.sets, top)
	case focusProjects:
		jump(&m.projects, top)
	case focusStages:
		m.treeCursor = 0
		if !top {
			m.treeCursor = len(m.treeRows()) - 1
		}
		m.clampTree()
	case focusTree:
		m.plan.treeCursor = 0
		if !top {
			m.plan.treeCursor = max(len(m.treePreviewRows())-1, 0)
		}
	}
}

func jump[T any](l *listState[T], top bool) {
	if top {
		l.top()
		return
	}
	l.bottom()
}

func (m *Model) clampTree() {
	n := len(m.treeRows())
	if m.treeCursor >= n {
		m.treeCursor = n - 1
	}
	if m.treeCursor < 0 {
		m.treeCursor = 0
	}
}

// treeRows is the stage and job tree of the open build, or nothing.
func (m Model) treeRows() []treeRow {
	if m.detail == nil {
		return nil
	}
	return buildTree(*m.detail, m.expanded)
}

func (m Model) selectedTreeRow() (treeRow, bool) {
	rows := m.treeRows()
	if m.treeCursor < 0 || m.treeCursor >= len(rows) {
		return treeRow{}, false
	}
	return rows[m.treeCursor], true
}

// leaveBuild drops the build the main panel is showing and the watch on it.
// Every path that changes which plan the Builds panel holds calls it: the
// open build belongs to the plan being left, and its watch would keep
// overwriting the main panel while the new plan loads.
func (m *Model) leaveBuild() {
	m.stopWatch()
	m.stopFollow()
	// Bump the detail generation too: a Main refresh already in flight would
	// otherwise be accepted and restore the abandoned build under the new
	// selection.
	m.detailGen++
	// A cancel confirmed for the build being left must not report against
	// the next one.
	m.cancelGen++
	m.detail, m.expanded, m.treeCursor = nil, nil, 0
	m.progress = provider.Progress{}
}

// drill is spec §4.1. Each row moves focus and starts the load its panel needs.
func (m Model) drill() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusBuilds:
		b, ok := m.builds.selected()
		if !ok || m.svc == nil {
			return m, nil
		}
		m.focus = focusStages
		if m.detail != nil && m.detail.Key == b.Key {
			return m, nil
		}
		return m.showBuild(b)
	case focusStages:
		row, ok := m.selectedTreeRow()
		if !ok {
			return m, nil
		}
		if row.Kind == rowStage {
			if m.expanded == nil {
				m.expanded = map[string]bool{}
			}
			m.expanded[row.Name] = !m.expanded[row.Name]
			m.clampTree()
			return m, nil
		}
		return m.openLogs(row.Key, false)
	case focusTree:
		return m.drillTree()
	}
	return m, nil
}

// handleWatchEvent folds one app.Event into the open build. The build in the
// event is the whole snapshot of that poll, so the tree follows it without a
// second request.
func (m Model) handleWatchEvent(e app.Event) (tea.Model, tea.Cmd) {
	if e.Build.Key != "" {
		m.noteLive(e.Build)
		b := e.Build
		m.detail = &b
		m.progress = e.Progress
		m.clampTree()
		m.plan.stagesLoading = false
		// "queued KEY" is news only while the build waits; after that the
		// detail panel shows its state. Other notices are not the watch's.
		if m.status == "queued "+b.Key && (b.State != provider.StateQueued || e.Type == app.EventDone) {
			m.status = ""
		}
	}
	switch e.Type {
	case app.EventError:
		m.err = e.Err
		m.plan.stagesLoading = false
		m.stopWatch()
		return m, nil
	case app.EventDone:
		if e.Build.Key == m.revisionRun.Key && m.revisionRun.Rev != "" && e.Build.State == provider.StateNotBuilt {
			m.status = "Bamboo could not build revision " + m.revisionRun.Rev
		}
		m = m.cacheBuild(e.Build)
		m.stopWatch()
		// The plan screen may sit under its logs or the run form; the
		// one re-read is still due (§4.3).
		if m.plan.key != "" && m.detail != nil && m.plan.tree.key == m.detail.Key {
			var tree tea.Cmd
			m, tree = m.loadTreePreview(m.detail.Key, true)
			if m.svc != nil && m.buildsPlan != "" {
				load := m.loadBuilds(m.buildsPlan, false)
				return m, tea.Batch(tree, load)
			}
			return m, tree
		}
		if m.svc != nil && m.buildsPlan != "" {
			cmd := m.loadBuilds(m.buildsPlan, false)
			return m, cmd
		}
		return m, nil
	}
	return m, watchCmd(m.watchCh, m.watchGen)
}

// startWatch begins watching key and cancels whatever was being watched
// before. At most one watch runs, so the UI polls at most one build and
// inherits app.Watch's own backoff. Ending a watch never stops the build.
func (m Model) startWatch(key string) (tea.Model, tea.Cmd) {
	m.stopWatch()
	if m.svc == nil {
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.watchCancel = cancel
	m.watchCh = m.svc.Watch(ctx, key)
	return m, watchCmd(m.watchCh, m.watchGen)
}

// handleLogKey owns the keys while the log screen fills the terminal; the
// rest go to the viewport, which scrolls itself.
func (m Model) handleLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Bottom):
		m.logs.vp.GotoBottom()
		m.logs.pinned = true
		return m, nil
	case key.Matches(msg, keys.Top):
		m.logs.vp.GotoTop()
		m.logs.pinned = m.logs.vp.AtBottom()
		return m, nil
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
		return m, nil
	case key.Matches(msg, keys.AllLogs):
		// The failed-log error advises pressing a, so a has to work here and
		// not fall through to the viewport.
		return m.openLogs("", true)
	case key.Matches(msg, keys.Follow):
		return m.toggleFollow()
	case key.Matches(msg, keys.Filter):
		return m.startInput()
	case key.Matches(msg, keys.NextMatch):
		m.logs.nextMatch(1)
		return m, nil
	case key.Matches(msg, keys.PrevMatch):
		m.logs.nextMatch(-1)
		return m, nil
	}
	var cmd tea.Cmd
	before := m.logs.vp.YOffset
	m.logs.vp, cmd = m.logs.vp.Update(msg)
	// Re-pin after the viewport moves, or when a scroll-down key was pressed
	// on the last screen (nothing to move, but the reader asked for the end).
	// An unbound key leaves the pin alone.
	km := m.logs.vp.KeyMap
	down := key.Matches(msg, km.Down, km.PageDown, km.HalfPageDown)
	if m.logs.vp.YOffset != before || down {
		m.logs.pinned = m.logs.vp.AtBottom()
	}
	return m, cmd
}

// openSelectionLogs is l: on the tree, the stage or job under the cursor;
// otherwise the current stage of a build still running, live, or else the
// failed jobs.
func (m Model) openSelectionLogs() (tea.Model, tea.Cmd) {
	if m.focus == focusStages {
		if row, ok := m.selectedTreeRow(); ok {
			if row.Kind == rowStage {
				return m.openStageLogs(row.Name)
			}
			return m.openLogs(row.Key, false)
		}
	}
	if b, ok := m.currentBuild(); ok && !b.State.Finished() {
		return m.openStageLogs("")
	}
	return m.openLogs("", false)
}

// openStageLogs takes the screen for one stage's jobs. An empty stage is the
// build's current one, resolved once the build is read in full.
func (m Model) openStageLogs(stage string) (tea.Model, tea.Cmd) {
	b, ok := m.currentBuild()
	if !ok || m.svc == nil {
		return m, nil
	}
	m.screen = screenLogs
	m.stopFollow() // reopening replaces the log state; its follow ends first
	// The header names the build from the start, so a slow or failed load
	// still says whose logs these are.
	m.logs = logState{title: b.Key, stage: stage}
	m.logsGen++
	return m, stageLogsCmd(m.baseCtx(), m.svc, b, stage, m.logsGen)
}

// openLogs takes the screen for the log viewport. An empty jobKey means the
// selected build's failed jobs, the same default bam logs uses.
func (m Model) openLogs(jobKey string, all bool) (tea.Model, tea.Cmd) {
	b, ok := m.currentBuild()
	if !ok || m.svc == nil {
		return m, nil
	}
	m.screen = screenLogs
	// Reopening replaces the log state, so an active follow has to end first
	// or its chunks land in the view that replaced it.
	m.stopFollow()
	m.logs = logState{title: cmp.Or(jobKey, b.Key), all: all}
	m.logsGen++
	// ListBuilds returns builds without their stage and job tree, so a row
	// straight from the Builds panel has no job to take a log from. Expand it
	// first rather than reporting that it has no failed job.
	if len(b.Stages) == 0 {
		return m, logsForKeyCmd(m.baseCtx(), m.svc, b.Key, jobKey, all, m.logsGen)
	}
	return m, loadLogsCmd(m.baseCtx(), m.svc, b, jobKey, all, m.logsGen)
}

// copiedMsg says the URL reached the terminal's clipboard.
type copiedMsg struct{}

// selectionURL is the Bamboo URL of whatever the focused panel points at, so
// o and y always act on what the user is looking at.
func (m Model) selectionURL() string {
	if m.screen == screenLogs && m.logs.url != "" {
		return m.logs.url
	}
	if m.screen == screenSet {
		return m.setSelectionURL()
	}
	if m.screen == screenTree {
		return m.treeSelectionURL()
	}
	switch m.focus {
	case focusPlans:
		if p, ok := m.plans.selected(); ok {
			return p.URL
		}
	case focusBuilds:
		if b, ok := m.builds.selected(); ok {
			return b.URL
		}
	case focusProjects:
		if p, ok := m.projects.selected(); ok {
			return p.URL
		}
	case focusStages:
		// A stage has no URL of its own, so the build's stands in.
		if row, ok := m.selectedTreeRow(); ok && row.URL != "" {
			return row.URL
		}
		if m.detail != nil {
			return m.detail.URL
		}
	case focusTree:
		if rows := m.treePreviewRows(); m.plan.treeCursor < len(rows) {
			if u := rows[m.plan.treeCursor].Build.URL; u != "" {
				return u
			}
		}
	}
	return ""
}

// openSelection and copySelection are shared by the columns and the log
// screen, so o and y mean the same thing on both.
func (m Model) openSelection() (tea.Model, tea.Cmd) {
	url := m.selectionURL()
	if url == "" || m.deps.Open == nil {
		return m, nil
	}
	open := m.deps.Open
	return m, func() tea.Msg {
		if err := open(url); err != nil {
			return errMsg{Err: err, Where: "open"}
		}
		return nil
	}
}

func (m Model) copySelection() (tea.Model, tea.Cmd) {
	url := m.selectionURL()
	if url == "" || m.deps.Clipboard == nil {
		return m, nil
	}
	w := m.deps.Clipboard
	return m, func() tea.Msg {
		if err := osc52(w, url); err != nil {
			return errMsg{Err: err, Where: "copy"}
		}
		return copiedMsg{}
	}
}

// presetSavedMsg says a build's variables were written as a preset.
type presetSavedMsg struct{ Name, Path string }

// startSavePreset asks for the name to save the plan screen's selected build
// under, as bam preset add NAME --from BUILD.
func (m Model) startSavePreset() (tea.Model, tea.Cmd) {
	if m.screen != screenPlan || m.svc == nil || m.deps.SavePreset == nil {
		return m, nil
	}
	b, ok := m.currentBuild()
	if !ok {
		return m, nil
	}
	m.presetBuild = b.Key
	m.inputFor = inputPreset
	m.input.SetValue("")
	m.input.Prompt = "save " + b.Key + " as preset: "
	m.input.Focus()
	return m, textinput.Blink
}

// savePreset writes the build asked about in startSavePreset. An empty name
// saves nothing, as esc does.
func (m Model) savePreset(name string) (tea.Model, tea.Cmd) {
	if name == "" || m.svc == nil {
		return m, nil
	}
	save, ctx, svc, build := m.deps.SavePreset, m.baseCtx(), m.svc, m.presetBuild
	m.status = "saving preset " + name + "…"
	return m, func() tea.Msg {
		path, err := save(ctx, svc, name, build)
		if err != nil {
			return errMsg{Err: err, Where: "preset"}
		}
		return presetSavedMsg{Name: name, Path: path}
	}
}

// toggleFollow starts or stops streaming the open logs. Following polls the
// build's logs only; it never touches the build.
func (m Model) toggleFollow() (tea.Model, tea.Cmd) {
	if m.logs.following {
		m.stopFollow()
		return m, nil
	}
	return m.startFollow()
}

// startFollow follows every job on the screen from the provider's own
// offsets, not from the number of lines drawn: only the first is a contract.
// A stage screen also moves on to the next stage.
func (m Model) startFollow() (tea.Model, tea.Cmd) {
	b, ok := m.logBuild()
	if !ok || m.svc == nil || len(m.logs.offsets) == 0 {
		return m, nil
	}
	for k := range m.logs.offsets {
		if _, ok := jobByKey(b, k); !ok {
			return m, nil
		}
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.followCancel = cancel
	m.logs.following = true
	m.followFrom = maps.Clone(m.logs.offsets)
	m.followEvents, m.followDone = followCmd(ctx, m.svc, b.Key, m.followFrom, m.logs.stage != "")
	m.logs.refresh() // an empty stage now says it is waiting
	return m, drainFollowCmd(m.followEvents, m.followDone, m.followGen)
}

func (m *Model) stopFollow() {
	if m.followCancel != nil {
		m.followCancel()
		m.followCancel = nil
	}
	m.followEvents, m.followDone = nil, nil
	m.logs.following = false
	m.logs.refresh()
	m.followGen++
}

func jobByKey(b provider.Build, key string) (provider.Job, bool) {
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if j.Key == key {
				return j, true
			}
		}
	}
	return provider.Job{}, false
}

// currentBuild is the open build when there is one, otherwise whatever the
// Builds cursor points at.
// handleFormKey owns the keys while the form fills the terminal. While a
// field is being edited every key is a character, so q is a q and ctrl-R
// cannot be struck by accident.
func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.form.loading {
		// Nothing else is safe: fields is nil and cursor is 0, which makes
		// onRevision() true and would let enter start editing the hidden
		// revision input before there is a form to edit.
		switch {
		case key.Matches(msg, keys.Back):
			return m.back()
		case key.Matches(msg, keys.Quit):
			return m.quit()
		}
		return m, nil
	}
	if m.form.editing {
		switch msg.Type {
		case tea.KeyEsc:
			m.form.cancelEdit()
			return m, nil
		case tea.KeyEnter:
			if m.form.naming {
				name := strings.TrimSpace(m.form.input.Value())
				if strings.ContainsAny(name, "= \t") {
					m.form.err = errs.Usagef("%q is not a variable name", name).WithWhy("a name cannot hold = or spaces")
					return m, nil
				}
				ok := m.form.acceptName()
				m.revalidate()
				if ok {
					m.form.startEditing()
					return m, textinput.Blink
				}
				return m, nil
			}
			m.form.acceptEdit()
			m.revalidate()
			return m, nil
		}
		var cmd tea.Cmd
		m.form.input, cmd = m.form.input.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.NextPanel), key.Matches(msg, keys.Down):
		m.form.move(1)
	case key.Matches(msg, keys.PrevPanel), key.Matches(msg, keys.Up):
		m.form.move(-1)
	case key.Matches(msg, keys.Trigger):
		return m.trigger()
	case key.Matches(msg, keys.DryRun):
		m.overlay = overlayDryRun
		return m, nil
	case key.Matches(msg, keys.ExpandErr):
		return m.expandErr()
	case key.Matches(msg, keys.AllLogs): // a: add a variable the plan may not declare
		m.form.startNaming()
		return m, textinput.Blink
	case key.Matches(msg, keys.RemoveRow):
		if m.form.removeAdded() {
			m.revalidate()
		}
		return m, nil
	case key.Matches(msg, keys.Follow):
		// f is "follow" on the log screen; in the form, on the Revision row,
		// it takes the commit of the build the form was prefilled from.
		if m.form.onRevision() && m.form.fromRevision != "" {
			m.form.revision = m.form.fromRevision
		}
		return m, nil
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.CycleOption):
		if !m.form.onRevision() && len(m.form.fields[m.form.cursor].Options) > 0 {
			m.form.cycle(1)
			m.revalidate()
			return m, nil
		}
		// space is only a cycle key; it never opens a text field.
		if key.Matches(msg, keys.CycleOption) {
			return m, nil
		}
		m.form.startEditing()
		return m, textinput.Blink
	}
	return m, nil
}

// trigger sends the run. It revalidates first and refuses on an error: the
// form stays open with everything typed still in it, because losing a filled
// form to a rejected value is worse than the rejection.
func (m Model) trigger() (tea.Model, tea.Cmd) {
	m.revalidate()
	if !m.canRun() || m.svc == nil {
		return m, nil
	}
	getenv := m.deps.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	vs, err := app.ValidateVars(m.form.ref, m.form.base, m.form.flags(), getenv)
	if err != nil {
		m.form.err = err
		return m, nil
	}
	ref := m.form.planRef()
	return m, runCmd(m.baseCtx(), m.svc, ref, m.form.stripUntypedMasks(vs), m.formGen)
}

// askCancel confirms before stopping a build. Cancelling is the one action
// in the UI that cannot be undone, so it always asks and always names the
// build it will stop.
func (m Model) askCancel() (tea.Model, tea.Cmd) {
	// C is a Builds and Main key. currentBuild falls back to the Builds
	// cursor, so without this a C struck in Plans or Presets would offer to
	// stop a build from the row the user is no longer looking at.
	if m.screen != screenPlan {
		return m, nil
	}
	b, ok := m.currentBuild()
	if !ok || m.svc == nil {
		return m, nil
	}
	if b.State.Finished() {
		m.status = b.Key + " has already finished"
		return m, nil
	}
	return m.ask("Cancel "+b.Key+"?", func(m Model) (tea.Model, tea.Cmd) {
		m.cancelGen++
		return m, cancelCmd(m.baseCtx(), m.svc, b.Key, m.cancelGen)
	})
}

// openTriggered shows the build that was just started and watches it. It is
// the drill-into-a-build path, so at most one watch still runs.
func (m Model) openTriggered(b provider.Build) (tea.Model, tea.Cmd) {
	m.revisionRun.Key, m.revisionRun.Rev = b.Key, m.form.planRef().RevisionShort()
	m.form = formState{}
	// Esc after a run returns to Home (§4.1), not to a set, a tree or a
	// drilled plan the form was opened from.
	m.fromSet, m.fromTree, m.logsFromTree = false, false, false
	m, cmd := m.enterPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
	// showBuild started the watch; note the build only after, since
	// stopping the previous watch clears the marker.
	m.noteLive(b)
	m.status = "queued " + b.Key
	return m, cmd
}

// openForm opens the run form for whatever the cursor is on: a preset by its
// name, so its branch and rules apply, or a plan key. Opening a form changes
// nothing on the server, which is why R needs no confirmation.
func (m Model) openForm() (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	var (
		planKey string
		target  *app.TargetInfo
	)
	switch m.focus {
	case focusPresets:
		t, ok := m.presets.selected()
		if !ok {
			return m, nil
		}
		target = &t
	case focusPlans:
		p, ok := m.plans.selected()
		if !ok {
			return m, nil
		}
		planKey = p.Key
	default:
		if b, ok := m.currentBuild(); ok {
			planKey = b.PlanKey
		} else {
			planKey = m.buildsPlan
		}
	}
	if target == nil && planKey == "" {
		return m, nil
	}

	// Prefill from the selected build when there is one, so re-running with a
	// tweak is the short path; otherwise from this repository's last build.
	from := "last"
	if b, ok := m.currentBuild(); ok && m.focus != focusPlans && m.focus != focusPresets {
		from = b.Key
	}

	m.formFrom = m.screen
	m.screen = screenForm
	m.formGen++
	// Name the plan while it loads, so the header is never blank.
	m.form = formState{loading: true, ref: app.PlanRef{PlanKey: planKey}}
	if target != nil {
		m.form.target, m.form.ref = target.Name, app.PlanRef{PlanKey: target.Plan, Branch: target.Branch}
	}
	return m, openFormCmd(m.baseCtx(), m.svc, planKey, target, from, m.formGen)
}

// logBuild is the build the open log came from, in full. It is not
// currentBuild: the Builds cursor may have moved, and a row from a listing
// carries no stages.
func (m Model) logBuild() (provider.Build, bool) {
	if m.logs.build != nil {
		return *m.logs.build, true
	}
	return m.currentBuild()
}

// currentBuild is what l, a, o, y and f act on. The Builds cursor wins when
// Builds has focus, because esc leaves the detail open: without this, moving
// down one row and pressing l would show the previous build's log.
func (m Model) currentBuild() (provider.Build, bool) {
	if m.focus == focusBuilds {
		if b, ok := m.builds.selected(); ok {
			return b, true
		}
	}
	if m.detail != nil {
		return *m.detail, true
	}
	return m.builds.selected()
}

// back pops exactly one level, in the order spec §4.1 fixes.
func (m Model) back() (tea.Model, tea.Cmd) {
	switch {
	case m.overlay != overlayNone:
		// Closing the error overlay is how an error is dismissed.
		if m.overlay == overlayError {
			m.err = nil
		}
		m.overlay = overlayNone
		return m, nil
	case m.screen == screenLogs && m.logsFromTree:
		m.stopFollow()
		m.logsGen++ // abandon a load still in flight
		m.logsFromTree, m.fromTree = false, false
		// The plan screen openTreeBuild opened under the logs is left too:
		// closing it stops its watch and drops its loads still in flight.
		m.closePlan()
		m.screen = screenTree
		return m.watchTree(false) // watching paused while the logs were open
	case m.screen == screenLogs:
		m.stopFollow()
		m.logsGen++ // abandon a load still in flight
		m.screen = screenPlan
		return m, nil
	case m.screen == screenForm:
		m.formGen++ // abandon a load still in flight
		from := m.formFrom
		m.screen = from
		m.form = formState{}
		if from == screenHome {
			// A tick that arrived while the form was open ended the
			// auto-refresh loop (homeTick drops beats off Home); nothing
			// else restarts it, so returning here has to.
			return m.goHome()
		}
		return m, nil
	case m.screen == screenHome:
		// Home is the top: esc clears the active table's filter, then on a
		// plans table reached from the projects view goes back there, and
		// otherwise does nothing.
		switch {
		case m.home.view == homeSets && m.sets.query != "":
			m.sets.setQuery("")
		case m.home.view == homePresets && m.presets.query != "":
			m.presets.setQuery("")
		case m.home.view == homeProjects && m.projects.query != "":
			m.projects.setQuery("")
		case m.home.view == homePlans && m.plans.query != "":
			m.plans.setQuery("")
		case m.home.view == homePlans && m.fromProjects:
			return m.backToProjects()
		}
		return m, nil
	case m.screen == screenPlan && m.focus == focusBuilds && m.builds.query != "":
		m.builds.setQuery("") // esc clears the focused panel's filter first (§4.1)
		return m, nil
	case m.screen == screenPlan && m.focus != focusBuilds:
		m.focus = focusBuilds
		return m, nil
	case m.screen == screenPlan && len(m.plan.stack) > 0:
		return m.popPlan()
	case m.screen == screenPlan && m.fromSet:
		// Closing the plan screen stops its watch and drops its loads still
		// in flight, so none lands on a plan the user has left.
		m.fromSet = false
		m.closePlan()
		m.screen = screenSet
		return m, nil
	case m.screen == screenPlan && m.fromTree:
		m.fromTree = false
		m.closePlan()
		m.screen = screenTree
		return m.watchTree(false) // watching paused while the build was open
	case m.screen == screenTree:
		return m.closeTree()
	case m.screen == screenSet:
		return m.showSets()
	}
	return m.goHome()
}

// switchServerMsg finishes a server switch once a train that was running
// has ended.
type switchServerMsg struct{ Alias string }

// quit cancels the watch on the way out. Cancelling a watch never stops the
// build; only bam build cancel does. A train running in this window is asked
// about first: quitting ends its watching and leaves its record interrupted.
func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.train.running() {
		set := m.train.set
		return m.ask(set+" is running in this window. Quit and stop watching it? Its builds keep running; continue with U or bam set resume.",
			func(m Model) (tea.Model, tea.Cmd) {
				// End the train's context, then wait for its channel to
				// close: by then it has written its record interrupted and
				// released its lock, so nothing is left half-done on disk.
				ch := m.train.ch
				m.train.end()
				m.stopWatch()
				m.stopTree()
				m.stopTreePreview()
				m.stopFollow()
				return m, func() tea.Msg {
					for range ch {
					}
					return tea.Quit()
				}
			})
	}
	m.stopWatch()
	m.stopTree()
	m.stopTreePreview()
	m.stopFollow()
	return m, tea.Quit
}

func (m *Model) stopWatch() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
	m.watchCh = nil
	m.watchGen++
	// At most one watch runs, so live holds only the build being watched (or
	// just triggered). Every watch ends here, including a switch to a
	// different build, so the old marker must not survive it.
	m.home.live = nil
}

func (m Model) View() string {
	if m.width < 4 || m.height < 4 {
		return ""
	}
	switch m.screen {
	case screenLogs:
		return m.overlayView(m.logsView())
	case screenForm:
		return m.overlayView(m.formView())
	case screenHome:
		return m.homeView()
	case screenSet:
		return m.overlayView(m.setScreenView())
	case screenSetVars:
		return m.overlayView(m.setVarsView())
	case screenTree:
		return m.overlayView(m.treeScreenView())
	}
	return m.planView()
}

// logHeight is the viewport's height: the terminal minus the name line (on
// a terminal of 20 rows or more), the header and the key line.
func (m Model) logHeight() int {
	h := m.height - 2
	if m.height >= 20 {
		h--
	}
	return max(h, 1)
}

// expandErr opens the error overlay on the error the status bar shows: its
// What, Why and Try. The run form offers it too, because a refused trigger
// leaves the user in the form with only the What on screen.
func (m Model) expandErr() (tea.Model, tea.Cmd) {
	if m.err == nil {
		return m, nil
	}
	m.overlay = overlayError
	return m, nil
}
