package cli

import (
	"encoding/json"
	"testing"

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
