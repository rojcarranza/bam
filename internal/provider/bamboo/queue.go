package bamboo

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// queueListMax is how many queued builds one Queue call reads. Bamboo lists
// the queue in order; a position past this is reported as unknown.
const queueListMax = "1000"

// agentReaders is how many agent details AgentJobs reads at once.
const agentReaders = 6

type queueListDTO struct {
	QueuedBuilds struct {
		QueuedBuild []queueDTO `json:"queuedBuild"`
	} `json:"queuedBuilds"`
}

// Queue lists the builds waiting for an agent, first in line first. A server
// without the queue list is learned once and answers ErrUnsupported.
func (c *Client) Queue(ctx context.Context) ([]provider.QueueEntry, error) {
	unsupported := errs.Bamboof("this Bamboo does not list its build queue").Wrap(errs.ErrUnsupported)
	caps := c.fresh(ctx)
	if caps.Queue == "no" {
		return nil, unsupported
	}
	var q queueListDTO
	if err := c.getJSON(ctx, api+"/queue", url.Values{"expand": {"queuedBuilds"}, "max-result": {queueListMax}}, &q); err != nil {
		if isUnsupportedStatus(err) {
			c.learn(ctx, func(cp *Capabilities) { cp.Queue = "no" })
			return nil, unsupported
		}
		return nil, err
	}
	if caps.Queue == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.Queue = "yes" })
	}
	out := make([]provider.QueueEntry, 0, len(q.QueuedBuilds.QueuedBuild))
	seen := map[string]bool{}
	for _, e := range q.QueuedBuilds.QueuedBuild {
		key, planKey := queuedBuildOf(e.BuildResultKey, e.PlanKey)
		if seen[key] {
			continue // a build with several waiting jobs holds one place
		}
		seen[key] = true
		out = append(out, provider.QueueEntry{Key: key, PlanKey: planKey, Number: e.BuildNumber, Reason: plainReason(e.TriggerReason)})
	}
	return out, nil
}

// queuedBuildOf names the build behind a queue entry. Bamboo queues jobs, so
// an entry can be a job result key, PROJ-PLAN-JOB1-44, of the build
// PROJ-PLAN-44 of the plan PROJ-PLAN; a build key is returned as it is.
func queuedBuildOf(resultKey, planKey string) (key, plan string) {
	parts := strings.Split(resultKey, "-")
	if len(parts) != 4 {
		return resultKey, planKey
	}
	plan = parts[0] + "-" + parts[1]
	return plan + "-" + parts[3], plan
}

type agentDTO struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
	Busy    bool   `json:"busy"`
}

// Agents lists the server's build agents.
func (c *Client) Agents(ctx context.Context) ([]provider.Agent, error) {
	var as []agentDTO
	if err := c.getJSON(ctx, api+"/agent", nil, &as); err != nil {
		if statusOf(err) == http.StatusForbidden {
			c.learn(ctx, func(cp *Capabilities) { cp.Agents = "no" })
			return nil, errs.Authf("403 from %s: the agent list needs an administrator", c.base.Host).
				WithWhy("Bamboo shows its build agents only to administrators").Wrap(err)
		}
		if isUnsupportedStatus(err) {
			return nil, errs.Bamboof("this Bamboo does not list its build agents").
				WithWhy("the agent list may need an administrator").Wrap(errs.ErrUnsupported)
		}
		return nil, err
	}
	if c.Capabilities().Agents != "yes" {
		c.learn(ctx, func(cp *Capabilities) { cp.Agents = "yes" })
	}
	out := make([]provider.Agent, 0, len(as))
	for _, a := range as {
		out = append(out, provider.Agent{ID: a.ID, Name: a.Name, Type: a.Type, Online: a.Active, Enabled: a.Enabled, Busy: a.Busy})
	}
	return out, nil
}

// CannotListAgents reports whether the server is known to refuse the agent
// list. It reads only what bam learned within the last week.
func (c *Client) CannotListAgents(ctx context.Context) bool {
	return c.fresh(ctx).Agents == "no"
}

type agentDetailDTO struct {
	ExecutableJobs struct {
		Size int `json:"size"`
		Jobs []struct {
			Key struct {
				Key string `json:"key"`
			} `json:"key"`
		} `json:"executableJobs"`
	} `json:"executableJobs"`
}

