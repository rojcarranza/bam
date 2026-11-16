package app

import (
	"context"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P1): the board counts agents, lists the queue with time
// waited, reads each pinned pipeline, and puts what needs attention in
// Needs you by the fixed limits.
func boardBamboo(now time.Time) *fake.Provider {
	p := fakeBamboo()
	p.Info = provider.ServerInfo{Version: "12.1.8"}
	p.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "linux-2", Type: "REMOTE", Online: false, Enabled: true},
		{ID: 3, Name: "linux-3", Type: "REMOTE", Online: true, Enabled: false},
	}
	p.QueueEntries = []provider.QueueEntry{
		{Key: "PROJ-BUILD-500", PlanKey: "PROJ-BUILD", Number: 500, Reason: "Manual build"},
		{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, Reason: "Code has changed"},
	}
	p.Sequences = map[string][]provider.Build{
		"PROJ-BUILD-500": {{Key: "PROJ-BUILD-500", State: provider.StateQueued, QueuedAt: now.Add(-7 * time.Minute)}},
		"PROJ-PROV-9":    {{Key: "PROJ-PROV-9", State: provider.StateQueued}},
	}
	return p
}

func pins(g string, pin ...string) []config.PinGroup { return []config.PinGroup{{Name: g, Pins: pin}} }

// Intent (BAM-101 P2): the board counts the queue from the one list and reads
// no queued build; a wait is news only for a pinned pipeline's build.
func TestBoardCountsAgentsAndTheQueueWithoutReadingQueuedBuilds(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	p := boardBamboo(now)
	svc.P = p
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, "12.1.8", b.Version)
	require.NotNil(t, b.Agents)
	assert.Equal(t, 3, b.Agents.Total)
	assert.Equal(t, 1, b.Agents.Online)
	assert.Equal(t, 1, b.Agents.Busy)
	assert.Equal(t, 1, b.Agents.Offline)
	assert.Equal(t, 1, b.Agents.Disabled)
	require.Len(t, b.Agents.Away, 2)

	assert.True(t, b.QueueOK)
	assert.Equal(t, 2, b.QueueLength)
	assert.Zero(t, p.GetCalls("PROJ-BUILD-500"), "the board reads no queued build")
	assert.Zero(t, p.GetCalls("PROJ-PROV-9"))
	assert.Equal(t, []Need{{Kind: "agent_offline", Text: "agent linux-2 is offline"}}, b.Needs,
		"a build waiting for an agent is news only when a pinned pipeline's build waits")
}

func TestNeedsYouSaysAPinnedBuildWaitedOverFiveMinutes(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	p := boardBamboo(now)
	p.History["PROJ-BUILD"] = []provider.Build{
		{Key: "PROJ-BUILD-500", Number: 500, State: provider.StateQueued, QueuedAt: now.Add(-12 * time.Minute)},
		{Key: "PROJ-BUILD-499", Number: 499, State: provider.StateSuccess, Duration: time.Minute, FinishedAt: now.Add(-time.Hour)},
	}
	p.History["PROJ-PROV12"] = []provider.Build{{Key: "PROJ-PROV12-9", Number: 9, State: provider.StateQueued, QueuedAt: now.Add(-3 * time.Minute)}}
	svc.P = p
	b, err := svc.Board(context.Background(), pins("integration", "PROJ-BUILD", "PROJ-PROV@develop"))
	require.NoError(t, err)
	assert.Contains(t, b.Needs, Need{Kind: "queue_wait", Text: "PROJ-BUILD-500 has waited 12m for an agent", Build: "PROJ-BUILD-500"})
	for _, n := range b.Needs {
		assert.NotContains(t, n.Text, "PROJ-PROV12-9", "under 5 minutes is not news")
		assert.NotContains(t, n.Text, "PROJ-PROV-9", "a build of an unpinned plan is not news")
	}
}

func TestNeedsYouReadsAPinnedQueuedBuildOnceWhenTheListGivesNoTime(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	p := boardBamboo(now)
	p.History["PROJ-BUILD"] = []provider.Build{{Key: "PROJ-BUILD-500", Number: 500, State: provider.StateQueued}}
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD-500": {{Key: "PROJ-BUILD-500", State: provider.StateQueued, QueuedAt: now.Add(-9 * time.Minute)}}}
	svc.P = p
	b, err := svc.Board(context.Background(), pins("integration", "PROJ-BUILD"))
	require.NoError(t, err)
	assert.Contains(t, b.Needs, Need{Kind: "queue_wait", Text: "PROJ-BUILD-500 has waited 9m for an agent", Build: "PROJ-BUILD-500"})
	assert.Equal(t, 1, p.GetCalls("PROJ-BUILD-500"))
}

func TestBoardSaysWhatBambooRefused(t *testing.T) {
	svc := newService(t, nil)
	p := boardBamboo(svc.Clock.Now())
	p.AgentsErr = errs.Authf("403: the agent list needs an administrator")
	p.QueueErr = errs.Bamboof("boom")
	svc.P = p
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err, "a refusal is a line on the board, not a failure")
	assert.Nil(t, b.Agents)
	assert.Contains(t, b.AgentsNote, "agent list is not shown")
	assert.False(t, b.QueueOK)
	assert.Contains(t, b.QueueNote, "queue could not be read")
	assert.Empty(t, b.Needs)
}

