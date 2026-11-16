package app

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// AgentForget is how long bam keeps the name of an agent Bamboo no longer
// lists before it forgets it.
const AgentForget = 30 * 24 * time.Hour

// agentSeenStep is how stale a listed agent's remembered time may grow before
// bam renews it, so the state file is not rewritten on every read.
const agentSeenStep = time.Hour

// AgentState is the health of one of your agents.
type AgentState string

const (
	AgentBuilding AgentState = "building" // online, enabled, busy
	AgentIdle     AgentState = "idle"     // online, enabled, not busy
	AgentDisabled AgentState = "disabled" // not enabled
	AgentDown     AgentState = "down"     // listed offline, or no longer listed
)

// MyAgent is one of your agents, once however many records Bamboo holds.
type MyAgent struct {
	Name  string // as Bamboo lists it, less the " (N)" of a registration repeated
	State AgentState
	// Gone is true for a remembered agent Bamboo no longer lists; Seen is
	// when bam last saw it listed.
	Gone bool
	Seen time.Time
	// Build is the job a building agent runs, when bam found it; nil for an
	// agent that does not build, and for one whose build was not found.
	Build *AgentBuild
}

// MyPool is the agents whose names differ only in their final number.
type MyPool struct {
	Name   string // the shared start, such as team-a-agent-pool-
	Agents []MyAgent
}

// MyAgents is the health of the agents whose names start with one of the
// name starts you support.
type MyAgents struct {
	Starts                         []string
	Building, Idle, Disabled, Down int
	Pools                          []MyPool
	// Away lists the agents that are down or disabled.
	Away []MyAgent
	// NoMatch lists the name starts no agent, listed or remembered, matches.
	NoMatch []string
}

// Total is the number of your agents.
func (m *MyAgents) Total() int { return m.Building + m.Idle + m.Disabled + m.Down }

var (
	agentSuffixRe = regexp.MustCompile(` \(\d+\)$`)
	agentNumberRe = regexp.MustCompile(`^(.*?)(\d+)$`)
)

// AgentBaseName is name without the " (N)" Bamboo adds when an agent
// registers again under a name that is taken.
func AgentBaseName(name string) string { return agentSuffixRe.ReplaceAllString(name, "") }

func agentMine(name string, starts []string) bool {
	return slices.ContainsFunc(starts, func(s string) bool { return s != "" && strings.HasPrefix(name, s) })
}

func agentState(a provider.Agent) AgentState {
	switch {
	case !a.Enabled:
		return AgentDisabled
	case !a.Online:
		return AgentDown
	case a.Busy:
		return AgentBuilding
	}
	return AgentIdle
}

// MineAgents says how your agents are, from the agent list listed and the
// names remembered (name to when it was last listed). It returns the
// remembered names to keep: those listed now, renewed once their time is
// agentSeenStep old, and those not listed for less than AgentForget. Two records with one base name count once, the online
// one winning.
func MineAgents(listed []provider.Agent, starts []string, remembered map[string]time.Time, now time.Time) (*MyAgents, map[string]time.Time) {
	byName := map[string]provider.Agent{}
	for _, a := range listed {
		base := AgentBaseName(a.Name)
		if !agentMine(base, starts) {
			continue
		}
		a.Name = base
		if prev, ok := byName[base]; ok && (prev.Online || !a.Online) {
			continue
		}
		byName[base] = a
	}
	keep := map[string]time.Time{}
	var agents []MyAgent
	for name, a := range byName {
		keep[name] = now
		if seen, ok := remembered[name]; ok && now.Sub(seen) < agentSeenStep {
			keep[name] = seen
		}
		agents = append(agents, MyAgent{Name: name, State: agentState(a)})
	}
	for name, seen := range remembered {
		if _, ok := byName[name]; ok || !agentMine(name, starts) || now.Sub(seen) >= AgentForget {
			continue
		}
		keep[name] = seen
		agents = append(agents, MyAgent{Name: name, State: AgentDown, Gone: true, Seen: seen})
	}
	m := &MyAgents{Starts: starts}
	for _, s := range starts {
		if !slices.ContainsFunc(agents, func(a MyAgent) bool { return strings.HasPrefix(a.Name, s) }) {
			m.NoMatch = append(m.NoMatch, s)
		}
	}
	pools := map[string][]MyAgent{}
	for _, a := range agents {
		switch a.State {
		case AgentBuilding:
			m.Building++
		case AgentIdle:
			m.Idle++
		case AgentDisabled:
			m.Disabled++
		default:
			m.Down++
		}
		pool, _ := agentPool(a.Name)
		pools[pool] = append(pools[pool], a)
	}
	for _, name := range slices.Sorted(maps.Keys(pools)) {
		as := pools[name]
		slices.SortFunc(as, func(x, y MyAgent) int {
			_, nx := agentPool(x.Name)
			_, ny := agentPool(y.Name)
			if nx != ny {
				return nx - ny
			}
			return strings.Compare(x.Name, y.Name)
		})
		m.Pools = append(m.Pools, MyPool{Name: name, Agents: as})
		for _, a := range as {
			if a.State == AgentDown || a.State == AgentDisabled {
				m.Away = append(m.Away, a)
			}
		}
	}
	return m, keep
}

