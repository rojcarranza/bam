package tui

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// Piece 5 (SCR-54): the UI rings the terminal bell once when a watched build
// or tree finishes, and a setting (a nil Deps.Bell) turns it off.

func bellModel(rings *int) Model {
	m := testModel()
	m.svc = testService()
	m.deps.Bell = func() { *rings++ }
	return m
}

func buildIn(state provider.State) provider.Build {
	b := sampleBuild()
	b.State = state
	return b
}

func TestBellRingsOnceWhenAWatchedBuildFinishes(t *testing.T) {
	rings := 0
	m := bellModel(&rings)
	m, _ = send(m, watchEventMsg{Event: app.Event{Type: app.EventStage, Build: buildIn(provider.StateRunning)}})
	require.Zero(t, rings, "a running build does not ring")
	send(m, watchEventMsg{Event: app.Event{Type: app.EventDone, Build: buildIn(provider.StateFailed)}})
	require.Equal(t, 1, rings)
}

func TestBellStaysQuietForABuildThatWasAlreadyFinished(t *testing.T) {
	rings := 0
	m := bellModel(&rings)
	send(m, watchEventMsg{Event: app.Event{Type: app.EventDone, Build: buildIn(provider.StateSuccess)}})
	require.Zero(t, rings)
}

func TestBellSettingOffSilencesTheWatch(t *testing.T) {
	m := testModel()
	m.svc = testService()
	require.Nil(t, m.deps.Bell)
	m, _ = send(m, watchEventMsg{Event: app.Event{Type: app.EventStage, Build: buildIn(provider.StateRunning)}})
	require.NotPanics(t, func() {
		send(m, watchEventMsg{Event: app.Event{Type: app.EventDone, Build: buildIn(provider.StateSuccess)}})
	})
}

func TestBellRingsOnceWhenAWatchedTreeFinishes(t *testing.T) {
	rings := 0
	m := bellModel(&rings)
	tree := func(s provider.State, typ app.TreeEventType) treeEventMsg {
		return treeEventMsg{Gen: m.treeGen, Event: app.TreeEvent{Type: typ,
			Tree: app.Tree{Root: &app.TreeNode{Build: provider.Build{State: s}}, State: s}}}
	}
	m, _ = send(m, tree(provider.StateRunning, app.TreeUpdate))
	require.Zero(t, rings)
	m, _ = send(m, tree(provider.StateSuccess, app.TreeDone))
	require.Equal(t, 1, rings)

	// A tree that opens already finished never rang for anything.
	rings = 0
	m = bellModel(&rings)
	send(m, tree(provider.StateSuccess, app.TreeDone))
	require.Zero(t, rings)
}
