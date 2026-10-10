package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func failedBuild() provider.Build {
	return provider.Build{Key: "PROJ-BUILD12-44", PlanKey: "PROJ-BUILD12", Number: 44, State: provider.StateFailed,
		Stages: []provider.Stage{
			{Name: "Checkout", Jobs: []provider.Job{{Key: "PROJ-BUILD12-JOB1-44", Name: "Checkout", State: provider.StateSuccess}}},
			{Name: "Test", Jobs: []provider.Job{
				{Key: "PROJ-BUILD12-UNIT-44", Name: "Unit", State: provider.StateFailed},
				{Key: "PROJ-BUILD12-INT-44", Name: "Integration", State: provider.StateFailed},
			}},
		}}
}

func logsProvider() *fake.Provider {
	p := fakeBamboo()
	p.Logs = map[string][]string{
		"PROJ-BUILD12-JOB1-44": {"c1"},
		"PROJ-BUILD12-UNIT-44": {"u1", "u2"},
		"PROJ-BUILD12-INT-44":  {"i1", "i2", "i3"},
	}
	return p
}

func TestLogsFailedOnly(t *testing.T) {
	s := newService(t, logsProvider())
	logs, err := s.Logs(bg, failedBuild(), LogsOptions{Failed: true})
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "Unit", logs[0].Job.Name)
	assert.Equal(t, "Test", logs[0].Stage)
	assert.Equal(t, []string{"u1", "u2"}, logs[0].Lines)
	assert.Equal(t, []string{"i1", "i2", "i3"}, logs[1].Lines)
}

func TestLogsTailAndJobSelection(t *testing.T) {
	s := newService(t, logsProvider())
	for _, job := range []string{"INT", "PROJ-BUILD12-INT", "PROJ-BUILD12-INT-44"} {
		logs, err := s.Logs(bg, failedBuild(), LogsOptions{Job: job, Tail: 1})
		require.NoError(t, err, job)
		require.Len(t, logs, 1, job)
		assert.Equal(t, []string{"i3"}, logs[0].Lines, job)
	}
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Job: "NOPE"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "JOB1 (Checkout), UNIT (Unit), INT (Integration)")
}

func TestLogsAllJobs(t *testing.T) {
	s := newService(t, logsProvider())
	logs, err := s.Logs(bg, failedBuild(), LogsOptions{})
	require.NoError(t, err)
	assert.Len(t, logs, 3)
	assert.Equal(t, "INT", ShortJobKey(failedBuild(), logs[2].Job))
}

func TestFollowLogStopsWhenJobFinishes(t *testing.T) {
	p := logsProvider()
	running := failedBuild()
	running.State = provider.StateRunning
	running.Stages[1].Jobs[1].State = provider.StateRunning
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD12-44": {running, failedBuild()}}
	s := newService(t, p)

	var got []string
	err := s.FollowLog(bg, "PROJ-BUILD12-44", running.Stages[1].Jobs[1], 1, func(lines []string) { got = append(got, lines...) })
	require.NoError(t, err)
	assert.Equal(t, []string{"i2", "i3"}, got)
	assert.Equal(t, []time.Duration{MinPoll}, s.Clock.(*fakeClock).Waits())
}

func TestFollowLogReportsAFailedFinalFetch(t *testing.T) {
	p := logsProvider()
	running := failedBuild()
	running.State = provider.StateRunning
	running.Stages[1].Jobs[1].State = provider.StateRunning
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD12-44": {running, failedBuild()}}
	boom := errs.Bamboof("log read failed")
	p.LogErrs = []error{nil, boom}
	s := newService(t, p)

	err := s.FollowLog(bg, "PROJ-BUILD12-44", running.Stages[1].Jobs[1], 1, func([]string) {})
	require.ErrorIs(t, err, boom, "--follow must not exit clean when the last log fetch fails")
}

func TestCancel(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {{Key: "PROJ-P-1", State: provider.StateRunning}}}
	s := newService(t, p)
	_, done, err := s.Cancel(bg, "PROJ-P-1")
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, []string{"PROJ-P-1"}, p.Stopped)

	_, done, err = s.Cancel(bg, "PROJ-BUILD-482")
	require.NoError(t, err)
	assert.True(t, done, "already finished")
	assert.Len(t, p.Stopped, 1)
}

func TestLogsOfOneStageWinOverFailed(t *testing.T) {
	s := newService(t, logsProvider())
	logs, err := s.Logs(bg, failedBuild(), LogsOptions{Stage: "Checkout", Failed: true})
	require.NoError(t, err)
	require.Len(t, logs, 1, "the stage's jobs, not only failed ones")
	assert.Equal(t, "Checkout", logs[0].Stage)
	assert.Equal(t, []string{"c1"}, logs[0].Lines)
	assert.Equal(t, 1, logs[0].Next)
}

func TestLogsOfAnUnknownStage(t *testing.T) {
	s := newService(t, logsProvider())
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Stage: "Nope"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "stages: Checkout, Test")
}

// TestLogsOfARunningStageToleratesJobsWithNoLogYet, spec §3.1.
func TestLogsOfARunningStageToleratesJobsWithNoLogYet(t *testing.T) {
	p := logsProvider()
	delete(p.Logs, "PROJ-BUILD12-UNIT-44") // running, but Bamboo has no log yet
	b := failedBuild()
	b.State = provider.StateRunning
	b.Stages[1].Jobs[0].State = provider.StateRunning
	b.Stages[1].Jobs[1].State = provider.StateQueued
	s := newService(t, p)

	logs, err := s.Logs(bg, b, LogsOptions{Stage: "Test"})
	require.NoError(t, err)
	require.Len(t, logs, 2)
	for _, l := range logs {
		assert.Empty(t, l.Lines, l.Job.Name)
		assert.Zero(t, l.Next, l.Job.Name)
	}
	assert.Zero(t, p.LogCalls("PROJ-BUILD12-INT-44"), "a queued job is not fetched")
}

func TestLogsWithoutAStageStillFailOnAMissingLog(t *testing.T) {
	p := logsProvider()
	delete(p.Logs, "PROJ-BUILD12-UNIT-44")
	s := newService(t, p)
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Failed: true})
	require.ErrorIs(t, err, errs.ErrNotFound, "bam logs is unchanged")
}

func TestCurrentStage(t *testing.T) {
	st := func(name string, s provider.State) provider.Stage { return provider.Stage{Name: name, State: s} }
	for _, tc := range []struct {
		name   string
		stages []provider.Stage
		want   string
		ok     bool
	}{
		{"running wins", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateQueued), st("C", provider.StateRunning)}, "C", true},
		{"first not finished", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateQueued)}, "B", true},
		{"skipped is passed over", []provider.Stage{st("A", provider.StateSkipped), st("B", provider.StateQueued)}, "B", true},
		{"all finished", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateNotBuilt)}, "", false},
		{"no stages", nil, "", false},
	} {
		got, ok := CurrentStage(provider.Build{Stages: tc.stages})
		assert.Equal(t, tc.want, got, tc.name)
		assert.Equal(t, tc.ok, ok, tc.name)
	}
}
