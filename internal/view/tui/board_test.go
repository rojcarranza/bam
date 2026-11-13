package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P2): the board refreshes by itself every 15 seconds (pins
// in full every 60), never stops for idleness, says how old its data is and
// marks it stale after a failed read; its keys open the existing screens and
// change nothing on the server; P writes only bam's settings; the bell rings
// once when something new needs you.

// instantBoardTick makes the board's beat fire at once, as instantHomeTick
// does for Home. tea.Tick reads the duration when the command is built.
func instantBoardTick(t *testing.T) {
	t.Helper()
	old := boardEvery
	boardEvery = 0
	t.Cleanup(func() { boardEvery = old })
}

func sampleBoard(at time.Time) app.Board {
	failed := provider.Build{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, State: provider.StateFailed, Duration: 192 * time.Second}
	ok := func(n int) provider.Build {
		return provider.Build{Key: "PROJ-PROV-" + string(rune('0'+n)), PlanKey: "PROJ-PROV", Number: n, State: provider.StateSuccess, Duration: 4 * time.Minute}
	}
	running := provider.Build{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, State: provider.StateRunning, StartedAt: at.Add(-3 * time.Minute)}
	b := app.Board{
		At: at, Server: "lab", Version: "12.1.8",
		Agents: &app.AgentSummary{Total: 4, Online: 2, Busy: 1, Offline: 1, Disabled: 1, Away: []provider.Agent{
			{ID: 2, Name: "linux-2", Type: "REMOTE", Enabled: true},
			{ID: 3, Name: "linux-3", Type: "REMOTE", Online: true},
		}},
		QueueOK: true,
		Queue: []app.QueueRow{
			{Position: 1, QueueEntry: provider.QueueEntry{Key: "PROJ-BUILD-500", PlanKey: "PROJ-BUILD", Number: 500, Reason: "Manual build"}, Waited: 7 * time.Minute},
			{Position: 2, QueueEntry: provider.QueueEntry{Key: "OPS-NIGHTLY-3", PlanKey: "OPS-NIGHTLY", Number: 3, Reason: "Scheduled"}, Waited: 20 * time.Second, AtLeast: true},
		},
		Groups: []app.PinGroupView{
			{Name: "integration", Pins: []app.PinView{
				{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD", Results: []provider.Build{failed, ok(3)}, Last: 192 * time.Second, Usual: 3 * time.Minute},
				{Pin: "PROJ-PROV", PlanKey: "PROJ-PROV", Results: []provider.Build{ok(8), ok(7)}, Current: &running, Last: 4 * time.Minute, Usual: 4 * time.Minute},
			}},
		},
	}
	b.Needs = app.NeedsYou(b, at)
	return b
}

// boardModel is a model on the board with sample data delivered the way a
// read delivers it, on a clock that stands still.
func boardModel(w, h int) Model {
	m := testModel()
	m.width, m.height = w, h
	m.screen = screenBoard
	m.svc = testService()
	m.server = "lab"
	m.info = provider.ServerInfo{Version: "12.1.8"}
	m.user = provider.User{Name: "jdoe"}
	m.now = func() time.Time { return goldenNow }
	m.board.loading, m.board.loadGen = true, 1
	m, _ = send(m, boardLoadedMsg{Gen: 1, Full: true, Board: sampleBoard(goldenNow)})
	return m
}

func TestBoardGolden80x24(t *testing.T) { requireGolden(t, "board-80x24", boardModel(80, 24).View()) }
func TestBoardGolden120x30(t *testing.T) {
	requireGolden(t, "board-120x30", boardModel(120, 30).View())
}

// Under 20 rows or 60 columns the board shows its header and Needs you only.
func TestBoardGolden60x16(t *testing.T) { requireGolden(t, "board-60x16", boardModel(60, 16).View()) }

func TestBoardSmallTerminalHidesEverythingButNeedsYou(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {59, 30}, {120, 19}} {
		v := boardModel(size[0], size[1]).View()
		require.Contains(t, v, "Needs you", "%v", size)
		require.NotContains(t, v, "Queue", "%v", size)
		require.NotContains(t, v, "Pinned", "%v", size)
	}
	v := boardModel(60, 20).View()
	require.Contains(t, v, "Pinned", "20 rows by 60 columns is the smallest full board")
}

// At the smallest full board, a full Needs you list and several away agents
// still leave room for the whole Pinned panel, bottom border included.
func TestBoardGolden60x20FullNeeds(t *testing.T) {
	m := boardModel(60, 20)
	data := sampleBoard(goldenNow)
	data.Agents.Away = append(data.Agents.Away, provider.Agent{ID: 4, Name: "linux-4", Type: "REMOTE", Enabled: true})
	data.Agents.Offline++
	data.Needs = app.NeedsYou(data, goldenNow)
	require.GreaterOrEqual(t, len(data.Needs), 4)
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	require.Len(t, lines, 20)
	require.True(t, strings.HasPrefix(lines[18], "╰"), "the Pinned panel ends in its bottom border: %q", lines[18])
	requireGolden(t, "board-60x20", m.View())
}

func TestBoardHeaderSaysHowOldTheDataIs(t *testing.T) {
	m := boardModel(100, 30)
	require.Contains(t, m.View(), "updated 0s ago")
	now := goldenNow.Add(40 * time.Second)
	m.now = func() time.Time { return now }
	require.Contains(t, m.View(), "updated 40s ago")
}

// runBoardCmd runs a command batch and returns the board reads in it.
func boardReads(cmd tea.Cmd) []boardLoadedMsg {
	var out []boardLoadedMsg
	for _, msg := range runBatch(cmd) {
		if l, ok := msg.(boardLoadedMsg); ok {
			out = append(out, l)
		}
	}
	return out
}

// The beat reads agents, the queue and running builds every 15 seconds and
// everything, pins included, once a minute on the model's clock.
func TestBoardBeatReadsQuicklyThenInFullEveryMinute(t *testing.T) {
	instantBoardTick(t)
	m := boardModel(100, 30)
	m.board.fullAt = goldenNow
	m.board.tickGen = 1
	at := func(d time.Duration) { now := goldenNow.Add(d); m.now = func() time.Time { return now } }

	for _, d := range []time.Duration{15 * time.Second, 30 * time.Second, 45 * time.Second} {
		at(d)
		var cmd tea.Cmd
		m, cmd = send(m, boardTickMsg{Gen: 1})
		reads := boardReads(cmd)
		require.Len(t, reads, 1, "one read per beat at %s", d)
		require.False(t, reads[0].Full, "a quick read at %s", d)
		m, _ = send(m, reads[0])
	}
	at(60 * time.Second)
	m, cmd := send(m, boardTickMsg{Gen: 1})
	reads := boardReads(cmd)
	require.Len(t, reads, 1)
	require.True(t, reads[0].Full, "the minute's beat reads the pins in full")
	m, _ = send(m, reads[0])
	require.Equal(t, goldenNow.Add(60*time.Second), m.board.fullAt)

	at(75 * time.Second)
	_, cmd = send(m, boardTickMsg{Gen: 1})
	require.False(t, boardReads(cmd)[0].Full, "the minute starts again from the full read")
}

// The board keeps refreshing with no key pressed; Home stops after ten idle
// minutes, the board never does.
func TestBoardNeverStopsForIdleness(t *testing.T) {
	instantBoardTick(t)
	m := boardModel(100, 30)
	m.board.tickGen = 1
	m.home.lastKey = goldenNow.Add(-3 * time.Hour)
	now := goldenNow.Add(3 * time.Hour)
	m.now = func() time.Time { return now }
	_, cmd := send(m, boardTickMsg{Gen: 1})
	var ticks, reads int
	for _, msg := range runBatch(cmd) { // a tick command can be run once
		switch msg.(type) {
		case boardTickMsg:
			ticks++
		case boardLoadedMsg:
			reads++
		}
	}
	require.Equal(t, 1, reads, "still reading after three idle hours")
	require.Equal(t, 1, ticks, "and the next beat is scheduled")
}

func TestBoardBeatEndsWhenTheBoardIsLeftOrReplaced(t *testing.T) {
	instantBoardTick(t)
	m := boardModel(100, 30)
	m.board.tickGen = 2
	_, cmd := send(m, boardTickMsg{Gen: 1})
	require.Nil(t, cmd, "a beat of an older loop is dropped, so two loops never run")
	m.screen = screenHome
	_, cmd = send(m, boardTickMsg{Gen: 2})
	require.Nil(t, cmd, "a beat that arrives off the board ends its loop")
}

func TestBoardBeatWaitsForTheReadInFlight(t *testing.T) {
	instantBoardTick(t)
	m := boardModel(100, 30)
	m.board.tickGen, m.board.loading = 1, true
	_, cmd := send(m, boardTickMsg{Gen: 1})
	require.Empty(t, boardReads(cmd), "no second read while one is out")
}

// A read that got nothing marks the data stale and keeps it on screen; the
// next good read clears the mark.
func TestBoardMarksStaleAfterAFailedRead(t *testing.T) {
	m := boardModel(100, 30)
	require.NotContains(t, m.View(), "stale")

	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: app.Board{AgentsNote: "no", QueueNote: "no"}})
	require.True(t, m.board.stale)
	require.Contains(t, m.View(), "stale")
	require.Contains(t, m.View(), "PROJ-BUILD-500", "what was read before stays on screen")

	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: sampleBoard(goldenNow)})
	require.False(t, m.board.stale)
	require.NotContains(t, m.View(), "stale")

	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Err: errors.New("interrupted")})
	require.True(t, m.board.stale)
}

