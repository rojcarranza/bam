package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// planHome is Home's plans table on the lab server, connected.
func planHome(t *testing.T) Model {
	t.Helper()
	m := goldenModel(80, 24)
	m.svc = testService()
	m.screen, m.focus = screenHome, focusPlans
	m.home.view = homePlans
	m.builds.setItems(nil)
	m.buildsPlan = ""
	return m
}

func TestEnterOnHomeOpensThePlanScreen(t *testing.T) {
	m := planHome(t)
	require.True(t, m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "PROJ-BUILD" }))

	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	require.Equal(t, "PROJ-BUILD", m.plan.key)
	require.Equal(t, "PROJ-BUILD", m.buildsPlan)
	require.NotNil(t, cmd)

	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.NotNil(t, m.detail, "the newest build opens as soon as the builds load")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, "PROJ-BUILD", selectedKey(m), "Home's cursor stays on the plan")
	require.Empty(t, m.plan.key, "leaving closes the plan screen")
	require.Nil(t, m.detail)
}

func TestEnterOnAPresetOpensItsPlanAndBranch(t *testing.T) {
	m := presetModel()
	m.svc = testService()
	require.True(t, m.presets.selectFirst(func(ti app.TargetInfo) bool { return ti.Name == "smoke" }))

	// drillFromHome directly: through handleKey the command may be wrapped
	// in a batch with Home's idle tick.
	next, cmd := m.drillFromHome(focusBuilds)
	m = next.(Model)
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	msg := cmd()
	loaded, ok := msg.(buildsLoadedMsg)
	require.True(t, ok, "got %T", msg)
	require.Equal(t, "PROJ-PROV12", loaded.PlanKey, "a preset's branch resolves to its branch plan")

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homePresets, m.home.view)
}

func TestPlanWithNoBuildsStillRuns(t *testing.T) {
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: m.buildsPlan})
	require.Nil(t, m.detail)
	require.NotPanics(t, func() { _ = m.View() })

	m, cmd := send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen, "R opens the form for the plan itself")
	require.NotNil(t, cmd)
}

func TestSetScreenRunOpensThePlanScreenOnThatBuild(t *testing.T) {
	m := planHome(t)
	b := sampleBuild()
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &b, focusStages)
	require.Equal(t, focusStages, m.focus)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key, "the wanted build shows before the list loads")

	older := b
	older.Key, older.Number = "PROJ-BUILD-3", 3
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &older, focusStages)
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	rows := m.builds.rows()
	require.Equal(t, "PROJ-BUILD-3", rows[0].Key, "a build not among the newest is pinned on top")
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)
	require.Equal(t, 0, m.builds.cursor)
}

func TestServerSwitchLeavesThePlanScreen(t *testing.T) {
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.Equal(t, screenHome, m.screen)
	require.Empty(t, m.plan.key)
}

func olderBuild() provider.Build {
	b := sampleBuild()
	b.Key, b.Number = "PROJ-BUILD-3", 3
	return b
}

// The pin holds until the user moves off the build, across refreshes.
func TestPinnedOlderBuildSurvivesARefresh(t *testing.T) {
	m := planHome(t)
	older := olderBuild()
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &older, focusStages)
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)

	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	rows := m.builds.rows()
	require.Equal(t, "PROJ-BUILD-3", rows[0].Key)
	require.Equal(t, 0, m.builds.cursor)
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)
}

// The list can arrive while the logs or the form are on screen.
func TestWantedBuildIsPlacedWhileAnotherScreenIsCurrent(t *testing.T) {
	m := planHome(t)
	older := olderBuild()
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &older, focusStages)
	m.screen = screenLogs
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.Nil(t, m.plan.want)
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)
	require.Equal(t, "PROJ-BUILD-3", m.builds.rows()[0].Key)
}

func TestEscFromTreeLogsStopsTheBuildWatch(t *testing.T) {
	m, f := treeTUIModel(t)
	running := f.History["PROJ-PROV"][0]
	running.State = provider.StateRunning
	f.History["PROJ-PROV"] = []provider.Build{running}
	m = firstTree(t, m)
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen)
	require.Nil(t, m.watchCancel, "the tree watches itself; no build watch may linger")
}

// instantRest makes the Builds rest fire at once.
func instantRest(t *testing.T) {
	t.Helper()
	old := planRestDelay
	planRestDelay = 0
	t.Cleanup(func() { planRestDelay = old })
}

