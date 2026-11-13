package app

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// whyTTL is how long the agent list and a job's possible agents are kept, so
// a watched tree or a refreshing board does not ask again every poll.
const whyTTL = 30 * time.Second

// whyJobs is how many of a build's queued jobs are looked at.
const whyJobs = 5

// whyCache holds what the why-it-waits sources answered, for whyTTL.
type whyCache struct {
	mu       sync.Mutex
	agentsAt time.Time
	agents   []provider.Agent
	agentsOK bool
	jobsAt   time.Time
	jobs     map[string][]string // agent name → the jobs it can run
	jobsOK   bool
}

// seedAgents keeps an agent list the caller has just read, or its refusal.
func (s *Service) seedAgents(as []provider.Agent, ok bool) {
	c := &s.why
	c.mu.Lock()
	defer c.mu.Unlock()
	c.agentsAt, c.agents, c.agentsOK = s.Clock.Now(), as, ok
}

// whyAgents is the agent list, or false when the server refuses it. A
// refusal removes detail from the answer and is never an error. The cache
// stays locked while it is read, so concurrent callers ask the server once.
func (s *Service) whyAgents(ctx context.Context) ([]provider.Agent, bool) {
	c := &s.why
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.agentsAt.IsZero() && s.Clock.Now().Sub(c.agentsAt) < whyTTL {
		return c.agents, c.agentsOK
	}
	if ch, ok := s.P.(provider.AgentListChecker); ok && ch.CannotListAgents(ctx) {
		c.agentsAt, c.agents, c.agentsOK = s.Clock.Now(), nil, false
		return nil, false
	}
	as, err := s.P.Agents(ctx)
	if ctx.Err() != nil {
		return nil, false
	}
	c.agentsAt, c.agents, c.agentsOK = s.Clock.Now(), as, err == nil
	return as, err == nil
}

// whyPossible names the agents in agents that can run the job planKey, in
// name order, or false when the server refuses to say. Like whyAgents, it
// holds the cache while it asks.
func (s *Service) whyPossible(ctx context.Context, agents []provider.Agent, planKey string) ([]string, bool) {
	c := &s.why
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jobsAt.IsZero() || s.Clock.Now().Sub(c.jobsAt) >= whyTTL {
		jobs, err := s.P.AgentJobs(ctx, agents)
		if ctx.Err() != nil {
			return nil, false
		}
		c.jobsAt, c.jobs, c.jobsOK = s.Clock.Now(), jobs, err == nil
	}
	if !c.jobsOK {
		return nil, false
	}
	names := []string{}
	for name, keys := range c.jobs {
		if slices.Contains(keys, planKey) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, true
}

// jobPlanKey is the job's plan key, PROJ-PLAN-JOB1, from its result key
// PROJ-PLAN-JOB1-44.
func jobPlanKey(resultKey string) string {
	if i := strings.LastIndexByte(resultKey, '-'); i > 0 {
		return resultKey[:i]
	}
	return resultKey
}

func free(a provider.Agent) bool { return a.Online && a.Enabled && !a.Busy }

// WhyWaits says why the queued build b waits. ahead is how many builds come
// before it in the queue, -1 when that is not known. It never fails: a source
// the server refuses removes its detail, and nil means nothing could be said.
func (s *Service) WhyWaits(ctx context.Context, b provider.Build, ahead int) *provider.Why {
	if b.State != provider.StateQueued {
		return nil
	}
	var jobs []provider.Job
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if j.State == provider.StateQueued {
				jobs = append(jobs, j)
			}
		}
		if len(jobs) > 0 {
			break // later stages wait for this one, not for an agent
		}
	}
	if len(jobs) > whyJobs {
		jobs = jobs[:whyJobs]
	}
	var worst *provider.Why
	for _, j := range jobs {
		w := s.whyJob(ctx, j, ahead)
		if worst == nil || whyRank(w.Case) < whyRank(worst.Case) {
			worst = w
		}
	}
	if worst == nil || (worst.Case == "" && worst.AnyAgent == nil) {
		return nil
	}
	return worst
}

func whyRank(c string) int {
	switch c {
	case provider.WhyNoAgent:
		return 0
	case provider.WhyBusy:
		return 1
	case provider.WhyAhead:
		return 2
	case provider.WhyNext:
		return 3
	}
	return 4
}

func (s *Service) whyJob(ctx context.Context, j provider.Job, ahead int) *provider.Why {
	yes, no := true, false
	w := &provider.Why{}
	agents, agentsOK := s.whyAgents(ctx)
	if !agentsOK {
		return w
	}
	names, possibleOK := s.whyPossible(ctx, agents, jobPlanKey(j.Key))
	if possibleOK {
		w.Count = len(names)
		if len(names) > 0 && len(names) <= 3 {
			w.Agents = names
		}
		if len(names) == 0 {
			w.AnyAgent, w.Case = &no, provider.WhyNoAgent
			return w
		}
		w.AnyAgent = &yes
	}
	canRun := func(a provider.Agent) bool { return !possibleOK || slices.Contains(names, a.Name) }
	anyFree := false
	for _, a := range agents {
		if free(a) && canRun(a) {
			anyFree = true
		}
	}
	switch {
	case !anyFree:
		w.Case = provider.WhyBusy
	case !possibleOK:
		// An idle agent may still not match the job: say nothing more.
	case ahead == 0:
		w.Case = provider.WhyNext
	case ahead > 0:
		w.Case, w.Ahead = provider.WhyAhead, ahead
	}
	return w
}
