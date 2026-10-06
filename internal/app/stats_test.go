package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

var statsAt = time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)

// sb is a build of PROJ-BUILD with number n, state st and duration d,
// started n hours after statsAt.
func sb(n int, st provider.State, d time.Duration, failedJobs ...string) provider.Build {
	start := statsAt.Add(time.Duration(n) * time.Hour)
	b := provider.Build{Key: "PROJ-BUILD-" + strconv.Itoa(n), PlanKey: "PROJ-BUILD", Number: n, State: st,
		StartedAt: start, FinishedAt: start.Add(d), Duration: d}
	if len(failedJobs) > 0 {
		var jobs []provider.Job
		for i, j := range failedJobs {
			jobs = append(jobs, provider.Job{Key: b.Key + "-J" + strconv.Itoa(i), Name: j, State: provider.StateFailed})
		}
		b.Stages = []provider.Stage{{Name: "Test", State: provider.StateFailed, Jobs: jobs}}
	}
	return b
}

// statsService serves builds (newest first) as PROJ-BUILD's history.
func statsService(t *testing.T, builds ...provider.Build) (*Service, *fake.Provider) {
	p := fakeBamboo()
	p.History["PROJ-BUILD"] = builds
	return newService(t, p), p
}

func statsRef() PlanRef { return PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"} }

func TestPlanStatsCounts(t *testing.T) {
	s, _ := statsService(t,
		sb(9, provider.StateRunning, 0),
		sb(8, provider.StateQueued, 0),
		sb(7, provider.StateSuccess, 3*time.Minute),
		sb(6, provider.StateStopped, time.Minute),
		sb(5, provider.StateFailed, time.Minute, "Integration"),
		sb(4, provider.StateSuccess, 2*time.Minute),
		sb(3, provider.StateNotBuilt, 0),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, 7, st.Builds)
	require.Equal(t, 3, st.First)
	require.Equal(t, 9, st.Last)
	require.Equal(t, 2, st.Passed)
	require.Equal(t, 1, st.Failed)
	require.Equal(t, map[provider.State]int{provider.StateStopped: 1, provider.StateNotBuilt: 1}, st.Other)
	rate, ok := st.SuccessRate()
	require.True(t, ok)
	require.InDelta(t, 2.0/3, rate, 1e-9)
}

func TestPlanStatsDurationsUseSuccessfulBuildsOnly(t *testing.T) {
	s, _ := statsService(t,
		sb(5, provider.StateFailed, 10*time.Second, "Unit"),
		sb(4, provider.StateSuccess, 4*time.Minute),
		sb(3, provider.StateSuccess, 2*time.Minute),
		sb(2, provider.StateSuccess, 9*time.Minute),
		sb(1, provider.StateSuccess, 3*time.Minute),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.NotNil(t, st.Durations)
	require.Equal(t, 4, st.Durations.Of)
	require.Equal(t, 3*time.Minute, st.Durations.Median, "lower middle of 2,3,4,9")
	require.Equal(t, 9*time.Minute, st.Durations.P90)
	require.Equal(t, 9*time.Minute, st.Durations.Max)
	require.Equal(t, 2, st.Durations.MaxBuild)
}

func TestPlanStatsPercentiles(t *testing.T) {
	for _, tc := range []struct {
		n           int
		median, p90 int // minutes
	}{{1, 1, 1}, {2, 1, 2}, {10, 5, 9}, {11, 6, 10}} {
		var builds []provider.Build
		for i := tc.n; i >= 1; i-- {
			builds = append(builds, sb(i, provider.StateSuccess, time.Duration(i)*time.Minute))
		}
		s, _ := statsService(t, builds...)
		st, err := s.PlanStats(bg, statsRef(), 100)
		require.NoError(t, err)
		require.Equal(t, time.Duration(tc.median)*time.Minute, st.Durations.Median, "n=%d", tc.n)
		require.Equal(t, time.Duration(tc.p90)*time.Minute, st.Durations.P90, "n=%d", tc.n)
	}
}

func TestPlanStatsStreakSkipsOtherBuilds(t *testing.T) {
	s, _ := statsService(t,
		sb(6, provider.StateRunning, 0),
		sb(5, provider.StateSuccess, time.Minute),
		sb(4, provider.StateStopped, time.Minute),
		sb(3, provider.StateSuccess, time.Minute),
		sb(2, provider.StateFailed, time.Minute, "Unit"),
		sb(1, provider.StateSuccess, time.Minute),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, &Streak{State: provider.StateSuccess, Count: 2}, st.Streak)
}

func TestPlanStatsFailingJobs(t *testing.T) {
	s, _ := statsService(t,
		sb(6, provider.StateFailed, time.Minute, "Integration"),
		sb(5, provider.StateFailed, time.Minute, "Integration", "Deploy"),
		sb(4, provider.StateSuccess, time.Minute),
		sb(3, provider.StateFailed, time.Minute, "Deploy"),
		sb(2, provider.StateFailed, time.Minute, "Integration"),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, []JobFailures{
		{Name: "Integration", Failures: 3, LastBuild: 6},
		{Name: "Deploy", Failures: 2, LastBuild: 5},
	}, st.FailingJobs)
	require.Empty(t, st.Warnings)
}

func TestPlanStatsUnreadableFailedBuild(t *testing.T) {
	gone := sb(5, provider.StateFailed, time.Minute)
	s, p := statsService(t, gone, sb(4, provider.StateSuccess, time.Minute))
	// The list knows build 5; reading it alone fails.
	p.Sequences = map[string][]provider.Build{}
	p.History["PROJ-BUILD"] = []provider.Build{gone, sb(4, provider.StateSuccess, time.Minute)}
	s.P = &failingGet{Provider: p, key: gone.Key}
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, []JobFailures{{Name: UnknownJob, Failures: 1, LastBuild: 5}}, st.FailingJobs)
	require.Len(t, st.Warnings, 1)
	require.Contains(t, st.Warnings[0], "could not read PROJ-BUILD-5")
}

func TestPlanStatsNoFinishedBuilds(t *testing.T) {
	s, _ := statsService(t, sb(1, provider.StateRunning, 0))
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	_, ok := st.SuccessRate()
	require.False(t, ok)
	require.Nil(t, st.Durations)
	require.Nil(t, st.Streak)
	require.Empty(t, st.FailingJobs)
}

func TestPlanStatsRange(t *testing.T) {
	s, _ := statsService(t, sb(5, provider.StateSuccess, time.Minute), sb(2, provider.StateSuccess, time.Minute))
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, statsAt.Add(2*time.Hour), st.From)
	require.Equal(t, statsAt.Add(5*time.Hour+time.Minute), st.To)
}

