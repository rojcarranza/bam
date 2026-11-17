package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// MineRow is one building agent as Your agents lists it under its pool.
type MineRow struct {
	Agent  app.MyAgent
	Number string // the agent's number in its pool
	// Job is the job as Bamboo shows it, PROJ-PLAN-JOB1-44; empty when bam
	// did not find the agent's build.
	Job     string
	Plan    string // the plan with its branch
	Group   string // the pin group the build belongs to
	Running string // how long the build runs
}

// MineNotFound is what a row says for a building agent whose build bam did not
// find.
const MineNotFound = "building · build not found"

// MineDeploying stands in a row's job column for an agent that runs a
// deployment; the plan column names the deployment.
const MineDeploying = "deploying"

// MineAgentNumber is the agent's number in its pool, "" for a pool of one name.
func MineAgentNumber(name string) string {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	return name[i:]
}

// MineElapsed is how long a build runs: seconds under a minute, minutes under
// an hour, then hours and minutes.
func MineElapsed(d time.Duration) string {
	switch {
	case d <= 0:
		return "–"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// MineGroupName is how Your agents writes a pin group's name: a capital first
// letter, with dashes and underscores shown as spaces.
func MineGroupName(name string) string {
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	r, n := utf8.DecodeRuneInString(name)
	return strings.ToUpper(string(r)) + name[n:]
}

// MineRows is a row for each building agent of pool p, in number order, with
// the build or the deployment it runs at now.
func MineRows(p app.MyPool, now time.Time) []MineRow {
	var out []MineRow
	for _, a := range p.Agents {
		if a.State != app.AgentBuilding {
			continue
		}
		r := MineRow{Agent: a, Number: MineAgentNumber(a.Name)}
		if b := a.Build; b != nil {
			r.Job, r.Plan, r.Running = b.Job, b.Label(), MineElapsed(b.RunningFor(now))
			if b.Group != "" {
				r.Group = MineGroupName(b.Group)
			}
		} else if d := a.Deploy; d != nil {
			r.Job, r.Plan, r.Running = MineDeploying, d.Label(), MineElapsed(d.RunningFor(now))
		}
		out = append(out, r)
	}
	return out
}

// MineIdleLine names the idle agents of pool p by number on one line; empty
// when none is idle.
func MineIdleLine(p app.MyPool) string {
	var nums []string
	for _, a := range p.Agents {
		if a.State == app.AgentIdle {
			n := MineAgentNumber(a.Name)
			if n == "" {
				n = a.Name
			}
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return ""
	}
	return "idle  " + strings.Join(nums, " ")
}

// MineRowCols is the widths the rows of one drawing share, so the columns of
// every pool line up.
type MineRowCols struct{ Number, Job, Plan, Group int }

// MineColumns measures rows for a drawing inner columns wide. A plan is cut,
// down to a floor, when the rest does not fit; the group column is left out
// when no row has one.
func MineColumns(rows []MineRow, inner int) MineRowCols {
	var c MineRowCols
	planWant := 0
	for _, r := range rows {
		c.Number = max(c.Number, len(r.Number))
		c.Job = max(c.Job, utf8.RuneCountInString(r.Job))
		c.Group = max(c.Group, utf8.RuneCountInString(r.Group))
		planWant = max(planWant, utf8.RuneCountInString(r.Plan))
	}
	fixed := 2 + c.Number + 2 + c.Job + 2 + 2 + 6 // indent, number, job, gap before the plan, gap and time
	if c.Group > 0 {
		fixed += c.Group + 2
	}
	c.Plan = max(min(planWant, inner-fixed), min(planWant, 12))
	return c
}

// MineRowText writes one row in the columns c: the number, the job, the plan
// with its branch, the group and the time running. A row whose build was not
// found says so after its number. paint colors the group and the time; nil
// leaves them plain.
func MineRowText(r MineRow, c MineRowCols, paint func(part, s string) string) string {
	if paint == nil {
		paint = func(_, s string) string { return s }
	}
	head := fmt.Sprintf("%*s  ", c.Number, r.Number)
	if r.Job == "" {
		return head + paint("dim", MineNotFound)
	}
	plan := r.Plan
	if utf8.RuneCountInString(plan) > c.Plan {
		plan = string([]rune(plan)[:max(c.Plan-1, 0)]) + "…"
	}
	line := fmt.Sprintf("%s%-*s  %-*s", head, c.Job, r.Job, c.Plan, plan)
	if c.Group > 0 {
		line += "  " + paint("group", fmt.Sprintf("%-*s", c.Group, r.Group))
	}
	return line + "  " + paint("dim", fmt.Sprintf("%6s", r.Running))
}

// MineLines is every line of the Your agents section, without indent. With
// now set, each pool lists the build of each building agent under its line of
// marks and names its idle agents; without it only the pools' lines show.
func MineLines(m *app.MyAgents, paint func(app.AgentState, string) string, now time.Time) []string {
	out := []string{MineSummary(m)}
	var rows []MineRow
	for _, p := range m.Pools {
		rows = append(rows, MineRows(p, now)...)
	}
	cols := MineColumns(rows, 120)
	for _, p := range m.Pools {
		out = append(out, MinePoolLine(p, paint))
		if now.IsZero() {
			continue
		}
		for _, r := range MineRows(p, now) {
			out = append(out, "  "+MineRowText(r, cols, nil))
		}
		if idle := MineIdleLine(p); idle != "" {
			out = append(out, "  "+idle)
		}
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
	// WorkNote says why some builds could not be looked up, when they could not.
	WorkNote string `json:"work_note,omitempty"`
}

// MineAgentDoc is one of your agents in the JSON.
type MineAgentDoc struct {
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Gone   bool    `json:"gone,omitempty"`
	SeenAt *string `json:"last_seen,omitempty"`
	// Build is the job a building agent runs; absent for an agent that does
	// not build and for one whose build bam did not find.
	Build *MineBuildDoc `json:"build,omitempty"`
	// Deploy is the deployment a building agent runs, when bam found one.
	Deploy *MineDeployDoc `json:"deployment,omitempty"`
}

// MineDeployDoc is the deployment one of your agents runs.
type MineDeployDoc struct {
	Project     string  `json:"project"`
	Environment string  `json:"environment"`
	Release     string  `json:"release"`
	URL         string  `json:"url"`
	StartedAt   *string `json:"started_at"`
}

// MineBuildDoc is the build one of your agents runs.
type MineBuildDoc struct {
	Job       string  `json:"job"`
	BuildKey  string  `json:"build_key"`
	PlanKey   string  `json:"plan_key"`
	PlanName  string  `json:"plan_name"`
	Branch    string  `json:"branch,omitempty"`
	Group     string  `json:"group,omitempty"`
	StartedAt *string `json:"started_at"`
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
			if b := a.Build; b != nil {
				ad.Build = &MineBuildDoc{Job: b.Job, BuildKey: b.BuildKey, PlanKey: b.PlanKey, PlanName: b.PlanName,
					Branch: b.Branch, Group: b.Group, StartedAt: timePtr(b.StartedAt)}
			}
			if d := a.Deploy; d != nil {
				ad.Deploy = &MineDeployDoc{Project: d.Project, Environment: d.Environment, Release: d.Release,
					URL: d.URL, StartedAt: timePtr(d.StartedAt)}
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
