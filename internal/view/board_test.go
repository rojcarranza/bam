package view

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view/style"
	"github.com/stretchr/testify/assert"
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
			{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD", Name: "Build and test", Current: &running,
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

// Intent (BAM-42 P3): the queue of the board reads in plain words: a wait of
// 48 hours or more is days and hours, a wait bam did not read is a dash, and a
// line counts the builds that wait for others.
func TestQueueWaitsReadAsDaysAndDashes(t *testing.T) {
	for _, c := range []struct {
		row  app.QueueRow
		want string
	}{
		{app.QueueRow{Waited: 47*time.Hour + 59*time.Minute}, "47h59m"},
		{app.QueueRow{Waited: 48 * time.Hour}, "2d 0h"},
		{app.QueueRow{Waited: 1372*time.Hour + 36*time.Minute}, "57d 4h"},
		{app.QueueRow{Waited: 5 * time.Minute, AtLeast: true}, "–"},
	} {
		require.Equal(t, c.want, waitedText(c.row))
	}
}

func TestBoardTextCountsTheBuildsThatWaitForOthers(t *testing.T) {
	b := sampleBoard()
	b.QueueFiltered, b.QueueOthers = true, 37
	var sb strings.Builder
	require.NoError(t, Board(Out{W: &sb}, b, func(k string) string { return k }))
	out := sb.String()
	require.Contains(t, out, "YOUR QUEUE")
	require.Contains(t, out, "37 more builds wait for other agents")
	require.Contains(t, out, "1 building · 1 idle · 1 down · 1 disabled")
	require.Equal(t, "1 more build waits for other agents", OthersText(1))

	doc := BoardJSON(b, func(k string) string { return k })
	require.Equal(t, 37, doc.QueueOthers)
	require.NotNil(t, doc.Queue[0].Mine)
	require.True(t, *doc.Queue[0].Mine)
}

// Intent (issue #183): bam top --once shows a pinned plan's name cut at its
// end to the terminal width (spec §TTY: free-text columns are cut with …),
// and a pipe keeps the full name.
func TestBoardCutsALongPinnedNameToTheTerminal(t *testing.T) {
	long := "Integrate every service for the next release and run the long checks"
	b := sampleBoard()
	b.Groups[0].Pins[0].Name = long

	o, buf := testOut(true)
	o.Width = 80
	require.NoError(t, Board(o, b, boardURL))
	var row string
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		assert.LessOrEqual(t, style.Width(l), 80, l)
		if row == "" && strings.HasPrefix(style.Strip(l), "    Integrate every") {
			row = style.Strip(l)
		}
	}
	require.NotEmpty(t, row, "the pinned row stays on one line")
	assert.Contains(t, row, "…")
	assert.Contains(t, row, "#484 running", "the other columns are not cut")

	o, buf = testOut(false)
	o.Width = 80
	require.NoError(t, Board(o, b, boardURL))
	assert.Contains(t, buf.String(), long, "a pipe keeps the full name")
}

// Intent: a pinned pipeline's pass-record line stays within the terminal width
// at every width, and a short name is printed whole, never padded.
func TestBoardKeepsAPassRecordLineWithinTheTerminal(t *testing.T) {
	b := sampleBoard()
	b.Groups[0].Pins[0].Name = "lab"
	for width := 10; width <= 120; width++ {
		o, buf := testOut(true)
		o.Width = width
		require.NoError(t, Board(o, b, boardURL), "width %d", width)
		for _, l := range strings.Split(buf.String(), "\n") {
			if plain := style.Strip(l); strings.HasPrefix(plain, "    lab") && strings.Contains(plain, " · ") {
				assert.LessOrEqual(t, style.Width(l), width, "width %d: %s", width, l)
				assert.NotContains(t, l, "\x00", "width %d: a short name is not padded", width)
			}
		}
	}
}

// Intent (BAM-42 P4, issue 193): a pin's branches print under it, indented,
// then a count of those left out, and --json carries them as branches.
func TestBoardPrintsAPinsBranchesUnderIt(t *testing.T) {
	b := sampleBoard()
	b.Groups[0].Pins[0].Branches = []app.PinView{
		{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD12", Branch: "develop", Name: "Build and test",
			Results: []provider.Build{{Key: "PROJ-BUILD12-3", Number: 3, State: provider.StateFailed, Duration: time.Minute}}, Last: time.Minute},
	}
	b.Groups[0].Pins[0].BranchesMore = 4
	var sb strings.Builder
	require.NoError(t, Board(Out{W: &sb}, b, boardURL))
	out := sb.String()
	require.Contains(t, out, "\n      Build and test@develop")
	require.Contains(t, out, "\n      +4 more branches")
	require.Less(t, strings.Index(out, "Build and test  "), strings.Index(out, "Build and test@develop"))

	doc := BoardJSON(b, boardURL)
	pin := doc.Pins[0].Pipelines[0]
	require.Len(t, pin.Branches, 1)
	require.Equal(t, "develop", pin.Branches[0].Branch)
	require.Equal(t, "PROJ-BUILD12", pin.Branches[0].PlanKey)
	require.Equal(t, 4, pin.BranchesMore)
	require.Empty(t, pin.Branches[0].Branches)
}

// Intent: a pinned group's heading stays within the terminal width on narrow
// terminals too, where the heading floor would otherwise push it past the edge.
func TestBoardKeepsTheGroupHeadingWithinNarrowTerminals(t *testing.T) {
	b := sampleBoard()
	for width := 3; width <= 40; width++ {
		o, buf := testOut(true)
		o.Width = width
		require.NoError(t, Board(o, b, boardURL), "width %d", width)
		lines := strings.Split(buf.String(), "\n")
		heading := -1
		for i, l := range lines {
			if style.Strip(l) == "PINNED" {
				heading = i + 1
			}
		}
		require.Greater(t, heading, 0, "width %d: the PINNED section is printed", width)
		assert.LessOrEqual(t, style.Width(lines[heading]), width, "width %d: %s", width, lines[heading])
	}
}
