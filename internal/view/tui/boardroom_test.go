package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P6, "Room on the board"): from 40 rows no panel is tight,
// each keeps empty rows and columns around its lines, Needs you shows all its
// lines, and a terminal from 160 columns wide uses two columns. Below 40 rows
// the board lays out as before.

// boardWith is boardModel with n needs-you lines and the given pin groups.
func boardWith(w, h, needs int, groups []app.PinGroupView) Model {
	m := boardModel(w, h)
	data := sampleBoard(goldenNow)
	data.Agents.Away = nil // the sample's offline agent is a need of its own
	data.Queue = nil
	for i := 1; i <= needs; i++ {
		key := fmt.Sprintf("PROJ-BUILD-%d", i)
		data.Queue = append(data.Queue, app.QueueRow{Position: i, QueueEntry: provider.QueueEntry{Key: key, PlanKey: "PROJ-BUILD", Number: i, Reason: "Manual build"}, Waited: 9 * time.Minute,
			Why: &provider.Why{Case: provider.WhyBusy, Count: i + 1}}) // each reason is its own, so each is a line of Needs you
	}
	if groups != nil {
		data.Groups = groups
	}
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	return m
}

func TestBoardRoomGolden120x45(t *testing.T) {
	requireGolden(t, "board-120x45", boardModel(120, 45).View())
}

func TestBoardRoomGolden200x50(t *testing.T) {
	requireGolden(t, "board-200x50", boardModel(200, 50).View())
}

// The panels share the terminal exactly: every size the board draws fully,
// from 60 by 20 to 240 by 70, fills its rows and never passes its width, and
// the layout's heights and widths add up.
func TestBoardPanelsFillTheTerminalAtEverySize(t *testing.T) {
	for h := 20; h <= 70; h++ {
		for w := 60; w <= 240; w += 10 {
			for _, needs := range []int{0, 3, 25} {
				m := boardWith(w, h, needs, nil)
				lines := strings.Split(m.View(), "\n")
				require.Len(t, lines, h, "%dx%d needs %d", w, h, needs)
				for i, l := range lines {
					require.LessOrEqual(t, ansi.StringWidth(l), w, "%dx%d line %d", w, h, i)
				}
				if h < roomyRows {
					continue
				}
				sz := boardSizes(w, h-2, 3, 4, 2)
				if sz.wide {
					require.Equal(t, w, sz.leftW+sz.rightW)
					require.Equal(t, h-2, sz.needs+sz.agents+sz.queue, "%dx%d left column", w, h)
					require.Equal(t, h-2, sz.pins, "%dx%d right column", w, h)
				} else {
					require.Equal(t, h-2, sz.needs+sz.agents+sz.queue+sz.pins, "%dx%d", w, h)
					third := (h - 2 - sz.agents) / 3
					require.GreaterOrEqual(t, sz.pins, third, "%dx%d Pinned keeps a third", w, h)
				}
				for _, v := range []int{sz.needs, sz.agents, sz.queue, sz.pins} {
					require.GreaterOrEqual(t, v, roomyMinPanel, "%dx%d every panel shows a line", w, h)
				}
				// the bottom border of the last panel sits on the row above the status bar
				require.True(t, strings.Contains(ansi.Strip(lines[h-2]), "╯"), "%dx%d bottom border row: %q", w, h, ansi.Strip(lines[h-2]))
			}
		}
	}
}

// cols cuts the runes from col a to b of every line: one panel column of a
// board drawn in two.
func cols(v string, a, b int) []string {
	var out []string
	for _, l := range strings.Split(ansi.Strip(v), "\n") {
		r := []rune(l)
		out = append(out, string(r[min(a, len(r)):min(b, len(r))]))
	}
	return out
}

// Needs you with 25 queue waits shows more than 4 on 58 rows and counts them.
func TestBoardNeedsYouShowsMoreThanFourAndCountsThem(t *testing.T) {
	for _, w := range []int{214, 120} {
		m := boardWith(w, 58, 25, nil)
		total := len(m.board.data.Needs)
		require.GreaterOrEqual(t, total, 25)
		v := ansi.Strip(m.View())
		shown := strings.Count(v, "has waited 9m in the queue")
		require.Greater(t, shown, 4, "%d columns", w)
		require.Less(t, shown, total, "it scrolls once it fills a third of the rows")
		require.Contains(t, v, fmt.Sprintf("Needs you · %d of %d", shown, total))
	}
}

func TestBoardNeedsYouCountsWhenAllFit(t *testing.T) {
	m := boardWith(120, 45, 3, nil)
	total := len(m.board.data.Needs)
	v := ansi.Strip(m.View())
	require.Contains(t, v, fmt.Sprintf("Needs you · %d", total))
	require.Equal(t, 3, strings.Count(v, "has waited 9m in the queue"))
	require.Contains(t, v, "Build and test: the last build", "every need is shown")
}

