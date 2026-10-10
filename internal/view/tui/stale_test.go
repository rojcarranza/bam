package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// The tests here pin the Copilot review findings on PR #5: a response, or a
// channel close, that belongs to something the user has already left must not
// land on what they are looking at now.

// TestStaleWatchCloseDoesNotStopTheCurrentWatch is the sharpest of them: the
// abandoned watch's command is still scheduled, and its watchClosedMsg used
// to cancel the watch that replaced it.
func TestStaleWatchCloseDoesNotStopTheCurrentWatch(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{
		{Key: "PROJ-BUILD-44", Number: 44}, {Key: "PROJ-BUILD-43", Number: 43},
	}})
	m.focus = focusBuilds

	m, _ = send(m, mkKey("enter")) // watch A
	staleGen := m.watchGen

	m.focus = focusBuilds
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter")) // watch B
	require.NotEqual(t, staleGen, m.watchGen)
	require.NotNil(t, m.watchCancel)

	m, cmd := send(m, watchClosedMsg{Gen: staleGen})
	require.NotNil(t, m.watchCancel, "watch B must survive watch A's close")
	require.Nil(t, cmd)
	m.stopWatch()
}

// TestStaleWatchEventDoesNotOverwriteTheCurrentBuild.
func TestStaleWatchEventDoesNotOverwriteTheCurrentBuild(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.watchGen = 7

	old := sampleBuild()
	old.Key, old.State = "PROJ-BUILD-43", provider.StateSuccess
	m, _ = send(m, watchEventMsg{Gen: 6, Event: app.Event{Type: app.EventState, Build: old}})
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
}

// TestStaleDoneDoesNotRefreshOrStop.
func TestStaleDoneDoesNotRefreshOrStop(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.buildsPlan = "PROJ-BUILD"
	m.watchGen = 3
	stopped := false
	m.watchCancel = func() { stopped = true }

	m, cmd := send(m, watchEventMsg{Gen: 2, Event: app.Event{Type: app.EventDone, State: provider.StateFailed}})
	require.False(t, stopped)
	require.Nil(t, cmd)
	require.NotNil(t, m.watchCancel)
}

// TestStalePlansResponseIsDropped: switching project must not be undone by the
// previous project's slower reply.
func TestStalePlansResponseIsDropped(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.plans.setItems([]provider.Plan{{Key: "OPS-NIGHTLY"}})
	stale := m.plansGen

	m.plansGen++ // as a project switch would
	m, _ = send(m, plansLoadedMsg{Gen: stale, Plans: []provider.Plan{{Key: "PROJ-BUILD"}, {Key: "PROJ-PROV"}}})
	require.Equal(t, 1, m.plans.len())
	sel, _ := m.plans.selected()
	require.Equal(t, "OPS-NIGHTLY", sel.Key)
}

// TestStaleBuildsResponseIsDropped, the same during a branch switch.
func TestStaleBuildsResponseIsDropped(t *testing.T) {
	m := testModel()
	m.svc = testService()
	stale := m.buildsGen
	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: stale, PlanKey: "PROJ-BUILD", Builds: []provider.Build{{Key: "PROJ-BUILD-44"}}})
	require.Equal(t, 0, m.builds.len())
	require.Equal(t, "", m.buildsPlan)
}

// TestStaleLogsResponseIsDropped: opening a second log before the first
// arrives must not show the first.
func TestStaleLogsResponseIsDropped(t *testing.T) {
	m := logModel()
	stale := m.logsGen
	m.logsGen++
	m, _ = send(m, logsLoadedMsg{Gen: stale, JobKey: "OTHER", Title: "OTHER", Lines: []string{"wrong log"}})
	require.Equal(t, "PROJ-BUILD-INT-44", m.logs.title)
	require.NotContains(t, m.logs.lines, "wrong log")
}

// TestStaleFollowChunkIsDropped.
func TestStaleFollowChunkIsDropped(t *testing.T) {
	m := logModel()
	before := len(m.logs.lines)
	m.followGen = 4
	m, _ = send(m, logEventMsg{Gen: 3, Event: linesEvent("PROJ-BUILD-INT-44", 5, "from a job we left")})
	require.Len(t, m.logs.lines, before)
}

