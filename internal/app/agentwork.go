package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
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

// AgentDeploy is the deployment one agent runs now.
type AgentDeploy struct {
	Project     string // the deployment project's name
	Environment string
	Release     string
	URL         string // the deployment's page in Bamboo
	StartedAt   time.Time
}

// AgentWork is what the agents of the board build: the job or the deployment
// each busy agent runs, by agent name, and Note when a part could not be
// looked up.
type AgentWork struct {
	Builds  map[string]AgentBuild
	Deploys map[string]AgentDeploy
	Note    string
}

// agentWorkCache remembers, while they run, the agent of each job, each build
// and when each deployment started, so each is read once however many
// lookups see it.
type agentWorkCache struct {
	mu     sync.Mutex
	job    map[string]string
	build  map[string]provider.Build // by build result key
	deploy map[string]time.Time      // by deployment result id
}

// LookAgentWork finds the build or the deployment each of the busy agents
// runs. It asks Bamboo for nothing while no agent is busy. groups are the
// pins on the board, which name the group a build belongs to.
//
// It reads Bamboo's Build activity list (see LiveAgentWork). When the server
// does not give that list, Bamboo lists the running builds of main plans and
// of plan branches, and the first lines of a running job's log name its
// agent, read once for each job. Jobs that none of the busy agents can run
// are not read. A part Bamboo cannot give leaves its jobs out and says so in
// Note; it never fails.
func (s *Service) LookAgentWork(ctx context.Context, busy []provider.Agent, groups []PinGroupView) AgentWork {
	if len(busy) == 0 {
		return AgentWork{}
	}
	live, ok := s.LiveAgentWork(ctx, busy, groups)
	if ok {
		return live
	}
	w := s.slowAgentWork(ctx, busy, groups)
	if live.Note != "" {
		w.Note = strings.Join(slices.DeleteFunc([]string{live.Note, w.Note}, func(n string) bool { return n == "" }), "; ")
	}
	return w
}

// slowAgentWork is LookAgentWork without the Build activity list.
func (s *Service) slowAgentWork(ctx context.Context, busy []provider.Agent, groups []PinGroupView) AgentWork {
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

// activityRefused says what the board leaves out on a server that refuses
// the Build activity list.
const activityRefused = "deployments are not shown: Bamboo gives no Build activity list"

// LiveAgentWork finds what each of the busy agents runs from Bamboo's Build
// activity list, which names the agent of every running job and deployment
// in one read, the moment it starts. Each build is then read once while it
// runs, for its plan, branch, start and pin group, and each deployment's
// start once. ok is false when the server does not give the list, its read
// failed, or it names no agent while agents are busy: the caller keeps what
// it had or looks the slow way. When the server refuses the list, Note says
// what the slow way leaves out.
func (s *Service) LiveAgentWork(ctx context.Context, busy []provider.Agent, groups []PinGroupView) (w AgentWork, ok bool) {
	if len(busy) == 0 {
		return AgentWork{}, true
	}
	ar, isReader := s.P.(provider.ActivityReader)
	if !isReader {
		return AgentWork{Note: activityRefused}, false
	}
	acts, err := ar.Activity(ctx)
	if errors.Is(err, errs.ErrUnsupported) {
		return AgentWork{Note: activityRefused}, false
	}
	if err != nil || len(acts) == 0 {
		return AgentWork{}, false
	}
	mine := map[string]bool{}
	for _, a := range busy {
		mine[AgentBaseName(a.Name)] = true
	}
	var runs []provider.AgentActivity
	for _, a := range acts {
		if mine[AgentBaseName(a.Agent)] {
			runs = append(runs, a)
		}
	}

	c := &s.work
	c.mu.Lock()
	if c.build == nil {
		c.build, c.deploy = map[string]provider.Build{}, map[string]time.Time{}
	}
	var builds, deploys []string
	liveBuild, liveDeploy := map[string]bool{}, map[string]bool{}
	for _, a := range runs {
		switch {
		case a.Deploy != nil:
			if _, known := c.deploy[a.Deploy.ID]; !known && !liveDeploy[a.Deploy.ID] {
				deploys = append(deploys, a.Deploy.ID)
			}
			liveDeploy[a.Deploy.ID] = true
		default:
			if _, known := c.build[a.Build]; !known && !liveBuild[a.Build] {
				builds = append(builds, a.Build)
			}
			liveBuild[a.Build] = true
		}
	}
	for k := range c.build {
		if !liveBuild[k] {
			delete(c.build, k) // the build is over
		}
	}
	for k := range c.deploy {
		if !liveDeploy[k] {
			delete(c.deploy, k)
		}
	}
	c.mu.Unlock()

	var wg sync.WaitGroup
	sem := make(chan struct{}, jobAgentReaders)
	read := func(f func()) {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() == nil {
				f()
			}
		})
	}
	// A read that fails is tried again by the next lookup.
	for _, k := range builds {
		read(func() {
			if b, err := s.P.GetBuild(ctx, k); err == nil {
				c.mu.Lock()
				c.build[k] = b
				c.mu.Unlock()
			}
		})
	}
	for _, id := range deploys {
		read(func() {
			if t, err := ar.DeploymentStarted(ctx, id); err == nil {
				c.mu.Lock()
				c.deploy[id] = t
				c.mu.Unlock()
			}
		})
	}
	wg.Wait()

	w = AgentWork{Builds: map[string]AgentBuild{}, Deploys: map[string]AgentDeploy{}}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range runs {
		agent := AgentBaseName(a.Agent)
		if _, taken := w.Builds[agent]; taken {
			continue
		}
		if _, taken := w.Deploys[agent]; taken {
			continue
		}
		if d := a.Deploy; d != nil {
			w.Deploys[agent] = AgentDeploy{Project: d.Project, Environment: d.Environment, Release: d.Release,
				URL: d.URL, StartedAt: c.deploy[d.ID]}
			continue
		}
		b, read := c.build[a.Build]
		if !read {
			b = provider.Build{Key: a.Build, PlanKey: a.PlanKey}
		}
		w.Builds[agent] = AgentBuild{Job: a.Job, BuildKey: b.Key, PlanKey: b.PlanKey, PlanName: b.PlanName,
			Branch: b.Branch, Group: groupOfBuild(groups, b), StartedAt: b.StartedAt}
	}
	return w, true
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

// DeployOf is the deployment agent runs, or false when it runs none bam knows.
func (w AgentWork) DeployOf(agent string) (AgentDeploy, bool) {
	d, ok := w.Deploys[AgentBaseName(agent)]
	return d, ok
}

// RunningFor is how long the build has run at now: from the start of the
// build, 0 when its start is not known.
func (b AgentBuild) RunningFor(now time.Time) time.Duration { return elapsedSince(b.StartedAt, now) }

// RunningFor is how long the deployment has run at now, 0 when its start is
// not known.
func (d AgentDeploy) RunningFor(now time.Time) time.Duration { return elapsedSince(d.StartedAt, now) }

func elapsedSince(start, now time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return max(now.Sub(start), 0)
}

// Label is the deployment project, environment and release, as Bamboo names
// them, leaving out any it did not give.
func (d AgentDeploy) Label() string {
	var parts []string
	for _, p := range []string{d.Project, d.Environment, d.Release} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " · ")
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
