package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (issue 189, BAM-42 P2): bam agent mine lists the name starts of the
// agents you support, bam top shows only those agents with their health,
// --all shows the whole server, --json gains agents.mine, and bam agent list
// --mine filters.
func mineHarness(t *testing.T) *harness {
	h := topHarness(t)
	h.fake.AgentList = []provider.Agent{
		{ID: 1, Name: "team-a-agent-pool-0", Type: "REMOTE", Online: true, Enabled: true, Busy: true},
		{ID: 2, Name: "team-a-agent-pool-1", Type: "REMOTE", Online: false, Enabled: true},
		{ID: 3, Name: "team-a-agent-pool-2", Type: "REMOTE", Online: true, Enabled: false},
		{ID: 4, Name: "other-agent-0", Type: "REMOTE", Online: false, Enabled: true},
	}
	return h
}

func TestAgentMineAddListRemoveAndForget(t *testing.T) {
	h := mineHarness(t)
	require.Equal(t, 0, h.run("agent", "mine", "list"))
	assert.Contains(t, h.stdout.String(), "bam agent mine add NAME-START")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent", "team-b-agent"))
	assert.Contains(t, h.stdout.String(), "Listed team-a-agent")
	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent"))
	assert.Contains(t, h.stdout.String(), "team-a-agent is already listed")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "mine", "list", "--json"))
	var starts []string
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &starts))
	assert.Equal(t, []string{"team-a-agent", "team-b-agent"}, starts)

	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "mine", "rm", "team-b-agent"))
	assert.Equal(t, 2, h.run("agent", "mine", "rm", "team-b-agent"), "a start that is not listed is a usage error")
	assert.Equal(t, 2, h.run("agent", "mine", "add", "team-*"), "no wildcards")
	require.Equal(t, 0, h.run("agent", "mine", "add", "Team B build"), "Bamboo agent names may hold spaces")
	require.Equal(t, 0, h.run("agent", "mine", "rm", "Team B build"))

	assert.Equal(t, 2, h.run("agent", "mine", "forget", "team-a-agent-pool-9"), "nothing remembered under that name")
	require.Equal(t, 0, h.run("top", "--once"))
	h.fake.AgentList = h.fake.AgentList[1:]
	require.Equal(t, 0, h.run("top", "--once"))
	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "mine", "forget", "team-a-agent-pool-0"))
	assert.Contains(t, h.stdout.String(), "Forgot team-a-agent-pool-0")
}

func TestAgentMineKeepsTheMachineFilesComments(t *testing.T) {
	h := mineHarness(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.env.Paths.MachineConfig), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\n# kept\n"), 0o644))
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent"))
	data, err := os.ReadFile(h.env.Paths.MachineConfig)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# kept")
	assert.Contains(t, string(data), "agents:")
}

func TestTopOnceShowsYourAgentsAndAllShowsTheServer(t *testing.T) {
	h := mineHarness(t)
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent", "team-b-agent"))
	h.stdout.Reset()
	require.Equal(t, 0, h.run("top", "--once"))
	out := h.stdout.String()
	assert.Contains(t, out, "YOUR AGENTS")
	assert.Contains(t, out, "1 building · 0 idle · 1 down · 1 disabled")
	assert.Contains(t, out, "team-a-agent-pool-…  3  ●✗⊘")
	assert.Contains(t, out, "team-a-agent-pool-1  down")
	assert.Contains(t, out, "team-a-agent-pool-2  disabled")
	assert.Contains(t, out, "no agent starts with team-b-agent")
	assert.Contains(t, out, "! agent team-a-agent-pool-1 is down")
	assert.Contains(t, out, "! agent team-a-agent-pool-2 is disabled")
	assert.NotContains(t, out, "other-agent-0", "another team's agent is not on the board")
	assert.NotContains(t, out, "Show only your agents")

	h.stdout.Reset()
	require.Equal(t, 0, h.run("top", "--once", "--all"))
	out = h.stdout.String()
	assert.NotContains(t, out, "YOUR AGENTS")
	assert.Contains(t, out, "4 agents: 1 online, 1 busy, 2 offline, 1 disabled")
	assert.Contains(t, out, "! agent other-agent-0 is offline")
}

func TestTopOnceWithoutAgentsSetSaysHowToListThem(t *testing.T) {
	h := mineHarness(t)
	require.Equal(t, 0, h.run("top", "--once"))
	assert.Contains(t, h.stdout.String(), "Show only your agents: bam agent mine add NAME-START")
	assert.Contains(t, h.stdout.String(), "! agent other-agent-0 is offline")
}

func TestTopJSONGainsAgentsMine(t *testing.T) {
	h := mineHarness(t)
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent"))
	h.stdout.Reset()
	require.Equal(t, 0, h.run("top", "--json"))
	var doc struct {
		Agents struct {
			Total int `json:"total"`
			Mine  struct {
				Starts   []string `json:"starts"`
				Building int      `json:"building"`
				Down     int      `json:"down"`
				Disabled int      `json:"disabled"`
				Agents   []struct {
					Name, State string
				} `json:"agents"`
			} `json:"mine"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, 4, doc.Agents.Total, "the existing fields stay")
	assert.Equal(t, []string{"team-a-agent"}, doc.Agents.Mine.Starts)
	assert.Equal(t, 1, doc.Agents.Mine.Building)
	assert.Equal(t, 1, doc.Agents.Mine.Down)
	assert.Equal(t, 1, doc.Agents.Mine.Disabled)
	require.Len(t, doc.Agents.Mine.Agents, 3)
	assert.Equal(t, "down", doc.Agents.Mine.Agents[1].State)

	h2 := mineHarness(t)
	require.Equal(t, 0, h2.run("top", "--json"))
	assert.NotContains(t, h2.stdout.String(), `"mine"`, "without agents: set the document is as before")
}

func TestAgentListMineFilters(t *testing.T) {
	h := mineHarness(t)
	assert.Equal(t, 2, h.run("agent", "list", "--mine"), "no agents are listed yet")
	require.Equal(t, 0, h.run("agent", "mine", "add", "team-a-agent"))
	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "list", "--mine"))
	assert.Contains(t, h.stdout.String(), "team-a-agent-pool-2")
	assert.NotContains(t, h.stdout.String(), "other-agent-0")
	h.stdout.Reset()
	require.Equal(t, 0, h.run("agent", "list"))
	assert.Contains(t, h.stdout.String(), "other-agent-0", "the default output is the whole server")
}
