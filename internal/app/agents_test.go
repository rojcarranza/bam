package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var starts = []string{"team-a-agent", "team-b-agent"}

func agent(name string, online, enabled, busy bool) provider.Agent {
	return provider.Agent{Name: name, Type: "REMOTE", Online: online, Enabled: enabled, Busy: busy}
}

func TestMineAgentsStatesAndOwnership(t *testing.T) {
	now := newClock().Now()
	m, _ := MineAgents([]provider.Agent{
		agent("team-a-agent-pool-0", true, true, true),
		agent("team-a-agent-pool-1", true, true, false),
		agent("team-a-agent-pool-2", true, false, false),  // disabled
		agent("team-a-agent-pool-3", false, true, false),  // down
		agent("team-a-agent-pool-4", false, false, false), // disabled wins over offline
		agent("Team-A-agent-pool-9", true, true, true),    // case-sensitive: not yours
		agent("other-agent-0", false, true, false),        // another team's
	}, starts, nil, now)
	assert.Equal(t, 1, m.Building)
	assert.Equal(t, 1, m.Idle)
	assert.Equal(t, 2, m.Disabled)
	assert.Equal(t, 1, m.Down)
	assert.Equal(t, 5, m.Total())
	require.Len(t, m.Pools, 1)
	assert.Equal(t, "team-a-agent-pool-", m.Pools[0].Name)
	assert.Equal(t, []string{"team-b-agent"}, m.NoMatch, "a start no agent matches is reported")
	assert.Len(t, m.Away, 3)
}

func TestMineAgentsIgnoreTheSuffixAndCountDuplicatesOnce(t *testing.T) {
	now := newClock().Now()
	m, keep := MineAgents([]provider.Agent{
		agent("team-a-agent-0 (2)", true, true, false),
		agent("team-a-agent-0", false, true, false), // the dead record of the same agent
		agent("team-a-agent-1", false, true, false),
		agent("team-a-agent-1 (2)", false, true, false), // both down: still once
	}, starts, nil, now)
	assert.Equal(t, 1, m.Idle, "the online record wins")
	assert.Equal(t, 1, m.Down)
	assert.Equal(t, 2, m.Total())
	assert.Equal(t, map[string]time.Time{"team-a-agent-0": now, "team-a-agent-1": now}, keep, "the suffix is not remembered")
}

func TestMineAgentsPoolsAreInNumberOrder(t *testing.T) {
	m, _ := MineAgents([]provider.Agent{
		agent("team-a-agent-pool-10", true, true, false),
		agent("team-a-agent-pool-2", true, true, false),
		agent("team-a-agent-pool-1", true, true, false),
		agent("team-b-agent", true, true, false), // no number: a pool of its own
	}, starts, nil, newClock().Now())
	require.Len(t, m.Pools, 2)
	var names []string
	for _, a := range m.Pools[0].Agents {
		names = append(names, a.Name)
	}
	assert.Equal(t, []string{"team-a-agent-pool-1", "team-a-agent-pool-2", "team-a-agent-pool-10"}, names)
	assert.Equal(t, "team-b-agent", m.Pools[1].Name)
}

func TestMineAgentsRememberOneBambooNoLongerLists(t *testing.T) {
	now := newClock().Now()
	seen := map[string]time.Time{"team-a-agent-pool-7": now.Add(-time.Hour)}
	m, keep := MineAgents([]provider.Agent{agent("team-a-agent-pool-0", true, true, false)}, starts, seen, now)
	assert.Equal(t, 1, m.Down)
	require.Len(t, m.Away, 1)
	assert.True(t, m.Away[0].Gone)
	assert.Equal(t, "team-a-agent-pool-7", m.Away[0].Name)
	assert.Equal(t, seen["team-a-agent-pool-7"], keep["team-a-agent-pool-7"], "the time last seen is kept, not renewed")
	assert.Equal(t, []Need{{Kind: "agent_down", Text: "agent team-a-agent-pool-7 is down: Bamboo no longer lists it"}}, needsAgents(m))
}

