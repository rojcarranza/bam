package tui

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: "PROJ-BUILD-43"})
	require.Equal(t, before, m.plan.tree, "a preview for another build is dropped")

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

// TestBuildsRuleSaysStaleAfterAFailedReload: a failed Builds load leaves the
// old rows on screen, so the rule says they are stale until a load lands.
func TestBuildsRuleSaysStaleAfterAFailedReload(t *testing.T) {
	m := planGolden(80, 24)
	m, _ = send(m, errMsg{Err: errors.New("boom"), Where: "builds", Stream: streamBuilds, Gen: m.buildsGen})
	require.Contains(t, strings.Split(m.View(), "\n")[2], "stale")

	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: m.builds.items})
	require.NotContains(t, strings.Split(m.View(), "\n")[2], "stale")
}

// TestDimRowsAreDimToTheEnd: a styled glyph inside a dim row would end the
// dim at its reset, leaving the rest of the row bright.
func TestDimRowsAreDimToTheEnd(t *testing.T) {
	forceColour(t)
	m := planGolden(80, 24)
	m.plan.tree.tree.Root.Children = []*app.TreeNode{
		{PlanKey: "PROJ-OTHER", Build: provider.Build{Key: "PROJ-OTHER-1", Number: 1, State: provider.StateFailed}},
		{PlanKey: "PROJ-PROV", Superseded: true, Build: provider.Build{Key: "PROJ-PROV-7", Number: 7, State: provider.StateFailed}},
	}
	row := strings.Split(m.treeBody(46, 5), "\n")[1]
	require.Equal(t, 1, strings.Count(row, "\x1b[0m"), "one reset: %q", row)

	older := provider.Build{Key: "PROJ-BUILD-9", PlanKey: "PROJ-BUILD", Number: 9, State: provider.StateFailed,
		FinishedAt: goldenNow.Add(-48 * time.Hour)}
	m.builds.setItems(append(m.builds.items, older))
	m.plan.older = older.Key
	row = strings.Split(m.planBuildRows(40, 5), "\n")[2]
	require.Equal(t, 1, strings.Count(row, "\x1b[0m"), "one reset: %q", row)
}

// TestALongReasonStaysOnItsLine: the panel wraps an over-long body line,
// which would push Tree and the status bar off the screen.
func TestALongReasonStaysOnItsLine(t *testing.T) {
	m := planGolden(80, 24)
	m.detail.Reason = strings.Repeat("Manual run by jdoe ", 11)[:200]
	lines := strings.Split(m.View(), "\n")
	require.Len(t, lines, 24)
	for _, l := range lines {
		require.LessOrEqual(t, lipgloss.Width(l), 80, l)
	}
}

func TestKeyLineFollowsThePlanScreenFocus(t *testing.T) {
	m := planGolden(80, 24)
	for _, tc := range []struct {
		f    focus
		want string
	}{
		{focusBuilds, "enter stages  R run  l logs  t tree  / filter  esc back"},
		{focusStages, "enter expand  l logs  R run  t tree  esc builds"},
		{focusTree, "enter open  t tree  l logs  esc builds"},
	} {
		m.focus = tc.f
		require.Equal(t, tc.want, m.keyLine())
		bar := m.statusBar(80)
		require.Contains(t, bar, tc.want, "the line fits an 80-column bar beside lab · 9.6.4 · jdoe")
	}
}

func TestRunFormNamesThePlan(t *testing.T) {
	m := formModel()
	m.plans.setItems([]provider.Plan{{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Provision lab", strings.TrimRight(ansi.Strip(lines[0]), " "))
	require.Contains(t, lines[1], "PROJ-PROV12")
	require.Contains(t, lines[1], "branch develop")
	require.Contains(t, lines[2], "Variables")
}

func TestLogScreenNamesThePlan(t *testing.T) {
	m := logModel()
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Build and test", ansi.Strip(lines[0]))
	require.Contains(t, lines[1], "logs")
	require.Len(t, lines, m.height)
}

func TestSmallScreensKeepTheirOldHeaders(t *testing.T) {
	m := logModel()
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"}})
	m.height = 12
	m.logs.resize(m.width, m.logHeight())
	require.NotContains(t, strings.Split(m.View(), "\n")[0], "Build and test")
}

// I1: Bamboo's listing omits queued and running builds, so a just-triggered
// build is newer than the list, not older: it goes on top without the
// "(older)" label and the header counts it as the last build.
func TestATriggeredBuildIsNotPinnedAsOlder(t *testing.T) {
	m := planHome(t)
	queued := sampleBuild()
	queued.Key, queued.Number, queued.State = "PROJ-BUILD-45", 45, provider.StateQueued
	queued.Stages = nil
	next, _ := m.openTriggered(queued)
	m = next.(Model)
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: threeBuilds()})
	rows := m.builds.rows()
	require.Equal(t, "PROJ-BUILD-45", rows[0].Key, "the triggered build goes on top")
	require.Empty(t, m.plan.older, "a newer build is not older")
	require.NotContains(t, ansi.Strip(m.View()), "(older)")
	require.Contains(t, strings.Split(m.View(), "\n")[1], "last build #45")

	listed := append([]provider.Build{queued}, threeBuilds()...)
	listed[0].State = provider.StateSuccess
	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: listed})
	require.Empty(t, m.plan.older)
	require.Len(t, m.builds.rows(), 4, "no duplicate row once the list has it")
	require.Equal(t, "PROJ-BUILD-45", m.builds.rows()[0].Key)
}