// A user who may not list agents, on a server that refuses the queue, still
// gets the pins: a read counts as nothing only when every part of it failed.
func TestBoardShowsPinsWhenAgentsAndQueueAreRefused(t *testing.T) {
	m := testModel()
	m.width, m.height = 100, 30
	m.screen = screenBoard
	m.now = func() time.Time { return goldenNow }
	m.board.loading, m.board.loadGen = true, 1
	data := sampleBoard(goldenNow)
	data.Agents, data.AgentsNote = nil, "the agent list needs admin rights"
	data.Queue, data.QueueOK, data.QueueNote = nil, false, "the queue was refused"
	m, _ = send(m, boardLoadedMsg{Gen: 1, Full: true, Board: data})
	require.False(t, m.board.stale)
	require.Contains(t, m.View(), "PROJ-BUILD-44")
	require.NotContains(t, m.View(), "stale")

	data.Groups = []app.PinGroupView{{Name: "integration", Pins: []app.PinView{{Pin: "PROJ-BUILD", Err: errors.New("refused")}}}}
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: data})
	require.True(t, m.board.stale, "agents, queue and every pin failed")
	require.Contains(t, m.View(), "PROJ-BUILD-44", "what was read before stays on screen")
}

func TestBoardDropsAReadOfAnOlderRequest(t *testing.T) {
	m := boardModel(100, 30)
	m.board.loadGen = 5
	b := sampleBoard(goldenNow)
	b.Queue = nil
	m, _ = send(m, boardLoadedMsg{Gen: 4, Board: b})
	require.Len(t, m.board.data.Queue, 2)
}