// Each panel keeps an empty row under its title and above its bottom border,
// and two empty columns between its border and its text.
func TestBoardPanelsKeepEmptyRowsAndColumns(t *testing.T) {
	for _, size := range [][3]int{{120, 45, 120}, {200, 50, 90}} {
		lines := cols(boardModel(size[0], size[1]).View(), 0, size[2])
		panels := 0
		for i, l := range lines {
			if !strings.HasPrefix(l, "╭") && !strings.HasPrefix(l, "┏") {
				continue
			}
			panels++
			require.Empty(t, strings.TrimSpace(strings.Trim(lines[i+1], "│┃")), "%v: the row under %q is empty", size, l)
			require.True(t, strings.HasPrefix(lines[i+2], "│  ") || strings.HasPrefix(lines[i+2], "┃  "), "%v: two columns before the text: %q", size, lines[i+2])
			j := i + 1
			for !strings.HasPrefix(lines[j], "╰") && !strings.HasPrefix(lines[j], "┗") {
				j++
			}
			require.Empty(t, strings.TrimSpace(strings.Trim(lines[j-1], "│┃")), "%v: the row above the bottom of %q is empty", size, l)
		}
		require.GreaterOrEqual(t, panels, 3)
	}
}

// Below 40 rows nothing changes: no empty rows, text up against its border,
// one column even where the terminal is wide.
func TestBoardBelowFortyRowsStaysTight(t *testing.T) {
	lines := strings.Split(ansi.Strip(boardModel(200, 39).View()), "\n")
	require.True(t, strings.HasPrefix(lines[1], "┏━Needs you━"), "no count in the title: %q", lines[1])
	require.True(t, strings.HasPrefix(lines[2], "┃▸ !"), "the first line follows the title at once: %q", lines[2])
	require.NotContains(t, lines[1], "Pinned", "one column below 40 rows")
	require.Equal(t, 200, ansi.StringWidth(lines[1]))
}

// From 160 columns the left column stacks Needs you, Agents and Queue at 45%
// of the width and Pinned fills the right column from top to bottom.
func TestBoardTwoColumnsFromOneHundredSixtyWide(t *testing.T) {
	one := strings.Split(ansi.Strip(boardModel(159, 50).View()), "\n")
	require.Contains(t, one[1], "Needs you")
	require.NotContains(t, one[1], "Pinned", "159 columns stay in one column")

	v := boardModel(160, 50).View()
	two := strings.Split(ansi.Strip(v), "\n")
	require.Contains(t, two[1], "Needs you")
	require.Contains(t, two[1], "Pinned", "Pinned heads the right column")
	require.Equal(t, 72, len([]rune(two[1][:strings.Index(two[1], "╭")])), "left column is 45%% of 160")
	require.Equal(t, 1, strings.Count(strings.Join(two, "\n"), "Queue"))
	require.Equal(t, 2, strings.Count(two[48], "╯")+strings.Count(two[48], "┛"), "both columns end on the same row: %q", two[48])
	require.Equal(t, 3, strings.Count(strings.Join(cols(v, 0, 72), "\n"), "╭")+strings.Count(strings.Join(cols(v, 0, 72), "\n"), "┏"), "three panels stack on the left")
}

// A group gets an empty row above it while every pin still fits; when they
// would not all fit, the rows go to the pins.
func TestBoardPinGroupsSpacedOnlyWhileEveryPinFits(t *testing.T) {
	pin := func(n int) app.PinView {
		k := fmt.Sprintf("PROJ-P%d", n)
		return app.PinView{Pin: k, PlanKey: k}
	}
	groups := []app.PinGroupView{{Name: "alpha", Pins: []app.PinView{pin(1)}}, {Name: "beta", Pins: []app.PinView{pin(2)}}}
	right := cols(boardWith(200, 50, 0, groups).View(), 90, 200)
	var text []string
	for _, l := range right {
		text = append(text, strings.TrimSpace(strings.Trim(l, "│ ")))
	}
	at := func(s string) int {
		for i, l := range text {
			if strings.HasPrefix(l, s) {
				return i
			}
		}
		return -1
	}
	require.Equal(t, at("alpha")+3, at("beta"), "alpha, its pin, an empty row, then beta: %q", text)
	require.Empty(t, text[at("beta")-1])

	// More pins than the panel holds: the empty row goes before a pin does.
	var many []app.PinView
	for i := 1; i <= 32; i++ {
		many = append(many, pin(i))
	}
	groups = []app.PinGroupView{{Name: "alpha", Pins: many[:16]}, {Name: "beta", Pins: many[16:]}}
	m := boardWith(200, 40, 0, groups)
	rows := m.board.data.Groups
	require.Len(t, rows, 2)
	right = cols(m.View(), 90, 200)
	var gap int
	for i, l := range right {
		if strings.Contains(l, "beta") {
			gap = i
		}
	}
	require.NotEmpty(t, strings.TrimSpace(strings.Trim(right[gap-1], "│ ")), "no empty row above beta: pins would be cut")
	require.Contains(t, strings.Join(right, "\n"), "PROJ-P32 ", "every pin shows")
}

// A long plan name is cut to the Pinned panel's own width, so the rest of
// its row (the results and the usual time) stays in view in every layout,
// including the narrower right column of a wide terminal.
func TestBoardPinnedNameFitsItsPanelInEveryLayout(t *testing.T) {
	long := strings.Repeat("Provision a lab cluster ", 6)
	groups := []app.PinGroupView{{Name: "integration", Pins: []app.PinView{
		{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD", Name: long, Last: 192 * time.Second, Usual: 3 * time.Minute},
	}}}
	for _, size := range [][2]int{{120, 30}, {120, 45}, {200, 50}} {
		v := ansi.Strip(boardWith(size[0], size[1], 0, groups).View())
		require.Contains(t, v, "Provision a lab", "%dx%d", size[0], size[1])
		require.Contains(t, v, "usual 3m", "%dx%d: the row's end is not cut off:\n%s", size[0], size[1], v)
	}
}
