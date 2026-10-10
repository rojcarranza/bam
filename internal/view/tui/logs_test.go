package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

func logLines() []string {
	return []string{
		"12:04:31  [INFO] Running integration suite",
		"12:06:58  [ERROR] ConnectionRefused: bamboo.lab.example:5432",
		"12:06:58  [ERROR] 3 tests failed",
		"12:07:01  Finished with exit code 1",
	}
}

func logModel() Model {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m, _ = send(m, logsLoadedMsg{Gen: m.logsGen, JobKey: "PROJ-BUILD-INT-44", Title: "PROJ-BUILD-INT-44",
		URL: labOrigin + "/browse/PROJ-BUILD-INT-44", Lines: logLines(),
		Offsets: map[string]int{"PROJ-BUILD-INT-44": 4}})
	m.screen = screenLogs
	return m
}

// TestLOpensTheLogScreenFullWidth, spec §3.1.
func TestLOpensTheLogScreenFullWidth(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{sampleBuild()})
	m.detail = ptr(sampleBuild())
	m.focus = focusBuilds
	m, cmd := send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	require.NotNil(t, cmd)

	m, _ = send(m, logsLoadedMsg{Gen: m.logsGen, JobKey: "PROJ-BUILD-INT-44", Title: "PROJ-BUILD-INT-44", Lines: logLines()})
	v := m.View()
	require.NotContains(t, v, "1 Plans", "the left column is hidden")
	require.Contains(t, v, "ConnectionRefused")
}

// TestLDefaultsToTheFailedJobs, matching bam logs --last --failed.
func TestLDefaultsToTheFailedJobs(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{sampleBuild()})
	m.detail = ptr(sampleBuild())
	m.focus = focusBuilds
	_, cmd := send(m, mkKey("l"))
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.False(t, msg.All)
	require.Equal(t, "PROJ-BUILD-INT-44", msg.JobKey, "the failed job, not the first job")
}

func TestAOpensEveryJobsLog(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{sampleBuild()})
	m.detail = ptr(sampleBuild())
	m.focus = focusBuilds
	_, cmd := send(m, mkKey("a"))
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.True(t, msg.All)
}

func TestEscLeavesTheLogScreen(t *testing.T) {
	m := logModel()
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenColumns, m.screen)
}

// TestLogHeaderNamesTheJobAndThePosition, spec §3.1's header.
func TestLogHeaderNamesTheJobAndThePosition(t *testing.T) {
	v := logModel().View()
	require.Contains(t, v, "PROJ-BUILD-INT-44")
	require.Contains(t, v, "logs")
	require.Contains(t, v, "failed")
	require.Contains(t, v, "/4", "the line count is in the header")
}

func TestLogScreenNeverExceedsTheTerminal(t *testing.T) {
	for i, line := range strings.Split(logModel().View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 80, "line %d", i)
	}
}

func TestLogGolden(t *testing.T) {
	requireGolden(t, "logs-80x24", logModel().View())
}

func TestOpeningLogsWithNoBuildDoesNothing(t *testing.T) {
	m := goldenModel(80, 24)
	m.detail = nil
	m.builds.setItems([]provider.Build{})
	m, cmd := send(m, mkKey("l"))
	require.Equal(t, screenColumns, m.screen)
	require.Nil(t, cmd)
}

func TestSearchJumpsToTheFirstMatch(t *testing.T) {
	m := logModel()
	m.logs.search("connectionrefused")
	require.Equal(t, []int{1}, m.logs.matches, "search is case-insensitive")
	require.Equal(t, 1, m.logs.line)
}

func TestNAndShiftNWalkTheMatchesAndWrap(t *testing.T) {
	m := logModel()
	m.logs.search("ERROR")
	require.Equal(t, []int{1, 2}, m.logs.matches)
	require.Equal(t, 0, m.logs.match)

	m.logs.nextMatch(1)
	require.Equal(t, 1, m.logs.match)
	m.logs.nextMatch(1)
	require.Equal(t, 0, m.logs.match, "n wraps at the end")
	m.logs.nextMatch(-1)
	require.Equal(t, 1, m.logs.match, "N wraps at the start")
}

