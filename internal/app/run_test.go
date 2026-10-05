package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTriggersChangedVariablesAndRecordsLast(t *testing.T) {
	p := fakeBamboo()
	p.TriggerResult = provider.Build{Key: "PROJ-PROV12-10", Number: 10, State: provider.StateQueued}
	s := newService(t, p)
	withEnv(s, map[string]string{"LAB_DB_PASSWORD": "hunter2"})
	ref, _ := s.ResolvePlan(bg, "provision-lab", "")
	vs, err := s.ResolveVars(bg, ref, VarOptions{Flags: []string{"cluster_name=alpha"}})
	require.NoError(t, err)

	b, err := s.Run(bg, ref, vs)
	require.NoError(t, err)
	assert.Equal(t, "PROJ-PROV12-10", b.Key)

	require.Len(t, p.Triggered, 1)
	assert.Equal(t, "PROJ-PROV12", p.Triggered[0].PlanKey)
	assert.Equal(t, map[string]string{"cluster_name": "alpha", "compute_nodes": "2", "db_password": "hunter2"}, p.Triggered[0].Variables)
	assert.Equal(t, map[string]bool{"db_password": true}, p.Triggered[0].Secret)

	rec, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	require.True(t, ok)
	assert.Equal(t, LastRecord{BuildKey: "PROJ-PROV12-10", Origin: s.Origin, PlanKey: "PROJ-PROV12", Target: "provision-lab",
		TriggeredAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}, rec)
}

func TestRunErrorRecordsNothing(t *testing.T) {
	p := fakeBamboo()
	p.TriggerErr = errs.Bamboof("could not start build of PROJ-BUILD")
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	_, err := s.Run(bg, ref, VarSet{})
	require.Error(t, err)
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok)
}

func build(state provider.State, stageState, jobState provider.State) provider.Build {
	return provider.Build{Key: "PROJ-P-1", State: state, Stages: []provider.Stage{
		{Name: "Build", State: stageState, Jobs: []provider.Job{{Key: "PROJ-P-JOB1-1", Name: "Compile", State: jobState}}},
	}}
}

func collect(ch <-chan Event) []Event {
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestWatchEventsAndBackoff(t *testing.T) {
	p := fakeBamboo()
	q := provider.StateQueued
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		build(q, provider.StateNotBuilt, provider.StateNotBuilt),
		build(q, provider.StateNotBuilt, provider.StateNotBuilt),
		build(q, provider.StateNotBuilt, provider.StateNotBuilt),
		build(provider.StateRunning, provider.StateRunning, provider.StateRunning),
		build(provider.StateRunning, provider.StateRunning, provider.StateRunning),
		build(provider.StateSuccess, provider.StateSuccess, provider.StateSuccess),
	}}
	s := newService(t, p)
	events := collect(s.Watch(bg, "PROJ-P-1"))

	var kinds []string
	for _, e := range events {
		kinds = append(kinds, string(e.Type)+":"+string(e.State))
	}
	assert.Equal(t, []string{
		"state:queued",
		"state:running", "stage:running", "job:running",
		"state:success", "stage:success", "job:success",
		"done:success",
	}, kinds)
	assert.Equal(t, "Build", events[2].Name)
	assert.Equal(t, "PROJ-P-JOB1-1", events[3].Key)
	assert.Equal(t, provider.StateSuccess, events[len(events)-1].Build.State)

	clock := s.Clock.(*fakeClock)
	assert.Equal(t, []time.Duration{2 * time.Second, 3 * time.Second, 4500 * time.Millisecond, 2 * time.Second, 3 * time.Second}, clock.Waits())
}

func TestWatchBackoffCapsAtTenSeconds(t *testing.T) {
	p := fakeBamboo()
	var seq []provider.Build
	for i := 0; i < 8; i++ {
		seq = append(seq, provider.Build{Key: "PROJ-P-1", State: provider.StateQueued})
	}
	seq = append(seq, provider.Build{Key: "PROJ-P-1", State: provider.StateFailed})
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": seq}
	s := newService(t, p)
	events := collect(s.Watch(bg, "PROJ-P-1"))
	assert.Equal(t, EventDone, events[len(events)-1].Type)
	waits := s.Clock.(*fakeClock).Waits()
	assert.Equal(t, 6750*time.Millisecond, waits[3])
	for _, w := range waits[4:] {
		assert.Equal(t, 10*time.Second, w)
	}
}

func TestWatchFinishedBuildEmitsStateThenDone(t *testing.T) {
	p := fakeBamboo()
	s := newService(t, p)
	events := collect(s.Watch(bg, "PROJ-BUILD-482"))
	require.Len(t, events, 2)
	assert.Equal(t, EventState, events[0].Type)
	assert.Equal(t, EventDone, events[1].Type)
	assert.Empty(t, s.Clock.(*fakeClock).Waits())
}

func TestWatchErrorEndsWithErrorEvent(t *testing.T) {
	s := newService(t, fakeBamboo())
	events := collect(s.Watch(bg, "PROJ-NOPE-1"))
	require.Len(t, events, 1)
	assert.Equal(t, EventError, events[0].Type)
	assert.True(t, errors.Is(events[0].Err, errs.ErrNotFound))
}