// A refresh through the real use case: a refused agent list is a line on the
// board, and the rest still shows.
func TestBoardReadsTheServerThroughTheUseCase(t *testing.T) {
	m := boardModel(100, 30)
	f := m.svc.P.(*fake.Provider)
	f.AgentsErr = errors.New("403")
	f.QueueEntries = []provider.QueueEntry{{Key: "PROJ-BUILD-500", PlanKey: "PROJ-BUILD", Number: 500}}
	m.board.loadGen = 0
	cmd := m.loadBoard(true)
	msg := cmd().(boardLoadedMsg)
	m, _ = send(m, msg)
	require.Nil(t, m.board.data.Agents)
	require.Contains(t, m.View(), "could not be read")
	require.False(t, m.board.stale, "the queue was read, so the data is fresh")
	require.Len(t, m.board.data.Queue, 1)
}

// A build whose record gave no time it began waiting counts from when the
// board first saw it, and keeps counting across reads.
func TestBoardCountsAnUnknownWaitFromFirstSight(t *testing.T) {
	m := boardModel(100, 30)
	now := goldenNow.Add(2 * time.Minute)
	m.now = func() time.Time { return now }
	m.board.loadGen++
	b := sampleBoard(now)
	b.Queue[1].Waited = 0
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: b})
	require.Equal(t, 2*time.Minute, m.board.data.Queue[1].Waited, "first seen at the first read, two minutes ago")
	require.Equal(t, 7*time.Minute, m.board.data.Queue[0].Waited, "a known wait is the record's")
}

