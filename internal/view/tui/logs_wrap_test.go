package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// A deployment line far wider than the 80-column terminal, as a real build
// log has: a long command line with its tail at the end.
const longLine = "12:08:02  [INFO] mvn -B -ntp verify -Dmaven.repo.local=/var/cache/bamboo/m2 " +
	"-Dsurefire.rerunFailingTestsCount=2 -pl services/billing,services/ledger TAIL-MARKER"

// wrapLines is the log with the long line second, so wrapping shifts the rows
// of every line after it.
func wrapLines() []string {
	l := logLines()
	return append([]string{l[0], longLine}, l[1:]...)
}

func wrapModel() Model {
	m := logModel()
	m.logs.setLines(m.width, m.logHeight(), wrapLines())
	m.logs.vp.GotoTop()
	return m
}

// BAM-18: w wraps long log lines instead of cutting them.
func TestWWrapsLongLogLines(t *testing.T) {
	m := wrapModel()
	require.NotContains(t, ansi.Strip(m.View()), "TAIL-MARKER", "unwrapped, the line is cut")

	m, _ = send(m, mkKey("w"))
	require.True(t, m.logs.wrap)
	v := ansi.Strip(m.View())
	require.Contains(t, v, "TAIL-MARKER", "wrapped, the end of the line is on screen")
	for _, row := range strings.Split(v, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(row), 80)
	}

	m, _ = send(m, mkKey("w"))
	require.False(t, m.logs.wrap)
	require.NotContains(t, ansi.Strip(m.View()), "TAIL-MARKER")
}

// Search and next-failure address lines, so wrapping must not move them to
// the wrong viewport row.
func TestWrappedSearchLandsOnTheMatchingLine(t *testing.T) {
	m := wrapModel()
	m.logs.vp.Height = 2
	m, _ = send(m, mkKey("w"))
	m.logs.search("exit code")
	require.Equal(t, 4, m.logs.line)
	require.Greater(t, m.logs.starts[4], 4, "the long line above takes more than one row")
	// The viewport clamps its offset near the end, so the match is checked
	// as being on screen, not as being the top row.
	top := m.logs.vp.YOffset
	require.True(t, m.logs.starts[4] >= top && m.logs.starts[4] < top+m.logs.vp.Height)
	require.Contains(t, ansi.Strip(m.View()), "exit code 1")
}

// Toggling must keep the reader on the same line.
func TestWrapKeepsTheTopLine(t *testing.T) {
	m := wrapModel()
	m.logs.vp.Height = 2
	m.logs.pinned = false
	m.logs.vp.SetYOffset(3)
	m, _ = send(m, mkKey("w"))
	require.Equal(t, 3, m.logs.lineAt(m.logs.vp.YOffset))
	m, _ = send(m, mkKey("w"))
	require.Equal(t, 3, m.logs.vp.YOffset)
}

// BAM-18: E opens the whole log, not only the visible window, in the pager.
func TestEPipesTheWholeLogToThePager(t *testing.T) {
	var gotCmd, gotText string
	m := wrapModel()
	m.deps.Pager = "less -FRX"
	m.deps.RunPager = func(cmd string, r io.Reader) error {
		b, err := io.ReadAll(r)
		gotCmd, gotText = cmd, string(b)
		return err
	}
	m.logs.vp.Height = 2 // the window shows two lines; the pager gets all of it

	m, cmd := send(m, mkKey("E"))
	require.NotNil(t, cmd)
	require.Nil(t, m.err)

	exec := &pagerExec{run: m.deps.RunPager, cmd: m.deps.Pager, text: m.logs.fullText()}
	require.NoError(t, exec.Run())
	require.Equal(t, "less -FRX", gotCmd)
	require.Equal(t, strings.Join(wrapLines(), "\n")+"\n", gotText)
}

func TestEWithoutAPagerSaysSo(t *testing.T) {
	m := wrapModel()
	m, cmd := send(m, mkKey("E"))
	require.Nil(t, cmd)
	require.Error(t, m.err)
	require.Contains(t, m.View(), "no pager")
}

func TestEWithAnEmptyLogDoesNothing(t *testing.T) {
	m := wrapModel()
	m.deps.Pager, m.deps.RunPager = "less", func(string, io.Reader) error { return nil }
	m.logs.lines = nil
	_, cmd := send(m, mkKey("E"))
	require.Nil(t, cmd)
}

func TestFailedPagerIsReported(t *testing.T) {
	m := wrapModel()
	m, _ = send(m, pagerDoneMsg{Err: io.ErrClosedPipe})
	require.Error(t, m.err)
}
