package app

import (
	"context"
	"strings"
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
	for _, job := range []string{"INT", "int", "PROJ-BUILD12-INT", "proj-build12-int-44"} {
		logs, err := s.Logs(bg, failedBuild(), LogsOptions{Job: job, Tail: 1})
		require.NoError(t, err, job)
		require.Len(t, logs, 1, job)
		assert.Equal(t, []string{"i3"}, logs[0].Lines, job)
	}
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Job: "NOPE"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "JOB1 (Checkout), UNIT (Unit), INT (Integration)")
	assert.Empty(t, tryOf(t, err), "nothing is close to NOPE")
	_, err = s.Logs(bg, failedBuild(), LogsOptions{Job: "UNTI"})
	require.Error(t, err)
	assert.Equal(t, "did you mean --job UNIT?", tryOf(t, err))
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

const liveKey = "PROJ-BUILD12-45"

func liveJob(short string, st provider.State) provider.Job {
	return provider.Job{Key: "PROJ-BUILD12-" + short + "-45", Name: short, State: st}
}

func liveStage(name string, st provider.State, jobs ...provider.Job) provider.Stage {
	return provider.Stage{Name: name, State: st, Jobs: jobs}
}

func liveBuild(st provider.State, stages ...provider.Stage) provider.Build {
	return provider.Build{Key: liveKey, PlanKey: "PROJ-BUILD12", Number: 45, State: st, Stages: stages}
}

func liveJobKey(short string) string { return "PROJ-BUILD12-" + short + "-45" }

func recordTo(evs *[]LogEvent) func(LogEvent) {
	return func(e LogEvent) { *evs = append(*evs, e) }
}

// logTrail flattens events to one readable string each.
func logTrail(evs []LogEvent) []string {
	var out []string
	for _, e := range evs {
		switch e.Kind {
		case LogLines:
			for _, l := range e.Lines {
				out = append(out, e.Job.Name+" "+l)
			}
		case StageStart:
			out = append(out, "stage "+e.Stage)
		case LogStatus:
			out = append(out, "status "+e.Stage+" "+string(stageState(e.Build, e.Stage)))
		}
	}
	return out
}

const (
	stRun  = provider.StateRunning
	stOK   = provider.StateSuccess
	stQue  = provider.StateQueued
	stFail = provider.StateFailed
)

// TestFollowLogsInterleavesJobsWithOneBuildPollPerTick, spec §3.2.
func TestFollowLogsInterleavesJobsWithOneBuildPollPerTick(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun), liveJob("INT", stRun), liveJob("E2E", stRun))),
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stOK), liveJob("INT", stRun), liveJob("E2E", stRun))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK), liveJob("INT", stOK), liveJob("E2E", stOK))),
	}}
	p.LogGrowth = map[string][][]string{
		liveJobKey("UNIT"): {{"u1"}, {"u1", "u2"}},
		liveJobKey("INT"):  {{"i1"}, {"i1", "i2"}, {"i1", "i2", "i3"}},
	}
	p.Logs = map[string][]string{liveJobKey("E2E"): {"e1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0, liveJobKey("INT"): 0, liveJobKey("E2E"): 0}, false, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"status Test running", "UNIT u1", "INT i1", "E2E e1",
		"UNIT u2", "INT i2",
		"status Test success", "INT i3",
	}, logTrail(evs))
	waits := s.Clock.(*fakeClock).Waits()
	assert.Equal(t, []time.Duration{MinPoll, MinPoll}, waits)
	assert.Equal(t, len(waits)+1, p.GetCalls(liveKey), "one build request per tick, however many jobs")
	last := evs[len(evs)-1]
	assert.Equal(t, 3, last.Next, "LogLines carries the job's offset")
}

