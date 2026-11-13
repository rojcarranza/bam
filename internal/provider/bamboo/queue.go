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
// at once; a refusal is learned once and answers ErrUnsupported afterwards.
func (c *Client) AgentJobs(ctx context.Context, agents []provider.Agent) (map[string][]string, error) {
	refused := errs.Bamboof("this Bamboo does not tell which jobs each agent can run").
		WithWhy("the agent details need an administrator").Wrap(errs.ErrUnsupported)
	if c.fresh(ctx).AgentJobs == "no" {
		return nil, refused
	}
	keys := make([][]string, len(agents))
	errOf := make([]error, len(agents))
	sem := make(chan struct{}, agentReaders)
	var wg sync.WaitGroup
	for i, a := range agents {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			keys[i], errOf[i] = c.agentJobs(ctx, a.ID)
		})
	}
	wg.Wait()
	out := make(map[string][]string, len(agents))
	for i, a := range agents {
		if err := errOf[i]; err != nil {
			if s := statusOf(err); s == http.StatusForbidden || s == http.StatusUnauthorized || isUnsupportedStatus(err) {
				c.learn(ctx, func(cp *Capabilities) { cp.AgentJobs = "no" })
				return nil, refused
			}
			if errors.Is(err, errs.ErrUnsupported) {
				return nil, refused
			}
			return nil, err
		}
		out[a.Name] = keys[i]
	}
	if c.Capabilities().AgentJobs != "yes" {
		c.learn(ctx, func(cp *Capabilities) { cp.AgentJobs = "yes" })
	}
	return out, nil
}

// agentJobs reads the jobs the agent id can run. A list Bamboo cut short
// answers ErrUnsupported: it would name too few agents.
func (c *Client) agentJobs(ctx context.Context, id int) ([]string, error) {
	var d agentDetailDTO
	q := url.Values{"expand": {"executableJobs[0:" + queueListMax + "]"}}
	if err := c.getJSON(ctx, api+"/agent/"+strconv.Itoa(id), q, &d); err != nil {
		return nil, err
	}
	if d.ExecutableJobs.Size > len(d.ExecutableJobs.Jobs) {
		return nil, errs.ErrUnsupported
	}
	keys := make([]string, 0, len(d.ExecutableJobs.Jobs))
	for _, j := range d.ExecutableJobs.Jobs {
		keys = append(keys, j.Key.Key)
	}
	return keys, nil
}
