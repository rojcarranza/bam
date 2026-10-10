package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func runningEstimate() provider.Progress {
	return provider.Progress{Valid: true, Average: 4 * time.Minute, Elapsed: 2 * time.Minute,
		Remaining: 2 * time.Minute, Percent: 0.5, Stage: "Test"}
}

func runningDetail() provider.Build {
	b := sampleBuild()
	b.State = provider.StateRunning
	b.Duration = 0
	b.Stages[1].State = provider.StateRunning
	b.Stages[1].Jobs[0].State = provider.StateRunning
	b.FailedTests = nil
	return b
}

func TestDetailBodyShowsTheEstimate(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())
	m.expanded = defaultExpanded(runningDetail())
	m.progress = runningEstimate()

	body := m.stagesBody(56, 20)

	require.Contains(t, body, "50%")
	require.Contains(t, body, "~2m00s left")
}

func TestDetailBodyWithoutAnEstimateShowsNoBar(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())
	m.expanded = defaultExpanded(runningDetail())

	require.NotContains(t, m.stagesBody(56, 20), "%")
}

func TestWatchEventCarriesTheEstimateIntoTheModel(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())

	m, _ = send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{
		Type: app.EventState, Build: runningDetail(), Progress: runningEstimate(), State: provider.StateRunning}})

	require.Equal(t, runningEstimate(), m.progress)
	require.Contains(t, m.stagesBody(56, 20), "50%")
}

func TestOpeningAnotherBuildDropsTheOldEstimate(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())
	m.progress = runningEstimate()

	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: sampleBuild()})

	require.Equal(t, provider.Progress{}, m.progress, "a finished build keeps no estimate")
}

func TestDetailBodyDrawsCellsWhenThePanelIsWide(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())
	m.expanded = defaultExpanded(runningDetail())
	m.progress = runningEstimate()

	require.Contains(t, m.stagesBody(110, 20), "[")
}

func TestProgressEventMovesTheBar(t *testing.T) {
	m := testModel()
	m.detail = ptr(runningDetail())
	m.expanded = defaultExpanded(runningDetail())
	m.progress = runningEstimate()
	later := runningEstimate()
	later.Elapsed, later.Remaining, later.Percent = 3*time.Minute, time.Minute, 0.75

	m, cmd := send(m, watchEventMsg{Gen: m.watchGen, Event: app.Event{
		Type: app.EventProgress, Build: runningDetail(), Progress: later}})

	require.Contains(t, m.stagesBody(56, 20), "75%")
	require.NotNil(t, cmd, "a progress event keeps the watch going")
}
