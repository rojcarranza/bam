package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-101 P2): Your agents says what each building agent builds. Bamboo
// does not, so bam lists the running builds, reads the agent of each running
// job from its log once, and matches it to your busy agents.

func runningBuild(key, plan, branch string, started time.Time, jobs ...string) provider.Build {
	b := provider.Build{Key: key, PlanKey: plan, PlanName: "Deploy", Branch: branch, State: provider.StateRunning, StartedAt: started}
	var js []provider.Job
	for _, j := range jobs {
		js = append(js, provider.Job{Key: j, State: provider.StateRunning})
	}
	b.Stages = []provider.Stage{{Name: "Run", Jobs: js}}
	return b
}

// workService is a server with three agents of yours (two busy) and one of
// another team's, and the builds those busy agents run. It gives no Build
// activity list, so bam looks the slow way; activityService gives one.
func workService(t *testing.T) (*Service, *fake.Provider) {
	t.Helper()
	svc := newService(t, nil)
	svc.Config().Project.Agents = []string{"team-a-agent"}
	now := svc.Clock.Now()
	p := fakeBamboo()
	p.AgentList = []provider.Agent{
		agent("team-a-agent-0", true, true, true),
		agent("team-a-agent-1", true, true, false),
		agent("team-a-agent-2", true, true, true),
		agent("other-agent-0", true, true, true),
	}
	p.AgentJobMap = map[string][]string{
		"team-a-agent-0": {"PROJ-MAIN-JOB1", "PROJ-BRANCH5-JOB1"},
		"team-a-agent-1": {"PROJ-MAIN-JOB1"},
		"team-a-agent-2": {"PROJ-MAIN-JOB1", "PROJ-BRANCH5-JOB1"},
		"other-agent-0":  {"OTHER-PLAN-JOB1"},
	}
	p.Running = provider.RunningBuilds{Builds: []provider.Build{
		runningBuild("PROJ-MAIN-30", "PROJ-MAIN", "", now.Add(-12*time.Minute), "PROJ-MAIN-JOB1-30"),
		runningBuild("PROJ-BRANCH5-7", "PROJ-BRANCH5", "feature-x", now.Add(-3*time.Minute), "PROJ-BRANCH5-JOB1-7"),
		runningBuild("OTHER-PLAN-4", "OTHER-PLAN", "", now.Add(-time.Minute), "OTHER-PLAN-JOB1-4"),
	}}
	p.JobAgentMap = map[string]string{
		"PROJ-MAIN-JOB1-30":   "team-a-agent-0",
		"PROJ-BRANCH5-JOB1-7": "team-a-agent-2 (2)",
		"OTHER-PLAN-JOB1-4":   "other-agent-0",
	}
	p.NoActivity = true
	svc.P = p
	return svc, p
}

// Intent (#257): on Bamboo 9.6.9 Your agents said "build not found" for an
// agent on a plan-branch build or a deployment, and for a build that had just
// started. Bamboo's Build activity list names every agent's job and
// deployment at once, so bam reads it on every board read.

// activityService is workService with the Build activity list: agent 1 runs a
// deployment, and the builds can be read by key.
func activityService(t *testing.T) (*Service, *fake.Provider) {
	t.Helper()
	svc, p := workService(t)
	now := svc.Clock.Now()
	p.NoActivity = false
	p.AgentList[1].Busy = true
	p.Acts = []provider.AgentActivity{
		{Agent: "team-a-agent-0", Job: "PROJ-MAIN-JOB1-30", Build: "PROJ-MAIN-30", PlanKey: "PROJ-MAIN"},
		{Agent: "team-a-agent-1", Deploy: &provider.Deployment{ID: "1769473", Project: "Lab release", Environment: "Staging",
			Release: "release-1", URL: "https://bamboo.example.com/deploy/viewDeploymentResult.action?deploymentResultId=1769473"}},
		{Agent: "team-a-agent-2 (2)", Job: "PROJ-BRANCH5-JOB1-7", Build: "PROJ-BRANCH5-7", PlanKey: "PROJ-BRANCH5"},
		{Agent: "other-agent-0", Job: "OTHER-PLAN-JOB1-4", Build: "OTHER-PLAN-4", PlanKey: "OTHER-PLAN"},
	}
	p.DeployStarts = map[string]time.Time{"1769473": now.Add(-5 * time.Minute)}
	p.History = map[string][]provider.Build{}
	for _, b := range p.Running.Builds {
		p.History[b.PlanKey] = []provider.Build{b}
	}
	return svc, p
}

