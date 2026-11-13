package app

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

// queuedBuild is a queued build whose one queued job is PROJ-BUILD-JOB1.
func queuedBuild() provider.Build {
	return provider.Build{Key: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD", State: provider.StateQueued,
		Stages: []provider.Stage{{Name: "Build", State: provider.StateQueued, Jobs: []provider.Job{
			{Key: "PROJ-BUILD-JOB1-483", Name: "Compile", State: provider.StateQueued}}}}}
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
	// Bamboo answers the jobs only of the agents it lists, so every agent
	// named below is in the list.
	pool := append([]provider.Agent{{Name: "a", Online: true, Enabled: true, Busy: true},
		{Name: "b", Online: true, Enabled: true, Busy: true}, {Name: "c"}}, linuxAgents...)
	cases := []struct {
		name     string
		possible map[string][]string
		ahead    int
		want     provider.Why
	}{
		{"no agent matches the job's requirements", map[string][]string{"PROJ-BUILD-JOB1": {}}, 0,
			provider.Why{Case: provider.WhyNoAgent, AnyAgent: ptr(false)}},
		{"agents can run it and all are busy or away", map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "linux-2"}}, 0,
			provider.Why{Case: provider.WhyBusy, AnyAgent: ptr(true), Count: 2, Agents: []string{"linux-1", "linux-2"}}},
		{"an agent is free and it is next", map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-1"}}, 0,
			provider.Why{Case: provider.WhyNext, AnyAgent: ptr(true), Count: 2, Agents: []string{"linux-1", "win-1"}}},
		{"an agent is free but others come first", map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}}, 2,
			provider.Why{Case: provider.WhyAhead, AnyAgent: ptr(true), Count: 1, Agents: []string{"win-1"}, Ahead: 2}},
		{"more than three agents are counted, not named", map[string][]string{"PROJ-BUILD-JOB1": {"a", "b", "c", "linux-1"}}, 0,
			provider.Why{Case: provider.WhyBusy, AnyAgent: ptr(true), Count: 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := queuedBuild()
			svc, _ := whyService(t, b, pool, c.possible)
			got := svc.WhyWaits(context.Background(), b, c.ahead)
			require.NotNil(t, got)
			assert.Equal(t, c.want, *got)
		})
	}
}

func TestWhyWithoutTheAgentListDoesNotAskWhichJobsAgentsRun(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, nil, map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-1"}})
	p.AgentsErr = errs.Authf("403: the agent list needs an administrator")
	assert.Nil(t, svc.WhyWaits(context.Background(), b, 0))
	assert.Zero(t, p.AgentJobsCalls)
}

func TestWhyWithOnlyTheAgentListNamesBusyAgentsButNotNext(t *testing.T) {
	b := queuedBuild()
	allBusy := []provider.Agent{{Name: "linux-1", Online: true, Enabled: true, Busy: true}, {Name: "linux-2"}}
	svc, _ := whyService(t, b, allBusy, nil)
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyBusy, got.Case)
	assert.Zero(t, got.Count)

	// An idle agent may not match the job, and without the possible agents bam cannot tell.
	svc, _ = whyService(t, b, linuxAgents, nil)
	assert.Nil(t, svc.WhyWaits(context.Background(), b, 0))
}

func TestWhyWithNoSourceSaysNothing(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, nil, nil)
	p.AgentsErr = errs.Authf("403")
	assert.Nil(t, svc.WhyWaits(context.Background(), b, 0))
}

func TestWhyLooksOnlyAtTheJobsOfTheCurrentStage(t *testing.T) {
	b := queuedBuild()
	b.Stages = append(b.Stages, provider.Stage{Name: "Deploy", Jobs: []provider.Job{
		{Key: "PROJ-BUILD-JOB2-483", State: provider.StateQueued}}})
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}, "PROJ-BUILD-JOB2": {}})
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNext, got.Case)
}