// A pinned older build that a refresh lists after all loses its label.
func TestAPinnedBuildLosesItsPinOnceListed(t *testing.T) {
	m := planHome(t)
	older := olderBuild()
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &older, focusStages)
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)

	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild(), older}})
	require.Empty(t, m.plan.older)
	require.Len(t, m.builds.rows(), 2)
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-3", b.Key)
}

// I2: the tree preview belongs to the plan screen, not to whichever screen
// is on top when it lands; the logs and the form sit above the plan screen.
func TestATreePreviewLandsWhileTheLogsAreOpen(t *testing.T) {
	m := planWithTree(t)
	m, cmd := m.loadTreePreview(m.detail.Key, true)
	require.NotNil(t, cmd)
	m.screen = screenLogs
	m, _ = send(m, cmd())
	require.True(t, m.plan.tree.have, "the preview read for this build is kept")
	require.False(t, m.plan.tree.loading)
}

// A watched build that finishes while its logs are open still gets its one
// tree re-read (§4.3).
func TestABuildFinishingUnderItsLogsReReadsTheTree(t *testing.T) {
	m := planWithTree(t)
	running := sampleBuild()
	running.State = provider.StateRunning
	m, _ = m.showBuild(running)
	require.NotNil(t, m.watchCancel)
	m.plan.tree = treePreview{key: running.Key, have: true}
	m.screen = screenLogs
	gen := m.treePreviewGen

	done := sampleBuild()
	m, cmd := send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{Type: app.EventDone, Build: done}})
	require.Greater(t, m.treePreviewGen, gen, "the tree is read again")
	require.True(t, m.plan.tree.loading)
	require.NotNil(t, cmd)
}

// I3: the plan cache is the old server's after a switch, whichever screen
// the switch is made from.
func TestServerSwitchFromATreeScreenDropsThePlanCache(t *testing.T) {
	m, _ := treeTUIModel(t)
	m.screen, m.focus = screenHome, focusPlans
	next, cmd := m.openTree("PROJ-BUILD-44")
	m = next.(Model)
	m, _ = send(m, cmd())
	m, _ = send(m, mkKey("enter")) // the root, PROJ-BUILD-44
	require.Equal(t, screenPlan, m.screen)
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: sampleBuild()})
	require.Contains(t, m.plan.cache, "PROJ-BUILD-44")
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen)

	next, _ = m.switchServer("lab")
	m = next.(Model)
	require.Empty(t, m.plan.cache, "nothing keyed to one origin is shown under another")
	require.Empty(t, m.plan.key)
}

func TestServerSwitchFromASetScreenDropsThePlanCache(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: sampleBuild()})
	require.Contains(t, m.plan.cache, "PROJ-BUILD-44")
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen)

	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.Empty(t, m.plan.cache)
}

// Entering from the set screen starts a fresh plan screen: no drill stack,
// cache or pin from an earlier visit.
func TestEnteringFromTheSetScreenStartsFresh(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m.plan.stack = []planFrame{{key: "PROJ-PROV", buildsPlan: "PROJ-PROV"}}
	m.plan.cache = map[string]provider.Build{"PROJ-PROV-7": {Key: "PROJ-PROV-7"}}
	m.plan.older = "PROJ-PROV-7"
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Empty(t, m.plan.stack)
	require.Empty(t, m.plan.cache)
	require.Empty(t, m.plan.older)

	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen)
}