func TestFollowLogsAdvancesToTheNextStage(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Build", stRun, liveJob("JOB1", stRun)), liveStage("Test", stQue, liveJob("UNIT", stQue))),
		liveBuild(stRun, liveStage("Build", stOK, liveJob("JOB1", stOK)), liveStage("Test", stRun, liveJob("UNIT", stRun))),
		liveBuild(stOK, liveStage("Build", stOK, liveJob("JOB1", stOK)), liveStage("Test", stOK, liveJob("UNIT", stOK))),
	}}
	p.LogGrowth = map[string][][]string{
		liveJobKey("JOB1"): {{"c1"}, {"c1", "c2"}},
		liveJobKey("UNIT"): {{"u1"}, {"u1", "u2"}},
	}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("JOB1"): 0}, true, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"status Build running", "JOB1 c1",
		"status Build success", "JOB1 c2", "stage Test", "UNIT u1",
		"status Test success", "UNIT u2",
	}, logTrail(evs))
}

func TestFollowLogsStopsWhenAStageFailsTheBuild(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)), liveStage("Deploy", stQue, liveJob("DEP", stQue))),
		liveBuild(stFail, liveStage("Test", stFail, liveJob("UNIT", stFail)), liveStage("Deploy", provider.StateNotBuilt, liveJob("DEP", provider.StateNotBuilt))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, true, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{"status Test running", "UNIT u1", "status Test failed"}, logTrail(evs))
	assert.Zero(t, p.LogCalls(liveJobKey("DEP")))
}

func TestFollowLogsWaitsForQueuedJobsAndDropsSkippedOnes(t *testing.T) {
	p := fakeBamboo()
	skipped := provider.StateSkipped
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stQue), liveJob("SKIP", skipped))),
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun), liveJob("SKIP", skipped))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK), liveJob("SKIP", skipped))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0, liveJobKey("SKIP"): 0}, false, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{"status Test running", "UNIT u1", "status Test success"}, logTrail(evs))
	assert.Equal(t, 2, p.LogCalls(liveJobKey("UNIT")), "not fetched while queued")
	assert.Zero(t, p.LogCalls(liveJobKey("SKIP")))
}

// TestFollowLogsEndsWhenTheBuildStopsWithAJobQueued: Review Focus 2.
func TestFollowLogsEndsWhenTheBuildStopsWithAJobQueued(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stQue))),
		liveBuild(provider.StateStopped, liveStage("Test", provider.StateStopped, liveJob("UNIT", stQue))),
	}}
	s := newService(t, p)

	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, true, func(LogEvent) {})
	require.NoError(t, err, "no log at all is not an error once the build is over")
	assert.Equal(t, 1, p.LogCalls(liveJobKey("UNIT")), "one last read, then the follow ends")
}

func TestFollowLogsTreatsNotFoundOnARunningJobAsNoLinesYet(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	p.LogErrs = []error{errs.Bamboof("job not found").Wrap(errs.ErrNotFound)}
	s := newService(t, p)

	var evs []LogEvent
	require.NoError(t, s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, recordTo(&evs)))
	assert.Contains(t, logTrail(evs), "UNIT u1")
}

func TestFollowLogsReturnsOtherLogErrors(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))}}
	boom := errs.Bamboof("log read failed")
	p.LogErrs = []error{boom}
	s := newService(t, p)

	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, func(LogEvent) {})
	require.ErrorIs(t, err, boom)
}

func TestFollowLogsReportsStatusOnlyOnAChange(t *testing.T) {
	p := fakeBamboo()
	same := liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))
	p.Sequences = map[string][]provider.Build{liveKey: {same, same, same,
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK)))}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	require.NoError(t, s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, recordTo(&evs)))
	var statuses []string
	for _, line := range logTrail(evs) {
		if strings.HasPrefix(line, "status") {
			statuses = append(statuses, line)
		}
	}
	assert.Equal(t, []string{"status Test running", "status Test success"}, statuses)
}

func TestFollowLogsEndsWithTheContext(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()

	err := s.FollowLogs(ctx, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, func(e LogEvent) {
		if e.Kind == LogLines {
			cancel()
		}
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, s.Clock.(*fakeClock).Waits(), "a cancelled follow does not wait another tick")
	assert.Empty(t, p.Stopped, "ending a follow never stops the build")
}

func tryOf(t *testing.T, err error) string {
	t.Helper()
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	return e.Try
}
