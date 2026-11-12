package bamboo

import (
	"context"
	"net/http"
	"net/url"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// queueListMax is how many queued builds one Queue call reads. Bamboo lists
// the queue in order; a position past this is reported as unknown.
const queueListMax = "1000"

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
	for _, e := range q.QueuedBuilds.QueuedBuild {
		out = append(out, provider.QueueEntry{Key: e.BuildResultKey, PlanKey: e.PlanKey, Number: e.BuildNumber, Reason: plainReason(e.TriggerReason)})
	}
	return out, nil
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
			return nil, errs.Authf("403 from %s: the agent list needs an administrator", c.base.Host).
				WithWhy("Bamboo shows its build agents only to administrators").Wrap(err)
		}
		if isUnsupportedStatus(err) {
			return nil, errs.Bamboof("this Bamboo does not list its build agents").
				WithWhy("the agent list may need an administrator").Wrap(errs.ErrUnsupported)
		}
		return nil, err
	}
	out := make([]provider.Agent, 0, len(as))
	for _, a := range as {
		out = append(out, provider.Agent{ID: a.ID, Name: a.Name, Type: a.Type, Online: a.Active, Enabled: a.Enabled, Busy: a.Busy})
	}
	return out, nil
}