// TestNWithNoSearchGoesToTheNextFailure, which is what the key line promises.
func TestNWithNoSearchGoesToTheNextFailure(t *testing.T) {
	m := logModel()
	m.logs.query = ""
	m.logs.nextFailure(1)
	require.Equal(t, 1, m.logs.line, "the first ERROR line")
}

func TestSlashOpensTheSearchInputAndKeysGoToIt(t *testing.T) {
	m := logModel()
	m, _ = send(m, mkKey("/"))
	require.Equal(t, inputSearch, m.inputFor)

	for _, r := range "quota" {
		m, _ = send(m, mkKey(string(r)))
	}
	require.Equal(t, "quota", m.input.Value())
	require.Equal(t, screenLogs, m.screen, "q while typing does not quit")

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, inputNone, m.inputFor)
	require.Equal(t, "quota", m.logs.query)
}

func TestEscCancelsTheSearchInputBeforeTheScreen(t *testing.T) {
	m := logModel()
	m, _ = send(m, mkKey("/"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, inputNone, m.inputFor)
	require.Equal(t, screenLogs, m.screen, "the input closes before the screen")
}

func TestSlashInAPanelFiltersThatPanel(t *testing.T) {
	m := goldenModel(80, 24)
	m, _ = send(m, mkKey("/"))
	require.Equal(t, inputFilter, m.inputFor)
	for _, r := range "ops" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, 1, m.plans.len())
	sel, _ := m.plans.selected()
	require.Equal(t, "OPS-NIGHTLY", sel.Key)
}

func linesEvent(job string, next int, lines ...string) app.LogEvent {
	return app.LogEvent{Kind: app.LogLines, Job: provider.Job{Key: job}, Lines: lines, Next: next}
}

// TestDrainFollowDeliversOneEventPerCall, the same pattern the watch uses.
func TestDrainFollowDeliversOneEventPerCall(t *testing.T) {
	events := make(chan app.LogEvent, 2)
	done := make(chan error, 1)
	events <- linesEvent("PROJ-BUILD-INT-44", 5, "12:07:02  still running")
	events <- linesEvent("PROJ-BUILD-INT-44", 6, "12:07:05  done")
	close(events)
	done <- nil

	first := drainFollowCmd(events, done, 0)().(logEventMsg)
	require.Equal(t, []string{"12:07:02  still running"}, first.Event.Lines)
	second := drainFollowCmd(events, done, 0)().(logEventMsg)
	require.Equal(t, []string{"12:07:05  done"}, second.Event.Lines)
	end := drainFollowCmd(events, done, 0)()
	require.IsType(t, followEndedMsg{}, end)
	require.NoError(t, end.(followEndedMsg).Err)
}

func TestLogEventAppendsAndStaysPinnedToTheBottom(t *testing.T) {
	m := logModel()
	before := len(m.logs.lines)
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 5, "12:07:05  done")})
	require.Len(t, m.logs.lines, before+1)
	require.Equal(t, "12:07:05  done", m.logs.lines[before], "a single job's lines stay bare")
	require.True(t, m.logs.vp.AtBottom())
}
func TestFToggleStartsAndStopsFollowing(t *testing.T) {
	m := logModel()

	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	require.NotNil(t, m.followCancel)

	m.followCancel()
	stopped := false
	m.followCancel = func() { stopped = true }
	m, _ = send(m, mkKey("f"))
	require.False(t, m.logs.following)
	require.True(t, stopped)
	require.Nil(t, m.followCancel)
}

// TestLeavingTheLogScreenStopsFollowing: no goroutine outlives the screen.
func TestLeavingTheLogScreenStopsFollowing(t *testing.T) {
	m := logModel()
	m.logs.following = true
	stopped := false
	m.followCancel = func() { stopped = true }
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenColumns, m.screen)
	require.True(t, stopped)
	require.Nil(t, m.followCancel)
}

