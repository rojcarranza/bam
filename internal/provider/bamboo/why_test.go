package bamboo

import (
	"errors"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const possiblePath = "GET /rest/api/latest/config/job/PROJ-BUILD-JOB1/agent-assignment/possible-agent-assignment"

func TestQueuedJobsCarryBambooAnswerToHasExecutableAgents(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD-483?expand=" + buildExpand: {fixture: "result_queued_job.json"}})
	b, err := c.GetBuild(ctx, "PROJ-BUILD-483")
	require.NoError(t, err)
	jobs := b.Stages[0].Jobs
	require.Len(t, jobs, 2)
	require.NotNil(t, jobs[0].AnyAgent)
	assert.True(t, *jobs[0].AnyAgent)
	require.NotNil(t, jobs[1].AnyAgent)
	assert.False(t, *jobs[1].AnyAgent)
}

func TestPossibleAgentsNamesOnlyTheOnesWhoseCapabilitiesMatch(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{possiblePath: {fixture: "possible_agents.json"}})
	saved := knownCaps(c, Capabilities{})
	names, err := c.PossibleAgents(ctx, "PROJ-BUILD-JOB1")
	require.NoError(t, err)
	assert.Equal(t, []string{"linux-1", "linux-2"}, names)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].PossibleAgents)
}

func TestPossibleAgentsRefusalIsLearnedOnceAndNeverAsksAgain(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{possiblePath: {status: 403, body: `{"message":"forbidden"}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.PossibleAgents(ctx, "PROJ-BUILD-JOB1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].PossibleAgents)
	before := len(rec.all())
	_, err = c.PossibleAgents(ctx, "PROJ-BUILD-JOB1")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "a learned no does not ask again")
}

func TestPossibleAgentsOfAnUnknownJobIsRefusedToo(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{})
	_, err := c.PossibleAgents(ctx, "PROJ-BUILD-JOB1")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
}

func TestAgentListRefusalIsLearnedForTheWhyCheck(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/agent": {status: 403, body: `{"message":"forbidden"}`}})
	saved := knownCaps(c, Capabilities{})
	assert.False(t, c.CannotListAgents(ctx))
	_, err := c.Agents(ctx)
	require.Error(t, err)
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Agents)
	assert.True(t, c.CannotListAgents(ctx))
}

func TestAgentListSuccessIsLearned(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/agent": {fixture: "agents.json"}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.Agents(ctx)
	require.NoError(t, err)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Agents)
	assert.False(t, c.CannotListAgents(ctx))
}

var _ provider.AgentListChecker = (*Client)(nil)