func TestBoardShowsWhatEachAgentRunsFromTheActivityList(t *testing.T) {
	svc, p := activityService(t)
	now := svc.Clock.Now()
	b, err := svc.Board(context.Background(), pins("integration", "PROJ-MAIN"))
	require.NoError(t, err)
	agents := b.Mine.Pools[0].Agents
	require.Len(t, agents, 3)

	a0 := agents[0].Build
	require.NotNil(t, a0, "a main-plan build")
	assert.Equal(t, "PROJ-MAIN-JOB1-30", a0.Job)
	assert.Equal(t, "PROJ-MAIN-30", a0.BuildKey)
	assert.Equal(t, "integration", a0.Group)
	assert.Equal(t, 12*time.Minute, a0.RunningFor(now))

	assert.Nil(t, agents[1].Build)
	d := agents[1].Deploy
	require.NotNil(t, d, "a deployment")
	assert.Equal(t, "Lab release · Staging · release-1", d.Label())
	assert.Equal(t, 5*time.Minute, d.RunningFor(now))
	assert.Equal(t, p.Acts[1].Deploy.URL, d.URL)

	a2 := agents[2].Build
	require.NotNil(t, a2, "a build on a plan branch, whose agent carries a repeat suffix")
	assert.Equal(t, "PROJ-BRANCH5-JOB1-7", a2.Job)
	assert.Equal(t, "Deploy · feature-x", a2.Label())
	assert.Equal(t, 3*time.Minute, a2.RunningFor(now))

	assert.Empty(t, b.WorkNote)
	assert.Zero(t, p.RunningCalls, "no slow lookup")
	assert.Empty(t, p.JobAgentCalls)
	assert.Zero(t, p.GetCalls("OTHER-PLAN-4"), "another team's build is not read")
}

func TestQuickReadsFollowTheActivityListAndReadEachBuildOnce(t *testing.T) {
	svc, p := activityService(t)
	now := svc.Clock.Now()
	first, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)

	// Between two quick reads agent 1's deployment ends and it starts a build.
	next := runningBuild("PROJ-MAIN-31", "PROJ-MAIN", "", now, "PROJ-MAIN-JOB1-31")
	p.History["PROJ-MAIN"] = append(p.History["PROJ-MAIN"], next)
	p.Acts[1] = provider.AgentActivity{Agent: "team-a-agent-1", Job: "PROJ-MAIN-JOB1-31", Build: "PROJ-MAIN-31", PlanKey: "PROJ-MAIN"}
	svc.Clock.(*fakeClock).After(15 * time.Second)
	quick, err := svc.RefreshBoard(context.Background(), nil, &first)
	require.NoError(t, err)

	a1 := quick.Mine.Pools[0].Agents[1]
	assert.Nil(t, a1.Deploy, "the quick read sees the deployment end")
	require.NotNil(t, a1.Build, "and the build that started")
	assert.Equal(t, "PROJ-MAIN-JOB1-31", a1.Build.Job)
	assert.Equal(t, 2, p.ActsCalls, "one read of the list for each board read")
	assert.Equal(t, 1, p.GetCalls("PROJ-MAIN-30"), "a running build is read once")
	assert.Equal(t, []string{"1769473"}, p.DeployCalls, "a deployment's start is read once")
	assert.Zero(t, p.RunningCalls)
}