// M2: Esc after a run returns to Home (§4.1), even when the run form was
// opened from a drilled plan screen reached from a set.
func TestEscAfterARunReturnsHome(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.fromSet)
	m.plan.stack = []planFrame{{key: "PROJ-PROV", buildsPlan: "PROJ-PROV"}}

	queued := sampleBuild()
	queued.Key, queued.Number, queued.State = "PROJ-BUILD-46", 46, provider.StateQueued
	m, _ = send(m, triggeredMsg{Gen: m.formGen, Build: queued})
	require.Equal(t, screenPlan, m.screen)
	require.Empty(t, m.plan.stack)
	require.False(t, m.fromSet)

	m, _ = send(m, mkKey("esc")) // Stages to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
}

// I4(a): the tree screen opened from a drilled plan screen comes back to
// that drilled screen, stack and all; esc from a build opened on the tree
// returns to the tree before any stack pop.
func TestTreeFromADrilledPlanComesBackToIt(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	m, _ = send(m, mkKey("enter")) // drill into PROJ-PROV-7
	require.Equal(t, "PROJ-PROV", m.plan.key)
	require.Len(t, m.plan.stack, 1)

	m = firstTree(t, m)
	require.Equal(t, "PROJ-PROV-7", m.buildTree.root)
	m, _ = send(m, mkKey("enter")) // the root
	require.Equal(t, screenPlan, m.screen)
	require.True(t, m.fromTree)
	m, _ = send(m, mkKey("esc")) // Stages to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen, "esc returns to the tree, not down the drill stack")
	require.False(t, m.fromTree)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-PROV", m.plan.key)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	require.Equal(t, focusStages, m.focus, "focus is where t was pressed")
	require.Len(t, m.plan.stack, 1, "the drill stack is restored")
	require.False(t, m.fromTree)

	m, _ = send(m, mkKey("esc")) // Stages to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "PROJ-BUILD", m.plan.key, "the drill stack pops as before")
}

// I4(b): esc off the tree screen lands on the plan screen t was pressed on,
// with its running build watched again.
func TestTreeFromAPlanComesBackToItsRunningBuild(t *testing.T) {
	m := planWithTree(t)
	running := sampleBuild()
	running.State = provider.StateRunning
	m, _ = m.showBuild(running)
	require.NotNil(t, m.watchCancel)
	m.focus = focusStages

	m = firstTree(t, m)
	require.Nil(t, m.watchCancel, "the tree screen watches the tree only")
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter")) // PROJ-PROV-7
	require.Equal(t, "PROJ-PROV", m.plan.key)
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-BUILD", m.plan.key, "back where t was pressed")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Equal(t, focusStages, m.focus)
	require.NotNil(t, m.watchCancel, "the running build is watched again")
}

// Leaving a tree opened on Home goes Home with nothing of the build opened
// on the tree left behind.
func TestEscOffATreeFromHomeLeavesNoPlanBehind(t *testing.T) {
	m, _ := treeTUIModel(t)
	m.screen, m.focus = screenHome, focusPlans
	next, cmd := m.openTree("PROJ-BUILD-44")
	m = next.(Model)
	m, _ = send(m, cmd())
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, focusPlans, m.focus)
	require.Empty(t, m.plan.key)
	require.Nil(t, m.detail)
}

// M1: j then enter within the rest delay opens the build at once; the rest
// that lands after must not read it again or restart its watch.
func TestARestForTheOpenBuildDoesNothing(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	m, cmd := send(m, mkKey("j"))
	rest := cmd()
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "PROJ-BUILD-43", m.detail.Key)
	gen, treeGen := m.detailGen, m.treePreviewGen

	m, cmd = send(m, rest)
	require.Nil(t, cmd)
	require.Equal(t, gen, m.detailGen, "the open build is not read again")
	require.Equal(t, treeGen, m.treePreviewGen)
}

// M3: on the plan screen only Builds has a filter; a Builds filter that
// moves the cursor loads the build it lands on, as a cursor move does.
func TestFilterIsForBuildsAndLoadsWhereItLands(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	for _, f := range []focus{focusStages, focusTree} {
		m.focus = f
		m, _ = send(m, mkKey("/"))
		require.Equal(t, inputNone, m.inputFor, "no prompt that would be ignored")
	}

	m.focus = focusBuilds
	m, _ = send(m, mkKey("/"))
	require.Equal(t, inputFilter, m.inputFor)
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("BUILD-42")})
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyEnter})
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-42", b.Key)
	require.NotNil(t, cmd, "the filtered cursor schedules a rest")
	m, _ = send(m, cmd())
	require.Equal(t, "PROJ-BUILD-42", m.detail.Key, "Stages shows a build the list shows")
}