// failingGet fails GetBuild for one key.
type failingGet struct {
	*fake.Provider
	key string
}

func (f *failingGet) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	if key == f.key {
		return provider.Build{}, errs.Bamboof("server error reading %s", key)
	}
	return f.Provider.GetBuild(ctx, key)
}

func TestPlanStatsKeepsTheMasterKey(t *testing.T) {
	s, _ := statsService(t, sb(1, provider.StateSuccess, time.Minute))
	st, err := s.PlanStats(bg, PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD", Branch: ""}, 20)
	require.NoError(t, err)
	require.Equal(t, "PROJ-BUILD", st.MasterKey)
}

// An interrupt while the failed builds are read is an interrupt, not a run
// of "could not read" warnings and an exit 0.
func TestPlanStatsStopsOnAnEndedContext(t *testing.T) {
	s, _ := statsService(t, sb(2, provider.StateFailed, time.Minute, "Unit"), sb(1, provider.StateSuccess, time.Minute))
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := s.PlanStats(ctx, statsRef(), 20)
	require.ErrorIs(t, err, context.Canceled)
}

// Skipped is a terminal state even though Finished() leaves it out (a build
// can be skipped before it starts): stats count it under other.
func TestPlanStatsCountsSkippedBuilds(t *testing.T) {
	s, _ := statsService(t, sb(2, provider.StateSkipped, 0), sb(1, provider.StateSuccess, time.Minute))
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, map[provider.State]int{provider.StateSkipped: 1}, st.Other)

	s, _ = statsService(t, sb(1, provider.StateSkipped, 0))
	st, err = s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, 1, st.Other[provider.StateSkipped], "a window of only skipped builds still has finished builds")
}
