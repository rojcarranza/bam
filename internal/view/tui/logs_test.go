package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/r0jjames/bam-cli/internal/provider"
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
		URL: labOrigin + "/browse/PROJ-BUILD-INT-44", Lines: logLines()})
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

// TestDrainFollowDeliversOneChunkPerCall, the same pattern the watch uses.
func TestDrainFollowDeliversOneChunkPerCall(t *testing.T) {
	lines := make(chan []string, 2)
	done := make(chan error, 1)
	lines <- []string{"12:07:02  still running"}
	lines <- []string{"12:07:05  done"}
	close(lines)
	done <- nil

	first := drainFollowCmd(lines, done, 0)().(logChunkMsg)
	require.Equal(t, []string{"12:07:02  still running"}, first.Lines)

	second := drainFollowCmd(lines, done, 0)().(logChunkMsg)
	require.Equal(t, []string{"12:07:05  done"}, second.Lines)

	end := drainFollowCmd(lines, done, 0)()
	require.IsType(t, followEndedMsg{}, end)
	require.NoError(t, end.(followEndedMsg).Err)
}

func TestLogChunkAppendsAndStaysPinnedToTheBottom(t *testing.T) {
	m := logModel()
	m.logs.vp.GotoBottom()
	before := len(m.logs.lines)
	m, _ = send(m, logChunkMsg{Lines: []string{"12:07:05  done"}})
	require.Len(t, m.logs.lines, before+1)
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

// TestFollowingAJobThatIsNotInTheBuildDoesNothing.
func TestFollowingAJobThatIsNotInTheBuildDoesNothing(t *testing.T) {
	m := logModel()
	m.logs.jobKey = "PROJ-BUILD-GONE-44"
	m, cmd := send(m, mkKey("f"))
	require.False(t, m.logs.following)
	require.Nil(t, cmd)
}

// TestFollowStartsFromTheProviderOffsetNotTheLineCount. app.JobLog.Next is
// the provider's offset contract; the rendered line count is not, and it is
// further wrong when several jobs are concatenated with separators.
func TestFollowStartsFromTheProviderOffsetNotTheLineCount(t *testing.T) {
	m := logModel()
	m.logs.offset = 41 // what the server said, not len(lines)
	require.NotEqual(t, len(m.logs.lines), m.logs.offset)

	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	require.Equal(t, 41, m.followFrom, "FollowLog must resume at the provider's offset")
	m.stopFollow()
}

// TestFollowIsRefusedWhileSeveralJobsAreShown: the screen is a concatenation,
// so there is no single job to resume, and appending to it would interleave
// one job's output into another's.
func TestFollowIsRefusedWhileSeveralJobsAreShown(t *testing.T) {
	m := logModel()
	m.logs.multi = true
	m, cmd := send(m, mkKey("f"))
	require.False(t, m.logs.following)
	require.Nil(t, cmd)
	require.Contains(t, m.status, "one job")
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
