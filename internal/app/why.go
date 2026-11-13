package app

import (
	"context"
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
	possible map[string]possibleAnswer // by job plan key
}

type possibleAnswer struct {
	at    time.Time
	names []string
	ok    bool
}

// seedAgents keeps an agent list the caller has just read, or its refusal.
func (s *Service) seedAgents(as []provider.Agent, ok bool) {
	c := &s.why
	c.mu.Lock()
	defer c.mu.Unlock()
	c.agentsAt, c.agents, c.agentsOK = s.Clock.Now(), as, ok
}

// whyAgents is the agent list, or false when the server refuses it. A
// refusal removes detail from the answer and is never an error.
func (s *Service) whyAgents(ctx context.Context) ([]provider.Agent, bool) {
	c := &s.why
	c.mu.Lock()
	if !c.agentsAt.IsZero() && s.Clock.Now().Sub(c.agentsAt) < whyTTL {
		as, ok := c.agents, c.agentsOK
		c.mu.Unlock()
		return as, ok
	}
	c.mu.Unlock()
	if ch, ok := s.P.(provider.AgentListChecker); ok && ch.CannotListAgents(ctx) {
		s.seedAgents(nil, false)
		return nil, false
	}
	as, err := s.P.Agents(ctx)
	if ctx.Err() != nil {
		return nil, false
	}
	s.seedAgents(as, err == nil)
	return as, err == nil
}

// whyPossible names the agents that can run the job planKey, or false when
// the server refuses to say.
func (s *Service) whyPossible(ctx context.Context, planKey string) ([]string, bool) {
	c := &s.why
	c.mu.Lock()
	if a, ok := c.possible[planKey]; ok && s.Clock.Now().Sub(a.at) < whyTTL {
		c.mu.Unlock()
		return a.names, a.ok
	}
	c.mu.Unlock()
	names, err := s.P.PossibleAgents(ctx, planKey)
	if ctx.Err() != nil {
		return nil, false
	}
	c.mu.Lock()
	if c.possible == nil {
		c.possible = map[string]possibleAnswer{}
	}
	c.possible[planKey] = possibleAnswer{at: s.Clock.Now(), names: names, ok: err == nil}
	c.mu.Unlock()
	return names, err == nil
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
	w := &provider.Why{AnyAgent: j.AnyAgent}
	if j.AnyAgent != nil && !*j.AnyAgent {
		w.Case = provider.WhyNoAgent
		return w
	}
	agents, agentsOK := s.whyAgents(ctx)
	names, possibleOK := s.whyPossible(ctx, jobPlanKey(j.Key))
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
	if !agentsOK {
		return w
	}
	canRun := func(a provider.Agent) bool { return !possibleOK || slicesContains(names, a.Name) }
	if !possibleOK && j.AnyAgent == nil {
		return w // without either answer an idle agent says nothing about this job
	}
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

func slicesContains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