func TestWhyReportsTheMostBlockedJob(t *testing.T) {
	b := queuedBuild()
	b.Stages[0].Jobs = append(b.Stages[0].Jobs, provider.Job{Key: "PROJ-BUILD-JOB2-483", State: provider.StateQueued})
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}, "PROJ-BUILD-JOB2": {}})
	got := svc.WhyWaits(context.Background(), b, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNoAgent, got.Case)
}

func TestWhyAsksEachSourceOncePerPoll(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	for range 3 {
		svc.WhyWaits(context.Background(), b, 0)
	}
	assert.Equal(t, 1, p.AgentJobsCalls)
}

func TestBuildWithQueueSaysWhyAndPlace(t *testing.T) {
	b := queuedBuild()
	svc, _ := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	got, err := svc.BuildWithQueue(context.Background(), b.Key)
	require.NoError(t, err)
	assert.Equal(t, 2, got.QueuePosition)
	require.NotNil(t, got.Why)
	assert.Equal(t, provider.WhyAhead, got.Why.Case)
	assert.Equal(t, 1, got.Why.Ahead)
}

func TestQueueRowsSayWhyEachBuildWaits(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {}})
	p.Sequences["PROJ-OTHER-1"] = []provider.Build{{Key: "PROJ-OTHER-1", State: provider.StateQueued}}
	rows, err := svc.QueueRows(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Nil(t, rows[0].Why)
	require.NotNil(t, rows[1].Why)
	assert.Equal(t, provider.WhyNoAgent, rows[1].Why.Case)
}

func TestWhyIsNotAskedForABuildThatIsNotQueued(t *testing.T) {
	b := queuedBuild()
	b.State = provider.StateRunning
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	assert.Nil(t, svc.WhyWaits(context.Background(), b, 0))
	assert.Zero(t, p.AgentJobsCalls)
}

func TestWhyAsksOnceWhenBuildsAskAtTheSameTime(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { svc.WhyWaits(context.Background(), b, 0) })
	}
	wg.Wait()
	assert.Equal(t, 1, p.AgentJobsCalls)
}

func TestTreeSaysWhyWhenTheQueueIsRefusedAsBuildShowDoes(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"linux-1"}})
	p.QueueErr = errs.Authf("403")
	shown, err := svc.BuildWithQueue(context.Background(), b.Key)
	require.NoError(t, err)
	require.NotNil(t, shown.Why)

	root := &TreeNode{Build: b}
	svc.markQueued(context.Background(), root)
	require.NotNil(t, root.Build.Why)
	assert.Equal(t, *shown.Why, *root.Build.Why)
	assert.Equal(t, provider.WhyBusy, root.Build.Why.Case)
	assert.Zero(t, root.Build.QueuePosition)
}

// Intent (BAM-43): on a server with hundreds of agents, bam asks Bamboo which
// jobs an agent can run rarely and for few agents: each agent's answer is
// kept for an hour, between runs too, and only an agent bam has no answer for
// is read again. Bamboo answers one agent per request.

func advanceClock(svc *Service, d time.Duration) { <-svc.Clock.After(d) }

func TestWhyKeepsWhatEachAgentCanRunForAnHour(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	ctx := context.Background()
	want := svc.WhyWaits(ctx, b, 0)
	require.NotNil(t, want)
	for range 3 { // a board left open, a watch or another bam queue within the hour
		advanceClock(svc, 15*time.Minute)
		assert.Equal(t, want, svc.WhyWaits(ctx, b, 0))
	}
	assert.Equal(t, [][]string{{"linux-1", "linux-2", "win-1"}}, p.AgentJobsAsked)

	advanceClock(svc, 15*time.Minute) // an hour since the agents were read
	svc.WhyWaits(ctx, b, 0)
	assert.Equal(t, [][]string{{"linux-1", "linux-2", "win-1"}, {"linux-1", "linux-2", "win-1"}}, p.AgentJobsAsked)
}

