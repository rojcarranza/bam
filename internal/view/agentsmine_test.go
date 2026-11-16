package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-101 P2): Your agents shows a row for each building agent, with
// what it builds, and names the idle agents of a pool on one line.

func minePool() app.MyPool {
	b := func(job, plan, branch, group string, ago time.Duration) *app.AgentBuild {
		return &app.AgentBuild{Job: job, BuildKey: "X", PlanName: plan, Branch: branch, Group: group, StartedAt: fixedNow.Add(-ago)}
	}
	return app.MyPool{Name: "team-a-agent-pool-", Agents: []app.MyAgent{
		{Name: "team-a-agent-pool-1", State: app.AgentBuilding, Build: b("PROJ-DEPLOY-JOB1-44", "Deploy", "", "integration", 12*time.Minute)},
		{Name: "team-a-agent-pool-2", State: app.AgentIdle},
		{Name: "team-a-agent-pool-3", State: app.AgentBuilding}, // build not found
		{Name: "team-a-agent-pool-4", State: app.AgentIdle},
		{Name: "team-a-agent-pool-10", State: app.AgentBuilding, Build: b("PROJ-TEST-JOB2-7", "Test", "feature-x", "", 90*time.Minute)},
		{Name: "team-a-agent-pool-11", State: app.AgentDown},
	}}
}

func TestMineRowsAreTheBuildingAgentsInOrder(t *testing.T) {
	rows := MineRows(minePool(), fixedNow)
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"1", "3", "10"}, []string{rows[0].Number, rows[1].Number, rows[2].Number})
	assert.Equal(t, "Deploy", rows[0].Plan)
	assert.Equal(t, "Integration", rows[0].Group)
	assert.Equal(t, "12m", rows[0].Running)
	assert.Equal(t, "Test · feature-x", rows[2].Plan)
	assert.Equal(t, "1h30m", rows[2].Running)
	assert.Empty(t, rows[1].Job, "an agent whose build was not found has no job")
}

func TestMineIdleLineNamesTheIdleAgentsByNumber(t *testing.T) {
	assert.Equal(t, "idle  2 4", MineIdleLine(minePool()))
	assert.Empty(t, MineIdleLine(app.MyPool{Agents: []app.MyAgent{{Name: "a-1", State: app.AgentBuilding}}}))
	assert.Equal(t, "idle  solo", MineIdleLine(app.MyPool{Agents: []app.MyAgent{{Name: "solo", State: app.AgentIdle}}}), "a pool of one name has no number")
}

func TestMineRowTextAlignsColumnsAndSaysWhenABuildWasNotFound(t *testing.T) {
	rows := MineRows(minePool(), fixedNow)
	cols := MineColumns(rows, 120)
	assert.Equal(t, 2, cols.Number)
	assert.Equal(t, len("PROJ-DEPLOY-JOB1-44"), cols.Job)
	assert.Equal(t, len("Integration"), cols.Group)

	l0, l1, l2 := MineRowText(rows[0], cols, nil), MineRowText(rows[1], cols, nil), MineRowText(rows[2], cols, nil)
	assert.Equal(t, " 1  PROJ-DEPLOY-JOB1-44  Deploy            Integration     12m", l0)
	assert.Equal(t, " 3  "+MineNotFound, l1)
	assert.Equal(t, "10  PROJ-TEST-JOB2-7     Test · feature-x                1h30m", l2)
}

func TestMineColumnsCutAPlanToFitAndKeepAFloor(t *testing.T) {
	long := MineRow{Number: "1", Job: "PROJ-A-JOB1-1", Plan: strings.Repeat("p", 80), Running: "1m"}
	c := MineColumns([]MineRow{long}, 60)
	line := MineRowText(long, c, nil)
	assert.LessOrEqual(t, len([]rune(line))+2, 60)
	assert.Contains(t, line, "…")
	assert.GreaterOrEqual(t, c.Plan, 12)
	assert.Zero(t, c.Group, "no group column when no row has a group")
}

func TestMineElapsedAndGroupName(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "–", 45 * time.Second: "45s", 59 * time.Minute: "59m", time.Hour: "1h00m", 2*time.Hour + 5*time.Minute: "2h05m"} {
		assert.Equal(t, want, MineElapsed(d))
	}
	assert.Equal(t, "Release train", MineGroupName("release-train"))
	assert.Equal(t, "Nightly runs", MineGroupName("nightly_runs"))
	assert.Equal(t, "Integration", MineGroupName("integration"))
}

func TestMineLinesListsRowsAndIdleUnderEachPoolOnlyWithATime(t *testing.T) {
	m := &app.MyAgents{Building: 3, Idle: 2, Down: 1, Pools: []app.MyPool{minePool()}}
	plain := MineLines(m, func(_ app.AgentState, s string) string { return s }, time.Time{})
	assert.Len(t, plain, 2, "without a time: the summary and the pool line")

	lines := MineLines(m, func(_ app.AgentState, s string) string { return s }, fixedNow)
	require.Len(t, lines, 2+3+1)
	assert.Contains(t, lines[2], "PROJ-DEPLOY-JOB1-44")
	assert.Contains(t, lines[3], MineNotFound)
	assert.Equal(t, "  idle  2 4", lines[5])
}

func TestBoardJSONGivesABuildingAgentsBuild(t *testing.T) {
	m := &app.MyAgents{Starts: []string{"team-a-agent"}, Pools: []app.MyPool{minePool()}}
	d := mineDoc(m)
	out, err := json.Marshal(d)
	require.NoError(t, err)
	var got struct {
		Agents []struct {
			Name  string
			Build *struct {
				Job, BuildKey, PlanName, Group string
			}
		}
	}
	require.NoError(t, json.Unmarshal(out, &got))
	require.NotNil(t, got.Agents[0].Build)
	assert.Equal(t, "PROJ-DEPLOY-JOB1-44", got.Agents[0].Build.Job)
	assert.Equal(t, "integration", got.Agents[0].Build.Group, "the JSON keeps the group as written")
	assert.Nil(t, got.Agents[1].Build)
	assert.Nil(t, got.Agents[2].Build, "build not found: no build field")
}
