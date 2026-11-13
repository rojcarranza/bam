package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P1): bam top prints the board with pins, without pins,
// without administrator rights and with an empty queue; --json prints the
// same board; bam pin edits the machine file.
func topHarness(t *testing.T) *harness {
	h := queueHarness(t)
	h.fake.AgentList = append(h.fake.AgentList, provider.Agent{ID: 3, Name: "linux-3", Type: "REMOTE", Online: true, Enabled: false})
	h.fake.Sequences["PROJ-PROV-9"] = []provider.Build{{Key: "PROJ-PROV-9", State: provider.StateQueued, QueuedAt: fixedNow.Add(-8 * time.Minute)}}
	return h
}

func pinned(t *testing.T, h *harness) {
	require.NoError(t, os.MkdirAll(strings.TrimSuffix(h.env.Paths.MachineConfig, "/config.yaml"), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig,
		[]byte("version: 1\nmonitor:\n  integration: [PROJ-BUILD, provision-lab]\n"), 0o644))
}

func TestTopWithPins(t *testing.T) {
	h := topHarness(t)
	pinned(t, h)
	require.Equal(t, 0, h.run("top"))
	out := h.stdout.String()
	assert.Contains(t, out, "Bamboo 9.6.2")
	assert.Contains(t, out, "! agent linux-2 is offline")
	assert.Contains(t, out, "! PROJ-PROV-9 has waited 8m in the queue")
	assert.Contains(t, out, "3 agents: 1 online, 1 busy, 1 offline, 1 disabled")
	assert.Contains(t, out, "linux-3  remote  disabled")
	assert.Regexp(t, `1\s+PROJ-PROV-9\s+Code has changed\s+8m00s`, out)
	assert.Contains(t, out, "integration")
	assert.Regexp(t, `PROJ-BUILD\s+.*2m11s`, out)
	assert.Regexp(t, `provision-lab\s+.*`, out)
	assert.Contains(t, out, "! provision-lab: the last build, PROJ-PROV12-8, failed")
}

func TestTopWithoutPinsSaysHowToPin(t *testing.T) {
	h := topHarness(t)
	require.Equal(t, 0, h.run("top"))
	out := h.stdout.String()
	assert.Contains(t, out, "AGENTS")
	assert.Contains(t, out, "QUEUE")
	assert.Contains(t, out, "No pipelines pinned. Pin one with: bam pin add GROUP PLAN")
}

func TestTopWithoutAdministratorRightsStillPrintsTheRest(t *testing.T) {
	h := topHarness(t)
	h.fake.AgentsErr = errs.Authf("403: the agent list needs an administrator")
	require.Equal(t, 0, h.run("top"))
	out := h.stdout.String()
	assert.Contains(t, out, "the agent list is not shown: Bamboo refused it")
	assert.Contains(t, out, "PROJ-PROV-9")
	assert.NotContains(t, out, "offline")
	assert.Empty(t, h.stderr.String())

	h.fake.QueueErr = errs.Bamboof("boom")
	require.Equal(t, 0, h.run("top"))
	assert.Contains(t, h.stdout.String(), "the queue could not be read")
}

func TestTopWithAnEmptyQueue(t *testing.T) {
	h := topHarness(t)
	h.fake.QueueEntries = nil
	require.Equal(t, 0, h.run("top"))
	assert.Contains(t, h.stdout.String(), "The queue is empty")
}

func TestTopJSON(t *testing.T) {
	h := topHarness(t)
	pinned(t, h)
	require.Equal(t, 0, h.run("top", "--json"))
	var doc struct {
		Server struct{ Alias, Version string }
		Needs  []struct{ Kind, Text string } `json:"needs_you"`
		Agents struct {
			Total, Offline, Disabled int
			Away                     []map[string]any
		}
		Queue []struct {
			Key      string
			WaitedMS int64 `json:"waited_ms"`
		}
		Pins []struct {
			Name      string
			Pipelines []struct {
				Pin     string
				Results []struct{ Key, State string }
			}
		}
	}
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "work", doc.Server.Alias)
	assert.Equal(t, "9.6.2", doc.Server.Version)
	assert.Equal(t, 3, doc.Agents.Total)
	assert.Len(t, doc.Agents.Away, 2)
	assert.Equal(t, int64(8*60*1000), doc.Queue[0].WaitedMS)
	require.Len(t, doc.Pins, 1)
	assert.Equal(t, "integration", doc.Pins[0].Name)
	assert.Equal(t, "PROJ-BUILD", doc.Pins[0].Pipelines[0].Pin)
	assert.Equal(t, "PROJ-BUILD-482", doc.Pins[0].Pipelines[0].Results[0].Key)
	kinds := []string{}
	for _, n := range doc.Needs {
		kinds = append(kinds, n.Kind)
	}
	assert.ElementsMatch(t, []string{"agent_offline", "queue_wait", "pin_failed"}, kinds)
}

