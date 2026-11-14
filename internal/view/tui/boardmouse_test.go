package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P7, the captain's answer "one plain click on the underlined
// name or number", issue 204): on the live board a click on a pinned
// pipeline's name opens its Bamboo page and a click on its build number opens
// that build's page; any other click only selects, and the wheel scrolls the
// panel under the pointer. The tests aim at cells found in the rendered
// screen, so a drift between drawing and hit-testing fails them.

var boardSizes2 = [][2]int{{120, 45}, {200, 50}}

// recordOpener makes the board's opener a stand-in that records addresses.
func recordOpener(m Model) (Model, *[]string) {
	opened := &[]string{}
	m.deps.Open = func(u string) error { *opened = append(*opened, u); return nil }
	return m, opened
}

// clickOpening sends a click and runs the command it returns, as the program
// would, so an address handed to the opener lands in the recording.
func clickOpening(m Model, x, y int) Model {
	m, cmd := send(m, click(x, y))
	if cmd != nil {
		cmd()
	}
	return m
}

// cellAfter is the cell of the first occurrence of text on the line that
// holds anchor, so a number is found on the row it belongs to.
func cellAfter(t *testing.T, m Model, anchor, text string) (x, y int) {
	t.Helper()
	for y, line := range strings.Split(plain(m), "\n") {
		if strings.Contains(line, anchor) {
			if i := strings.Index(line, text); i >= 0 {
				return len([]rune(line[:i])), y
			}
		}
	}
	require.Failf(t, "not on screen", "%q on a line with %q in:\n%s", text, anchor, plain(m))
	return 0, 0
}

func TestBoardClickOnAPinsNameOrNumberOpensItsPage(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		base := m.svc.P.URL("")

		x, y := cellAfter(t, m, "usual", "Top pipeline")
		m = clickOpening(m, x+2, y)
		require.Equal(t, []string{base + "PROJ-TOP"}, *opened, "%v: the name opens the plan", size)
		require.Equal(t, boardPins, m.board.panel)
		require.Equal(t, 0, m.board.cursor[boardPins], "the click also selects the row")

		*opened = nil
		x, y = cellAfter(t, m, "Top pipeline", "#9")
		m = clickOpening(m, x+1, y)
		require.Equal(t, []string{base + "PROJ-TOP-9"}, *opened, "%v: the number opens that build", size)
	}
}

func TestBoardClickOnAChildPipelineOpensItsPlanOrBuild(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		base := m.svc.P.URL("")

		x, y := cellAfter(t, m, "PROJ-DOCS-3", "PROJ-DOCS")
		m = clickOpening(m, x, y)
		require.Equal(t, []string{base + "PROJ-DOCS"}, *opened, "%v: the plan key opens the plan", size)
		require.Equal(t, 3, m.board.cursor[boardPins], "the child line is selected: the pin, then the two children and the grandchild above it")

		*opened = nil
		x, y = cellAfter(t, m, "PROJ-DOCS-3", "-3")
		m = clickOpening(m, x+1, y)
		require.Equal(t, []string{base + "PROJ-DOCS-3"}, *opened, "%v: the number opens the build", size)

		// A child on a plan branch opens that branch's plan, named in the
		// parentheses with its branch.
		*opened = nil
		x, y = cellAfter(t, m, "PROJ-K8S0-4", "Kubernetes")
		m = clickOpening(m, x+1, y)
		require.Equal(t, []string{base + "PROJ-K8S0"}, *opened, "%v", size)
	}
}

func TestBoardClickBesideTheLinksOnlySelects(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		// The "last" column of the second pin's row, and the stage line of a child.
		x, y := cellAfter(t, m, "Build and test", "last")
		m = clickOpening(m, x, y)
		require.Empty(t, *opened, "%v", size)
		require.Equal(t, boardPins, m.board.panel)
		require.Positive(t, m.board.cursor[boardPins], "the pin's row is selected")

		x, y = cellOf(t, m, "stage Publish")
		before := m.board.cursor[boardPins]
		m = clickOpening(m, x, y)
		require.Empty(t, *opened, "a stage line opens nothing")
		require.Equal(t, before, m.board.cursor[boardPins], "and a stage line is not selectable")
	}
}

