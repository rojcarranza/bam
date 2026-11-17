package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-101 P2): Your agents shows a row for each building agent, with
// the job it runs, its plan and branch, the pin group it belongs to and how
// long it runs, and names the idle agents on one line; there is no Queue panel.

// workPools is n pools of agents each: the first `building` agents of each
// build, and the rest are idle. A building agent of the first pool belongs to
// the pin group "integration".
func workPools(pools, perPool, building int) *app.MyAgents {
	m := &app.MyAgents{Starts: []string{"team-a-agent"}}
	for p := range pools {
		pool := app.MyPool{Name: fmt.Sprintf("team-%c-agent-pool-", 'a'+p)}
		for i := range perPool {
			a := app.MyAgent{Name: fmt.Sprintf("%s%d", pool.Name, i+1), State: app.AgentIdle}
			if i < building {
				a.State = app.AgentBuilding
				n := p*100 + i + 1
				group := ""
				if p == 0 {
					group = "integration"
				}
				a.Build = &app.AgentBuild{
					Job: fmt.Sprintf("PROJ-DEPLOY%d-JOB1-%d", n, n), BuildKey: fmt.Sprintf("PROJ-DEPLOY%d-%d", n, n), PlanKey: fmt.Sprintf("PROJ-DEPLOY%d", n),
					PlanName: "Deploy", Branch: "feature-x", Group: group, StartedAt: goldenNow.Add(-time.Duration(i+1) * time.Minute),
				}
				m.Building++
			} else {
				m.Idle++
			}
			pool.Agents = append(pool.Agents, a)
		}
		m.Pools = append(m.Pools, pool)
	}
	return m
}

func workBoard(w, h int, mine *app.MyAgents) Model {
	m := boardModel(w, h)
	data := sampleBoard(goldenNow)
	data.MineSet, data.Mine = true, mine
	data.Needs = app.NeedsYou(data, goldenNow)
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	return m
}

func jobsOf(mine *app.MyAgents) (jobs []string) {
	for _, p := range mine.Pools {
		for _, a := range p.Agents {
			if a.Build != nil {
				jobs = append(jobs, a.Build.Job)
			}
		}
	}
	return jobs
}

// The captain's terminal: 210 by 57, six pools, 76 agents, 19 of them building.
func TestBoardShowsEveryBuildingAgentAt210x57(t *testing.T) {
	mine := workPools(6, 13, 0)
	mine.Pools = mine.Pools[:6]
	mine.Pools[5].Agents = mine.Pools[5].Agents[:11] // 6 pools, 76 agents
	mine.Building, mine.Idle = 0, 0
	built := 0
	for p := range mine.Pools {
		for i := range mine.Pools[p].Agents {
			a := &mine.Pools[p].Agents[i]
			if built < 19 && i < 4 && p < 5 {
				a.State = app.AgentBuilding
				n := built + 1
				a.Build = &app.AgentBuild{Job: fmt.Sprintf("PROJ-RUN-JOB1-%d", n), BuildKey: fmt.Sprintf("PROJ-RUN-%d", n), PlanKey: "PROJ-RUN", PlanName: "Run", StartedAt: goldenNow.Add(-time.Duration(n) * time.Minute)}
				built++
				mine.Building++
			} else {
				a.State = app.AgentIdle
				mine.Idle++
			}
		}
	}
	require.Equal(t, 19, mine.Building)
	require.Equal(t, 76, mine.Building+mine.Idle)

	m := workBoard(210, 57, mine)
	v := ansi.Strip(m.View())
	for _, job := range jobsOf(mine) {
		require.Contains(t, v, job, "every building agent shows without scrolling")
	}
	require.Equal(t, 6, strings.Count(v, "  idle  "), "each pool names its idle agents on one line")
	require.NotContains(t, v, "Queue")
	requireGolden(t, "board-work-210x57", m.View())
}

func TestBoardAgentRowsShowTheGroupPlanBranchAndTime(t *testing.T) {
	v := ansi.Strip(workBoard(200, 50, workPools(1, 4, 2)).View())
	require.Regexp(t, `PROJ-DEPLOY1-JOB1-1\s+Deploy · feature-x\s+Integration\s+1m`, v)
	require.Regexp(t, `PROJ-DEPLOY2-JOB1-2\s+Deploy · feature-x\s+Integration\s+2m`, v)
	require.Contains(t, v, "idle  3 4")
}

// A terminal that is not wide and tall keeps one line a pool; enter lists the
// pool's agents with what each builds.
func TestBoardSmallTerminalKeepsOneLinePerPoolAndEnterListsBuilds(t *testing.T) {
	m := workBoard(120, 45, workPools(3, 4, 2))
	v := ansi.Strip(m.View())
	require.NotContains(t, v, "PROJ-DEPLOY1-JOB1-1", "no rows below 160 columns")
	require.Equal(t, 3, len(m.board.agentSels()), "the cursor stands on pools only")

	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, overlayInfo, m.overlay)
	body := ansi.Strip(m.View())
	require.Contains(t, body, "PROJ-DEPLOY1-JOB1-1")
	require.Contains(t, body, "Deploy · feature-x")
	require.Contains(t, body, "Integration")
}