func threeBuilds() []provider.Build {
	out := make([]provider.Build, 0, 3)
	for i, n := range []int{44, 43, 42} {
		b := sampleBuild()
		b.Key, b.Number = "PROJ-BUILD-"+strconv.Itoa(n), n
		b.Stages = nil // a listing carries no stages
		if i == 0 {
			b.State = provider.StateFailed
		} else {
			b.State = provider.StateSuccess
		}
		out = append(out, b)
	}
	return out
}

// planOnBuilds is the plan screen on PROJ-BUILD with three builds, the
// newest open.
func planOnBuilds(t *testing.T) Model {
	t.Helper()
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: threeBuilds()})
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	return m
}

func TestOnlyTheRestingBuildLoads(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	var rests []tea.Msg
	for range 2 {
		var cmd tea.Cmd
		m, cmd = send(m, mkKey("j"))
		require.NotNil(t, cmd, "a cursor move schedules a rest")
		rests = append(rests, cmd())
	}
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key, "moving alone loads nothing")

	m, cmd := send(m, rests[0])
	require.Nil(t, cmd, "an older rest is dropped")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)

	m, cmd = send(m, rests[1])
	require.NotNil(t, cmd)
	require.Equal(t, "PROJ-BUILD-42", m.detail.Key)
}

func TestAFinishedBuildIsServedFromTheCache(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	full := sampleBuild()
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: full})
	done := app.Tree{Root: &app.TreeNode{PlanKey: "PROJ-BUILD", Build: full}, State: provider.StateFailed}
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: full.Key, Tree: done})
	require.Contains(t, m.plan.cache, "PROJ-BUILD-44")
	require.Contains(t, m.plan.trees, "PROJ-BUILD-44")

	m, cmd := send(m, mkKey("j"))
	m, _ = send(m, cmd())
	m, cmd = send(m, mkKey("k"))
	m, cmd = send(m, cmd())
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Len(t, m.detail.Stages, 3, "the cached build has its stages")
	require.False(t, m.plan.stagesLoading)
	require.True(t, m.plan.tree.have, "the cached tree shows at once")
	require.Nil(t, cmd, "a cached finished build and tree are not read again")
}

func TestAFailedStagesReadClearsStagesLoading(t *testing.T) {
	m := planOnBuilds(t)
	m, _ = m.showBuild(sampleBuild())
	require.True(t, m.plan.stagesLoading)
	m, _ = send(m, errMsg{Stream: streamDetail, Gen: m.detailGen, Err: errBoom})
	require.False(t, m.plan.stagesLoading)
}

func TestARunningBuildIsNeverCached(t *testing.T) {
	m := planOnBuilds(t)
	running := sampleBuild()
	running.State = provider.StateRunning
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: running})
	require.NotContains(t, m.plan.cache, running.Key)
}

func TestBuildsRefreshKeepsTheCursorOnTheOpenBuild(t *testing.T) {
	m := planOnBuilds(t)
	m.builds.cursor = 1
	m, _ = m.showBuild(m.builds.rows()[1]) // PROJ-BUILD-43 open
	newer := sampleBuild()
	newer.Key, newer.Number = "PROJ-BUILD-45", 45
	refreshed := append([]provider.Build{newer}, threeBuilds()...)

	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: refreshed})
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-43", b.Key)
	require.Equal(t, "PROJ-BUILD-43", m.detail.Key)
}

func TestASlowFirstBuildsLoadDoesNotOpenABuildBehindAnotherScreen(t *testing.T) {
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	require.Nil(t, m.detail)
	m.screen = screenSet // as if esc went back to a set screen

	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: threeBuilds()})
	require.Nil(t, m.watchCancel, "no watch starts behind another screen")
	require.Nil(t, m.detail)
}

// planWithTree is the plan screen on PROJ-BUILD with build 44 open and its
// tree preview delivered.
func planWithTree(t *testing.T) Model {
	t.Helper()
	m, _ := treeTUIModel(t)
	m.screen, m.focus = screenHome, focusPlans
	require.True(t, m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "PROJ-BUILD" }))
	m, _ = send(m, mkKey("enter"))
	b := sampleBuild()
	m, cmd := m.showBuild(b)
	for _, msg := range runBatch(cmd) {
		if tp, ok := msg.(treePreviewMsg); ok {
			m, _ = send(m, tp)
		}
	}
	require.True(t, m.plan.tree.have)
	return m
}

func TestTreePanelListsDirectChildren(t *testing.T) {
	m := planWithTree(t)
	rows := m.treePreviewRows()
	require.Len(t, rows, 1)
	require.Equal(t, "PROJ-PROV-7", rows[0].Build.Key)
}