// TestBranchSwitchClearsTheBuildsRows: the rows on screen belong to the branch
// being left, and Enter on one of them would open the wrong build.
func TestBranchSwitchClearsTheBuildsRows(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-PROV"}}})
	require.Positive(t, m.builds.len())

	m, _ = send(m, mkKey("b"))
	m, _ = send(m, branchesLoadedMsg{Gen: m.pickerGen, MasterKey: "PROJ-PROV", Branches: []provider.Branch{
		{Key: "PROJ-PROV12", ShortName: "develop", PlanKey: "PROJ-PROV"},
	}})
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, 0, m.builds.len(), "the old branch's builds are gone while the new ones load")
}

// TestRefreshKeepsTheBuildsRowsAndTheCursor: a refresh is not a switch.
func TestRefreshKeepsTheBuildsRowsAndTheCursor(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.buildsPlan = "PROJ-BUILD"
	m.focus = focusBuilds
	m.builds.move(1)

	m, _ = send(m, mkKey("r"))
	require.Equal(t, 2, m.builds.len())
	require.Equal(t, 1, m.builds.cursor)
}

// TestCurrentBuildFollowsTheBuildsCursorWhenBuildsHasFocus: esc leaves the
// detail open, so l after moving down must not show the old build's log.
func TestCurrentBuildFollowsTheBuildsCursorWhenBuildsHasFocus(t *testing.T) {
	m := goldenModel(80, 24)
	m.detail = ptr(sampleBuild()) // PROJ-BUILD-44
	m.focus = focusBuilds
	m.builds.move(1) // onto #43

	b, ok := m.currentBuild()
	require.True(t, ok)
	require.Equal(t, "PROJ-BUILD-43", b.Key)

	m.focus = focusMain
	b, _ = m.currentBuild()
	require.Equal(t, "PROJ-BUILD-44", b.Key, "Main still acts on the open build")
}

// TestResizeReflowsTheLogViewport: the viewport holds its own dimensions.
func TestResizeReflowsTheLogViewport(t *testing.T) {
	m := logModel()
	require.Equal(t, 80, m.logs.vp.Width)

	m, _ = send(m, windowSize(120, 40))
	require.Equal(t, 120, m.logs.vp.Width)
	require.Equal(t, m.logHeight(), m.logs.vp.Height)
}

func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// TestStaleConnectIsDropped: switching from one server to another before the
// first handshake returns must not install the server being left.
func TestStaleConnectIsDropped(t *testing.T) {
	m := testModel()
	m.connGen = 4
	m, _ = send(m, connectedMsg{Gen: 3, Alias: "old", Svc: testService(),
		Info: provider.ServerInfo{Version: "1.0.0"}})
	require.Nil(t, m.svc)
	require.NotEqual(t, "old", m.server)
	require.NotEqual(t, "1.0.0", m.info.Version)
}