// ---- bell ----

func TestBoardRingsOnceWhenSomethingNewNeedsYou(t *testing.T) {
	rings := 0
	m := boardModel(100, 30)
	m.deps.Bell = func() { rings++ }
	load := func(b app.Board) {
		m.board.loadGen++
		m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: b})
	}

	load(sampleBoard(goldenNow))
	require.Zero(t, rings, "what was already there does not ring")

	later := sampleBoard(goldenNow)
	later.Queue[0].Waited = 9 * time.Minute // its text changes, it is still the same line
	load(later)
	require.Zero(t, rings, "a line that only changes its numbers is not new")

	more := sampleBoard(goldenNow)
	more.Agents.Away = append(more.Agents.Away, provider.Agent{ID: 5, Name: "linux-5", Type: "REMOTE", Enabled: true})
	more.Needs = app.NeedsYou(more, goldenNow)
	load(more)
	require.Equal(t, 1, rings, "an agent going offline rings once")
	load(more)
	require.Equal(t, 1, rings, "and not again while it stays")
}

func TestBoardBellCanBeSilenced(t *testing.T) {
	m := boardModel(100, 30) // Deps.Bell is nil: bell: false
	more := sampleBoard(goldenNow)
	more.Agents.Away = append(more.Agents.Away, provider.Agent{ID: 5, Name: "linux-5", Enabled: true})
	more.Needs = app.NeedsYou(more, goldenNow)
	m.board.loadGen++
	require.NotPanics(t, func() { send(m, boardLoadedMsg{Gen: m.board.loadGen, Board: more}) })
}

// ---- keys ----

func pinCursor(m Model) Model {
	m, _ = send(m, mkKey("tab")) // Queue
	m, _ = send(m, mkKey("tab")) // Pinned
	return m
}

func TestBoardTabMovesBetweenPanels(t *testing.T) {
	m := boardModel(100, 30)
	require.Equal(t, boardNeeds, m.board.panel)
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, boardQueue, m.board.panel)
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, boardPins, m.board.panel)
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, boardNeeds, m.board.panel)
	m, _ = send(m, mkKey("shift+tab"))
	require.Equal(t, boardPins, m.board.panel)
}

func TestBoardJKMoveWithinThePanel(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.board.cursor[boardPins])
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.board.cursor[boardPins], "stops at the last pipeline")
	m, _ = send(m, mkKey("g"))
	require.Zero(t, m.board.cursor[boardPins])
	m, _ = send(m, mkKey("G"))
	require.Equal(t, 1, m.board.cursor[boardPins])
	m, _ = send(m, mkKey("k"))
	require.Zero(t, m.board.cursor[boardPins])
}

func TestBoardEnterOnAPinOpensItsPlanAndEscComesBack(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-BUILD", m.plan.key)
	require.True(t, m.fromBoard)

	m, cmd = send(m, mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)
	require.False(t, m.fromBoard)
	require.NotNil(t, cmd, "the board reads again and its beat restarts")
}

func TestBoardEnterOnAQueuedBuildOpensTheBuild(t *testing.T) {
	m := boardModel(100, 30)
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-BUILD-500", m.detail.Key)
	m, _ = send(m, mkKey("esc")) // from Stages to Builds, as on every plan screen
	require.Equal(t, screenPlan, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)
}