// AgentJobs says which jobs each of agents can run, by agent name: the jobs
// whose requirements the agent's capabilities meet. Bamboo shows it only to
// administrators, one agent at a time, so up to agentReaders agents are read
// at once. A refusal, or a list Bamboo cut short, is learned once and answers
// ErrUnsupported afterwards with no request. An agent removed since the list
// was read is left out. When every agent asked about is missing and the
// endpoint is not yet known to work, the agent list is read once to tell the
// causes apart: if none of them is listed they were removed and the answer is
// an empty map; if any is still listed the endpoint is absent and that is
// learned; if the list cannot be read the answer is a refusal, not learned.
// Once the endpoint is known to work, those agents were removed and the
// answer is an empty map. The first other failure stops the agents not read
// yet.
func (c *Client) AgentJobs(ctx context.Context, agents []provider.Agent) (map[string][]string, error) {
	refused := errs.Bamboof("this Bamboo does not tell which jobs each agent can run").
		WithWhy("the agent details need an administrator").Wrap(errs.ErrUnsupported)
	if c.fresh(ctx).AgentJobs == "no" {
		return nil, refused
	}
	read, cancel := context.WithCancel(ctx)
	defer cancel()
	keys := make([][]string, len(agents))
	got := make([]bool, len(agents))
	var mu sync.Mutex
	var first error // the answer that stopped the reads
	sem := make(chan struct{}, agentReaders)
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if read.Err() != nil {
				return
			}
			k, err := c.agentJobs(read, a.ID)
			switch {
			case err == nil:
				keys[i], got[i] = k, true
			case statusOf(err) == http.StatusNotFound: // removed since the list was read
			default:
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
				cancel()
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if first != nil {
		s := statusOf(first)
		if s == http.StatusForbidden || s == http.StatusUnauthorized || isUnsupportedStatus(first) || errors.Is(first, errs.ErrUnsupported) {
			c.learn(ctx, func(cp *Capabilities) { cp.AgentJobs = "no" })
			if errors.Is(first, errs.ErrUnsupported) {
				return nil, first
			}
			return nil, refused
		}
		return nil, first
	}
	out := make(map[string][]string, len(agents))
	for i, a := range agents {
		if got[i] {
			out[a.Name] = keys[i]
		}
	}
	known := c.Capabilities().AgentJobs == "yes"
	if len(agents) > 0 && len(out) == 0 && !known {
		listed, err := c.Agents(ctx)
		if err != nil {
			return nil, refused
		}
		if !anyListed(agents, listed) {
			return out, nil
		}
		c.learn(ctx, func(cp *Capabilities) { cp.AgentJobs = "no" })
		return nil, refused
	}
	if !known {
		c.learn(ctx, func(cp *Capabilities) { cp.AgentJobs = "yes" })
	}
	return out, nil
}

// anyListed reports whether any of agents is still in listed, by ID.
func anyListed(agents, listed []provider.Agent) bool {
	ids := make(map[int]bool, len(listed))
	for _, a := range listed {
		ids[a.ID] = true
	}
	for _, a := range agents {
		if ids[a.ID] {
			return true
		}
	}
	return false
}

// agentJobs reads the jobs the agent id can run. A list Bamboo cut short
// answers ErrUnsupported: it would name too few agents, and it stays short
// on every later read.
func (c *Client) agentJobs(ctx context.Context, id int) ([]string, error) {
	var d agentDetailDTO
	q := url.Values{"expand": {"executableJobs[0:" + queueListMax + "]"}}
	if err := c.getJSON(ctx, api+"/agent/"+strconv.Itoa(id), q, &d); err != nil {
		return nil, err
	}
	if d.ExecutableJobs.Size > len(d.ExecutableJobs.Jobs) {
		return nil, errs.Bamboof("Bamboo lists %d of the %d jobs agent %d can run", len(d.ExecutableJobs.Jobs), d.ExecutableJobs.Size, id).
			WithWhy("it cuts the list short, so bam cannot tell which agents can run a job").Wrap(errs.ErrUnsupported)
	}
	keys := make([]string, 0, len(d.ExecutableJobs.Jobs))
	for _, j := range d.ExecutableJobs.Jobs {
		keys = append(keys, j.Key.Key)
	}
	return keys, nil
}