func TestEnterOnATreeRowOpensTheChildPlanAndEscComesBack(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-PROV", m.plan.key)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	require.Equal(t, focusStages, m.focus)
	require.Len(t, m.plan.stack, 1)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "PROJ-BUILD", m.plan.key, "esc on Builds pops the drill stack")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Equal(t, focusTree, m.focus, "focus returns to where the drill started")
	require.Empty(t, m.plan.stack)

	m.focus = focusBuilds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
}

func TestALateTreePreviewIsDropped(t *testing.T) {
	m := planWithTree(t)
	stale := treePreviewMsg{Gen: m.treePreviewGen - 1, Key: "PROJ-BUILD-44"}
	before := m.plan.tree
	m, _ = send(m, stale)
	require.Equal(t, before, m.plan.tree)

	gen := m.treePreviewGen
	next, _ := m.switchServer("lab")
	m = next.(Model)
	m, _ = send(m, treePreviewMsg{Gen: gen, Key: "PROJ-BUILD-44"})
	require.False(t, m.plan.tree.have, "a preview from before a server switch is dropped")
}

func TestTreePreviewErrorStaysInItsPanel(t *testing.T) {
	m := planWithTree(t)
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: m.detail.Key, Err: errBoom})
	require.Equal(t, errBoom, m.plan.tree.err)
	require.Nil(t, m.err, "the status bar is not the tree's")
	require.NotNil(t, m.detail)
}

func TestRefreshOnTreeReadsItAgain(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	gen := m.treePreviewGen
	m, cmd := send(m, mkKey("r"))
	require.NotNil(t, cmd)
	require.Greater(t, m.treePreviewGen, gen)
	require.True(t, m.plan.tree.loading)
}

func TestTreeRowURLIsTheChildBuilds(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	require.Equal(t, labOrigin+"/browse/PROJ-PROV-7", m.selectionURL())
}

func TestOnlyAFinalTreeIsCached(t *testing.T) {
	m := planWithTree(t)
	key := m.detail.Key
	root := func(s provider.State) app.Tree {
		b := sampleBuild()
		b.State = s
		return app.Tree{Root: &app.TreeNode{PlanKey: "PROJ-BUILD", Build: b}, State: provider.StateSuccess}
	}
	delete(m.plan.trees, key)
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: key, Tree: root(provider.StateQueued)})
	require.NotContains(t, m.plan.trees, key, "a tree whose root is not final is never cached")
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: key, Tree: root(provider.StateFailed)})
	require.Contains(t, m.plan.trees, key)
}

func TestAWatchErrorClearsStagesLoading(t *testing.T) {
	m := planOnBuilds(t)
	running := sampleBuild()
	running.State = provider.StateRunning
	m, _ = m.showBuild(running)
	require.True(t, m.plan.stagesLoading)
	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventError, Err: errBoom}})
	require.False(t, m.plan.stagesLoading)
}

func TestPlanHeaderNamesThePlanAndNeverItsKeyInARow(t *testing.T) {
	m := planGolden(80, 24)
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Build and test", lines[0])
	require.Contains(t, lines[1], "PROJ › PROJ-BUILD")
	require.Contains(t, lines[1], "last build #44")
	for _, l := range lines[3:] {
		require.NotContains(t, l, "PROJ-BUILD-", "rows show numbers, not build keys")
	}
}

func TestLongPlanNameIsCutWithAnEllipsis(t *testing.T) {
	m := planGolden(80, 24)
	long := strings.Repeat("Provision the integration lab cluster ", 4)[:120]
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", Name: long, ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.True(t, strings.HasSuffix(lines[0], "…"), lines[0])
	for _, l := range lines {
		require.LessOrEqual(t, lipgloss.Width(l), 80)
	}
}

func TestStagesTitleSaysLoading(t *testing.T) {
	m := planGolden(80, 24)
	m.plan.stagesLoading = true
	require.Contains(t, m.View(), "Stages · #44 · loading")
}

// TestPlanHeaderNamesABranchPlansMaster: the header names the plan the
// screen is about, also while its Builds list is a branch plan's, and falls
// back to the key for a plan outside the plans load.
func TestPlanHeaderNamesABranchPlansMaster(t *testing.T) {
	m := planGolden(80, 24)
	m.buildsPlan = "PROJ-BUILD12"
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Build and test", lines[0])
	require.Contains(t, lines[1], "PROJ › PROJ-BUILD @ main")

	m.plan.key, m.buildsPlan = "LAB-OTHER", "LAB-OTHER"
	require.Equal(t, " LAB-OTHER", strings.Split(m.View(), "\n")[0])
}