// agentPool splits an agent name into the start its pool shares and its
// final number; a name without one is a pool of its own, numbered -1.
func agentPool(name string) (string, int) {
	if mm := agentNumberRe.FindStringSubmatch(name); mm != nil {
		n, _ := strconv.Atoi(mm[2])
		return mm[1], n
	}
	return name, -1
}

// AgentStarts is the name starts of the agents you support on this server.
func (s *Service) AgentStarts() []string {
	cfg := s.Config()
	if cfg == nil {
		return nil
	}
	return cfg.AgentStarts(s.Server)
}

// myAgents reads your agents from the list as, and keeps the names it saw.
func (s *Service) myAgents(as []provider.Agent) *MyAgents {
	starts := s.AgentStarts()
	if len(starts) == 0 {
		return nil
	}
	var seen map[string]time.Time
	if s.State != nil {
		seen, _ = s.State.SeenAgents(s.Origin)
	}
	m, keep := MineAgents(as, starts, seen, s.Clock.Now())
	if s.State != nil && !maps.Equal(keep, seen) {
		_ = s.State.SetSeenAgents(s.Origin, keep)
	}
	return m
}

type agentScopeKey struct{}

// WholeServer marks ctx as asking for the whole server: with agents: set,
// the board otherwise reads agent details for your agents only.
func WholeServer(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentScopeKey{}, false)
}

// boardScope marks ctx so that the board's reads of agent details cover your
// agents only, unless ctx already asks for the whole server.
func (s *Service) boardScope(ctx context.Context) context.Context {
	if len(s.AgentStarts()) == 0 || ctx.Value(agentScopeKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, agentScopeKey{}, true)
}

// scopeAgents keeps the agents the board reads details for: yours, when the
// board marked ctx for it. bam queue and bam agent list keep the whole list.
func (s *Service) scopeAgents(ctx context.Context, as []provider.Agent) []provider.Agent {
	if mine, _ := ctx.Value(agentScopeKey{}).(bool); !mine {
		return as
	}
	return OnlyAgents(as, s.AgentStarts())
}

// OnlyAgents keeps the records of as whose names start with one of starts,
// as bam agent list --mine shows them.
func OnlyAgents(as []provider.Agent, starts []string) []provider.Agent {
	return slices.DeleteFunc(slices.Clone(as), func(a provider.Agent) bool { return !agentMine(AgentBaseName(a.Name), starts) })
}

// needsAgents are the Needs you lines for your agents: one for each that is
// down or disabled.
func needsAgents(m *MyAgents) []Need {
	var out []Need
	for _, a := range m.Away {
		switch {
		case a.Gone:
			out = append(out, Need{Kind: "agent_down", Text: fmt.Sprintf("agent %s is down: Bamboo no longer lists it", a.Name)})
		case a.State == AgentDown:
			out = append(out, Need{Kind: "agent_down", Text: fmt.Sprintf("agent %s is down", a.Name)})
		default:
			out = append(out, Need{Kind: "agent_disabled", Text: fmt.Sprintf("agent %s is disabled", a.Name)})
		}
	}
	return out
}