func TestWatchCancelStopsWatchingButNotTheBuild(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {{Key: "PROJ-P-1", State: provider.StateRunning}}}
	s := newService(t, p)
	ctx, cancel := context.WithCancel(bg)
	ch := s.Watch(ctx, "PROJ-P-1")
	first := <-ch
	assert.Equal(t, EventState, first.Type)
	cancel()
	for e := range ch {
		assert.NotEqual(t, EventDone, e.Type)
	}
	assert.Empty(t, p.Stopped, "interrupting a watch never stops the build")
}

func ignoredRevision(p *fake.Provider) {
	p.TriggerResult = provider.Build{Key: "PROJ-BUILD-46", Number: 46, State: provider.StateQueued}
	p.TriggerErr = errs.Bamboof("Bamboo ignored the revision").Wrap(errs.ErrUnsupported)
}

func notFound() error {
	return errs.Bamboof("queued or running build not found").Wrap(errs.ErrNotFound)
}

func TestRunPassesTheRevision(t *testing.T) {
	p := fakeBamboo()
	p.TriggerResult = provider.Build{Key: "PROJ-BUILD-46", State: provider.StateQueued}
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	ref.Revision = "abc1234"
	_, err := s.Run(bg, ref, VarSet{})
	require.NoError(t, err)
	require.Len(t, p.Triggered, 1)
	assert.Equal(t, "abc1234", p.Triggered[0].Revision)
}

func TestRunStopsABuildWhoseRevisionWasIgnored(t *testing.T) {
	p := fakeBamboo()
	ignoredRevision(p)
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	ref.Revision = "abc1234"

	_, err := s.Run(bg, ref, VarSet{})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo ignored the revision; PROJ-BUILD-46 was stopped", e.What)
	assert.Equal(t, "this Bamboo does not support building a chosen revision over REST", e.Why)
	assert.Equal(t, "run without --revision, or use Run customised in Bamboo", e.Try)
	assert.Equal(t, []string{"PROJ-BUILD-46"}, p.Stopped)
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok, "a stopped build is not this repository's last build")
}

// A build that was just queued may have no job to stop yet.
func TestRunRetriesTheStopWhileTheBuildIsNotFound(t *testing.T) {
	p := fakeBamboo()
	ignoredRevision(p)
	p.StopErrs = []error{notFound(), notFound()}
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	ref.Revision = "abc1234"

	_, err := s.Run(bg, ref, VarSet{})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "Bamboo ignored the revision; PROJ-BUILD-46 was stopped", e.What)
	assert.Len(t, p.Stopped, 3)
}

func TestRunGivesUpStoppingAfterThirtySeconds(t *testing.T) {
	p := fakeBamboo()
	ignoredRevision(p)
	p.StopErr = notFound()
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	ref.Revision = "abc1234"
	start := s.Clock.Now()

	_, err := s.Run(bg, ref, VarSet{})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "Bamboo ignored the revision; PROJ-BUILD-46 is building the newest commit", e.What)
	assert.Equal(t, "bam build cancel PROJ-BUILD-46", e.Try)
	assert.Equal(t, 30*time.Second, s.Clock.Now().Sub(start))
	assert.Len(t, p.Stopped, 16, "one try at once, then one every 2s for 30s")
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok)
}

// TestRunDoesNotRetryAStopThatCannotWork: only "not found" is worth waiting
// for; an unsupported stop fails once, immediately.
func TestRunDoesNotRetryAStopThatCannotWork(t *testing.T) {
	p := fakeBamboo()
	ignoredRevision(p)
	p.StopErr = errs.Bamboof("stopping builds is not supported").Wrap(errs.ErrUnsupported)
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	ref.Revision = "abc1234"

	_, err := s.Run(bg, ref, VarSet{})
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "Bamboo ignored the revision; PROJ-BUILD-46 is building the newest commit", e.What)
	assert.Len(t, p.Stopped, 1)
	assert.True(t, errors.Is(err, errs.ErrUnsupported), "the stop's cause must still be visible, e.g. for --debug")
}

// Without a revision, an unsupported error from Trigger is an ordinary failure.
func TestRunWithoutRevisionDoesNotStopOnUnsupported(t *testing.T) {
	p := fakeBamboo()
	ignoredRevision(p)
	s := newService(t, p)
	ref, _ := s.ResolvePlan(bg, "PROJ-BUILD", "")
	_, err := s.Run(bg, ref, VarSet{})
	require.Error(t, err)
	assert.Empty(t, p.Stopped)
}

func TestBuildRevision(t *testing.T) {
	p := fakeBamboo()
	p.History["PROJ-BUILD"][0].Revision = "9b30b9aba03a4f45881629b03531006b89c1e0e7"
	s := newService(t, p)

	rev, err := s.BuildRevision(bg, "PROJ-BUILD-482")
	require.NoError(t, err)
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", rev)
}

func TestBuildRevisionOfABuildWithoutOne(t *testing.T) {
	s := newService(t, fakeBamboo())
	rev, err := s.BuildRevision(bg, "PROJ-BUILD-482")
	assert.Equal(t, "", rev)
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, "PROJ-BUILD-482 has no revision to reuse", e.What)
	assert.Equal(t, "pass a commit with --revision", e.Try)
}

func TestBuildRevisionPassesAReadErrorThrough(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.BuildRevision(bg, "PROJ-BUILD-999")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}