func TestMineAgentsForgetAfterThirtyDays(t *testing.T) {
	now := newClock().Now()
	seen := map[string]time.Time{
		"team-a-agent-0": now.Add(-AgentForget + time.Minute), // still remembered
		"team-a-agent-1": now.Add(-AgentForget),               // forgotten
	}
	m, keep := MineAgents(nil, starts, seen, now)
	assert.Equal(t, 1, m.Down)
	assert.Contains(t, keep, "team-a-agent-0")
	assert.NotContains(t, keep, "team-a-agent-1")
}

func TestNeedsYouRaisesYourDownAndDisabledAgentsOnly(t *testing.T) {
	p := fakeBamboo()
	p.AgentList = []provider.Agent{
		agent("team-a-agent-0", false, true, false), // yours, down
		agent("team-a-agent-1", true, false, false), // yours, disabled
		agent("team-a-agent-2", true, true, true),
		agent("other-agent-0", false, true, false), // another team's, down
	}
	svc := newService(t, p)
	svc.Cfg.Project.Agents = starts
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	var texts []string
	for _, n := range b.Needs {
		texts = append(texts, n.Text)
	}
	assert.Equal(t, []string{"agent team-a-agent-0 is down", "agent team-a-agent-1 is disabled"}, texts)
	assert.Equal(t, 4, b.Agents.Total, "the whole-server counts stay on the board")
}

func TestBoardWithoutAgentsSetKeepsTheOfflineLine(t *testing.T) {
	p := fakeBamboo()
	p.AgentList = []provider.Agent{agent("other-agent-0", false, true, false)}
	b, err := newService(t, p).Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, b.Mine)
	require.Len(t, b.Needs, 1)
	assert.Equal(t, "agent_offline", b.Needs[0].Kind)
}

func TestBoardRemembersAnAgentAcrossReadsAndForgetsItOnRequest(t *testing.T) {
	p := fakeBamboo()
	p.AgentList = []provider.Agent{agent("team-a-agent-0", true, true, false), agent("team-a-agent-1", true, true, false)}
	svc := newService(t, p)
	svc.Cfg.Project.Agents = starts
	_, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)

	p.AgentList = p.AgentList[:1] // Bamboo drops agent 1
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, b.Mine.Away, 1)
	assert.True(t, b.Mine.Away[0].Gone)
	assert.Equal(t, "team-a-agent-1", b.Mine.Away[0].Name)

	gone, err := svc.ForgetAgents([]string{"team-a-agent-1", "never-seen"})
	require.NoError(t, err)
	assert.Equal(t, []string{"team-a-agent-1"}, gone)
	b, err = svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, b.Mine.Away)

	svc.Clock.(*fakeClock).now = svc.Clock.Now().Add(AgentForget - time.Minute)
	p.AgentList = nil // every agent gone, a little under thirty days on
	b, err = svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, b.Mine.Down, "agent 0 was last seen within thirty days")
	svc.Clock.(*fakeClock).now = svc.Clock.Now().Add(2 * time.Minute)
	b, err = svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Zero(t, b.Mine.Total())
}

// With 201 agents of which 30 are yours, the board asks for the details of
// your 30 only; the whole-server view asks for all 201.
func TestBoardAsksAgentDetailsForYourAgentsOnly(t *testing.T) {
	b := queuedBuild()
	var all []provider.Agent
	for i := range 201 {
		name := fmt.Sprintf("other-agent-%d", i)
		if i < 30 {
			name = fmt.Sprintf("team-a-agent-%d", i)
		}
		all = append(all, agent(name, true, true, false))
	}
	svc, p := whyService(t, b, all, nil)
	svc.Cfg.Project.Agents = starts
	_, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, p.AgentJobsCalls)
	assert.Len(t, p.AgentJobsAsked[0], 30)

	svc2, p2 := whyService(t, b, all, nil)
	svc2.Cfg.Project.Agents = starts
	_, err = svc2.Board(WholeServer(context.Background()), nil)
	require.NoError(t, err)
	assert.Len(t, p2.AgentJobsAsked[0], 201)
}
