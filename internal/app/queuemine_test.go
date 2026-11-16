package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P3, "Your part of the queue, in plain words"): the board's
// queue lists the builds that wait for your agents, pipelines or projects,
// with their true place, and counts the rest; their waits and reasons are
// read first, wherever they sit.

func queueEntry(plan string, n int) provider.QueueEntry {
	return provider.QueueEntry{Key: fmt.Sprintf("%s-%d", plan, n), PlanKey: plan, Number: n, Reason: "Manual build"}
}

// queueBamboo is a server with one agent of yours that runs the plan
// MINE-PLAN and one of another team's that runs the others.
func queueBamboo(now time.Time, entries ...provider.QueueEntry) *fake.Provider {
	p := fakeBamboo()
	p.AgentList = []provider.Agent{
		{ID: 1, Name: "team-a-agent-0", Type: "REMOTE", Online: true, Enabled: true},
		{ID: 2, Name: "other-agent-0", Type: "REMOTE", Online: true, Enabled: true},
	}
	p.AgentJobMap = map[string][]string{
		"team-a-agent-0": {"MINE-PLAN-JOB1"},
		"other-agent-0":  {"OTHER-PLAN-JOB1", "PINNED-PLAN-JOB1", "TEAM-PLAN-JOB1"},
	}
	p.QueueEntries = entries
	p.Sequences = map[string][]provider.Build{}
	for _, e := range entries {
		p.Sequences[e.Key] = []provider.Build{{Key: e.Key, PlanKey: e.PlanKey, State: provider.StateQueued, QueuedAt: now.Add(-10 * time.Minute),
			Stages: []provider.Stage{{Jobs: []provider.Job{{Key: e.PlanKey + "-JOB1-" + fmt.Sprint(e.Number), State: provider.StateQueued}}}}}}
	}
	return p
}

func TestBoardQueueShowsOnlyWhatWaitsForYourAgentsPipelinesAndProjects(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	svc.Config().Project.Projects = []string{"TEAM"}
	svc.Config().Project.Agents = []string{"team-a-agent"}
	svc.P = queueBamboo(now,
		queueEntry("OTHER-PLAN", 1),  // another team's agent runs it
		queueEntry("MINE-PLAN", 2),   // one of your agents can run it
		queueEntry("PINNED-PLAN", 3), // its plan is pinned
		queueEntry("TEAM-PLAN", 4),   // its project is in projects:
		queueEntry("OTHER-PLAN", 5),
	)
	b, err := svc.Board(context.Background(), pins("g", "PINNED-PLAN"))
	require.NoError(t, err)

	require.True(t, b.QueueFiltered)
	var keys []string
	var places []int
	for _, r := range b.Queue {
		keys = append(keys, r.Key)
		places = append(places, r.Position)
	}
	assert.Equal(t, []string{"MINE-PLAN-2", "PINNED-PLAN-3", "TEAM-PLAN-4"}, keys)
	assert.Equal(t, []int{2, 3, 4}, places, "each build keeps its place in the whole queue")
	assert.Equal(t, 2, b.QueueOthers)
	assert.Equal(t, 10*time.Minute, b.Queue[0].Waited)
}

func TestBoardQueueSaysNoneOfYourAgentsCanRunABuildOfAPinnedPlan(t *testing.T) {
	svc := newService(t, nil)
	svc.Config().Project.Projects = nil
	svc.Config().Project.Agents = []string{"team-a-agent"}
	svc.P = queueBamboo(svc.Clock.Now(), queueEntry("PINNED-PLAN", 3))
	b, err := svc.Board(context.Background(), pins("g", "PINNED-PLAN"))
	require.NoError(t, err)
	require.Len(t, b.Queue, 1)
	require.NotNil(t, b.Queue[0].Why)
	assert.Equal(t, "none of your agents can run it", b.Queue[0].Why.Text())
}

func TestBoardQueueShowsEverythingForTheWholeServerOrWithNothingListed(t *testing.T) {
	entries := []provider.QueueEntry{queueEntry("OTHER-PLAN", 1), queueEntry("MINE-PLAN", 2)}

	svc := newService(t, nil)
	svc.Config().Project.Projects = nil // no agents, pins or projects
	svc.P = queueBamboo(svc.Clock.Now(), entries...)
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, b.QueueFiltered)
	assert.Len(t, b.Queue, 2)
	assert.Zero(t, b.QueueOthers)

	svc = newService(t, nil)
	svc.Config().Project.Projects = nil
	svc.Config().Project.Agents = []string{"team-a-agent"}
	svc.P = queueBamboo(svc.Clock.Now(), entries...)
	b, err = svc.Board(WholeServer(context.Background()), nil)
	require.NoError(t, err)
	assert.False(t, b.QueueFiltered, "a asks for the whole server")
	assert.Len(t, b.Queue, 2)
}

