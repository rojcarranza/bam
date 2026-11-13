package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// Intent (issue 189, BAM-42 P2): with agents: set the board's agents panel is
// Your agents, with four counts, one row per pool and the names of agents that
// are down or disabled; it is a tab stop, enter on a pool lists its agents, and
// a shows the whole server again.

// mineBoard is boardModel with Your agents filled from a pool of n agents of
// which the first is down, the second disabled and the third building, one
// gone from Bamboo's list, and a start no agent matches.
func mineBoard(w, h, n int) Model {
	m := boardModel(w, h)
	var as []provider.Agent
	for i := range n {
		as = append(as, provider.Agent{Name: fmt.Sprintf("team-a-agent-pool-%d", i), Online: i != 0, Enabled: i != 1, Busy: i == 2})
	}
	mine, _ := app.MineAgents(as, []string{"team-a-agent", "team-b-agent"},
		map[string]time.Time{"team-a-agent-pool-9": goldenNow.Add(-48 * time.Hour)}, goldenNow)
	data := sampleBoard(goldenNow)
	data.MineSet, data.Mine = true, mine
	data.Needs = app.NeedsYou(data, goldenNow)
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	return m
}

func TestBoardYourAgentsGolden(t *testing.T) {
	requireGolden(t, "board-mine-120x45", mineBoard(120, 45, 5).View())
	requireGolden(t, "board-mine-80x24", mineBoard(80, 24, 5).View())
}

func TestBoardYourAgentsShowsCountsPoolsAndWhatIsWrong(t *testing.T) {
	v := mineBoard(120, 45, 5).View()
	for _, want := range []string{
		"Your agents",
		"1 building · 2 idle · 2 down · 1 disabled",
		"team-a-agent-pool-…  6  ✗⊘●○○",
		"team-a-agent-pool-0  down",
		"team-a-agent-pool-1  disabled",
		"team-a-agent-pool-9  down: Bamboo no longer lists it",
		"no agent starts with team-b-agent",
		"agent team-a-agent-pool-0 is down",
		"agent team-a-agent-pool-1 is disabled",
	} {
		require.Contains(t, v, want)
	}
	require.NotContains(t, v, "linux-2", "the whole server's offline agents are not on the board")
}

func TestBoardYourAgentsIsATabStopOnlyWhenShown(t *testing.T) {
	m := mineBoard(120, 45, 5)
	var order []boardPanel
	for range 4 {
		order = append(order, m.board.panel)
		m, _ = send(m, mkKey("tab"))
	}
	require.Equal(t, []boardPanel{boardNeeds, boardAgents, boardQueue, boardPins}, order)

	m = boardModel(120, 45)
	m, _ = send(m, mkKey("tab"))
	require.Equal(t, boardQueue, m.board.panel, "no agents set: tab goes from Needs you to the queue, as before")
}

func TestBoardEnterOnAPoolListsItsAgents(t *testing.T) {
	m := mineBoard(120, 45, 5)
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, overlayInfo, m.overlay)
	body := m.View()
	for _, want := range []string{"team-a-agent-pool-0  down", "team-a-agent-pool-1  disabled", "team-a-agent-pool-2  building", "team-a-agent-pool-4  idle"} {
		require.Contains(t, body, want)
	}
}

func TestBoardYourAgentsScrollsWhenPoolsOutnumberItsLines(t *testing.T) {
	m := mineBoard(120, 45, 5)
	data := m.board.data
	var as []provider.Agent
	for i := range 12 {
		as = append(as, provider.Agent{Name: fmt.Sprintf("team-%c-agent-0", 'a'+i), Online: true, Enabled: true})
	}
	data.Mine, _ = app.MineAgents(as, []string{"team-"}, nil, goldenNow)
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	m, _ = send(m, mkKey("tab"))
	for range 11 {
		m, _ = send(m, mkKey("j"))
	}
	require.Contains(t, m.View(), "team-l-agent-…", "the last pool scrolls into view")
	require.Contains(t, m.View(), "▸ team-l-agent-…")
	require.Equal(t, 11, m.board.cursor[boardAgents])
}

func TestBoardAShowsTheWholeServerAndAgain(t *testing.T) {
	m := mineBoard(120, 45, 5)
	m, cmd := send(m, mkKey("a"))
	require.True(t, m.board.whole)
	require.NotNil(t, cmd, "the board reads again for the other view")
	require.Equal(t, "Agents · whole server", m.agentsTitle())
	m, _ = send(m, mkKey("a"))
	require.False(t, m.board.whole)

	m = boardModel(120, 45)
	m, _ = send(m, mkKey("a"))
	require.False(t, m.board.whole, "with no agents listed there is nothing to toggle")
	require.Contains(t, m.status, "bam agent mine add")
}

// When Bamboo refuses the agent list, the board still shows your agents' view,
// not the whole server's.
func TestBoardAgentsTitleFollowsTheViewNotTheReading(t *testing.T) {
	m := mineBoard(120, 45, 5)
	m.board.data.Mine = nil
	require.Equal(t, "Your agents", m.agentsTitle())
	m, _ = send(m, mkKey("a"))
	require.Equal(t, "Agents · whole server", m.agentsTitle())
	m, _ = send(m, mkKey("a"))
	require.Equal(t, "Your agents", m.agentsTitle(), "no whole-server title while your agents load again")
}

func TestBoardWithoutAgentsSetShowsTheHintWhereThereIsRoom(t *testing.T) {
	require.Contains(t, boardModel(120, 45).View(), "Show only your agents: bam agent mine add NAME-START")
	require.NotContains(t, boardModel(60, 20).View(), "Show only your agents")
}

// In the regular-height layout the Your agents panel has fewer lines than it
// has rows. mineBoard has three agents down or disabled (pool-0, pool-1, the
// gone pool-9) and one start no agent matches (team-b-agent); a row that does
// not fit must be counted, not cut without notice.
func TestBoardYourAgentsSaysWhatDoesNotFit(t *testing.T) {
	v := mineBoard(80, 24, 5).View()
	require.Contains(t, v, "team-a-agent-pool-0  down")
	require.Contains(t, v, "… 2 more down or disabled · 1 start matches no agent")

	v = mineBoard(120, 36, 5).View()
	require.Contains(t, v, "team-a-agent-pool-9  down: Bamboo no longer lists it")
	require.Contains(t, v, "no agent starts with team-b-agent", "with room for every row, none is counted away")
	require.NotContains(t, v, "matches no agent")
}
