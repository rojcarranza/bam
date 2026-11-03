package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-22, piece 8 of SCR-54): a click selects a row and the wheel
// scrolls the panel under the pointer. The tests aim at cells found in the
// rendered screen, so a drift between drawing and hit-testing fails them.

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

func wheel(x, y int, up bool) tea.MouseMsg {
	b := tea.MouseButtonWheelDown
	if up {
		b = tea.MouseButtonWheelUp
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: b}
}

// cellOf is where text first appears on the rendered screen.
func cellOf(t *testing.T, m Model, text string) (x, y int) {
	t.Helper()
	for y, line := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return len([]rune(line[:i])), y
		}
	}
	require.Failf(t, "not on screen", "%q in:\n%s", text, ansi.Strip(m.View()))
	return 0, 0
}

func TestClickSelectsTheHomeRowUnderThePointer(t *testing.T) {
	m := homeModel()
	require.Equal(t, "OPS-NIGHTLY", selectedKey(m))
	x, y := cellOf(t, m, "PROJ-PROV")
	m, _ = send(m, click(x, y))
	require.Equal(t, "PROJ-PROV", selectedKey(m))
	require.Equal(t, screenHome, m.screen, "a click selects; it does not open")
}

func TestClickOnTheHomeHeaderOrBelowTheRowsSelectsNothing(t *testing.T) {
	m := homeModel()
	x, y := cellOf(t, m, "PROJ-PROV")
	m, _ = send(m, click(x, y))
	_, hy := cellOf(t, m, "KEY")
	m, _ = send(m, click(x, hy))
	require.Equal(t, "PROJ-PROV", selectedKey(m), "the column header is not a row")
	m, _ = send(m, click(x, m.height-3))
	require.Equal(t, "PROJ-PROV", selectedKey(m), "blank space under the last row is not a row")
}

func TestWheelScrollsTheHomeTable(t *testing.T) {
	m := homeModel()
	x, y := cellOf(t, m, "PROJ-PROV")
	m, _ = send(m, wheel(x, y, false))
	require.Equal(t, "PROJ-PROV", selectedKey(m), "three notches down from the first of four rows stops at the last")
	m, _ = send(m, wheel(x, y, true))
	require.Equal(t, "OPS-NIGHTLY", selectedKey(m))
}

func TestClickAfterTheTableScrolledHitsTheDrawnRow(t *testing.T) {
	m := homeModel()
	var plans []provider.Plan
	for i := 0; i < 40; i++ {
		plans = append(plans, provider.Plan{Key: "PROJ-P" + strconv.Itoa(100+i), Name: "n", ProjectKey: "PROJ"})
	}
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: plans})
	m.plans.cursor = 35 // the window has scrolled; row 0 is no longer on screen
	x, y := cellOf(t, m, "PROJ-P130")
	m, _ = send(m, click(x, y))
	require.Equal(t, "PROJ-P130", selectedKey(m))
}

func TestMouseIsIgnoredWhileAnOverlayOrPromptIsOpen(t *testing.T) {
	m := homeModel()
	x, y := cellOf(t, m, "PROJ-PROV")
	over := m
	over.overlay = overlayError
	over, _ = send(over, click(x, y))
	require.Equal(t, "OPS-NIGHTLY", selectedKey(over))
	typing := m
	typing.inputFor = inputFilter
	typing, _ = send(typing, wheel(x, y, false))
	require.Equal(t, "OPS-NIGHTLY", selectedKey(typing))
}

func TestClickOnABuildRowSelectsItAndOpensItAfterTheRest(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	x, y := cellOf(t, m, "#43")
	m.focus = focusStages
	m, cmd := send(m, click(x, y))
	require.Equal(t, focusBuilds, m.focus, "the click focuses the panel it landed in")
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-43", b.Key)
	var rested bool
	for _, msg := range runBatch(cmd) {
		if r, ok := msg.(planRestMsg); ok {
			rested = true
			m, _ = send(m, r)
		}
	}
	require.True(t, rested, "moving the Builds cursor schedules its build, as j does")
	require.Equal(t, "PROJ-BUILD-43", m.detail.Key)
}

// planWithStages is planOnBuilds with build 44's stages read in.
func planWithStages(t *testing.T) Model {
	t.Helper()
	m := planOnBuilds(t)
	b := sampleBuild()
	m.detail = &b
	m.plan.stagesLoading = false
	return m
}

func TestClickOnAStageRowMovesTheStagesCursor(t *testing.T) {
	m := planWithStages(t)
	x, y := cellOf(t, m, "Deploy")
	m, _ = send(m, click(x, y))
	require.Equal(t, focusStages, m.focus)
	row, ok := m.selectedTreeRow()
	require.True(t, ok)
	require.Equal(t, "Deploy", row.Name)
}

