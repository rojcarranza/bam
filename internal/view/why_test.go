package view

import (
	"testing"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
)

// Intent (BAM-34 P3): the words that say why a build waits.
func TestWhyText(t *testing.T) {
	yes := true
	for _, c := range []struct {
		w    *provider.Why
		want string
	}{
		{nil, ""},
		{&provider.Why{}, ""},
		{&provider.Why{Case: provider.WhyNoAgent}, "no agent can run it"},
		{&provider.Why{Case: provider.WhyBusy, Count: 2, Agents: []string{"linux-1", "linux-2"}}, "2 agents can run it (linux-1, linux-2), all busy, offline or disabled"},
		{&provider.Why{Case: provider.WhyBusy, Count: 1, Agents: []string{"linux-1"}}, "1 agent can run it (linux-1), busy, offline or disabled"},
		{&provider.Why{Case: provider.WhyBusy, Count: 12}, "12 agents can run it, all busy, offline or disabled"},
		{&provider.Why{Case: provider.WhyBusy}, "every agent is busy, offline or disabled"},
		{&provider.Why{Case: provider.WhyNext, Count: 2}, "agents are free, it is next"},
		{&provider.Why{Case: provider.WhyAhead, Ahead: 3}, "agents are free, 3 before it"},
		{&provider.Why{AnyAgent: &yes, Count: 3, Agents: []string{"a", "b", "c"}}, "3 agents can run it (a, b, c)"},
	} {
		assert.Equal(t, c.want, WhyText(c.w))
	}
}

func TestWaitTextOnlyForQueuedBuilds(t *testing.T) {
	why := &provider.Why{Case: provider.WhyNoAgent}
	assert.Equal(t, "queue 2 of 5 · no agent can run it",
		WaitText(provider.Build{State: provider.StateQueued, QueuePosition: 2, QueueLength: 5, Why: why}))
	assert.Equal(t, "no agent can run it", WaitText(provider.Build{State: provider.StateQueued, Why: why}))
	assert.Empty(t, WaitText(provider.Build{State: provider.StateRunning, Why: why}))
}

func TestQueueListWithWhyIsGolden(t *testing.T) {
	o, buf := testOut(true)
	q := []app.QueueRow{
		{Position: 1, QueueEntry: provider.QueueEntry{Key: "PROJ-PROV-9", Reason: "Code has changed"}, Waited: 7 * 60e9, Why: &provider.Why{Case: provider.WhyNoAgent}},
		{Position: 2, QueueEntry: provider.QueueEntry{Key: "PROJ-BUILD-45", Reason: "Manual build"}, AtLeast: true,
			Why: &provider.Why{Case: provider.WhyBusy, Count: 2, Agents: []string{"linux-1", "linux-2"}}},
		{Position: 3, QueueEntry: provider.QueueEntry{Key: "PROJ-OTHER-3", Reason: "Manual build"}, AtLeast: true},
	}
	_ = QueueList(o, q, func(k string) string { return "" })
	golden(t, "queue_why", buf.String())
}
