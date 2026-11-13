package app

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// whyTTL is how long the agent list is kept, so a watched tree or a
// refreshing board does not ask again every poll. It is also the shortest
// time between two asks for agent details; what those said is kept for
// agentJobsTTL.
const whyTTL = 30 * time.Second

// whyJobs is how many of a build's queued jobs are looked at.
const whyJobs = 5

// whyCache holds what the why-it-waits sources answered: the agent list for
// whyTTL, and the jobs each agent can run for agentJobsTTL.
type whyCache struct {
	mu       sync.Mutex
	agentsAt time.Time
	agents   []provider.Agent
	agentsOK bool

	loaded   bool                 // jobs holds what the store kept
	jobs     map[string]agentJobs // by agent name
	jobsTry  time.Time            // when bam last asked for agent details
	jobsWait time.Duration        // how long after jobsTry it may ask again
	jobsOK   bool                 // false when the last ask was refused or failed
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
// name order, or false when bam cannot tell: the server refuses to say, or
// no agent's list names the job and some list was read before the job was
// queued at queuedAt, so the job's plan may be newer than the lists. A zero
// queuedAt, a job whose queued time is not known, trusts only a list read
// now.
//
// Each agent's list is kept for agentJobsTTL, in the Service and in its
// AgentJobs store, so only an agent bam has no list for, or an old list, is
// read again, and no more often than whyTTL apart. Like whyAgents, it holds
// the cache while it asks.
func (s *Service) whyPossible(ctx context.Context, agents []provider.Agent, planKey string, queuedAt time.Time) ([]string, bool) {
	c := &s.why
	c.mu.Lock()
	defer c.mu.Unlock()
	now := s.Clock.Now()
	if queuedAt.IsZero() {
		queuedAt = now
	}
	if !c.loaded {
		c.loaded, c.jobsOK = true, true
		c.jobs = map[string]agentJobs{}
		if s.AgentJobs != nil {
			c.jobs = s.AgentJobs.Load(s.Origin, now)
		}
	}
	var stale []provider.Agent
	for _, a := range agents {
		if e, ok := c.jobs[a.Name]; !ok || now.Sub(e.Read) >= agentJobsTTL {
			stale = append(stale, a)
		}
	}
	if len(stale) > 0 && (c.jobsTry.IsZero() || now.Sub(c.jobsTry) >= c.jobsWait) {
		jobs, err := s.P.AgentJobs(ctx, stale)
		if ctx.Err() != nil {
			return nil, false
		}
		c.jobsTry = now
		switch {
		case err == nil:
			for _, a := range stale {
				// An agent left out was removed since the list was read: it runs nothing.
				c.jobs[a.Name] = agentJobs{Read: now, Jobs: slices.Sorted(slices.Values(jobs[a.Name]))}
			}
			listed := map[string]bool{}
			for _, a := range agents {
				listed[a.Name] = true
			}
			maps.DeleteFunc(c.jobs, func(name string, _ agentJobs) bool { return !listed[name] })
			if s.AgentJobs != nil {
				s.AgentJobs.Save(s.Origin, c.jobs)
			}
			c.jobsOK, c.jobsWait = true, whyTTL
		case errors.Is(err, errs.ErrUnsupported):
			c.jobsOK, c.jobsWait = false, whyTTL // learned by the provider: asking again sends nothing
		default:
			c.jobsOK, c.jobsWait = false, agentJobsRetry
		}
	}
	if !c.jobsOK && len(stale) > 0 {
		return nil, false // a list bam needs could not be read
	}
	names := []string{}
	unsure := false
	for _, a := range agents {
		e, ok := c.jobs[a.Name]
		switch {
		case !ok:
			unsure = true // listed since bam last asked: not read yet
		case e.canRun(planKey):
			names = append(names, a.Name)
		case e.Read.Before(queuedAt):
			unsure = true
		}
	}
	if len(names) == 0 && unsure {
		return nil, false
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
		queuedAt := j.QueuedAt
		if queuedAt.IsZero() {
			queuedAt = b.QueuedAt
		}
		if queuedAt.IsZero() {
			queuedAt = b.StartedAt // a queued build can carry only the time it was started, as on the board
		}
		w := s.whyJob(ctx, j, ahead, queuedAt)
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

func (s *Service) whyJob(ctx context.Context, j provider.Job, ahead int, queuedAt time.Time) *provider.Why {
	yes, no := true, false
	w := &provider.Why{}
	agents, agentsOK := s.whyAgents(ctx)
	if !agentsOK {
		return w
	}
	names, possibleOK := s.whyPossible(ctx, agents, jobPlanKey(j.Key), queuedAt)
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
