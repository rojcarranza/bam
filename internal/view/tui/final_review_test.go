package tui

import (
	"fmt"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// F1a: leaving the log screen before a live load lands must not start a
// follow nobody can see.
func TestLeavingLogsBeforeALiveLoadLandsStartsNoFollow(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{runningBuild()})
	m.focus = focusBuilds
	m, cmd := send(m, mkKey("l"))
	require.NotNil(t, cmd)
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, cmd())
	require.False(t, m.logs.following)
	require.Nil(t, m.followCancel)
	m.stopFollow()
}

// F1b: switching build or server ends a live follow.
func TestLeavingABuildOrServerStopsTheFollow(t *testing.T) {
	m := stageModel()
	m, _ = send(m, mkKey("f"))
	if !m.logs.following {
		m, _ = send(m, mkKey("f"))
	}
	m.startFollowForTest()
	require.True(t, m.logs.following)
	m.leaveBuild()
	require.False(t, m.logs.following)
	require.Nil(t, m.followCancel)

	m = stageModel()
	m.startFollowForTest()
	require.True(t, m.logs.following)
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.False(t, m.logs.following)
	require.Nil(t, m.followCancel)
}

// F2: n with a query that matches nothing stays put.
func TestNWithAnUnmatchedQueryDoesNotJumpToAFailure(t *testing.T) {
	m := tallLogModel(60)
	m.logs.lines[10] = "an error here"
	m.logs.search("zzz")
	m.logs.vp.GotoBottom()
	m.logs.pinned = true
	line := m.logs.line
	m.logs.nextMatch(1)
	require.Equal(t, line, m.logs.line)
	require.True(t, m.logs.pinned)
}

// F3: a pinned log keeps its bottom in view across a resize.
func TestResizeKeepsAPinnedLogAtTheBottom(t *testing.T) {
	m := tallLogModel(60)
	require.True(t, m.logs.pinned)
	m.logs.resize(60, 5)
	require.True(t, m.logs.vp.AtBottom())
}

// F4: a key the viewport ignores must not re-pin the reader.
func TestAnUnboundKeyDoesNotRepinAfterASearchLanded(t *testing.T) {
	m := tallLogModel(60)
	m.screen = screenLogs
	m.logs.lines[59] = "needle"
	m.logs.search("needle")
	require.False(t, m.logs.pinned)
	require.True(t, m.logs.vp.AtBottom())
	m, _ = send(m, mkKey("x"))
	require.False(t, m.logs.pinned, fmt.Sprint("YOffset ", m.logs.vp.YOffset))
}

// startFollowForTest starts a follow on the current stage screen.
func (m *Model) startFollowForTest() {
	next, _ := m.startFollow()
	*m = next.(Model)
}