func TestBoardReadsPinsAndFlagsFailedAndSlowOnes(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	p := boardBamboo(now)
	fin := func(key string, n int, s provider.State, d time.Duration) provider.Build {
		return provider.Build{Key: key, Number: n, State: s, Duration: d, FinishedAt: now.Add(-time.Hour)}
	}
	p.History["PROJ-BUILD"] = []provider.Build{
		{Key: "PROJ-BUILD-484", Number: 484, State: provider.StateRunning, StartedAt: now.Add(-20 * time.Minute)},
		fin("PROJ-BUILD-483", 483, provider.StateSuccess, 10*time.Minute),
		fin("PROJ-BUILD-482", 482, provider.StateSuccess, 10*time.Minute),
		fin("PROJ-BUILD-481", 481, provider.StateSuccess, 10*time.Minute),
	}
	p.History["PROJ-PROV12"] = []provider.Build{fin("PROJ-PROV12-9", 9, provider.StateFailed, time.Minute)}
	svc.P = p

	b, err := svc.Board(context.Background(), pins("integration", "PROJ-BUILD", "PROJ-PROV@develop", "PROJ-PROV@nope"))
	require.NoError(t, err)
	require.Len(t, b.Groups, 1)
	got := b.Groups[0].Pins
	require.Len(t, got, 3)

	assert.Len(t, got[0].Results, 3)
	require.NotNil(t, got[0].Current)
	assert.Equal(t, "PROJ-BUILD-484", got[0].Current.Key)
	assert.Equal(t, 10*time.Minute, got[0].Last)
	assert.Equal(t, 10*time.Minute, got[0].Usual)

	assert.Equal(t, "PROJ-PROV12", got[1].PlanKey)
	assert.Equal(t, "develop", got[1].Branch)
	assert.Equal(t, provider.StateFailed, got[1].Results[0].State)

	assert.Error(t, got[2].Err, "an unreadable pin does not stop the others")

	kinds := []string{}
	for _, n := range b.Needs {
		kinds = append(kinds, n.Kind)
	}
	assert.Contains(t, kinds, "pin_failed")
	assert.Contains(t, kinds, "pin_slow")
}

func TestBoardDoesNotReadAPresetOfAnotherServer(t *testing.T) {
	svc := newService(t, nil)
	svc.P = boardBamboo(svc.Clock.Now())
	svc.Config().Project.Servers["lab"] = config.Server{URL: "http://bamboo.lab.example:8085"}
	svc.Config().Project.Targets["deploy-lab"] = config.Target{Plan: "PROJ-BUILD", Server: "lab"}
	svc.Config().Project.Targets["deploy-work"] = config.Target{Plan: "PROJ-BUILD", Server: "work"}
	svc.Config().Project.Targets["provision-lab2"] = config.Target{Plan: "LAB-PROV", Branch: "develop", Server: "lab"}
	b, err := svc.Board(context.Background(), pins("release", "deploy-lab", "deploy-work", "unknown-preset", "provision-lab2"))
	require.NoError(t, err)
	got := b.Groups[0].Pins
	require.Error(t, got[0].Err)
	assert.Contains(t, got[0].Err.Error(), "preset deploy-lab is on server lab; bam top shows one server, work")
	assert.Empty(t, got[0].Results, "the plan is not read from the wrong server")
	assert.NoError(t, got[1].Err, "a preset on the board's server is read")
	assert.Equal(t, "PROJ-BUILD", got[1].PlanKey)
	require.Error(t, got[2].Err, "a preset unknown here is an error on its pin alone")
	assert.Contains(t, got[2].Err.Error(), "unknown-preset")
	require.Error(t, got[3].Err)
	assert.Contains(t, got[3].Err.Error(), "preset provision-lab2 is on server lab; bam top shows one server, work",
		"the branch of another server's plan is not looked up on this one")
}

func TestBoardDoesNotFlagARunWithinHalfAgainOfUsual(t *testing.T) {
	svc := newService(t, nil)
	now := svc.Clock.Now()
	p := fakeBamboo()
	p.History["PROJ-BUILD"] = []provider.Build{
		{Key: "PROJ-BUILD-484", Number: 484, State: provider.StateRunning, StartedAt: now.Add(-14 * time.Minute)},
		{Key: "PROJ-BUILD-483", Number: 483, State: provider.StateSuccess, Duration: 10 * time.Minute},
		{Key: "PROJ-BUILD-482", Number: 482, State: provider.StateSuccess, Duration: 10 * time.Minute},
		{Key: "PROJ-BUILD-481", Number: 481, State: provider.StateSuccess, Duration: 10 * time.Minute},
	}
	svc.P = p
	b, err := svc.Board(context.Background(), pins("g", "PROJ-BUILD"))
	require.NoError(t, err)
	assert.Empty(t, b.Needs)
}

// Intent (pin by name): a pin written as a plan key carries its plan's name,
// kept across reads; a preset pin keeps the preset's name.
func TestBoardPinsCarryTheirPlansName(t *testing.T) {
	svc := newService(t, nil)
	svc.P = boardBamboo(svc.Clock.Now())
	b, err := svc.Board(context.Background(), pins("integration", "PROJ-BUILD", "PROJ-PROV@develop", "provision-lab"))
	require.NoError(t, err)
	got := b.Groups[0].Pins
	assert.Equal(t, "Build and test", got[0].Name)
	assert.Equal(t, "Build and test", got[0].Label())
	assert.Equal(t, "Provision lab", got[1].Name, "a branch pin carries the plan's name")
	assert.Equal(t, "Provision lab@develop", got[1].Label())
	assert.Empty(t, got[2].Name, "a preset pin keeps the preset's name")
	assert.Equal(t, "provision-lab", got[2].Label())

	again, err := svc.RefreshBoard(context.Background(), pins("integration", "PROJ-BUILD"), &b)
	require.NoError(t, err)
	assert.Equal(t, "Build and test", again.Groups[0].Pins[0].Name, "the quick refresh keeps the name")
}