func TestWhyReadsOnlyTheAgentsItHasNoAnswerFor(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-2"}})
	ctx := context.Background()
	svc.WhyWaits(ctx, b, 0)

	p.AgentList = append(slices.Clone(linuxAgents), provider.Agent{Name: "win-2", Online: true, Enabled: true})
	advanceClock(svc, time.Minute) // the board reads the agent list again
	got := svc.WhyWaits(ctx, b, 0)
	assert.Equal(t, [][]string{{"linux-1", "linux-2", "win-1"}, {"win-2"}}, p.AgentJobsAsked)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNext, got.Case, "the new agent is free and can run it")
	assert.Equal(t, []string{"linux-1", "win-2"}, got.Agents)
}

func TestWhyKeepsTheAnswerBetweenRuns(t *testing.T) {
	b := queuedBuild()
	possible := map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "win-1"}}
	store := &AgentJobStore{Dir: t.TempDir()}
	ctx := context.Background()
	run := func(origin string, after time.Duration) (*provider.Why, *fake.Provider) {
		svc, p := whyService(t, b, linuxAgents, possible)
		svc.AgentJobs, svc.Origin = store, origin
		advanceClock(svc, after)
		return svc.WhyWaits(ctx, b, 0), p
	}
	want, first := run("https://bamboo.example.com", 0)
	require.NotNil(t, want)
	assert.Equal(t, []string{"linux-1", "win-1"}, want.Agents)
	assert.Equal(t, 1, first.AgentJobsCalls)

	got, again := run("https://bamboo.example.com", 50*time.Minute) // a second bam queue
	assert.Equal(t, want, got)
	assert.Zero(t, again.AgentJobsCalls)

	_, other := run("https://bamboo.lab.example", 0)
	assert.Equal(t, 1, other.AgentJobsCalls, "another server's agents are read from that server")

	_, later := run("https://bamboo.example.com", time.Hour)
	assert.Equal(t, 1, later.AgentJobsCalls, "an answer an hour old is read again")
}

func TestWhySaysLessOfAJobQueuedAfterTheAgentsWereRead(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, map[string][]string{"PROJ-BUILD-JOB1": {"win-1"}})
	ctx := context.Background()
	read := svc.Clock.Now()
	require.NotNil(t, svc.WhyWaits(ctx, b, 0))

	// A plan created after the agents were read is in no agent's list yet.
	newPlan := func(queued time.Time) provider.Build {
		return provider.Build{Key: "PROJ-NEW-7", PlanKey: "PROJ-NEW", State: provider.StateQueued, QueuedAt: queued,
			Stages: []provider.Stage{{Name: "Build", State: provider.StateQueued, Jobs: []provider.Job{
				{Key: "PROJ-NEW-JOB1-7", State: provider.StateQueued, QueuedAt: queued}}}}}
	}
	advanceClock(svc, 10*time.Minute)
	queuedSince := newPlan(read.Add(5 * time.Minute))
	assert.Nil(t, svc.WhyWaits(ctx, queuedSince, 0), "an agent is free and may run it: bam cannot tell")

	queuedBefore := newPlan(read.Add(-time.Minute))
	got := svc.WhyWaits(ctx, queuedBefore, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNoAgent, got.Case, "the lists were read while it waited")

	p.AgentJobMap["win-1"] = append(p.AgentJobMap["win-1"], "PROJ-NEW-JOB1")
	advanceClock(svc, time.Hour)
	got = svc.WhyWaits(ctx, queuedSince, 0)
	require.NotNil(t, got)
	assert.Equal(t, provider.WhyNext, got.Case)
	assert.Equal(t, []string{"win-1"}, got.Agents)
}

func TestWhyWaitsFiveMinutesAfterAFailedReadOfAgentDetails(t *testing.T) {
	b := queuedBuild()
	svc, p := whyService(t, b, linuxAgents, nil)
	p.AgentJobsErr = errs.Bamboof("500 from bamboo.example.com")
	ctx := context.Background()
	svc.WhyWaits(ctx, b, 0)
	for range 19 { // the board refreshes every 15 seconds
		advanceClock(svc, 15*time.Second)
		svc.WhyWaits(ctx, b, 0)
	}
	assert.Equal(t, 1, p.AgentJobsCalls)
	advanceClock(svc, 15*time.Second)
	svc.WhyWaits(ctx, b, 0)
	assert.Equal(t, 2, p.AgentJobsCalls)
}
