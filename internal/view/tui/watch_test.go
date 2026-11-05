package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// TestWatchCmdDeliversOneEventPerCall is the bubbletea channel pattern: one
// command, one message, then the model re-issues the command.
func TestWatchCmdDeliversOneEventPerCall(t *testing.T) {
	ch := make(chan app.Event, 2)
	ch <- app.Event{Type: app.EventStage, Name: "Test", State: provider.StateRunning}
	ch <- app.Event{Type: app.EventDone, State: provider.StateFailed}
	close(ch)

	first, ok := watchCmd(ch, 0)().(watchEventMsg)
	require.True(t, ok)
	require.Equal(t, app.EventStage, first.Event.Type)

	second := watchCmd(ch, 0)().(watchEventMsg)
	require.Equal(t, app.EventDone, second.Event.Type)

	require.IsType(t, watchClosedMsg{}, watchCmd(ch, 0)(), "a closed channel ends the loop")
}

// TestWatchEventUpdatesTheDetail: the tree must follow the build.
func TestWatchEventUpdatesTheDetail(t *testing.T) {
	m := testModel()
	m.detail = ptr(sampleBuild())
	m.expanded = map[string]bool{}

	running := sampleBuild()
	running.State = provider.StateRunning
	running.Stages[2].State = provider.StateRunning

	m, _ = send(m, watchEventMsg{Event: app.Event{Type: app.EventStage, Build: running,
		Name: "Deploy", State: provider.StateRunning, Time: time.Now()}})
	require.Equal(t, provider.StateRunning, m.detail.State)
	require.Equal(t, provider.StateRunning, m.treeRows()[2].State)
}

// TestEventDoneStopsTheWatchAndRefreshesTheBuildsList, spec §5.
func TestEventDoneStopsTheWatchAndRefreshesTheBuildsList(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.buildsPlan = "PROJ-BUILD"
	stopped := false
	m.watchCancel = func() { stopped = true }

	finished := sampleBuild()
	finished.State = provider.StateFailed
	m, cmd := send(m, watchEventMsg{Event: app.Event{Type: app.EventDone, Build: finished, State: provider.StateFailed}})
	require.True(t, stopped, "EventDone cancels the watch")
	require.Nil(t, m.watchCancel)
	require.NotNil(t, cmd, "EventDone re-fetches the builds list")
	require.IsType(t, buildsLoadedMsg{}, cmd())
}

// TestEventErrorShowsAndStopsWithoutQuitting, spec §5 and §6.
func TestEventErrorShowsAndStopsWithoutQuitting(t *testing.T) {
	m := testModel()
	stopped := false
	m.watchCancel = func() { stopped = true }
	m, cmd := send(m, watchEventMsg{Event: app.Event{Type: app.EventError, Err: errBoom}})
	require.Error(t, m.err)
	require.True(t, stopped)
	require.Nil(t, cmd)
}

// TestSelectingAnotherBuildCancelsTheFirstWatch is the load-bearing rule:
// at most one poll loop runs against Bamboo.
func TestSelectingAnotherBuildCancelsTheFirstWatch(t *testing.T) {
	m := testModel()
	m.svc = testService()
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{
		{Key: "PROJ-BUILD-44", Number: 44}, {Key: "PROJ-BUILD-43", Number: 43},
	}})
	m.focus = focusBuilds

	m, _ = send(m, mkKey("enter"))
	require.NotNil(t, m.watchCancel)
	first := m.watchCancel
	cancelled := false
	m.watchCancel = func() { cancelled = true; first() }

	m.focus = focusBuilds
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.True(t, cancelled, "opening another build cancels the first watch")
	require.NotNil(t, m.watchCancel)
	m.stopWatch()
}

// TestQuittingCancelsTheWatch. Cancelling a watch never stops the build; this
// only stops polling.
func TestQuittingCancelsTheWatch(t *testing.T) {
	m := testModel()
	cancelled := false
	m.watchCancel = func() { cancelled = true }
	_, cmd := send(m, mkKey("q"))
	require.True(t, cancelled)
	require.IsType(t, tea.QuitMsg{}, cmd())
}

// TestWatchClosedStopsQuietly: a cancelled watch is not an error.
func TestWatchClosedStopsQuietly(t *testing.T) {
	m := testModel()
	m.watchCancel = func() {}
	m, cmd := send(m, watchClosedMsg{})
	require.Nil(t, cmd)
	require.NoError(t, m.err)
	require.Nil(t, m.watchCancel)
}

func TestANotBuiltRevisionRunSaysSo(t *testing.T) {
	m := formModel()
	m.form.revision = "abc1234"
	next, _ := m.openTriggered(provider.Build{Key: "PROJ-PROV12-9", State: provider.StateQueued})
	m = next.(Model)
	next, _ = m.handleWatchEvent(app.Event{Type: app.EventDone, Build: provider.Build{Key: "PROJ-PROV12-9", State: provider.StateNotBuilt}})
	m = next.(Model)
	require.Equal(t, "Bamboo could not build revision abc1234", m.status)
}

// TestANotBuiltBuildWithNoRevisionRunLeavesStatusAlone: a plain not_built
// build (no watch was ever started with a revision) must not be mistaken for
// a revision Bamboo ignored.
func TestANotBuiltBuildWithNoRevisionRunLeavesStatusAlone(t *testing.T) {
	m := testModel()
	m.status = "before"
	next, _ := m.handleWatchEvent(app.Event{Type: app.EventDone, Build: provider.Build{Key: "PROJ-PROV12-9", State: provider.StateNotBuilt}})
	m = next.(Model)
	require.Equal(t, "before", m.status)
}

// TestANotBuiltBuildForAnotherKeyLeavesStatusAlone: revisionRun is scoped to
// the build it was set for; a different build going not_built is unrelated.
func TestANotBuiltBuildForAnotherKeyLeavesStatusAlone(t *testing.T) {
	m := formModel()
	m.form.revision = "abc1234"
	next, _ := m.openTriggered(provider.Build{Key: "PROJ-PROV12-9", State: provider.StateQueued})
	m = next.(Model)
	m.status = "before"
	next, _ = m.handleWatchEvent(app.Event{Type: app.EventDone, Build: provider.Build{Key: "PROJ-PROV12-99", State: provider.StateNotBuilt}})
	m = next.(Model)
	require.Equal(t, "before", m.status)
}
