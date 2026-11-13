package view

import (
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/view/style"
)

// MineSummary is the four counts of your agents.
func MineSummary(m *app.MyAgents) string {
	return fmt.Sprintf("%d building · %d idle · %d down · %d disabled", m.Building, m.Idle, m.Down, m.Disabled)
}

// MineMark is the one mark an agent gets in its pool's row, so that a pool of
// fifteen reads at a glance and the marks differ without color.
func MineMark(s app.AgentState) string {
	switch s {
	case app.AgentBuilding:
		return "●"
	case app.AgentIdle:
		return "○"
	case app.AgentDisabled:
		return "⊘"
	}
	return "✗"
}

// MinePoolLine is one pool: its shared name start, how many agents it has and
// one mark each in number order. paint colors a mark; nil leaves it plain.
func MinePoolLine(p app.MyPool, paint func(app.AgentState, string) string) string {
	var marks strings.Builder
	for _, a := range p.Agents {
		m := MineMark(a.State)
		if paint != nil {
			m = paint(a.State, m)
		}
		marks.WriteString(m)
	}
	name := p.Name
	if len(p.Agents) > 1 || strings.HasSuffix(p.Name, "-") {
		name += "…"
	}
	return fmt.Sprintf("%s  %d  %s", name, len(p.Agents), marks.String())
}

// MineAwayLine says why one agent is down or disabled.
func MineAwayLine(a app.MyAgent) string {
	switch {
	case a.Gone:
		return fmt.Sprintf("%s  down: Bamboo no longer lists it, last seen %s", a.Name, a.Seen.Local().Format("2006-01-02 15:04"))
	case a.State == app.AgentDisabled:
		return a.Name + "  disabled"
	}
	return a.Name + "  down"
}

// MineNoMatchLine says that no agent starts with start.
func MineNoMatchLine(start string) string { return "no agent starts with " + start }

// MineLines is every line of the Your agents section, without indent.
func MineLines(m *app.MyAgents, paint func(app.AgentState, string) string) []string {
	out := []string{MineSummary(m)}
	for _, p := range m.Pools {
		out = append(out, MinePoolLine(p, paint))
	}
	for _, a := range m.Away {
		out = append(out, MineAwayLine(a))
	}
	for _, s := range m.NoMatch {
		out = append(out, MineNoMatchLine(s))
	}
	return out
}

func minePaint(o Out) func(app.AgentState, string) string {
	return func(s app.AgentState, mark string) string {
		code := map[app.AgentState]string{app.AgentBuilding: "32", app.AgentIdle: "2", app.AgentDisabled: "33", app.AgentDown: "31"}[s]
		return style.Paint(o.Style, code, mark)
	}
}

// BoardMineDoc is agents.mine in bam top --json.
type BoardMineDoc struct {
	Starts   []string       `json:"starts"`
	Building int            `json:"building"`
	Idle     int            `json:"idle"`
	Down     int            `json:"down"`
	Disabled int            `json:"disabled"`
	Agents   []MineAgentDoc `json:"agents"`
	NoMatch  []string       `json:"no_match"`
}

// MineAgentDoc is one of your agents in the JSON.
type MineAgentDoc struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Gone   bool    `json:"gone,omitempty"`
	SeenAt *string `json:"last_seen,omitempty"`
}

func mineDoc(m *app.MyAgents) *BoardMineDoc {
	d := &BoardMineDoc{Starts: nonNil(m.Starts), Building: m.Building, Idle: m.Idle, Down: m.Down, Disabled: m.Disabled,
		Agents: []MineAgentDoc{}, NoMatch: nonNil(m.NoMatch)}
	for _, p := range m.Pools {
		for _, a := range p.Agents {
			ad := MineAgentDoc{Name: a.Name, State: string(a.State), Gone: a.Gone}
			if a.Gone {
				ad.SeenAt = timePtr(a.Seen)
			}
			d.Agents = append(d.Agents, ad)
		}
	}
	return d
}

// MineAwayLineOrName is a line for any of your agents: its name and, for one
// that is not working, why.
func MineAwayLineOrName(a app.MyAgent) string {
	if a.State == app.AgentDown || a.State == app.AgentDisabled {
		return MineAwayLine(a)
	}
	return a.Name + "  " + string(a.State)
}