func TestFollowEndedShowsAnErrorWithoutQuitting(t *testing.T) {
	m := logModel()
	m.logs.following = true
	m, cmd := send(m, followEndedMsg{Err: errBoom})
	require.False(t, m.logs.following)
	require.Error(t, m.err)
	require.Nil(t, cmd)
	require.Equal(t, screenLogs, m.screen)
}

func TestFollowingAJobThatIsNotInTheBuildDoesNothing(t *testing.T) {
	m := logModel()
	m.logs.offsets = map[string]int{"PROJ-BUILD-GONE-44": 0}
	m, cmd := send(m, mkKey("f"))
	require.False(t, m.logs.following)
	require.Nil(t, cmd)
}

// TestFollowStartsFromTheProviderOffsets. app.JobLog.Next is the provider's
// offset contract; the rendered line count is not.
func TestFollowStartsFromTheProviderOffsets(t *testing.T) {
	m := logModel()
	m.logs.offsets = map[string]int{"PROJ-BUILD-INT-44": 41}
	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	require.Equal(t, map[string]int{"PROJ-BUILD-INT-44": 41}, m.followFrom)
	m.stopFollow()
}

// TestFollowWorksWhileSeveralJobsAreShown: the prefixes keep interleaved jobs
// apart, so the old refusal is gone (spec §4.3).
func TestFollowWorksWhileSeveralJobsAreShown(t *testing.T) {
	m := logModel()
	m.logs.multi = true
	m.logs.offsets = map[string]int{"PROJ-BUILD-INT-44": 4, "PROJ-BUILD-COMP-44": 1}
	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	m.stopFollow()
}

func tallLogModel(n int) Model {
	m := logModel()
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.logs.setLines(m.width, m.logHeight(), lines)
	return m
}

func TestAChunkScrollsWhilePinned(t *testing.T) {
	m := tallLogModel(60)
	require.True(t, m.logs.pinned, "a log opens pinned to the bottom")
	m.logs.appendLines([]string{"line 60"})
	require.True(t, m.logs.vp.AtBottom())
}

// TestAJumpUnpinsAndGRepins: a match on screen must not be scrolled away by
// the next chunk, spec §4.3.
func TestAJumpUnpinsAndGRepins(t *testing.T) {
	m := tallLogModel(60)
	m.logs.search("line 05")
	require.False(t, m.logs.pinned)
	y := m.logs.vp.YOffset
	m.logs.appendLines([]string{"line 60"})
	require.Equal(t, y, m.logs.vp.YOffset, "the reader stays on the match")

	m, _ = send(m, mkKey("G"))
	require.True(t, m.logs.pinned)
	m.logs.appendLines([]string{"line 61"})
	require.True(t, m.logs.vp.AtBottom())
}

func TestAViewportKeyAtTheBottomRepins(t *testing.T) {
	m := tallLogModel(60)
	m.logs.search("line 05")
	m.logs.vp.GotoBottom()
	m, _ = send(m, mkKey("down"))
	require.True(t, m.logs.pinned)
	m, _ = send(m, mkKey("up"))
	require.False(t, m.logs.pinned)
}

func TestASearchCountsLinesThatArriveLater(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	require.Equal(t, []int{1, 2}, m.logs.matches)
	m.logs.appendLines([]string{"12:07:09  [ERROR] again"})
	require.Equal(t, []int{1, 2, 4}, m.logs.matches)
	require.Equal(t, 0, m.logs.match, "the current match does not move")
	require.Contains(t, m.View(), "match 1/3")
}

func TestEscInTheSearchInputClearsTheSearch(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	m, _ = send(m, mkKey("/"))
	m, _ = send(m, mkKey("esc"))
	require.Empty(t, m.logs.query)
	require.Empty(t, m.logs.matches)
	require.NotContains(t, m.View(), "match")
}

func TestKeyLineSaysWhatNDoes(t *testing.T) {
	m := logModel()
	require.Contains(t, m.View(), "n next failure")
	m.logs.search("error")
	require.Contains(t, m.View(), "n next match")
}