func TestBoardAgentRowsFollowTheCursorAndOpenTheBuild(t *testing.T) {
	var opened string
	m := workBoard(200, 50, workPools(2, 3, 2))
	m.deps.Open = func(u string) error { opened = u; return nil }
	m, _ = send(m, mkKey("tab")) // Your agents, on the first pool's line
	require.Equal(t, boardAgents, m.board.panel)
	require.Equal(t, 2+2+2, len(m.board.agentSels()), "two pools and two building agents in each")
	m, _ = send(m, mkKey("j")) // the first building agent

	m, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-DEPLOY1-1", m.detail.Key, "enter opens the build the agent runs")
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenBoard, m.screen)

	_, cmd = send(m, mkKey("o"))
	cmd()
	require.Equal(t, labOrigin+"/browse/PROJ-DEPLOY1-1", opened)
}

func TestBoardLOnAnAgentRowOpensItsBuildLogs(t *testing.T) {
	m := workBoard(200, 50, workPools(1, 3, 2))
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-DEPLOY1-1", m.detail.Key)
}

func TestBoardEnterOnABuildNotFoundOpensNothing(t *testing.T) {
	mine := workPools(1, 2, 1)
	mine.Pools[0].Agents[0].Build = nil // building, build not found
	m := workBoard(200, 50, mine)
	require.Contains(t, ansi.Strip(m.View()), "building · build not found")
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenBoard, m.screen)
}

// Intent (#257): an agent running a deployment shows it, o opens it in
// Bamboo, and the build keys do nothing on it.
func TestBoardDeploymentRowSaysDeployingAndOOpensIt(t *testing.T) {
	var opened string
	mine := workPools(1, 2, 1)
	url := labOrigin + "/deploy/viewDeploymentResult.action?deploymentResultId=1769473"
	mine.Pools[0].Agents[1].State = app.AgentBuilding
	mine.Pools[0].Agents[1].Deploy = &app.AgentDeploy{Project: "Lab release", Environment: "Staging", Release: "release-1",
		URL: url, StartedAt: goldenNow.Add(-5 * time.Minute)}
	mine.Building, mine.Idle = 2, 0
	m := workBoard(200, 50, mine)
	m.deps.Open = func(u string) error { opened = u; return nil }
	view := ansi.Strip(m.View())
	require.Contains(t, view, "deploying")
	require.Contains(t, view, "Lab release · Staging · release-1")
	require.NotContains(t, view, "build not found")

	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("j")) // the deploying agent
	for _, k := range []string{"enter", "l", "t"} {
		m, _ = send(m, mkKey(k))
		require.Equal(t, screenBoard, m.screen, "%s opens no build on a deployment", k)
	}
	_, cmd := send(m, mkKey("o"))
	require.NotNil(t, cmd)
	cmd()
	require.Equal(t, url, opened)
}

// P on an agent row asks for a group and pins the plan the agent builds.
func TestBoardPOnAnAgentRowPinsThePlanItBuildsAfterAskingForTheGroup(t *testing.T) {
	var group, plan string
	m := workBoard(200, 50, workPools(1, 3, 2))
	m.deps.PinAdd = func(g, p string) error { group, plan = g, p; return nil }
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("P"))
	require.Equal(t, inputPin, m.inputFor)
	require.Contains(t, m.input.Prompt, "PROJ-DEPLOY1")
	for _, r := range "release" {
		m, _ = send(m, mkKey(string(r)))
	}
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, inputNone, m.inputFor)
	m, _ = send(m, cmd())
	require.Equal(t, "release", group)
	require.Equal(t, "PROJ-DEPLOY1", plan)
	require.Contains(t, m.status, "pinned PROJ-DEPLOY1 in release")

	// An empty group pins nothing, and esc cancels.
	calls := 0
	m = workBoard(200, 50, workPools(1, 3, 2))
	m.deps.PinAdd = func(string, string) error { calls++; return nil }
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("P"))
	m, cmd = send(m, mkKey("enter"))
	require.Nil(t, cmd)
	m, _ = send(m, mkKey("P"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, inputNone, m.inputFor)
	require.Zero(t, calls)
}

// Intent (BAM-53, machine 6, defect 2): the cursor was a number, so a read
// that listed another building agent above it moved P onto another build's
// plan. The cursor of an agent row is the agent.
func TestAgentRowSelectionStaysOnItsAgentAfterARefreshAddsOneAbove(t *testing.T) {
	m := workBoard(200, 50, workPools(1, 4, 2))
	m, _ = send(m, mkKey("tab"))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("j")) // the second building agent
	b, ok := m.board.agentBuild()
	require.True(t, ok)
	require.Equal(t, "PROJ-DEPLOY2-2", b.Key)

	m = readAgain(m, func(d *app.Board) {
		mine := workPools(1, 4, 3) // agent 3 now builds too, below: the order above is unchanged
		mine.Pools[0].Agents[0].Build, mine.Pools[0].Agents[2].Build = mine.Pools[0].Agents[2].Build, mine.Pools[0].Agents[0].Build
		d.MineSet, d.Mine = true, mine
	})
	b, ok = m.board.agentBuild()
	require.True(t, ok)
	require.Equal(t, "PROJ-DEPLOY2-2", b.Key)
}

func TestBoardSaysWhyBuildsAreNotShown(t *testing.T) {
	m := workBoard(200, 50, workPools(1, 3, 2))
	data := m.board.data
	data.WorkNote = "builds on plan branches are not shown: this Bamboo does not list them, or took too long"
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	require.Contains(t, ansi.Strip(m.View()), "builds on plan branches are not shown")
}
