package view

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-34 P1): the board's text and JSON are pinned by golden files.
func sampleBoard() app.Board {
	build := func(key string, n int, s provider.State, d time.Duration) provider.Build {
		return provider.Build{Key: key, Number: n, State: s, Duration: d}
	}
	running := build("PROJ-BUILD-484", 484, provider.StateRunning, 0)
	running.StartedAt = fixedNow.Add(-14 * time.Minute)
	return app.Board{
		At: fixedNow, Server: "work", URL: "https://bamboo.example.com", Version: "12.1.8",
		Agents: &app.AgentSummary{Total: 4, Online: 2, Busy: 1, Offline: 1, Disabled: 1, Away: []provider.Agent{
			{ID: 2, Name: "linux-2", Type: "REMOTE", Enabled: true},
			{ID: 3, Name: "linux-3", Type: "REMOTE", Online: true},
		}},
		Queue: []app.QueueRow{
			{Position: 1, QueueEntry: provider.QueueEntry{Key: "PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9, Reason: "Code has changed"}, Waited: 7 * time.Minute},
			{Position: 2, QueueEntry: provider.QueueEntry{Key: "PROJ-OTHER-3", PlanKey: "PROJ-OTHER", Number: 3, Reason: "Manual build"}, AtLeast: true},
		},
		QueueOK: true,
		Groups: []app.PinGroupView{{Name: "integration", Pins: []app.PinView{
			{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD", Current: &running,
				Progress: provider.Progress{Valid: true, Percent: 0.62, Average: 10 * time.Minute},
				Results: []provider.Build{
					build("PROJ-BUILD-483", 483, provider.StateSuccess, 9*time.Minute),
					build("PROJ-BUILD-482", 482, provider.StateFailed, 3*time.Minute),
					build("PROJ-BUILD-481", 481, provider.StateSuccess, 10*time.Minute)},
				Last: 9 * time.Minute, Usual: 10 * time.Minute},
			{Pin: "PROJ-GONE@x", Err: errors.New("PROJ-GONE has no plan branch for \"x\"")},
		}}},
		Needs: []app.Need{{Kind: "agent_offline", Text: "agent linux-2 is offline"}, {Kind: "queue_wait", Text: "PROJ-PROV-9 has waited 7m in the queue"}},
	}
}

func boardURL(key string) string { return "https://bamboo.example.com/browse/" + key }

func TestBoardGolden(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Board(o, sampleBoard(), boardURL))
	golden(t, "board", buf.String())
}

func TestBoardWithoutPinsOrAgentsGolden(t *testing.T) {
	b := sampleBoard()
	b.Groups, b.Agents, b.Needs, b.Queue = nil, nil, nil, nil
	b.AgentsNote = "the agent list is not shown: Bamboo refused it (it may need an administrator)"
	o, buf := testOut(false)
	require.NoError(t, Board(o, b, boardURL))
	golden(t, "board_bare", buf.String())
}

func TestBoardJSONGolden(t *testing.T) {
	out, err := json.MarshalIndent(BoardJSON(sampleBoard(), boardURL), "", "  ")
	require.NoError(t, err)
	golden(t, "board_json", string(out)+"\n")
}