func TestHighlightSpans(t *testing.T) {
	require.Equal(t, [][2]int{{3, 8}, {16, 21}}, highlightSpans("an Error and an ERROR", "error"))
	line := "ÉCHEC du test"
	require.Equal(t, [][2]int{{0, len(line)}}, highlightSpans(line, "échec"),
		"a non-ASCII case match highlights the whole line")
	require.Nil(t, highlightSpans("nothing here", "zzz"))
	require.Nil(t, highlightSpans("anything", ""))
}

func TestHighlightLeavesTheTextIntact(t *testing.T) {
	line := "12:06:58  [ERROR] ConnectionRefused"
	require.Equal(t, line, ansi.Strip(highlight(line, "error")))
}

// TestResizeKeepsTheSearch: Review Focus 3.
func TestResizeKeepsTheSearch(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	m.logs.resize(100, 30)
	require.Equal(t, "error", m.logs.query)
	require.Equal(t, []int{1, 2}, m.logs.matches)
	require.Len(t, m.logs.rendered, len(m.logs.lines))
}

func TestAnEmptyFollowedLogSaysItIsWaiting(t *testing.T) {
	m := logModel()
	m.logs.setLines(m.width, m.logHeight(), nil)
	m.logs.following = true
	m.logs.refresh()
	require.Contains(t, m.View(), waitingLine)
	require.Empty(t, m.logs.lines, "the waiting line is not part of the log")
	m.logs.search("waiting")
	require.Empty(t, m.logs.matches)
}

func runningBuild() provider.Build {
	b := sampleBuild()
	b.State = provider.StateRunning
	b.Stages[1].State = provider.StateRunning
	b.Stages[1].Jobs[0].State = provider.StateRunning
	b.Stages[2].State = provider.StateQueued
	b.Stages[2].Jobs = []provider.Job{{Key: "PROJ-BUILD-DEP-44", Name: "Deploy", State: provider.StateQueued}}
	return b
}

// stageModel is a live stage screen on runningBuild's Test stage.
func stageModel() Model {
	m := goldenModel(80, 24)
	m.svc = testService()
	b := runningBuild()
	m.detail = &b
	m, _ = send(m, logsLoadedMsg{Gen: m.logsGen, Title: b.Key, Stage: "Test", Multi: true, Build: b,
		Lines:   []string{"[INT] 12:04:31  [INFO] Running integration suite"},
		Offsets: map[string]int{"PROJ-BUILD-INT-44": 1}})
	m.screen = screenLogs
	return m
}

func TestLogLinesArePrefixedOnAMultiJobScreen(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 2, "12:06:58  [ERROR] refused")})
	require.Equal(t, "[INT] 12:06:58  [ERROR] refused", m.logs.lines[len(m.logs.lines)-1])
}

func TestLogLinesRecordTheJobsOffset(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 7, "x")})
	require.Equal(t, 7, m.logs.offsets["PROJ-BUILD-INT-44"])
}

func TestStageStartAddsASeparatorAndItsJobs(t *testing.T) {
	m := stageModel()
	next := runningBuild()
	next.Stages[1].State = provider.StateSuccess
	next.Stages[2].State = provider.StateRunning
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.StageStart, Stage: "Deploy", Build: next}})
	n := len(m.logs.lines)
	require.Equal(t, []string{"", "== Deploy =="}, m.logs.lines[n-2:])
	require.Equal(t, "Deploy", m.logs.stage)
	require.Equal(t, provider.StateRunning, m.logs.live)
	require.Contains(t, m.logs.offsets, "PROJ-BUILD-DEP-44")
	require.Zero(t, m.logs.offsets["PROJ-BUILD-DEP-44"])
}

func TestLogStatusShowsTheStageStateInTheHeader(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.LogStatus, Stage: "Test", Build: runningBuild()}})
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Test · running")
}

// TestRefollowResumesFromTheStreamedOffsets: Review Focus 1.
func TestRefollowResumesFromTheStreamedOffsets(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 9, "x")})
	m, _ = send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.Equal(t, 9, m.followFrom["PROJ-BUILD-INT-44"], "no line is read twice")
	m.stopFollow()
}