func TestBoardEnterOnANeedOpensItsBuild(t *testing.T) {
	m := boardModel(100, 30)
	// The first need is the agent; step to the queue wait, which names a build.
	for i, n := range m.board.data.Needs {
		if n.Kind == "queue_wait" {
			m.board.cursor[boardNeeds] = i
		}
	}
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-BUILD-500", m.detail.Key)
}

func TestBoardLOpensTheLogsAndEscReturnsThroughThePlanScreen(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	m, _ = send(m, mkKey("esc")) // Stages to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)
}

func TestBoardTOpensTheTreeAndEscComesBack(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	require.Equal(t, "PROJ-BUILD-44", m.buildTree.root, "a pin with nothing running shows its newest result")
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)
	require.NotNil(t, cmd)
}

func TestBoardIExplainsAFailedBuildAndOnlyThat(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, cmd := send(m, mkKey("i"))
	require.Equal(t, overlayInfo, m.overlay)
	require.NotNil(t, cmd)
	require.Contains(t, m.infoPane.title, "PROJ-BUILD-44")

	m = pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("j")) // PROJ-PROV, running
	m, _ = send(m, mkKey("i"))
	require.Equal(t, overlayNone, m.overlay)
	require.Contains(t, m.status, "did not fail")
}

// i on a Needs you line about a failed build explains that build, as it does
// from the Pinned panel; on a line about a build still queued it does not.
func TestBoardIOnANeedExplainsTheFailedBuild(t *testing.T) {
	needAt := func(m Model, kind string) Model {
		for i, n := range m.board.data.Needs {
			if n.Kind == kind {
				m.board.cursor[boardNeeds] = i
				return m
			}
		}
		t.Fatalf("no %s need", kind)
		return m
	}
	m := needAt(boardModel(100, 30), "pin_failed")
	m, cmd := send(m, mkKey("i"))
	require.Equal(t, overlayInfo, m.overlay)
	require.NotNil(t, cmd)
	require.Contains(t, m.infoPane.title, "PROJ-BUILD-44")

	m = needAt(boardModel(100, 30), "queue_wait")
	m, _ = send(m, mkKey("i"))
	require.Equal(t, overlayNone, m.overlay)
	require.Contains(t, m.status, "PROJ-BUILD-500 did not fail")
}

func TestBoardOAndYActOnTheSelection(t *testing.T) {
	var opened string
	var clip bytes.Buffer
	m := pinCursor(boardModel(100, 30))
	m.deps.Open = func(u string) error { opened = u; return nil }
	m.deps.Clipboard = &clip
	_, cmd := send(m, mkKey("o"))
	cmd()
	require.Equal(t, labOrigin+"/browse/PROJ-BUILD", opened, "a pipeline opens its plan page")
	_, cmd = send(m, mkKey("y"))
	cmd()
	require.Contains(t, clip.String(), "\x1b]52;", "y copies through the terminal")

	m, _ = send(m, mkKey("shift+tab")) // Queue
	_, cmd = send(m, mkKey("o"))
	cmd()
	require.Equal(t, labOrigin+"/browse/PROJ-BUILD-500", opened, "a queued build opens its own page")
}

func TestBoardEscOnTheBoardDoesNothing(t *testing.T) {
	m, cmd := send(boardModel(100, 30), mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)
	require.Nil(t, cmd)
}

func TestBoardQQuits(t *testing.T) {
	_, cmd := send(boardModel(100, 30), mkKey("q"))
	require.NotNil(t, cmd)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

func TestBoardRRefreshesInFull(t *testing.T) {
	m := boardModel(100, 30)
	_, cmd := send(m, mkKey("r"))
	reads := boardReads(cmd)
	require.Len(t, reads, 1)
	require.True(t, reads[0].Full)
}

func TestBoardColonTopOpensTheBoardFromHome(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.screen = screenHome
	m, _ = send(m, mkKey(":"))
	for _, r := range "top" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenBoard, m.screen)
	require.NotNil(t, cmd)

	m, _ = send(m, mkKey(":"))
	for _, r := range "plans" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenHome, m.screen, ":plans still opens the plan table")
}

