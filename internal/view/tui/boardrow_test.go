package tui

import (
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// readAgain delivers one more read of the board, the way a refresh does.
func readAgain(m Model, mod func(*app.Board)) Model {
	data := sampleBoard(goldenNow)
	m.board.loadGen++
	if mod != nil {
		mod(&data)
	}
	data.Needs = app.NeedsYou(data, goldenNow)
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: false, Board: data})
	return m
}

func newPin(name string) app.PinView {
	return app.PinView{Pin: name, PlanKey: name, Name: name,
		Results: []provider.Build{{Key: name + "-1", PlanKey: name, Number: 1, State: provider.StateSuccess}}}
}

// Intent (BAM-53, machine 6, defect 1): after a refresh P unpinned a pipeline
// the cursor was not on, because the selection of Pinned was only kept while
// that panel was in view. The cursor sits on PROJ-PROV, the person goes to
// another panel, a read adds a pin above it, and P still unpins PROJ-PROV.
func TestPUnpinsThePinTheCursorWasOnAfterARefreshWhileAwayFromPinned(t *testing.T) {
	var removed []string
	m := pinCursor(boardModel(100, 30))
	m.deps.PinRemove = func(p string) error { removed = append(removed, p); return nil }
	m, _ = send(m, mkKey("j")) // PROJ-PROV
	m, _ = send(m, mkKey("tab"))
	m = readAgain(m, func(b *app.Board) {
		b.Groups[0].Pins = append([]app.PinView{newPin("PROJ-NEW")}, b.Groups[0].Pins...)
	})
	m, _ = send(m, mkKey("shift+tab"))
	require.Equal(t, boardPins, m.board.panel)
	_, cmd := send(m, mkKey("P"))
	cmd()
	require.Equal(t, []string{"PROJ-PROV"}, removed)
}

// Intent (BAM-53, machine 6, defect 2): the cursor of Queue was a number, so
// a read that queued a build above it moved P onto another build's plan.
func TestPOnAQueuedBuildStaysOnItAfterARefreshQueuesAnotherAbove(t *testing.T) {
	m := boardModel(100, 30)
	m.deps.PinAdd = func(_, _ string) error { return nil }
	m, _ = send(m, mkKey("tab")) // Queue
	m, _ = send(m, mkKey("j"))   // OPS-NIGHTLY-3
	m = readAgain(m, func(b *app.Board) {
		first := app.QueueRow{Position: 1, QueueEntry: provider.QueueEntry{Key: "LAB-FIRST-1", PlanKey: "LAB-FIRST", Number: 1}}
		b.Queue = append([]app.QueueRow{first}, b.Queue...)
	})
	b, ok := m.boardBuild()
	require.True(t, ok)
	require.Equal(t, "OPS-NIGHTLY-3", b.Key)
	m, _ = send(m, mkKey("P"))
	require.Contains(t, m.input.Prompt, "pin OPS-NIGHTLY to group")
}

// Intent (BAM-53, machine 6, defect 3): the cursor of Your agents was a
// number too, so enter on a pool opened the pool that took its place.
func TestEnterOnAPoolStaysOnItAfterARefreshAddsAPoolAbove(t *testing.T) {
	pool := func(n string) app.MyPool {
		return app.MyPool{Name: n, Agents: []app.MyAgent{{Name: n + "1"}}}
	}
	m := boardModel(120, 40)
	m = readAgain(m, func(b *app.Board) {
		b.MineSet, b.Mine = true, &app.MyAgents{Pools: []app.MyPool{pool("pool-a-"), pool("pool-b-")}}
	})
	m.board.panel = boardAgents
	m, _ = send(m, mkKey("j")) // pool-b-
	m = readAgain(m, func(b *app.Board) {
		b.MineSet, b.Mine = true, &app.MyAgents{Pools: []app.MyPool{pool("pool-0-"), pool("pool-a-"), pool("pool-b-")}}
	})
	require.Equal(t, 2, m.board.cursor[boardAgents])
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "pool-b-…", m.infoPane.title)
}

// Intent (BAM-53, machine 6): a row that left the board leaves the cursor in
// its panel, and a pipeline of a tree that went falls back to its pin.
func TestSelectionOfAGoneRowStaysInsideThePanel(t *testing.T) {
	m := pinCursor(boardModel(100, 30))
	m, _ = send(m, mkKey("j"))
	m = readAgain(m, func(b *app.Board) { b.Groups[0].Pins = b.Groups[0].Pins[:1] })
	require.Equal(t, 0, m.board.cursor[boardPins])
}