func TestAllJobsAreGroupedByStageAndPrefixed(t *testing.T) {
	b := sampleBuild()
	jobs := []app.JobLog{
		{Stage: "Build", Job: b.Stages[0].Jobs[0], Lines: []string{"compiled"}},
		{Stage: "Build", Job: b.Stages[0].Jobs[1], Lines: []string{"passed"}},
		{Stage: "Test", Job: b.Stages[1].Jobs[0], Lines: []string{"refused"}},
	}
	require.Equal(t, []string{
		"== Build ==", "[COMP] compiled", "[UNIT] passed",
		"", "== Test ==", "[INT] refused",
	}, composeLogs(b, jobs, true, ""))
	require.Equal(t, []string{"[INT] refused"}, composeLogs(b, jobs[2:], true, "Test"),
		"a stage screen's header names the stage, so no separator")
	require.Equal(t, []string{"refused"}, composeLogs(b, jobs[2:], false, ""))
}

func TestLOnAStageRowOfARunningBuildOpensItLive(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	b := runningBuild()
	m.detail = &b
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1 // Build, Test, Deploy: Test

	m, cmd := send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Equal(t, "Test", msg.Stage)
	require.True(t, msg.Live)
	require.True(t, msg.Multi)
	require.Equal(t, "[INT] 12:04:31  [INFO] Running integration suite", msg.Lines[0])

	m, follow := send(m, msg)
	require.True(t, m.logs.following, "a running stage opens already following")
	require.NotNil(t, follow)
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Test")
	m.stopFollow()
}

func TestLOnAStageRowOfAFinishedBuildDoesNotFollow(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "Test", msg.Stage)
	require.False(t, msg.Live)
	m, _ = send(m, msg)
	require.False(t, m.logs.following)
}

func TestLOnAJobRowOpensThatJob(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{"Build": true}
	m.treeCursor = 2 // Build, Compile, Unit tests, Test, Deploy: Unit tests

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "PROJ-BUILD-UNIT-44", msg.JobKey)
	require.Empty(t, msg.Stage)
}

func TestEnterOnAStageRowStillTogglesIt(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.expanded["Test"])
	require.NotEqual(t, screenLogs, m.screen)
}

func TestLOnARunningBuildOpensItsCurrentStage(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{runningBuild()})
	m.focus = focusBuilds

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "Test", msg.Stage)
	require.True(t, msg.Live)
}

// TestLOnAQueuedBuildWaitsForJobs: Review Focus 5.
func TestLOnAQueuedBuildWaitsForJobs(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	queued := provider.Build{Key: "PROJ-BUILD-45", PlanKey: "PROJ-BUILD", Number: 45, State: provider.StateQueued,
		Stages: []provider.Stage{{Name: "Test", State: provider.StateQueued,
			Jobs: []provider.Job{{Key: "PROJ-BUILD-INT-45", Name: "Integration", State: provider.StateQueued}}}}}
	m.builds.setItems([]provider.Build{queued})
	m.focus = focusBuilds

	m, cmd := send(m, mkKey("l"))
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Empty(t, msg.Lines)
	m, _ = send(m, msg)
	require.True(t, m.logs.following)
	require.Contains(t, m.View(), waitingLine)
	m.stopFollow()
}

// TestLeavingALiveStageNeverStopsTheBuild: Review Focus 4.
func TestLeavingALiveStageNeverStopsTheBuild(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{runningBuild()})
	m.focus = focusBuilds
	m, cmd := send(m, mkKey("l"))
	m, _ = send(m, cmd())
	require.True(t, m.logs.following)

	m, _ = send(m, mkKey("esc"))
	require.False(t, m.logs.following)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped)
}

func TestStageLogGolden(t *testing.T) {
	m := stageModel()
	m.logs.appendLines([]string{"[INT] 12:06:58  [ERROR] ConnectionRefused: bamboo.lab.example:5432"})
	next := runningBuild()
	next.Stages[1].State = provider.StateSuccess
	next.Stages[2].State = provider.StateRunning
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.StageStart, Stage: "Deploy", Build: next}})
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-DEP-44", 1, "12:08:00  deploying")})
	m.logs.search("error")
	requireGolden(t, "logs-stage-80x24", m.View())
}
