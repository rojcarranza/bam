package app

import (
	"context"
	"sort"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/readonly"
)

// FindAgent resolves name to one agent of the server's agent list. The name
// must match a whole agent name, case-sensitively. An unknown name is a usage
// error that lists the agents whose names are close.
func (s *Service) FindAgent(ctx context.Context, name string) (provider.Agent, error) {
	name = strings.TrimSpace(name)
	agents, err := s.P.Agents(ctx)
	if err != nil {
		return provider.Agent{}, err
	}
	var hits []provider.Agent
	for _, a := range agents {
		if a.Name == name {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		var near []string
		for _, a := range agents {
			if name != "" && strings.Contains(strings.ToLower(a.Name), strings.ToLower(name)) {
				near = append(near, a.Name)
			}
		}
		sort.Strings(near)
		e := errs.Usagef("no agent is named %q", name)
		if len(near) > 0 {
			return provider.Agent{}, e.WithTry("did you mean " + strings.Join(near, ", ") + "? bam agent list shows every agent")
		}
		return provider.Agent{}, e.WithTry("bam agent list shows every agent")
	}
	return provider.Agent{}, errs.Usagef("%d agents are named %q", len(hits), name).
		WithTry("rename one of them in Bamboo, then try again")
}

// SetAgentEnabled takes agent out of rotation (enabled false) or puts it
// back. changed is false, and Bamboo is not asked, when the agent is already
// in that state. A server that cannot do it answers an error wrapping
// errs.ErrUnsupported.
func (s *Service) SetAgentEnabled(ctx context.Context, agent provider.Agent, enabled bool) (changed bool, err error) {
	if s.ReadOnly {
		return false, readonly.Refusal("switch an agent")
	}
	if agent.Enabled == enabled {
		return false, nil
	}
	sw, ok := s.P.(provider.AgentSwitcher)
	if !ok {
		return false, errs.Bamboof("this server cannot enable or disable agents").Wrap(errs.ErrUnsupported)
	}
	if err := sw.SetAgentEnabled(ctx, agent, enabled); err != nil {
		return false, err
	}
	return true, nil
}
