package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var boardAt = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func boardPlan() SetPlan {
	return SetPlan{Name: "release", Stages: [][]SetRunPlan{
		{{Name: "publish-specs", Target: "publish-specs"}},
		{{Name: "deploy[env=qa]", Target: "deploy"}, {Target: "smoke"}},
	}}
}

func TestSetBoardStartsPendingFromThePlan(t *testing.T) {
	b := NewSetBoard(boardPlan())
	require.Equal(t, "release", b.Name())
	st := b.Stages()
	require.Len(t, st, 2)
	require.Equal(t, "deploy[env=qa]", st[1][0].Name)
	require.Equal(t, "smoke", st[1][1].Name, "a plan run without a name is named after its preset")
	require.Empty(t, st[1][0].Build.Key)
}

func TestSetBoardSeedsFromARecord(t *testing.T) {
	b := NewSetBoard(boardPlan())
	b.Seed(SetRecord{ID: "release-1", Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", BuildKey: "PROJ-SPEC-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{{Target: "deploy", Name: "deploy[env=qa]", BuildKey: "OPS-DEP-9", State: provider.StateFailed, Reruns: 1},
			{Target: "smoke", Error: "queue full"}}},
	}})
	require.Equal(t, "release-1", b.ID())
	st := b.Stages()
	require.Equal(t, provider.StateFailed, st[1][0].Build.State)
	require.Equal(t, 1, st[1][0].Rerun)
	require.EqualError(t, st[1][1].Err, "queue full")
}

func TestSetBoardAppliesEvents(t *testing.T) {
	b := NewSetBoard(boardPlan())
	ev := func(typ SetEventType, run string, bld provider.Build, st provider.State) SetEvent {
		return SetEvent{Type: typ, ID: "release-1", Set: "release", Stage: 1, Target: "deploy", Run: run, Build: bld, State: st}
	}
	b.Apply(ev(SetRunTriggered, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, ""), boardAt)
	inner := Event{Type: EventState, Progress: provider.Progress{Valid: true, Percent: 0.5}}
	e := ev(SetRunEvent, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateRunning}, "")
	e.Event = &inner
	b.Apply(e, boardAt)
	row := b.Stages()[1][0]
	require.Equal(t, provider.StateRunning, row.Build.State)
	require.InDelta(t, 0.5, row.Progress.Percent, 1e-9)
	require.Equal(t, boardAt, row.At)

	done := ev(SetRunDone, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9"}, provider.StateFailed)
	done.Err = errs.Bamboof("boom")
	b.Apply(done, boardAt)
	row = b.Stages()[1][0]
	require.Equal(t, provider.StateFailed, row.Build.State)
	require.False(t, row.Progress.Valid)
	require.Error(t, row.Err)

	restart := ev(SetRunTriggered, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	restart.Rerun = 2
	b.Apply(restart, boardAt)
	require.Equal(t, 2, b.Stages()[1][0].Rerun)
	require.NoError(t, b.Stages()[1][0].Err)

	require.Nil(t, b.Final())
	b.Apply(SetEvent{Type: SetDone, ID: "release-1", Stage: 1, State: provider.StateSuccess}, boardAt)
	require.Equal(t, SetDone, b.Final().Type)
}

func TestSetBoardStagesAreCopies(t *testing.T) {
	b := NewSetBoard(boardPlan())
	st := b.Stages()
	st[0][0].Name = "changed"
	require.Equal(t, "publish-specs", b.Stages()[0][0].Name)
}