func TestBoardLooksTheSlowWayWithoutTheActivityList(t *testing.T) {
	for name, c := range map[string]struct {
		set     func(*fake.Provider)
		refused bool // the board says what the slow way leaves out
	}{
		"the server refuses the list":            {func(p *fake.Provider) { p.NoActivity = true }, true},
		"the list fails":                         {func(p *fake.Provider) { p.ActsErr = errs.Bamboof("Bamboo server error 503") }, false},
		"the list names no agent while one runs": {func(p *fake.Provider) { p.Acts = nil }, false},
	} {
		t.Run(name, func(t *testing.T) {
			svc, p := activityService(t)
			c.set(p)
			first, err := svc.Board(context.Background(), nil)
			require.NoError(t, err)
			assert.Equal(t, 1, p.RunningCalls, "the full read looks the slow way")
			require.NotNil(t, first.Mine.Pools[0].Agents[0].Build)
			if c.refused {
				assert.Contains(t, first.WorkNote, "deployments are not shown")
			} else {
				assert.NotContains(t, first.WorkNote, "Build activity list", "a list that failed once is no refusal")
			}

			quick, err := svc.RefreshBoard(context.Background(), nil, &first)
			require.NoError(t, err)
			assert.Equal(t, 1, p.RunningCalls, "a quick read keeps the board's answer")
			assert.Equal(t, first.Mine.Pools[0].Agents[0].Build, quick.Mine.Pools[0].Agents[0].Build)
		})
	}
}

func TestBoardShowsWhatEachBuildingAgentBuilds(t *testing.T) {
	svc, p := workService(t)
	now := svc.Clock.Now()
	groups := []PinGroupView{{Name: "integration", Pins: []PinView{{Pin: "PROJ-BRANCH5", PlanKey: "PROJ-BRANCH5"}}}}
	_ = groups
	b, err := svc.Board(context.Background(), pins("integration", "PROJ-MAIN"))
	require.NoError(t, err)
	require.NotNil(t, b.Mine)
	agents := b.Mine.Pools[0].Agents
	require.Len(t, agents, 3)

	a0 := agents[0].Build
	require.NotNil(t, a0, "a branch build and a main build are both found")
	assert.Equal(t, "PROJ-MAIN-JOB1-30", a0.Job)
	assert.Equal(t, "PROJ-MAIN-30", a0.BuildKey)
	assert.Equal(t, "Deploy", a0.Label())
	assert.Equal(t, "integration", a0.Group, "the build of a pinned plan names its group")
	assert.Equal(t, 12*time.Minute, a0.RunningFor(now))

	assert.Nil(t, agents[1].Build, "an idle agent builds nothing")
	a2 := agents[2].Build
	require.NotNil(t, a2, "a log that names the agent with a repeat suffix still matches")
	assert.Equal(t, "Deploy · feature-x", a2.Label())
	assert.Empty(t, a2.Group, "no pin shows this build")
	assert.Equal(t, activityRefused, b.WorkNote, "only that the server gives no Build activity list")
	assert.Equal(t, 1, p.RunningCalls)
}

func TestBoardSaysBuildingWhenNoBuildWasFoundForABusyAgent(t *testing.T) {
	svc, p := workService(t)
	p.JobAgentMap = map[string]string{"PROJ-MAIN-JOB1-30": "team-a-agent-0"} // the branch job's log names no agent
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	agents := b.Mine.Pools[0].Agents
	require.NotNil(t, agents[0].Build)
	assert.Nil(t, agents[2].Build)
	assert.Equal(t, AgentBuilding, agents[2].State, "it still counts as building")
}

func TestNoLookupWhileNoAgentIsBusy(t *testing.T) {
	svc, p := workService(t)
	for i := range p.AgentList {
		p.AgentList[i].Busy = false
	}
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.Zero(t, p.RunningCalls)
	assert.Empty(t, p.JobAgentCalls)
	assert.Empty(t, b.WorkNote)
}

func TestBoardReadsEachJobsLogOnceAndKeepsTheLookupOnQuickReads(t *testing.T) {
	svc, p := workService(t)
	first, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, p.JobAgentCalls, 2, "only the jobs your busy agents can run are read")
	assert.NotContains(t, p.JobAgentCalls, "OTHER-PLAN-JOB1-4")

	quick, err := svc.RefreshBoard(context.Background(), nil, &first)
	require.NoError(t, err)
	assert.Equal(t, 1, p.RunningCalls, "a quick read keeps the lookup of the board on screen")
	require.NotNil(t, quick.Mine.Pools[0].Agents[0].Build)

	svc.Clock.(*fakeClock).After(time.Minute)
	_, err = svc.Board(context.Background(), nil) // the full read a minute on
	require.NoError(t, err)
	assert.Equal(t, 2, p.RunningCalls)
	assert.Len(t, p.JobAgentCalls, 2, "a job already read is not read again")
}

