package fake

import (
	"context"
	"errors"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBuildWalksSequenceAndRepeatsLast(t *testing.T) {
	f := &Provider{Sequences: map[string][]provider.Build{
		"PROJ-P-1": {{Key: "PROJ-P-1", State: provider.StateQueued}, {Key: "PROJ-P-1", State: provider.StateSuccess}},
	}}
	ctx := context.Background()
	b1, _ := f.GetBuild(ctx, "PROJ-P-1")
	b2, _ := f.GetBuild(ctx, "PROJ-P-1")
	b3, _ := f.GetBuild(ctx, "PROJ-P-1")
	assert.Equal(t, provider.StateQueued, b1.State)
	assert.Equal(t, provider.StateSuccess, b2.State)
	assert.Equal(t, provider.StateSuccess, b3.State)
	assert.Equal(t, 3, f.GetCalls("PROJ-P-1"))
}

func TestGetBuildUnknownIsNotFound(t *testing.T) {
	f := &Provider{}
	_, err := f.GetBuild(context.Background(), "PROJ-P-9")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrNotFound))
	assert.Equal(t, errs.KindBamboo, errs.KindOf(err))
}

func TestTriggerRecordsRequest(t *testing.T) {
	f := &Provider{TriggerResult: provider.Build{Key: "PROJ-P-2"}}
	b, err := f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-P", Variables: map[string]string{"a": "1"}})
	require.NoError(t, err)
	assert.Equal(t, "PROJ-P-2", b.Key)
	assert.Equal(t, "PROJ-P", f.Triggered[0].PlanKey)
}

func TestFetchLogFromOffset(t *testing.T) {
	f := &Provider{Logs: map[string][]string{"PROJ-P-JOB1-2": {"a", "b", "c"}}}
	c, err := f.FetchLog(context.Background(), "PROJ-P-JOB1-2", provider.LogOptions{Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, c.Lines)
	assert.Equal(t, 3, c.Next)
}

func TestTriggerAnswersPerPlan(t *testing.T) {
	boom := errors.New("boom")
	f := &Provider{
		TriggerResult:  provider.Build{Key: "X-1"},
		TriggerResults: map[string]provider.Build{"PROJ-A": {Key: "PROJ-A-1"}},
		TriggerErrs:    map[string]error{"PROJ-B": boom},
	}
	b, err := f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-A"})
	require.NoError(t, err)
	require.Equal(t, "PROJ-A-1", b.Key)
	_, err = f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-B"})
	require.ErrorIs(t, err, boom)
	b, err = f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-C"})
	require.NoError(t, err)
	require.Equal(t, "X-1", b.Key)
	require.Len(t, f.Triggered, 3)
}