// TestSwitchingServerAbandonsPlansAndBuildsInFlight: clearing the rows is not
// enough, because the older request still matches the unchanged generation.
func TestSwitchingServerAbandonsPlansAndBuildsInFlight(t *testing.T) {
	m := New(Deps{Servers: []Server{{Alias: "lab"}, {Alias: "work"}}, Initial: "lab",
		Connect: func(context.Context, string) (*app.Service, error) { return testService(), nil }})
	m.width, m.height = 80, 24
	m.svc = testService()
	plansBefore, buildsBefore := m.plansGen, m.buildsGen

	m, _ = send(m, mkKey("S"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.NotEqual(t, plansBefore, m.plansGen)
	require.NotEqual(t, buildsBefore, m.buildsGen)

	m, _ = send(m, plansLoadedMsg{Gen: plansBefore, Plans: []provider.Plan{{Key: "OLD-PLAN"}}})
	require.Equal(t, 0, m.plans.len(), "the old server's plans are dropped")
}

// TestStaleBuildRefreshIsDropped: r on Main, then a different build opened.
func TestStaleBuildRefreshIsDropped(t *testing.T) {
	m := testModel()
	m.detail = ptr(sampleBuild())
	m.detailGen = 2
	other := sampleBuild()
	other.Key = "PROJ-BUILD-43"
	m, _ = send(m, buildLoadedMsg{Gen: 1, Build: other})
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
}

// TestStalePickerResponsesAreDropped: closing and reopening P, or switching
// server, must not repopulate the picker from the earlier request.
func TestStalePickerResponsesAreDropped(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, mkKey("P"))
	stale := m.pickerGen
	m.pickerGen++

	m, _ = send(m, projectsLoadedMsg{Gen: stale, Stream: streamPicker, Projects: []provider.Project{{Key: "OLD"}}})
	require.Equal(t, 0, m.picker.len())

	m, _ = send(m, branchesLoadedMsg{Gen: stale, MasterKey: "OLD", Branches: nil})
	require.Equal(t, 0, m.picker.len())
}

// TestFollowSurfacesItsError: the goroutine used to close the line channel
// before publishing the error, so a real log-fetch failure could be read as a
// clean end.
func TestFollowSurfacesItsError(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.LogErrs = []error{errBoom}
	f.Sequences = map[string][]provider.Build{"PROJ-BUILD-44": {sampleBuild()}}

	events, done := followCmd(t.Context(), svc, "PROJ-BUILD-44", map[string]int{"PROJ-BUILD-INT-44": 0}, false)

	var msg tea.Msg
	for {
		msg = drainFollowCmd(events, done, 0)()
		if _, ok := msg.(logEventMsg); !ok {
			break
		}
	}
	ended, ok := msg.(followEndedMsg)
	require.True(t, ok, "got %T", msg)
	require.Error(t, ended.Err, "the fetch failed, so the end must say so")
}

// TestStalePresetsResponseIsDropped: two refreshes can finish out of order,
// and the panel documents a live reread.
func TestStalePresetsResponseIsDropped(t *testing.T) {
	m := testModel()
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen, Targets: []app.TargetInfo{{Name: "current"}}})
	stale := m.presetsGen
	m.presetsGen++

	m, _ = send(m, presetsLoadedMsg{Gen: stale, Targets: []app.TargetInfo{{Name: "older"}, {Name: "older2"}}})
	require.Equal(t, 1, m.presets.len())
	sel, _ := m.presets.selected()
	require.Equal(t, "current", sel.Name)
}

// TestOpeningAnotherPlanStopsTheWatchAndClearsTheDetail: the open build
// belongs to the plan being left, and its watch would keep overwriting the
// main panel while the new plan loads.
func TestOpeningAnotherPlanStopsTheWatchAndClearsTheDetail(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-BUILD"}, {Key: "PROJ-PROV"}}})
	m.detail = ptr(sampleBuild())
	stopped := false
	m.watchCancel = func() { stopped = true }

	m.focus = focusPlans
	m, _ = send(m, mkKey("enter"))
	require.True(t, stopped)
	require.Nil(t, m.detail)
	require.Nil(t, m.watchCancel)
}

// TestOpeningAPresetStopsTheWatchToo.
func TestOpeningAPresetStopsTheWatchToo(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen,
		Targets: []app.TargetInfo{{Name: "smoke", Plan: "PROJ-PROV", Branch: "develop"}}})
	m.detail = ptr(sampleBuild())
	stopped := false
	m.watchCancel = func() { stopped = true }

	m.focus = focusPresets
	m, _ = send(m, mkKey("enter"))
	require.True(t, stopped)
	require.Nil(t, m.detail)
}

// TestSwitchingServerClearsThePresetsAndTheProjectFilter: both are keyed to
// the server being left.
func TestSwitchingServerClearsThePresetsAndTheProjectFilter(t *testing.T) {
	m := New(Deps{Servers: []Server{{Alias: "lab"}, {Alias: "work"}}, Initial: "lab",
		Connect: func(context.Context, string) (*app.Service, error) { return testService(), nil },
		Targets: func() ([]app.TargetInfo, error) { return []app.TargetInfo{{Name: "fresh"}}, nil }})
	m.width, m.height = 80, 24
	m.svc = testService()
	m.project = "OLD-PROJECT"
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen, Targets: []app.TargetInfo{{Name: "from-the-old-server"}}})
	require.Equal(t, 1, m.presets.len())

	m, _ = send(m, mkKey("S"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, 0, m.presets.len(), "the old server's presets are gone")
	require.Equal(t, "", m.project, "a project filter from another server would hide everything")
}

