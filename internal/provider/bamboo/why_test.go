package bamboo

import (
	"errors"
	"fmt"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P3): the facts that say why a build waits, read from what a
// real Bamboo 12.1.8 answered (testdata/recorded). Each source is learned
// once and a refusal never fails the command.

const agentDetailPath = "GET /rest/api/latest/agent/622593?expand=executableJobs[0:1000]"

// recordedAgent is the one agent in recorded/agents_one_remote.json.
var recordedAgent = []provider.Agent{{ID: 622593, Name: "linux-1"}}

func TestQueueNamesTheBuildBehindAWaitingJob(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/queue": {fixture: "recorded/queue_jobs_waiting.json"}})
	q, err := c.Queue(ctx)
	require.NoError(t, err)
	require.Len(t, q, 1)
	// Bamboo queues the job PROJ-NOCAP-SL-1 of the build PROJ-NOCAP-1.
	assert.Equal(t, provider.QueueEntry{Key: "PROJ-NOCAP-1", PlanKey: "PROJ-NOCAP", Number: 1, Reason: "Manual build"}, q[0])
}

func TestQueueHoldsOnePlaceForABuildWithSeveralWaitingJobs(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/queue": {body: `{"queuedBuilds":{"queuedBuild":[
		{"planKey":"PROJ-BUILD-JOB1","buildNumber":9,"buildResultKey":"PROJ-BUILD-JOB1-9"},
		{"planKey":"PROJ-BUILD-JOB2","buildNumber":9,"buildResultKey":"PROJ-BUILD-JOB2-9"},
		{"planKey":"PROJ-BUILD12-JOB1","buildNumber":3,"buildResultKey":"PROJ-BUILD12-JOB1-3"}]}}`}})
	q, err := c.Queue(ctx)
	require.NoError(t, err)
	require.Len(t, q, 2)
	assert.Equal(t, "PROJ-BUILD-9", q[0].Key)
	assert.Equal(t, "PROJ-BUILD12-3", q[1].Key)
}

func TestAgentJobsListsWhatEachAgentCanRun(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/agent": {fixture: "recorded/agents_one_remote.json"},
		agentDetailPath:              {fixture: "recorded/agent_detail_executable_jobs.json"},
	})
	saved := knownCaps(c, Capabilities{})
	as, err := c.Agents(ctx)
	require.NoError(t, err)
	require.Equal(t, recordedAgent[0].ID, as[0].ID)
	jobs, err := c.AgentJobs(ctx, as)
	require.NoError(t, err)
	// The agent can run the demo job; it cannot run the one that needs a capability it lacks.
	assert.Equal(t, map[string][]string{"linux-1": {"PROJ-HOLD-SL"}}, jobs)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].AgentJobs)
}

func TestAgentJobsReadsTheAgentsItIsGivenWithoutListingThemAgain(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{agentDetailPath: {fixture: "recorded/agent_detail_executable_jobs.json"}})
	knownCaps(c, Capabilities{})
	jobs, err := c.AgentJobs(ctx, recordedAgent)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"linux-1": {"PROJ-HOLD-SL"}}, jobs)
	assert.Len(t, rec.all(), 1)
}

func TestAgentJobsRefusalIsLearnedOnceAndNeverAsksAgain(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{agentDetailPath: {status: 403, body: `{"message":"forbidden"}`}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.AgentJobs(ctx, recordedAgent)
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].AgentJobs)
	before := len(rec.all())
	_, err = c.AgentJobs(ctx, recordedAgent)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "a learned no does not ask again")
}

func TestAgentJobsRefusedForOneAgentIsARefusal(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		agentDetailPath: {status: 403, body: `{"message":"forbidden"}`},
		"GET /rest/api/latest/agent/7?expand=executableJobs[0:1000]": {body: `{"executableJobs":{"size":0,"executableJobs":[]}}`},
	})
	knownCaps(c, Capabilities{})
	_, err := c.AgentJobs(ctx, append([]provider.Agent{{ID: 7, Name: "linux-2"}}, recordedAgent...))
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
}

func TestAgentJobsLeavesOutAnAgentRemovedSinceTheListWasRead(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{agentDetailPath: {fixture: "recorded/agent_detail_executable_jobs.json"}})
	saved := knownCaps(c, Capabilities{})
	gone := provider.Agent{ID: 9, Name: "elastic-9"} // its detail answers 404
	jobs, err := c.AgentJobs(ctx, append([]provider.Agent{gone}, recordedAgent...))
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"linux-1": {"PROJ-HOLD-SL"}}, jobs)
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].AgentJobs)
}

func TestAgentJobsWhenEveryAgentIsMissingIsLearnedAsRefused(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{})
	saved := knownCaps(c, Capabilities{})
	_, err := c.AgentJobs(ctx, recordedAgent)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, "no", (*saved)[len(*saved)-1].AgentJobs)
}

// Intent (BAM-43): a list Bamboo cut short stays short, so it is learned as a
// refusal and later runs read no agent's details only to be cut short again.
func TestAgentJobsWithACutShortListIsRefusedNotMisleading(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{
		agentDetailPath: {body: `{"executableJobs":{"size":3,"executableJobs":[{"key":{"key":"PROJ-HOLD-SL"}}]}}`},
	})
	saved := knownCaps(c, Capabilities{})
	_, err := c.AgentJobs(ctx, recordedAgent)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	require.NotEmpty(t, *saved)
	assert.Equal(t, "no", (*saved)[len(*saved)-1].AgentJobs)

	before := len(rec.all())
	_, err = c.AgentJobs(ctx, recordedAgent)
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Equal(t, before, len(rec.all()), "a learned no does not ask again")
}

// Intent (BAM-43): when agent details fail, bam stops reading the agents it
// has not read yet instead of asking a struggling server for every one.
func TestAgentJobsStopsAtTheFirstFailure(t *testing.T) {
	routes := map[string]*route{}
	var agents []provider.Agent
	for id := 1; id <= 60; id++ {
		routes[fmt.Sprintf("GET /rest/api/latest/agent/%d?expand=executableJobs[0:1000]", id)] = &route{status: 500, body: `{"message":"internal error"}`}
		agents = append(agents, provider.Agent{ID: id, Name: fmt.Sprintf("linux-%d", id)})
	}
	c, rec := newTestServer(t, routes)
	saved := knownCaps(c, Capabilities{})
	_, err := c.AgentJobs(ctx, agents)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errs.ErrUnsupported), "a failure is not a refusal")
	assert.LessOrEqual(t, len(rec.all()), agentReaders*maxAttempts, "only the agents being read when it failed")
	assert.Empty(t, *saved, "nothing is learned from a failure")
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