// When the server will not say which jobs an agent runs, a build bam cannot
// place is shown, not hidden.
func TestBoardQueueShowsABuildItCannotPlace(t *testing.T) {
	svc := newService(t, nil)
	svc.Config().Project.Projects = nil
	svc.Config().Project.Agents = []string{"team-a-agent"}
	p := queueBamboo(svc.Clock.Now(), queueEntry("OTHER-PLAN", 1))
	p.AgentJobMap = nil // Bamboo refuses the agents' job lists
	svc.P = p
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Len(t, b.Queue, 1)
}

// Waits are read for your builds first: with 30 of yours behind 40 of
// others, 25 of yours are read and none of the others.
func TestBoardQueueReadsYourWaitsFirstWhereverTheySit(t *testing.T) {
	svc := newService(t, nil)
	svc.Config().Project.Projects = nil
	svc.Config().Project.Agents = []string{"team-a-agent"}
	var entries []provider.QueueEntry
	for i := 1; i <= 40; i++ {
		entries = append(entries, queueEntry("OTHER-PLAN", i))
	}
	for i := 41; i <= 70; i++ {
		entries = append(entries, queueEntry("MINE-PLAN", i))
	}
	p := queueBamboo(svc.Clock.Now(), entries...)
	svc.P = p
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, b.Queue, 30)
	assert.Equal(t, 40, b.QueueOthers)
	read, dash := 0, 0
	for _, r := range b.Queue {
		if r.AtLeast {
			dash++
		} else {
			read++
		}
	}
	assert.Equal(t, waitReads, read)
	assert.Equal(t, 5, dash, "the five beyond the 25 read show a dash")
	for _, e := range entries[:40] {
		assert.Zero(t, p.GetCalls(e.Key), "%s waits for others: its record is not read", e.Key)
	}
}

func TestNeedsYouGroupsBuildsThatWaitForOneReason(t *testing.T) {
	long := 3 * time.Hour
	row := func(key string, w time.Duration, why *provider.Why) QueueRow {
		return QueueRow{QueueEntry: provider.QueueEntry{Key: key}, Waited: w, Why: why}
	}
	busy := func(agents ...string) *provider.Why {
		return &provider.Why{Case: provider.WhyBusy, Count: 2, Agents: agents}
	}
	got := needsQueue([]QueueRow{
		row("A-1", long, busy("x", "y")),
		row("B-2", 2*long, busy("z", "w")), // the same reason, whichever agents
		row("C-3", long, &provider.Why{Case: provider.WhyNoAgent, Mine: true}),
		row("D-4", time.Minute, busy()), // under the limit
		row("E-5", 0, nil),              // wait not read
	})
	require.Len(t, got, 2)
	assert.Equal(t, Need{Kind: "queue_wait", Build: "B-2",
		Text: "2 builds have waited over 5m in the queue, the longest B-2 for 6h00m: 2 agents can run it, all busy, offline or disabled"}, got[0])
	assert.Equal(t, Need{Kind: "queue_wait", Build: "C-3",
		Text: "C-3 has waited 3h00m in the queue: none of your agents can run it"}, got[1])
}

func TestLongWaitsReadAsDaysFrom48Hours(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		days string
		ok   bool
	}{
		{47*time.Hour + 59*time.Minute, "", false},
		{48 * time.Hour, "2d 0h", true},
		{1372*time.Hour + 36*time.Minute, "57d 4h", true},
	} {
		got, ok := LongWait(c.d)
		assert.Equal(t, c.ok, ok, c.d)
		assert.Equal(t, c.days, got, c.d)
	}
	assert.Equal(t, "47h59m", shortDuration(47*time.Hour+59*time.Minute))
	assert.Equal(t, "57d 4h", shortDuration(1372*time.Hour+36*time.Minute))
}

// An agent of yours that Bamboo lists before bam may ask again which jobs it
// runs runs nothing until then; it does not show the whole queue as yours.
func TestBoardQueueStaysYoursWhenANewAgentHasNoJobListYet(t *testing.T) {
	svc := newService(t, nil)
	svc.Config().Project.Projects = nil
	svc.Config().Project.Agents = []string{"team-a-agent"}
	p := queueBamboo(svc.Clock.Now(), queueEntry("OTHER-PLAN", 1), queueEntry("MINE-PLAN", 2))
	svc.P = p
	clock := svc.Clock.(*fakeClock)

	_, ok := svc.whyAgents(context.Background()) // the agent list is read first
	require.True(t, ok)
	clock.After(20 * time.Second)
	_, err := svc.Board(context.Background(), nil) // the job lists 20s later
	require.NoError(t, err)

	p.AgentList = append(p.AgentList, provider.Agent{ID: 3, Name: "team-a-agent-1", Type: "REMOTE", Online: true, Enabled: true})
	clock.After(15 * time.Second) // the agent list is read again, the job lists not yet
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.True(t, b.QueueFiltered)
	var keys []string
	for _, r := range b.Queue {
		keys = append(keys, r.Key)
	}
	assert.Equal(t, []string{"MINE-PLAN-2"}, keys)
	assert.Equal(t, 1, b.QueueOthers)
}