// TestAReopensTheLogsWithEveryJob: the failed-log error advises pressing a,
// so a has to work from the log screen.
func TestAReopensTheLogsWithEveryJob(t *testing.T) {
	m := logModel()
	m, cmd := send(m, mkKey("a"))
	require.Equal(t, screenLogs, m.screen)
	require.NotNil(t, cmd, "a must reopen the logs, not scroll the viewport")
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.True(t, msg.All)
}

// TestLogsOnASummaryBuildFetchTheFullBuildFirst. ListBuilds returns builds
// without their stage and job tree, so the row the cursor is on has no jobs
// to pick a log from.
func TestLogsOnASummaryBuildFetchTheFullBuildFirst(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	summary := provider.Build{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44}
	require.Empty(t, summary.Stages)
	m.builds.setItems([]provider.Build{summary})
	m.focus = focusBuilds
	m.svc.P.(*fake.Provider).History["PROJ-BUILD"] = []provider.Build{sampleBuild()}

	m, cmd := send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	require.NotNil(t, cmd)
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T: a summary row must be expanded before its logs are read", cmd())
	require.Equal(t, "PROJ-BUILD-INT-44", msg.JobKey)
}

// TestALongPickerRowDoesNotWidenTheModal: a configured URL can be long, and a
// row wider than the box breaks the whole layout.
func TestALongPickerRowDoesNotWidenTheModal(t *testing.T) {
	m := goldenModel(80, 24)
	m.overlay = overlayServers
	m.picker.setItems([]pickerItem{{
		Label:  "a-very-long-server-alias-that-nobody-would-really-use",
		Detail: "https://bamboo.lab.example/a/very/long/path/that/keeps/going/and/going",
	}})
	for i, line := range strings.Split(m.View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 80, "line %d", i)
	}
}

// TestTheDetailTreeScrollsWithItsCursor: the cursor used to walk onto rows
// that truncation had cut, so enter and o acted on something invisible.
func TestTheDetailTreeScrollsWithItsCursor(t *testing.T) {
	b := sampleBuild()
	for i := 0; i < 40; i++ {
		b.Stages = append(b.Stages, provider.Stage{
			Name: "Stage" + string(rune('A'+i%26)) + string(rune('0'+i/26)), State: provider.StateSuccess})
	}
	m := goldenModel(80, 24)
	m.detail, m.expanded, m.focus = &b, map[string]bool{}, focusMain
	m.treeCursor = len(m.treeRows()) - 1

	last, ok := m.selectedTreeRow()
	require.True(t, ok)
	require.Contains(t, m.detailBody(56, 18), last.Name,
		"the row the cursor is on must be on screen")
}

// TestPresetsAreKeptToTheConnectedServer: a preset may name a server of its
// own, and acting on one bound to another alias would resolve its plan
// against the Bamboo the UI is not connected to.
func TestPresetsAreKeptToTheConnectedServer(t *testing.T) {
	m := testModel()
	m.server = "lab"
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen, Targets: []app.TargetInfo{
		{Name: "lab-smoke", Plan: "LAB-SMOKE", Server: "lab"},
		{Name: "any", Plan: "PROJ-BUILD"},
		{Name: "work-only", Plan: "OPS-NIGHTLY", Server: "work"},
	}})

	names := []string{}
	for _, t := range m.presets.rows() {
		names = append(names, t.Name)
	}
	require.Equal(t, []string{"any", "lab-smoke"}, names, "the presets sort (alphabetical by target) now applies on load")
}

// TestAStaleErrorDoesNotReplaceTheCurrentStatus.
func TestAStaleErrorDoesNotReplaceTheCurrentStatus(t *testing.T) {
	m := testModel()
	m.plansGen = 5
	m, _ = send(m, errMsg{Err: errBoom, Where: "plans", Stream: streamPlans, Gen: 4})
	require.NoError(t, m.err)

	m, _ = send(m, errMsg{Err: errBoom, Where: "plans", Stream: streamPlans, Gen: 5})
	require.Error(t, m.err, "the current request's failure is shown")
}