func TestLookupReadsAgainAJobWhoseLogNamedNoAgentYet(t *testing.T) {
	svc, p := workService(t)
	p.JobAgentMap["PROJ-BRANCH5-JOB1-7"] = ""
	_, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	p.JobAgentMap["PROJ-BRANCH5-JOB1-7"] = "team-a-agent-2"
	b, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, b.Mine.Pools[0].Agents[2].Build)
}

func TestBoardNamesAListBambooLacks(t *testing.T) {
	for _, c := range []struct {
		name string
		run  provider.RunningBuilds
		err  error
		note string
	}{
		{"branches", provider.RunningBuilds{BranchesMissing: true}, nil, "builds on plan branches are not shown"},
		{"main", provider.RunningBuilds{MainMissing: true}, nil, "builds of main plans are not shown"},
		{"both", provider.RunningBuilds{MainMissing: true, BranchesMissing: true}, nil, "what the agents build is not shown"},
		{"refused", provider.RunningBuilds{}, errs.Authf("403"), "the running builds is not shown: Bamboo refused it"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, p := workService(t)
			p.Running, p.RunningErr = c.run, c.err
			b, err := svc.Board(context.Background(), nil)
			require.NoError(t, err, "a missing list is a line on the board, not a failure")
			assert.Contains(t, b.WorkNote, c.note)
			for _, a := range b.Mine.Pools[0].Agents {
				assert.Nil(t, a.Build)
			}
		})
	}
}

func TestLookupAsksDetailsOfYourBusyAgentsOnly(t *testing.T) {
	svc, p := workService(t)
	_, err := svc.Board(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, p.AgentJobsCalls)
	assert.ElementsMatch(t, []string{"team-a-agent-0", "team-a-agent-2"}, p.AgentJobsAsked[0], "not the idle one, not another team's")

	other, p2 := workService(t)
	_, err = other.Board(WholeServer(context.Background()), nil)
	require.NoError(t, err)
	assert.Zero(t, p2.RunningCalls, "the whole-server view has no Your agents to fill")
}

func TestAgentShowFindsABuildOnAPlanBranch(t *testing.T) {
	svc, _ := workService(t)
	rec, err := svc.AgentRecord(context.Background(), "team-a-agent-2", 10)
	require.NoError(t, err)
	require.Len(t, rec.Now, 1, "the newest builds hold no running job; the lookup finds the branch build")
	assert.Equal(t, "PROJ-BRANCH5-JOB1-7", rec.Now[0].Key)
	assert.Equal(t, "Deploy · feature-x", rec.Now[0].Name)

	idle, err := svc.AgentRecord(context.Background(), "team-a-agent-1", 10)
	require.NoError(t, err)
	assert.Empty(t, idle.Now)
}

func TestGroupOfBuildFollowsAPinnedRunsChildren(t *testing.T) {
	child := provider.Build{Key: "PROJ-CHILD-2", PlanKey: "PROJ-CHILD"}
	root := provider.Build{Key: "PROJ-ROOT-9", PlanKey: "PROJ-ROOT"}
	tree := &Tree{Root: &TreeNode{Build: root, Children: []*TreeNode{{Build: child}}}}
	groups := []PinGroupView{{Name: "integration", Pins: []PinView{{Pin: "PROJ-ROOT", PlanKey: "PROJ-ROOT", Tree: tree}}}}
	assert.Equal(t, "integration", groupOfBuild(groups, child), "a child pipeline of a pinned run")
	assert.Equal(t, "integration", groupOfBuild(groups, provider.Build{Key: "PROJ-ROOT-10", PlanKey: "PROJ-ROOT"}))
	assert.Empty(t, groupOfBuild(groups, provider.Build{Key: "PROJ-OTHER-1", PlanKey: "PROJ-OTHER"}))
	_ = fmt.Sprint
}