func TestTopRefusesABadMonitorGroup(t *testing.T) {
	h := topHarness(t)
	require.NoError(t, os.MkdirAll(strings.TrimSuffix(h.env.Paths.MachineConfig, "/config.yaml"), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("version: 1\nmonitor:\n  Bad: [PROJ-BUILD]\n"), 0o644))
	assert.Equal(t, 3, h.run("top"))
	assert.Contains(t, h.stderr.String(), `monitor group "Bad"`)
}

func TestQueueShowsTimeWaited(t *testing.T) {
	h := topHarness(t)
	require.Equal(t, 0, h.run("queue"))
	assert.Regexp(t, `WAITED`, h.stdout.String())
	assert.Regexp(t, `PROJ-PROV-9\s+Code has changed\s+8m00s`, h.stdout.String())
	assert.Regexp(t, `PROJ-OTHER-3\s+Manual build\s+at least 0s`, h.stdout.String(),
		"a build whose record has no start is counted from when bam saw it")
	require.Equal(t, 0, h.run("queue", "--json"))
	var docs []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &docs))
	assert.EqualValues(t, 8*60*1000, docs[0]["waited_ms"])
	assert.Equal(t, false, docs[0]["waited_at_least"])
	assert.Equal(t, true, docs[1]["waited_at_least"])
	assert.Equal(t, "PROJ-PROV-9", docs[0]["key"], "existing fields stay")
}

func TestPinAddListRm(t *testing.T) {
	h := topHarness(t)
	require.NoError(t, os.MkdirAll(strings.TrimSuffix(h.env.Paths.MachineConfig, "/config.yaml"), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig, []byte("# mine\nversion: 1\n"), 0o644))

	require.Equal(t, 0, h.run("pin", "add", "integration", "PROJ-BUILD"))
	require.Equal(t, 0, h.run("pin", "add", "integration", "provision-lab"))
	require.Equal(t, 0, h.run("pin", "add", "release", "PROJ-PROV@develop"))
	require.Equal(t, 0, h.run("pin", "list"))
	assert.Equal(t, "integration  PROJ-BUILD\nintegration  provision-lab\nrelease  PROJ-PROV@develop\n", h.stdout.String())
	data, _ := os.ReadFile(h.env.Paths.MachineConfig)
	assert.True(t, strings.HasPrefix(string(data), "# mine"))

	assert.Equal(t, 2, h.run("pin", "add", "release", "PROJ-BUILD"), "one pin, one group")
	assert.Contains(t, h.stderr.String(), "already pinned in group")
	assert.Equal(t, 2, h.run("pin", "add", "release", "nope-preset"))
	assert.Equal(t, 2, h.run("pin", "add", "Bad Group", "PROJ-BUILD"))

	require.Equal(t, 0, h.run("pin", "rm", "provision-lab"))
	assert.Equal(t, 2, h.run("pin", "rm", "provision-lab"))
	require.Equal(t, 0, h.run("pin", "list", "--json"))
	assert.JSONEq(t, `[{"group":"integration","pins":["PROJ-BUILD"]},{"group":"release","pins":["PROJ-PROV@develop"]}]`, h.stdout.String())
}

func TestPinAddPointsAtTheProjectFileForAProjectPin(t *testing.T) {
	h := topHarness(t)
	project := filepath.Join(h.root, ".bam.yaml")
	data, err := os.ReadFile(project)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(project, append(data, []byte("monitor:\n  integration: [PROJ-BUILD]\n")...), 0o644))
	assert.Equal(t, 2, h.run("pin", "add", "release", "PROJ-BUILD"))
	assert.Contains(t, h.stderr.String(), "remove it from "+project+" by hand first")
	assert.NotContains(t, h.stderr.String(), "bam pin rm")
}

func TestTopShowsAPresetUnknownHereOnItsPinAlone(t *testing.T) {
	h := topHarness(t)
	require.NoError(t, os.MkdirAll(strings.TrimSuffix(h.env.Paths.MachineConfig, "/config.yaml"), 0o755))
	require.NoError(t, os.WriteFile(h.env.Paths.MachineConfig,
		[]byte("version: 1\nmonitor:\n  integration: [PROJ-BUILD, deploy-qa]\n"), 0o644))
	require.Equal(t, 0, h.run("top"))
	assert.Contains(t, h.stdout.String(), "deploy-qa")
	assert.Regexp(t, `PROJ-BUILD\s+.*2m11s`, h.stdout.String())
	require.Equal(t, 0, h.run("pin", "list"))
	assert.Contains(t, h.stdout.String(), "integration  deploy-qa")
}

func TestPinListWithoutPins(t *testing.T) {
	h := topHarness(t)
	require.Equal(t, 0, h.run("pin", "list"))
	assert.Contains(t, h.stdout.String(), "No pipelines pinned")
}