// TestChangingTheProjectDropsTheOldProjectsBuildAndWatch.
func TestChangingTheProjectDropsTheOldProjectsBuildAndWatch(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.buildsPlan = "PROJ-BUILD"
	stopped := false
	m.watchCancel = func() { stopped = true }

	m, _ = send(m, mkKey("P"))
	m, _ = send(m, projectsLoadedMsg{Gen: m.pickerGen, Stream: streamPicker, Projects: []provider.Project{{Key: "OPS"}}})
	m, _ = send(m, mkKey("j")) // onto OPS
	m, _ = send(m, mkKey("enter"))

	require.Equal(t, "OPS", m.project)
	require.True(t, stopped, "the old project's watch stops")
	require.Nil(t, m.detail)
	require.Equal(t, 0, m.plans.len(), "the old project's plans are not selectable while the new ones load")
	require.Equal(t, 0, m.builds.len())
}

// TestTheWatchIsRootedInTheUIContext: bubbletea can return because its own
// context was cancelled, without any key reaching quit.
func TestTheWatchIsRootedInTheUIContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := testModel().withContext(ctx)
	m.svc = testService()
	m.builds.setItems([]provider.Build{{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD"}})
	m.focus = focusBuilds

	m, _ = send(m, mkKey("enter"))
	require.NotNil(t, m.watchCh)

	cancel()
	// The watch's own channel closes because its parent went away.
	for range m.watchCh { //nolint:revive // draining until closed is the assertion
	}
}

// TestLeavingABuildInvalidatesAPendingRefresh: leaveBuild cancelled the watch
// but left detailGen alone, so a refresh already in flight could restore the
// abandoned build under the new selection.
func TestLeavingABuildInvalidatesAPendingRefresh(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "PROJ-BUILD"}, {Key: "PROJ-PROV"}}})
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m, _ = send(m, mkKey("r")) // a Main refresh is now in flight
	pending := m.detailGen

	m.focus = focusPlans
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter")) // a different plan
	require.Nil(t, m.detail)

	m, _ = send(m, buildLoadedMsg{Gen: pending, Build: sampleBuild()})
	require.Nil(t, m.detail, "the abandoned build must not come back")
}

// TestReopeningTheLogsStopsAnActiveFollow: a on the log screen replaced the
// log state while the old follow was still running, so its chunks landed in
// the new view.
func TestReopeningTheLogsStopsAnActiveFollow(t *testing.T) {
	m := logModel()
	m, _ = send(m, mkKey("f"))
	require.True(t, m.logs.following)
	stopped := false
	m.followCancel = func() { stopped = true }
	staleGen := m.followGen

	m, _ = send(m, mkKey("a"))
	require.True(t, stopped, "reopening the logs stops the follow")
	require.False(t, m.logs.following)

	before := len(m.logs.lines)
	m, _ = send(m, logEventMsg{Gen: staleGen, Event: linesEvent("PROJ-BUILD-INT-44", 5, "from the job we left")})
	require.Len(t, m.logs.lines, before)
}

// TestFollowWorksForLogsOpenedFromASummaryRow: the command fetched the full
// build only into a local variable, so f later found no jobs on the summary
// the model still held.
func TestFollowWorksForLogsOpenedFromASummaryRow(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	running := sampleBuild()
	running.State = provider.StateRunning
	m.svc.P.(*fake.Provider).History["PROJ-BUILD"] = []provider.Build{running}
	m.builds.setItems([]provider.Build{{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44}})
	m.focus = focusBuilds

	m, cmd := send(m, mkKey("l"))
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, screenLogs, m.screen)

	m, followCmd := send(m, mkKey("f"))
	require.True(t, m.logs.following, "the full build is in hand, so f can follow")
	require.NotNil(t, followCmd)
	m.stopFollow()
}

// TestAPresetResolutionFailureIsShown: the command runs on the builds stream,
// so tagging its first failure as a presets failure had Update compare the
// generation against the wrong counter and drop a real error silently.
func TestAPresetResolutionFailureIsShown(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen,
		Targets: []app.TargetInfo{{Name: "gone", Plan: "PROJ-PROV", Branch: "no-such-branch"}}})
	m.focus = focusPresets

	m, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
	msg, ok := cmd().(errMsg)
	require.True(t, ok, "got %T", cmd())
	require.Equal(t, streamBuilds, msg.Stream, "the command is a builds load")
	require.Equal(t, m.buildsGen, msg.Gen)

	m, _ = send(m, msg)
	require.Error(t, m.err, "the failure must reach the status bar, not be dropped as stale")
}