func TestBoardClickOnABorderOrBlankSpaceOpensNothing(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		_, y := cellAfter(t, m, "usual", "Top pipeline")
		pins := m.boardBoxes()[3]
		m = clickOpening(m, pins.x, y)                 // the left border
		m = clickOpening(m, pins.x+pins.w-1, y)        // the right border
		m = clickOpening(m, pins.x+2, pins.y+1)        // the title border
		m = clickOpening(m, pins.x+3, pins.y+pins.h)   // the bottom border, last row
		m = clickOpening(m, pins.x+pins.w-3, pins.y+3) // empty space right of a line
		m = clickOpening(m, 0, 0)                      // the header
		m = clickOpening(m, m.width-1, m.height-1)     // the status bar
		require.Empty(t, *opened, "%v", size)
	}
}

func TestBoardClickOnTheTightBoardOpensToo(t *testing.T) {
	m, opened := recordOpener(treeBoardModel(120, 30, nil))
	x, y := cellAfter(t, m, "usual", "Top pipeline")
	m = clickOpening(m, x, y)
	require.Equal(t, []string{m.svc.P.URL("PROJ-TOP")}, *opened)
}

func TestBoardClickUnderTheOpenHelpOpensNothing(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		x, y := cellAfter(t, m, "usual", "Top pipeline")
		m, _ = send(m, mkKey("?"))
		require.Equal(t, overlayHelp, m.overlay)
		m = clickOpening(m, x, y)
		require.Empty(t, *opened, "%v: the help owns the mouse", size)
		require.Equal(t, overlayHelp, m.overlay)
	}
}

func TestBoardClickSelectsInEveryPanelAndWheelScrollsIt(t *testing.T) {
	for _, size := range boardSizes2 {
		m, opened := recordOpener(treeBoardModel(size[0], size[1], nil))
		rows := len(m.board.data.Queue)
		require.Greater(t, rows, 1, "the sample queue has rows to move over")
		qx, qy := cellOf(t, m, m.board.data.Queue[1].Key)
		m = clickOpening(m, qx, qy)
		require.Equal(t, boardQueue, m.board.panel)
		require.Equal(t, 1, m.board.cursor[boardQueue])

		m, _ = send(m, wheel(qx, qy, true))
		require.Equal(t, 0, m.board.cursor[boardQueue], "wheel up moves the queue's cursor")
		m, _ = send(m, wheel(qx, qy, false))
		require.Equal(t, min(wheelRows, rows-1), m.board.cursor[boardQueue])

		nx, ny := cellOf(t, m, "Needs you")
		m, _ = send(m, wheel(nx+3, ny+3, false))
		require.Equal(t, boardNeeds, m.board.panel, "the wheel focuses the panel under it")
		require.Equal(t, min(wheelRows, len(m.board.data.Needs)-1), m.board.cursor[boardNeeds])

		px, py := cellAfter(t, m, "usual", "Top pipeline")
		m, _ = send(m, wheel(px, py, false))
		require.Equal(t, boardPins, m.board.panel)
		require.Equal(t, min(wheelRows, len(m.board.pinSels())-1), m.board.cursor[boardPins])
		require.Empty(t, *opened, "the wheel and a queue click open nothing")
	}
}

func TestBoardPinNamesAndNumbersAreDrawnUnderlined(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	for _, size := range boardSizes2 {
		raw := treeBoardModel(size[0], size[1], nil).View()
		for _, want := range []string{"Top pipeline", "#9", "Build and test", "PROJ-DOCS", "3", "(Kubernetes · feature-x)"} {
			require.NotEqual(t, want, linkText(want), "the test needs colour on")
			require.Contains(t, raw, linkText(want), "%v: %q is underlined", size, want)
		}
		require.NotContains(t, raw, "\x1b]8;", "bam writes no terminal link into the board; it opens the page itself")
	}
}