// P: on a pinned pipeline it unpins; on a queued build it asks for the group
// and pins the build's plan. Both write only through the settings callbacks.
func TestBoardPUnpinsThePinnedPipeline(t *testing.T) {
	var removed []string
	m := pinCursor(boardModel(100, 30))
	m.deps.PinRemove = func(p string) error { removed = append(removed, p); return nil }
	m, cmd := send(m, mkKey("P"))
	require.Equal(t, inputNone, m.inputFor)
	msg := cmd()
	require.Equal(t, []string{"PROJ-BUILD"}, removed)
	m, cmd = send(m, msg)
	require.Contains(t, m.status, "unpinned PROJ-BUILD")
	require.Len(t, boardReads(cmd), 1, "the board reads the settings again")
}

func TestBoardPPinsAQueuedBuildsPlanAfterAskingForTheGroup(t *testing.T) {
	var group, plan string
	m := boardModel(100, 30)
	m.deps.PinAdd = func(g, p string) error { group, plan = g, p; return nil }
	m, _ = send(m, mkKey("tab")) // Queue, first row
	m, _ = send(m, mkKey("P"))
	require.Equal(t, inputPin, m.inputFor)
	require.Contains(t, m.input.Prompt, "PROJ-BUILD")
	for _, r := range "release" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, inputNone, m.inputFor)
	msg := cmd()
	require.Equal(t, "release", group)
	require.Equal(t, "PROJ-BUILD", plan)
	m, _ = send(m, msg)
	require.Contains(t, m.status, "pinned PROJ-BUILD in release")
}

func TestBoardPWithAnEmptyGroupPinsNothingAndEscCancels(t *testing.T) {
	calls := 0
	m := boardModel(100, 30)
	m.deps.PinAdd = func(string, string) error { calls++; return nil }
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("P"))
	m, cmd := send(m, mkKey("enter"))
	require.Nil(t, cmd)
	m, _ = send(m, mkKey("P"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, inputNone, m.inputFor)
	require.Zero(t, calls)
}

func TestBoardPShowsAWriteFailureInTheStatusBar(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m.deps.PinRemove = func(string) error { return errors.New("PROJ-BUILD is pinned in .bam.yaml; edit it there") }
	_, cmd := send(m, mkKey("P"))
	require.IsType(t, errMsg{}, cmd())
}

// No key of the board reaches the server with a write.
func TestBoardKeysNeverChangeTheServer(t *testing.T) {
	m := boardModel(100, 30)
	f := m.svc.P.(*fake.Provider)
	m.deps.Open = func(string) error { return nil }
	for _, k := range []string{"j", "k", "tab", "enter", "esc", "l", "t", "i", "o", "y", "r", "R", "C", "F", "p", "?"} {
		var cmd tea.Cmd
		m, cmd = send(m, mkKey(k))
		runBatchSafely(cmd)
		if m.screen != screenBoard {
			m, _ = send(m, mkKey("esc"))
			m, _ = send(m, mkKey("esc"))
			m.screen, m.overlay = screenBoard, overlayNone
		}
		m.overlay = overlayNone
	}
	require.Empty(t, f.Triggered)
	require.Empty(t, f.Stopped)
	require.Empty(t, f.Restarted)
}

// runBatchSafely runs the quick commands of a batch but not watches, which
// would block on a channel.
func runBatchSafely(cmd tea.Cmd) {
	done := make(chan struct{})
	go func() { defer close(done); _ = runBatchTimeout(cmd) }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
	}
}

func runBatchTimeout(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	return runBatch(cmd)
}

func TestBoardStartsOnTheBoardWhenAsked(t *testing.T) {
	m := New(Deps{StartOnBoard: true})
	require.Equal(t, screenBoard, m.screen)
	m = New(Deps{})
	require.Equal(t, screenHome, m.screen, "without pins bam opens the plan table")
}
