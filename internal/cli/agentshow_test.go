package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func showHarness(t *testing.T) *harness {
	h := topHarness(t)
	h.fake.AgentList = []provider.Agent{
		{ID: 1, Name: "linux-1", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "linux-2", Type: "REMOTE", Online: true, Enabled: true},
	}
	job := func(key string, st provider.State) provider.Build {
		return provider.Build{Key: key[:len("PROJ-BUILD-1")], PlanKey: "PROJ-BUILD", State: st,
			Stages: []provider.Stage{{Name: "Test", Jobs: []provider.Job{{Key: key, Name: "Unit", State: st, Duration: 90 * time.Second}}}}}
	}
	h.fake.Sequences["PROJ-BUILD-1"] = []provider.Build{job("PROJ-BUILD-1-U-1", provider.StateFailed)}
	h.fake.Sequences["PROJ-BUILD-2"] = []provider.Build{job("PROJ-BUILD-2-U-1", provider.StateSuccess)}
	h.fake.Recent = []provider.Build{{Key: "PROJ-BUILD-2"}, {Key: "PROJ-BUILD-1"}}
	h.fake.JobAgentMap = map[string]string{"PROJ-BUILD-1-U-1": "linux-1", "PROJ-BUILD-2-U-1": "linux-2"}
	return h
}

// Intent (issue 233, BAM-34 P5): bam agent show NAME prints the agent's
// state, its jobs and its failure share beside the other agents'.
func TestAgentShowPrintsTheTrackRecord(t *testing.T) {
	h := showHarness(t)
	require.Equal(t, 0, h.run("agent", "show", "linux-1"))
	out := h.stdout.String()
	assert.Contains(t, out, "linux-1  REMOTE  building")
	assert.Contains(t, out, "Failures    100% (1 of 1)")
	assert.Contains(t, out, "Others      0% (0 of 1), 1 other agent")
	assert.Regexp(t, `Unit\s+PROJ-BUILD-1\s+.*1m30s`, out)
}

func TestAgentShowJSON(t *testing.T) {
	h := showHarness(t)
	require.Equal(t, 0, h.run("agent", "show", "linux-2", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "linux-2", doc["name"])
	assert.EqualValues(t, 2, doc["builds_scanned"])
	assert.Equal(t, map[string]any{"jobs": 1.0, "failed": 0.0, "failure_share": 0.0}, doc["this_agent"])
	assert.Len(t, doc["recent"], 1)
	assert.Equal(t, false, doc["failing_more_than_others"])
}

func TestAgentShowUnknownNameAndRefusedList(t *testing.T) {
	h := showHarness(t)
	assert.Equal(t, 2, h.run("agent", "show", "nope"))
	assert.Contains(t, h.stderr.String(), "no agent is named")
	h.fake.AgentsErr = errs.Authf("403 from bamboo.example.com: the agent list needs an administrator")
	assert.Equal(t, 4, h.run("agent", "show", "linux-1"))
	assert.Contains(t, h.stderr.String(), "needs an administrator")
}

// Intent: bam build show fills the agent of a build and its jobs.
func TestBuildShowNamesTheAgent(t *testing.T) {
	h := showHarness(t)
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-1"))
	assert.Contains(t, h.stdout.String(), "Agent       linux-1")
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-1", "--json"))
	var doc struct {
		Agent  string
		Stages []struct{ Jobs []struct{ Agent string } }
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "linux-1", doc.Agent)
	assert.Equal(t, "linux-1", doc.Stages[0].Jobs[0].Agent)
}
