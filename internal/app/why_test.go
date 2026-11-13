package app

import (
	"context"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

// queuedBuild is a queued build whose one queued job is PROJ-BUILD-JOB1.
func queuedBuild(anyAgent *bool) provider.Build {
	return provider.Build{Key: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD", State: provider.StateQueued,
		Stages: []provider.Stage{{Name: "Build", State: provider.StateQueued, Jobs: []provider.Job{
			{Key: "PROJ-BUILD-JOB1-483", Name: "Compile", State: provider.StateQueued, AnyAgent: anyAgent}}}}}
}

func whyService(t *testing.T, b provider.Build, agents []provider.Agent, jobs map[string][]string) (*Service, *fake.Provider) {
	t.Helper()
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{b.Key: {b}}
	p.QueueEntries = []provider.QueueEntry{{Key: "PROJ-OTHER-1"}, {Key: b.Key}}
	p.AgentList = agents
	if jobs != nil { // jobs maps a job to the agents that can run it; the server reports the reverse
		p.AgentJobMap = map[string][]string{}
		for job, names := range jobs {
			for _, n := range names {
				p.AgentJobMap[n] = append(p.AgentJobMap[n], job)
			}
		}
	}
	return newService(t, p), p
}

var linuxAgents = []provider.Agent{
	{Name: "linux-1", Online: true, Enabled: true, Busy: true},
	{Name: "linux-2", Online: true, Enabled: false},
	{Name: "win-1", Online: true, Enabled: true},
}

func TestWhyNamesTheThreeCases(t *testing.T) {
	cases := []struct {
		name     string
		anyAgent *bool
		possible map[string][]string
		ahead    int
		want     provider.Why
	}{
		{"the server says no agent can run it", ptr(false), nil, 0,
			provider.Why{Case: provider.WhyNoAgent, AnyAgent: ptr(false)}},
		{"no agent matches the job's requirements", ptr(true), map[string][]string{"PROJ-BUILD-JOB1": {}}, 0,
			provider.Why{Case: provider.WhyNoAgent, AnyAgent: ptr(false)}},
		{"agents can run it and all are busy or away", ptr(true), map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "linux-2"}}, 0,
			provider.Why{Case: provider.WhyBusy, AnyAgent: ptr(true), Count: 2, Agents: []string{"linux-1", "linux-2"}}},
		{"an agent is free and it is next", ptr(true), map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-1"}}, 0,
			provider.Why{Case: provider.WhyNext, AnyAgent: ptr(true), Count: 2, Agents: []string{"linux-1", "win-1"}}},
		{"an agent is free but others come first", ptr(true), map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}}, 2,
			provider.Why{Case: provider.WhyAhead, AnyAgent: ptr(true), Count: 1, Agents: []string{"win-1"}, Ahead: 2}},
		{"more than three agents are counted, not named", ptr(true), map[string][]string{"PROJ-BUILD-JOB1": {"a", "b", "c", "linux-1"}}, 0,
			provider.Why{Case: provider.WhyBusy, AnyAgent: ptr(true), Count: 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := queuedBuild(c.anyAgent)
			svc, _ := whyService(t, b, linuxAgents, c.possible)
			got := svc.WhyWaits(context.Background(), b, c.ahead)
			require.NotNil(t, got)
			assert.Equal(t, c.want, *got)
		})
	}
}

func TestWhyWithoutTheAgentListStillCountsWhoCanRun(t *testing.T) {
	b := queuedBuild(ptr(true))
	svc, p := whyService(t, b, nil, map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-1"}})
	p.AgentsErr = errs.Authf("403: the agent list needs an administrator")
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.Why{AnyAgent: ptr(true), Count: 2, Agents: []string{"linux-1", "win-1"}}, *got)
}

func TestWhyWithOnlyTheAgentListNamesBusyAgentsButNotNext(t *testing.T) {
	b := queuedBuild(ptr(true))
	allBusy := []provider.Agent{{Name: "linux-1", Online: true, Enabled: true, Busy: true}, {Name: "linux-2"}}
	svc, _ := whyService(t, b, allBusy, nil)
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyBusy, got.Case)
	assert.Zero(t, got.Count)

	// An idle agent may not match the job, and without the possible agents bam cannot tell.
	svc, _ = whyService(t, b, linuxAgents, nil)
	got = svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Empty(t, got.Case)
}

func TestWhyWithNoSourceSaysNothing(t *testing.T) {
	b := queuedBuild(nil) // the server gave no answer for the job
	svc, p := whyService(t, b, nil, nil)
	p.AgentsErr = errs.Authf("403")
	assert.Nil(t, svc.WhyWaits(context.Background(), b, 0))
}

func TestWhyLooksOnlyAtTheJobsOfTheCurrentStage(t *testing.T) {
	b := queuedBuild(ptr(true))
	b.Stages = append(b.Stages, provider.Stage{Name: "Deploy", Jobs: []provider.Job{
		{Key: "PROJ-BUILD-JOB2-483", State: provider.StateQueued, AnyAgent: ptr(false)}}})
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNext, got.Case)
}

func TestWhyReportsTheMostBlockedJob(t *testing.T) {
	b := queuedBuild(ptr(true))
	b.Stages[0].Jobs = append(b.Stages[0].Jobs, provider.Job{Key: "PROJ-BUILD-JOB2-483", State: provider.StateQueued, AnyAgent: ptr(false)})
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNoAgent, got.Case)
}

func TestWhyAsksEachSourceOncePerPoll(t *testing.T) {
	b := queuedBuild(ptr(true))
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	for range 3 {
		svc.WhyWaits(context.Background(), b, 0)
	}
	assert.Equal(t, 1, p.AgentJobsCalls)
}

func TestBuildWithQueueSaysWhyAndPlace(t *testing.T) {
	b := queuedBuild(ptr(true))
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	got, err := svc.BuildWithQueue(context.Background(), b.Key)
	require.NoError(t, err)
	assert.Equal(t, 2, got.QueuePosition)
	require.NotNil(t, got.Why)
	assert.Equal(t, provider.WhyAhead, got.Why.Case)
	assert.Equal(t, 1, got.Why.Ahead)
}

func TestQueueRowsSayWhyEachBuildWaits(t *testing.T) {
	b := queuedBuild(ptr(false))
	svc, p := whyService(t, b, nil, nil)
	p.AgentsErr = errs.Authf("403")
	p.Sequences["PROJ-OTHER-1"] = []provider.Build{{Key: "PROJ-OTHER-1", State: provider.StateQueued}}
	rows, err := svc.QueueRows(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Nil(t, rows[0].Why)
	require.NotNil(t, rows[1].Why)
	assert.Equal(t, provider.WhyNoAgent, rows[1].Why.Case)
}
