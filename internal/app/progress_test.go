package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runningProgress(pct float64) provider.Progress {
	return provider.Progress{Valid: true, Average: 3 * time.Minute, Elapsed: time.Duration(pct*180) * time.Second,
		Remaining: time.Duration((1-pct)*180) * time.Second, Percent: pct, Stage: "Build"}
}

func TestWatchAttachesProgressToEveryEventOfAPoll(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		build(provider.StateRunning, provider.StateNotBuilt, provider.StateNotBuilt),
		build(provider.StateRunning, provider.StateRunning, provider.StateRunning),
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	p.Progressions = map[string][]provider.Progress{"PROJ-P-1": {runningProgress(0.25), runningProgress(0.75)}}
	s := newService(t, p)

	events := collect(s.Watch(bg, "PROJ-P-1"))

	require.NotEmpty(t, events)
	assert.Equal(t, 0.25, events[0].Progress.Percent, "first poll")
	stageEvents := 0
	for _, e := range events {
		if e.Type == EventStage || e.Type == EventJob {
			if e.Build.State == provider.StateRunning {
				assert.Equal(t, 0.75, e.Progress.Percent, "second poll %s event", e.Type)
				stageEvents++
			}
		}
	}
	assert.Equal(t, 2, stageEvents)
	last := events[len(events)-1]
	assert.Equal(t, EventDone, last.Type)
	assert.Equal(t, provider.Progress{}, last.Progress, "a finished build has a duration, not an estimate")
}

func TestWatchSurvivesAnUnsupportedProgressEndpoint(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		build(provider.StateRunning, provider.StateRunning, provider.StateRunning),
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	p.ProgressErr = errs.Bamboof("nope").Wrap(errs.ErrUnsupported)
	s := newService(t, p)

	events := collect(s.Watch(bg, "PROJ-P-1"))

	for _, e := range events {
		assert.NotEqual(t, EventError, e.Type)
		assert.Equal(t, provider.Progress{}, e.Progress)
	}
	assert.Equal(t, EventDone, events[len(events)-1].Type)
	assert.Equal(t, 1, p.ProgressCalls("PROJ-P-1"), "one unsupported answer stops the asking")
}

func TestWatchKeepsAskingAfterATransportError(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		build(provider.StateRunning, provider.StateNotBuilt, provider.StateNotBuilt),
		build(provider.StateRunning, provider.StateRunning, provider.StateRunning),
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	p.ProgressErr = errs.Bamboof("timeout")
	s := newService(t, p)

	events := collect(s.Watch(bg, "PROJ-P-1"))

	assert.Equal(t, EventDone, events[len(events)-1].Type)
	assert.Equal(t, 2, p.ProgressCalls("PROJ-P-1"), "a transport error is not a missing capability")
}

func TestServiceProgressSwallowsUnsupported(t *testing.T) {
	p := fakeBamboo()
	p.ProgressErr = errs.Bamboof("nope").Wrap(errs.ErrUnsupported)
	s := newService(t, p)

	got, err := s.Progress(bg, provider.Build{Key: "PROJ-P-1"})

	require.NoError(t, err)
	assert.Equal(t, provider.Progress{}, got)
}

func TestServiceProgressReturnsTransportErrors(t *testing.T) {
	p := fakeBamboo()
	p.ProgressErr = errs.Bamboof("boom")
	s := newService(t, p)

	_, err := s.Progress(bg, provider.Build{Key: "PROJ-P-1"})

	require.Error(t, err)
}

func TestServiceProgressReturnsTheServerValue(t *testing.T) {
	p := fakeBamboo()
	p.Progressions = map[string][]provider.Progress{"PROJ-P-1": {runningProgress(0.5)}}
	s := newService(t, p)

	got, err := s.Progress(bg, provider.Build{Key: "PROJ-P-1"})

	require.NoError(t, err)
	assert.Equal(t, runningProgress(0.5), got)
}

func TestWatchSendsProgressWhenOnlyTheEstimateMoves(t *testing.T) {
	p := fakeBamboo()
	running := build(provider.StateRunning, provider.StateRunning, provider.StateRunning)
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		running, running, running,
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	p.Progressions = map[string][]provider.Progress{"PROJ-P-1": {
		runningProgress(0.25), runningProgress(0.5), runningProgress(0.75)}}
	s := newService(t, p)

	events := collect(s.Watch(bg, "PROJ-P-1"))

	var percents []float64
	for _, e := range events {
		if e.Type == EventProgress {
			assert.Equal(t, provider.StateRunning, e.Build.State)
			percents = append(percents, e.Progress.Percent)
		}
	}
	assert.Equal(t, []float64{0.5, 0.75}, percents, "every poll's new estimate reaches the renderers")
	assert.Equal(t, []time.Duration{2 * time.Second, 3 * time.Second, 4500 * time.Millisecond},
		s.Clock.(*fakeClock).Waits(), "an estimate is not a change, so the backoff still grows")
}

func TestWatchStaysQuietWhenTheEstimateDoesNotMove(t *testing.T) {
	p := fakeBamboo()
	running := build(provider.StateRunning, provider.StateRunning, provider.StateRunning)
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		running, running,
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	p.Progressions = map[string][]provider.Progress{"PROJ-P-1": {runningProgress(0.5)}}
	s := newService(t, p)

	for _, e := range collect(s.Watch(bg, "PROJ-P-1")) {
		assert.NotEqual(t, EventProgress, e.Type)
	}
}