func TestClickOnATreeRowMovesTheTreeCursor(t *testing.T) {
	m := planWithTree(t)
	rows := m.treePreviewRows()
	require.NotEmpty(t, rows)
	m.plan.treeCursor = len(rows) - 1
	x, y := cellOf(t, m, rows[0].PlanKey)
	m, _ = send(m, click(x, y))
	require.Equal(t, focusTree, m.focus)
	require.Equal(t, 0, m.plan.treeCursor)
}

func TestWheelScrollsThePanelUnderThePointerNotTheFocusedOne(t *testing.T) {
	m := planWithStages(t)
	m.focus = focusBuilds
	x, y := cellOf(t, m, "Deploy")
	before, _ := m.builds.selected()
	m, _ = send(m, wheel(x, y, false))
	require.Equal(t, focusStages, m.focus)
	require.Equal(t, 2, m.treeCursor, "the Stages cursor moved, clamped to its three rows")
	after, _ := m.builds.selected()
	require.Equal(t, before.Key, after.Key, "the Builds cursor stayed")
}

func TestClickOnAPanelBorderOnlyFocusesIt(t *testing.T) {
	m := planWithStages(t)
	x, y := cellOf(t, m, "Stages")
	m, _ = send(m, click(x, y))
	require.Equal(t, focusStages, m.focus)
	require.Equal(t, 0, m.treeCursor)
}

func TestWheelScrollsTheLogViewport(t *testing.T) {
	m := testModel()
	m.screen = screenLogs
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	m.logs.setLines(m.width, m.logHeight(), lines)
	m.logs.vp.GotoBottom()
	m.logs.pinned = true
	bottom := m.logs.vp.YOffset
	m, _ = send(m, wheel(10, 5, true))
	require.Less(t, m.logs.vp.YOffset, bottom)
	require.False(t, m.logs.pinned, "scrolling up unpins the follow")
	m, _ = send(m, wheel(10, 5, false))
	m, _ = send(m, wheel(10, 5, false))
	require.Equal(t, bottom, m.logs.vp.YOffset)
	require.True(t, m.logs.pinned)
}

func TestClickInTheCompactPlanScreenHitsTheOnlyPanel(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	m.width, m.height = 50, 18
	require.True(t, m.planCompact())
	x, y := cellOf(t, m, "#42")
	m, _ = send(m, click(x, y))
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-42", b.Key)
}

// With wrapping on, the viewport holds wrapped rows, so a wheel notch moves
// rows of the wrapped text, and the pin follows where the reader ends up.
func TestWheelScrollsWrappedLogsByRows(t *testing.T) {
	m := testModel()
	m.screen = screenLogs
	long := strings.Repeat("abcdefghij ", 20) // 220 columns: three rows at width 80
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, strconv.Itoa(i)+" "+long)
	}
	m.logs.setLines(m.width, m.logHeight(), lines)
	m.logs.toggleWrap()
	require.True(t, m.logs.wrap)
	m.logs.vp.GotoBottom()
	m.logs.pinned = true
	bottom := m.logs.vp.YOffset
	require.Greater(t, bottom, len(lines), "the wrapped log is taller than its line count")

	m, _ = send(m, wheel(10, 5, true))
	require.Equal(t, bottom-3, m.logs.vp.YOffset, "a notch is three wrapped rows")
	require.False(t, m.logs.pinned)
	require.Equal(t, m.logs.lineAt(m.logs.vp.YOffset), m.logs.lineAt(bottom-3))
	m, _ = send(m, wheel(10, 5, false))
	require.Equal(t, bottom, m.logs.vp.YOffset)
	require.True(t, m.logs.pinned)
}

// The stats overlay (and help) scroll with the wheel while the pointer is
// over the box; a wheel beside it, or a click, leaves the screen behind alone.
func TestWheelScrollsTheStatsOverlayUnderThePointer(t *testing.T) {
	m := homeModel()
	var body []string
	for i := 0; i < 80; i++ {
		body = append(body, "failing job "+strconv.Itoa(i))
	}
	m = m.openInfo("Stats · PROJ-BUILD", strings.Join(body, "\n"))
	x, y := cellOf(t, m, "failing job 0")
	require.Equal(t, 0, m.infoPane.vp.vp.YOffset)

	m, _ = send(m, wheel(x, y, false))
	require.Equal(t, 3, m.infoPane.vp.vp.YOffset)
	m, _ = send(m, wheel(x, y, true))
	require.Equal(t, 0, m.infoPane.vp.vp.YOffset)

	m, _ = send(m, wheel(0, 0, false))
	require.Equal(t, 0, m.infoPane.vp.vp.YOffset, "a wheel outside the box does nothing")
	m, _ = send(m, click(x, y))
	require.Equal(t, "OPS-NIGHTLY", selectedKey(m), "a click does not reach the table behind")
	require.Equal(t, overlayInfo, m.overlay)
}
