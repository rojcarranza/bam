package app

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// agentWorkReads is the most job logs one lookup reads: a server with more
// running jobs shows the rest as "build not found" until a later lookup.
const agentWorkReads = 100

// AgentBuild is the job one agent builds now.
type AgentBuild struct {
	Job      string // the job's result key, such as PROJ-PLAN-JOB1-44
	BuildKey string
	PlanKey  string
	PlanName string
	Branch   string
	// Group is the name of the pin group whose run the build belongs to, "" for
	// a build no pin shows.
	Group     string
	StartedAt time.Time
}

// AgentWork is what the agents of the board build: the job each busy agent
// runs, by agent name, and Note when a part could not be looked up.
type AgentWork struct {
	Builds map[string]AgentBuild
	Note   string
}

// agentWorkCache remembers the agent of each job that runs now, so a job's
// log is read once however many lookups see it.
type agentWorkCache struct {
	mu  sync.Mutex
	job map[string]string
}

// LookAgentWork finds the build each of the busy agents runs. It asks Bamboo
// for nothing while no agent is busy. groups are the pins on the board, which
// name the group a build belongs to.
//
// Bamboo lists the running builds of main plans and of plan branches, and the
// first lines of a running job's log name its agent, read once for each job.
// Jobs that none of the busy agents can run are not read. A part Bamboo
// cannot give leaves its jobs out and says so in Note; it never fails.
func (s *Service) LookAgentWork(ctx context.Context, busy []provider.Agent, groups []PinGroupView) AgentWork {
	if len(busy) == 0 {
		return AgentWork{}
	}
	rb, ok := s.P.(provider.RunningBuilder)
	jr, ok2 := s.P.(provider.JobAgentReader)
	if !ok || !ok2 {
		return AgentWork{Note: "what the agents build is not shown: this Bamboo does not say which builds run"}
	}
	run, err := rb.RunningBuilds(ctx)
	if err != nil {
		return AgentWork{Note: refusal("the running builds", err)}
	}
	var note string
	switch {
	case run.MainMissing && run.BranchesMissing:
		note = "what the agents build is not shown: this Bamboo does not list its running builds"
	case run.MainMissing:
		note = "builds of main plans are not shown: this Bamboo does not list them"
	case run.BranchesMissing:
		note = "builds on plan branches are not shown: this Bamboo does not list them, or took too long"
	}

	type job struct {
		build provider.Build
		key   string
	}
	var jobs []job
	var plans []string
	for _, b := range run.Builds {
		for _, st := range b.Stages {
			for _, j := range st.Jobs {
				if j.State == provider.StateRunning {
					jobs = append(jobs, job{b, j.Key})
				}
			}
		}
		if !slices.Contains(plans, b.PlanKey) {
			plans = append(plans, b.PlanKey)
		}
	}
	if runs, known := s.planRunners(ctx, busy, plans); known {
		jobs = slices.DeleteFunc(jobs, func(j job) bool { return !runs[j.build.PlanKey] })
	}

	c := &s.work
	c.mu.Lock()
	if c.job == nil {
		c.job = map[string]string{}
	}
	var todo []string
	live := map[string]bool{}
	for _, j := range jobs {
		live[j.key] = true
		if _, ok := c.job[j.key]; !ok && len(todo) < agentWorkReads {
			todo = append(todo, j.key)
		}
	}
	for k := range c.job {
		if !live[k] {
			delete(c.job, k) // the job is over
		}
	}
	c.mu.Unlock()

	var wg sync.WaitGroup
	sem := make(chan struct{}, jobAgentReaders)
	for _, k := range todo {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			// A log that is not there yet, or names no agent, is read again
			// by the next lookup.
			if a, err := jr.JobAgent(ctx, k); err == nil && a != "" {
				c.mu.Lock()
				c.job[k] = AgentBaseName(a)
				c.mu.Unlock()
			}
		})
	}
	wg.Wait()

	out := AgentWork{Builds: map[string]AgentBuild{}, Note: note}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, j := range jobs {
		agent := c.job[j.key]
		if agent == "" {
			continue
		}
		if _, taken := out.Builds[agent]; taken {
			continue
		}
		out.Builds[agent] = AgentBuild{Job: j.key, BuildKey: j.build.Key, PlanKey: j.build.PlanKey,
			PlanName: j.build.PlanName, Branch: j.build.Branch, Group: groupOfBuild(groups, j.build),
			StartedAt: j.build.StartedAt}
	}
	return out
}

// groupOfBuild is the name of the pin group whose run b belongs to: a group
// that pins b's plan or branch, or whose pinned build started b as a child.
func groupOfBuild(groups []PinGroupView, b provider.Build) string {
	for _, g := range groups {
		for _, p := range g.Rows() {
			if p.Err != nil {
				continue
			}
			if p.PlanKey != "" && p.PlanKey == b.PlanKey {
				return g.Name
			}
			if p.HasChildren() {
				found := false
				p.Tree.Root.Walk(func(n *TreeNode, _ int) {
					if n.Build.Key == b.Key {
						found = true
					}
				})
				if found {
					return g.Name
				}
			}
		}
	}
	return ""
}

// BuildOf is the build agent runs, or false when none is known.
func (w AgentWork) BuildOf(agent string) (AgentBuild, bool) {
	b, ok := w.Builds[AgentBaseName(agent)]
	return b, ok
}

// RunningFor is how long the build has run at now: from the start of the
// build, 0 when its start is not known.
func (b AgentBuild) RunningFor(now time.Time) time.Duration {
	if b.StartedAt.IsZero() {
		return 0
	}
	return max(now.Sub(b.StartedAt), 0)
}

// Label is the plan with its branch, as a tree line names a build.
func (b AgentBuild) Label() string {
	switch {
	case b.PlanName == "":
		return b.PlanKey
	case b.Branch != "":
		return fmt.Sprintf("%s · %s", b.PlanName, b.Branch)
	}
	return b.PlanName
}
