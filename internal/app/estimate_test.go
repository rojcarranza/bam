package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// historyBamboo has no progress endpoint and three successful PROJ-BUILD
// builds of 6, 4 and 5 minutes, plus a 1-minute failure.
func historyBamboo() *fake.Provider {
	p := fakeBamboo()
	p.ProgressErr = errs.Bamboof("this Bamboo does not report build progress").Wrap(errs.ErrUnsupported)
	p.History["PROJ-BUILD"] = []provider.Build{
		{Key: "PROJ-BUILD-9", PlanKey: "PROJ-BUILD", Number: 9, State: provider.StateSuccess, Duration: 6 * time.Minute},
		{Key: "PROJ-BUILD-8", PlanKey: "PROJ-BUILD", Number: 8, State: provider.StateFailed, Duration: time.Minute},
		{Key: "PROJ-BUILD-7", PlanKey: "PROJ-BUILD", Number: 7, State: provider.StateSuccess, Duration: 4 * time.Minute},
		{Key: "PROJ-BUILD-6", PlanKey: "PROJ-BUILD", Number: 6, State: provider.StateSuccess, Duration: 5 * time.Minute},
	}
	return p
}

// runningFor is PROJ-BUILD-10, started d ago by s's clock.
func runningFor(s *Service, d time.Duration) provider.Build {
	return provider.Build{Key: "PROJ-BUILD-10", PlanKey: "PROJ-BUILD", Number: 10, State: provider.StateRunning,
		StartedAt: s.Clock.Now().Add(-d),
		Stages:    []provider.Stage{{Name: "Build", State: provider.StateSuccess}, {Name: "Test", State: provider.StateRunning}}}
}

func TestProgressFromHistory(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, runningFor(s, 2*time.Minute))
	require.NoError(t, err)
	require.True(t, pr.Valid)
	require.Equal(t, "history", pr.Source)
	require.Equal(t, 5*time.Minute, pr.Average, "median of 4, 5, 6; the failure is not counted")
	require.Equal(t, 2*time.Minute, pr.Elapsed)
	require.Equal(t, 3*time.Minute, pr.Remaining)
	require.InDelta(t, 0.4, pr.Percent, 1e-9)
	require.Equal(t, "Test", pr.Stage)
}

func TestProgressPastTheMedian(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, runningFor(s, 7*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1.0, pr.Percent)
	require.Zero(t, pr.Remaining)
}

func TestProgressNeedsThreeSuccessfulBuilds(t *testing.T) {
	p := historyBamboo()
	p.History["PROJ-BUILD"] = p.History["PROJ-BUILD"][:2] // one success, one failure
	s := newService(t, p)
	pr, err := s.Progress(bg, runningFor(s, time.Minute))
	require.NoError(t, err)
	require.False(t, pr.Valid)
}

func TestProgressNotRunningHasNone(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, provider.Build{Key: "PROJ-BUILD-10", PlanKey: "PROJ-BUILD", State: provider.StateQueued})
	require.NoError(t, err)
	require.False(t, pr.Valid)
}

func TestProgressPrefersBamboo(t *testing.T) {
	p := historyBamboo()
	p.ProgressErr = nil
	p.Progressions = map[string][]provider.Progress{"PROJ-BUILD-10": {{Valid: true, Average: time.Hour, Source: "bamboo"}}}
	s := newService(t, p)
	pr, err := s.Progress(bg, runningFor(s, time.Minute))
	require.NoError(t, err)
	require.Equal(t, "bamboo", pr.Source)
	require.Equal(t, 0, p.ListCalls("PROJ-BUILD"), "history is never read when Bamboo answers")
}

func TestProgressReadsHistoryOncePerPlan(t *testing.T) {
	p := historyBamboo()
	s := newService(t, p)
	for i := 0; i < 3; i++ {
		_, err := s.Progress(bg, runningFor(s, time.Minute))
		require.NoError(t, err)
	}
	require.Equal(t, 1, p.ListCalls("PROJ-BUILD"))
}

func TestWatchUsesHistoryWhenUnsupported(t *testing.T) {
	p := historyBamboo()
	s := newService(t, p)
	run := runningFor(s, time.Minute)
	done := run
	done.State = provider.StateSuccess
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD-10": {run, run, done}}
	var sawHistory bool
	for e := range s.Watch(bg, "PROJ-BUILD-10") {
		if e.Progress.Valid && e.Progress.Source == "history" {
			sawHistory = true
		}
	}
	require.True(t, sawHistory)
	require.Equal(t, 1, p.ListCalls("PROJ-BUILD"))
	require.Equal(t, 1, p.ProgressCalls("PROJ-BUILD-10"), "Bamboo asked once, then never again")
}
