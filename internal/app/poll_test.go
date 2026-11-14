package app

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
)

// droppy is a provider whose GetBuild fails on the listed call numbers
// (1-based) and otherwise answers as the fake does: a dropped connection
// in the middle of a watch.
type droppy struct {
	*fake.Provider
	mu    sync.Mutex
	calls int
	drop  map[int]bool
}

func (d *droppy) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	d.mu.Lock()
	d.calls++
	dropped := d.drop[d.calls]
	d.mu.Unlock()
	if dropped {
		return provider.Build{}, errs.Bamboof("connection dropped")
	}
	return d.Provider.GetBuild(ctx, key)
}

func dropping(p *fake.Provider, calls ...int) *droppy {
	d := &droppy{Provider: p, drop: map[int]bool{}}
	for _, c := range calls {
		d.drop[c] = true
	}
	return d
}

func droppyService(t *testing.T, p *fake.Provider, calls ...int) *Service {
	t.Helper()
	s := newService(t, p)
	s.P = dropping(p, calls...)
	return s
}

func runningThenDone(key string) *fake.Provider {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{key: {
		{Key: key, State: provider.StateRunning},
		{Key: key, State: provider.StateSuccess},
	}}
	return p
}

// Defect: one dropped connection ended bam watch with an error event, which
// the CLI turns into exit code 5.
func TestWatchRetriesOneFailedPoll(t *testing.T) {
	s := droppyService(t, runningThenDone("PROJ-P-1"), 2)
	events := collect(s.Watch(bg, "PROJ-P-1"))
	last := events[len(events)-1]
	require.Equal(t, EventDone, last.Type)
	assert.Equal(t, provider.StateSuccess, last.State)
	for _, e := range events {
		assert.NotEqual(t, EventError, e.Type)
	}
}

func TestWatchEndsAfterThreeFailedPollsInARow(t *testing.T) {
	s := droppyService(t, runningThenDone("PROJ-P-1"), 2, 3, 4)
	events := collect(s.Watch(bg, "PROJ-P-1"))
	last := events[len(events)-1]
	require.Equal(t, EventError, last.Type)
	assert.ErrorContains(t, last.Err, "connection dropped")
}

func TestWatchCountsOnlyFailuresInARow(t *testing.T) {
	// Two failures, a good poll, two more failures: no run of three.
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-P-1": {
		{Key: "PROJ-P-1", State: provider.StateRunning},
		{Key: "PROJ-P-1", State: provider.StateRunning},
		{Key: "PROJ-P-1", State: provider.StateSuccess},
	}}
	s := droppyService(t, p, 2, 3, 5, 6)
	events := collect(s.Watch(bg, "PROJ-P-1"))
	require.Equal(t, EventDone, events[len(events)-1].Type)
}

// Defect: a set run recorded the run as failed on one failed poll and never
// started the later stages, while Bamboo still built.
func TestRunSetSurvivesOneFailedPoll(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.P = dropping(fp, 2)
	id, evs := runSet(t, svc)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetSuccessful, rec.Status)
	require.Len(t, fp.Triggered, 2, "the later stage still starts")
}

func TestFollowLogRetriesAFailedPollWithoutRepeatingLines(t *testing.T) {
	p := logsProvider()
	running := failedBuild()
	running.State = provider.StateRunning
	running.Stages[1].Jobs[1].State = provider.StateRunning
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD12-44": {running, failedBuild()}}
	// Call 1 is the first build read; call 2 drops; call 3 reads the finished build.
	s := droppyService(t, p, 2)
	var got []string
	err := s.FollowLog(bg, "PROJ-BUILD12-44", running.Stages[1].Jobs[1], 0, func(l []string) { got = append(got, l...) })
	require.NoError(t, err)
	assert.Equal(t, []string{"i1", "i2", "i3"}, got)
}

func TestFollowLogsRetriesAFailedPoll(t *testing.T) {
	p := logsProvider()
	running := failedBuild()
	running.State = provider.StateRunning
	running.Stages[1].Jobs[1].State = provider.StateRunning
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD12-44": {running, running, failedBuild()}}
	s := droppyService(t, p, 2)
	var got []string
	err := s.FollowLogs(bg, "PROJ-BUILD12-44", map[string]int{"PROJ-BUILD12-INT-44": 0}, false, func(e LogEvent) {
		got = append(got, e.Lines...)
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"i1", "i2", "i3"}, got)
}

func TestPollSession(t *testing.T) {
	boom := errs.Bamboof("boom")
	var first pollSession
	assert.False(t, first.fail(boom), "the first poll gets no retry")
	assert.Equal(t, pollEnded, first.phase)

	var s pollSession
	s.ok()
	assert.True(t, s.fail(boom))
	assert.Equal(t, pollRetrying, s.phase)
	s.ok()
	assert.Equal(t, pollLive, s.phase)
	assert.True(t, s.fail(boom))
	assert.True(t, s.fail(boom))
	assert.False(t, s.fail(boom), "pollTries in a row end it")
	assert.False(t, s.fail(boom), "an ended session stays ended")

	var perm pollSession
	perm.ok()
	assert.False(t, perm.fail(errs.New(errs.KindAuth, "refused")), "a refused token is not retried")
}
