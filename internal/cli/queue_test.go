package cli

import (
	"encoding/json"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-11 P2): a queued build says its place in the queue in build
// show and the tree; bam queue and bam agent list say why it waits.
func queueHarness(t *testing.T) *harness {
	h := newHarness(t)
	b := queuedBuild("PROJ-BUILD-45")
	b.PlanKey = "PROJ-BUILD"
	h.fake.Sequences = map[string][]provider.Build{"PROJ-BUILD-45": {b}}
	h.fake.QueueEntries = []provider.QueueEntry{
		{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, Reason: "Code has changed"},
		{Key: "PROJ-OTHER-3", PlanKey: "PROJ-OTHER", Number: 3, Reason: "Manual build"},
		{Key: "PROJ-BUILD-45", PlanKey: "PROJ-BUILD", Number: 45, Reason: "Manual build"},
	}
	h.fake.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "linux-2", Type: "REMOTE", Online: false, Enabled: true},
	}
	return h
}

func TestBuildShowSaysThePlaceInTheQueue(t *testing.T) {
	h := queueHarness(t)
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45"))
	assert.Contains(t, h.stdout.String(), "queue 3 of 3")

	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.EqualValues(t, 3, doc["queue_position"])
	assert.EqualValues(t, 3, doc["queue_length"])
}

func TestBuildShowWithoutQueueListStillWorks(t *testing.T) {
	h := queueHarness(t)
	h.fake.QueueErr = assert.AnError
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45"))
	assert.NotContains(t, h.stdout.String(), "queue ")
}

func TestQueueList(t *testing.T) {
	h := queueHarness(t)
	require.Equal(t, 0, h.run("queue"))
	assert.Regexp(t, `1\s+PROJ-PROV-9\s+Code has changed`, h.stdout.String())
	assert.Regexp(t, `3\s+PROJ-BUILD-45`, h.stdout.String())

	require.Equal(t, 0, h.run("queue", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	require.Len(t, docs, 3)
	assert.EqualValues(t, 1, docs[0]["position"])
	assert.Equal(t, "PROJ-PROV-9", docs[0]["key"])

	h.fake.QueueEntries = nil
	require.Equal(t, 0, h.run("queue"))
	assert.Contains(t, h.stdout.String(), "The queue is empty")
	require.Equal(t, 0, h.run("queue", "--json"))
	assert.JSONEq(t, `[]`, h.stdout.String())
}

func TestAgentList(t *testing.T) {
	h := queueHarness(t)
	require.Equal(t, 0, h.run("agent", "list"))
	assert.Regexp(t, `linux-1\s+REMOTE\s+yes\s+yes\s+yes`, h.stdout.String())
	assert.Regexp(t, `linux-2\s+REMOTE\s+no\s+yes\s+no`, h.stdout.String())

	require.Equal(t, 0, h.run("agent", "list", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	require.Len(t, docs, 2)
	assert.Equal(t, false, docs[1]["online"])
}

// Intent (BAM-34 P3): a waiting build names why it waits in bam queue, build
// show and the tree, and never fails when a source is refused.
func whyHarness(t *testing.T) *harness {
	h := queueHarness(t)
	yes, no := true, false
	b := h.fake.Sequences["PROJ-BUILD-45"][0]
	b.Stages = []provider.Stage{{Name: "Build", State: provider.StateQueued, Jobs: []provider.Job{
		{Key: "PROJ-BUILD-JOB1-45", Name: "Compile", State: provider.StateQueued, AnyAgent: &yes}}}}
	h.fake.Sequences["PROJ-BUILD-45"] = []provider.Build{b}
	pb := queuedBuild("PROJ-PROV-9")
	pb.Stages = []provider.Stage{{Name: "Provision", State: provider.StateQueued, Jobs: []provider.Job{
		{Key: "PROJ-PROV-WIN-9", Name: "Windows", State: provider.StateQueued, AnyAgent: &no}}}}
	h.fake.Sequences["PROJ-PROV-9"] = []provider.Build{pb}
	h.fake.Possible = map[string][]string{"PROJ-BUILD-JOB1": {"linux-1", "linux-2"}}
	return h
}

func TestQueueNamesWhyEachBuildWaits(t *testing.T) {
	h := whyHarness(t)
	require.Equal(t, 0, h.run("queue"))
	out := h.stdout.String()
	assert.Regexp(t, `WAITED\s+WHY`, out)
	assert.Regexp(t, `PROJ-PROV-9\s+Code has changed\s+at least 0s\s+no agent can run it`, out)
	assert.Regexp(t, `PROJ-BUILD-45\s+Manual build\s+at least 0s\s+2 agents can run it \(linux-1, linux-2\), all busy, offline or disabled`, out)

	require.Equal(t, 0, h.run("queue", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	require.Len(t, docs, 3)
	assert.Equal(t, "no_agent", docs[0]["why_case"])
	assert.Equal(t, false, docs[0]["any_agent_can_run"])
	assert.Equal(t, "busy", docs[2]["why_case"])
	assert.Equal(t, []any{"linux-1", "linux-2"}, docs[2]["agents_that_can_run"])
	assert.EqualValues(t, 3, docs[2]["position"], "existing fields stay")
}

func TestBuildShowAndTreeNameWhyBesideTheQueuePlace(t *testing.T) {
	h := whyHarness(t)
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45"))
	assert.Contains(t, h.stdout.String(), "queue 3 of 3 · 2 agents can run it (linux-1, linux-2), all busy, offline or disabled")

	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "busy", doc["why_case"])
	assert.EqualValues(t, 3, doc["queue_position"])

	require.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-45"))
	assert.Contains(t, h.stdout.String(), "queue 3 of 3 · 2 agents can run it")
}

func TestWhyWithoutAdministratorRightsKeepsWhatIsReadable(t *testing.T) {
	h := whyHarness(t)
	h.fake.AgentsErr = errs.Authf("403 from bamboo.example.com: the agent list needs an administrator")
	require.Equal(t, 0, h.run("queue"))
	assert.Contains(t, h.stdout.String(), "no agent can run it")
	assert.Contains(t, h.stdout.String(), "2 agents can run it (linux-1, linux-2)")
	assert.NotContains(t, h.stdout.String(), "busy, offline")

	// Neither the possible agents nor the agent list: the job's own answer stands.
	h.fake.Possible = nil
	require.Equal(t, 0, h.run("queue"))
	assert.Contains(t, h.stdout.String(), "no agent can run it")
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-45"))
	assert.NotContains(t, h.stdout.String(), "agents can run it")
	assert.Contains(t, h.stdout.String(), "queue 3 of 3")
}

func TestQueueKeepsFourColumnsWhenNothingIsKnown(t *testing.T) {
	h := queueHarness(t)
	require.Equal(t, 0, h.run("queue"))
	assert.NotContains(t, h.stdout.String(), "WHY")
}
