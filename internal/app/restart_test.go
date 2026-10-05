package app

import (
	"context"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var failedAt = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func oldResult() provider.Build {
	return provider.Build{Key: "PROJ-BUILD-9", State: provider.StateFailed, FinishedAt: failedAt}
}

// restartBamboo answers GetBuild of PROJ-BUILD-9 with seq, one per call.
func restartBamboo(seq ...provider.Build) *fake.Provider {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD-9": seq}
	return p
}

func TestRestartWaitsForTheOldResultToGo(t *testing.T) {
	p := restartBamboo(oldResult(), oldResult(), oldResult(), provider.Build{Key: "PROJ-BUILD-9", State: provider.StateQueued})
	s := newService(t, p)
	b, err := s.Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	assert.Equal(t, provider.StateQueued, b.State)
	assert.Equal(t, []string{"PROJ-BUILD-9"}, p.Restarted)
	assert.Equal(t, []time.Duration{2 * time.Second, 2 * time.Second}, s.Clock.(*fakeClock).Waits())
}

func TestRestartCountsANewFinishAsRestarted(t *testing.T) {
	again := provider.Build{Key: "PROJ-BUILD-9", State: provider.StateSuccess, FinishedAt: failedAt.Add(time.Minute)}
	b, err := newService(t, restartBamboo(oldResult(), again)).Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	assert.Equal(t, provider.StateSuccess, b.State)
}

func TestRestartGivesUpAfterTwoMinutes(t *testing.T) {
	s := newService(t, restartBamboo(oldResult()))
	_, err := s.Restart(bg, "PROJ-BUILD-9")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo did not restart PROJ-BUILD-9", e.What)
	assert.Equal(t, "the build still shows its earlier result", e.Why)
	assert.Equal(t, "bam open PROJ-BUILD-9", e.Try)
	var total time.Duration
	for _, w := range s.Clock.(*fakeClock).Waits() {
		total += w
	}
	assert.Equal(t, 2*time.Minute, total)
}

func TestRestartPassesARefusalThrough(t *testing.T) {
	p := restartBamboo(oldResult())
	p.RestartErr = errs.Bamboof("Bamboo would not restart PROJ-BUILD-9")
	_, err := newService(t, p).Restart(bg, "PROJ-BUILD-9")
	require.ErrorContains(t, err, "would not restart")
	assert.Equal(t, 1, p.GetCalls("PROJ-BUILD-9"), "no polling after a refusal")
}

func TestRestartLeavesTheLastBuildAlone(t *testing.T) {
	s := newService(t, restartBamboo(oldResult(), provider.Build{Key: "PROJ-BUILD-9", State: provider.StateQueued}))
	_, err := s.Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok, "a restart is not a trigger")
}

func TestRestartWithAnEndedContextRestartsNothing(t *testing.T) {
	p := restartBamboo(oldResult())
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := newService(t, p).Restart(ctx, "PROJ-BUILD-9")
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, p.Restarted)
}
