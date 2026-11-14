package app

import (
	"context"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (issue 217, BAM-34 P6): an agent is found by its whole name; one
// already in the wanted state is not sent to Bamboo.
func TestFindAndSwitchAgent(t *testing.T) {
	p := fakeBamboo()
	p.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Enabled: true},
		{ID: 2, Name: "linux-2"},
		{ID: 3, Name: "linux-10", Enabled: true},
		{ID: 4, Name: "dup"}, {ID: 5, Name: "dup"},
	}
	svc := newService(t, p)
	ctx := context.Background()

	_, err := svc.FindAgent(ctx, "linux")
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.(*errs.Error).Try, "linux-1")
	_, err = svc.FindAgent(ctx, "dup")
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))

	a, err := svc.FindAgent(ctx, "linux-1")
	require.NoError(t, err)
	changed, err := svc.SetAgentEnabled(ctx, a, true)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Empty(t, p.AgentSwitched)

	changed, err = svc.SetAgentEnabled(ctx, a, false)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"disable linux-1"}, p.AgentSwitched)
}
