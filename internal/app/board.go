package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

// The fixed limits of the Needs you list.
const (
	// NeedsWait is how long a build waits in the queue before it needs you.
	NeedsWait = 5 * time.Minute
	// NeedsSlowFactor is how far past its usual time, as a multiple, a
	// pinned running build is before it needs you ("half again").
	NeedsSlowFactor = 1.5
	// PinResults is how many results a pinned pipeline shows.
	PinResults = 10

	boardReaders = 6  // reads in flight at once
	waitReads    = 25 // queued builds whose own record is read for the time waited
)

// Board is what bam top shows: the server, its agents and queue, and the
// pinned pipelines by group. A part Bamboo refused is nil and its Note says
// so; the rest of the board is still filled.
type Board struct {
	At         time.Time
	Server     string // the server's alias
	URL        string
	Version    string
	Agents     *AgentSummary
	AgentsNote string
	// Mine is how the agents you support are; nil when agents: is not set, the
	// whole server was asked for, or the agent list could not be read.
	Mine *MyAgents
	// MineSet is true when agents: lists name starts, so the board can show
	// the empty state apart from "not set".
	MineSet bool
	// Queue is the builds that wait for you; with QueueFiltered, QueueOthers
	// more wait for other agents, pipelines or projects.
	Queue         []QueueRow
	QueueOthers   int
	QueueFiltered bool
	QueueOK       bool // the queue was read; an empty Queue is then really empty
	QueueNote     string
	Groups        []PinGroupView
	Needs         []Need
}

// AgentSummary counts the agents and lists those that are not working.
type AgentSummary struct {
	Total, Online, Busy, Offline, Disabled int
	// Away lists every agent that is offline or disabled.
	Away []provider.Agent
}

// QueueRow is one queued build and how long it has waited.
type QueueRow struct {
	Position int
	provider.QueueEntry
	Waited time.Duration
	// AtLeast is true when the build's own record gave no time it began
	// waiting, so Waited counts from when bam first saw it.
	AtLeast bool
	// Why says why the build waits; nil when bam could not tell.
	Why *provider.Why
}

// PinGroupView is one monitor group with its pipelines.
type PinGroupView struct {
	Name string
	Pins []PinView
}

// PinView is one pinned pipeline. Err is set, and the rest empty, when the
// pipeline could not be read.
type PinView struct {
	Pin     string // as written in the settings
	PlanKey string
	Branch  string
	// Results are the last finished builds, newest first.
	Results []provider.Build
	// Current is the build queued or running now, or nil.
	Current  *provider.Build
	Progress provider.Progress
	// Last is how long the newest finished build took; Usual is the plan's
	// usual build time, 0 when its history is too short.
	Last, Usual time.Duration
	Err         error
}

// Need is one line of the Needs you list.
type Need struct {
	Kind string // agent_offline, queue_wait, pin_failed or pin_slow
	Text string
	// Build is the build the line is about, "" for an agent. The text
	// changes as time passes; Kind and Build name the same line throughout.
	Build string
}

// Board reads the board for groups. It never fails for a refused agent list
// or queue, or for a pipeline it cannot read: those are noted on the board.
// Only an interrupt is an error.
func (s *Service) Board(ctx context.Context, groups []config.PinGroup) (Board, error) {
	return s.RefreshBoard(ctx, groups, nil)
}

// RefreshBoard is Board for a board that is already on screen. With prev it
// is the quick refresh: agents, queue and the pinned builds are read again,
// but the server version, the plans the pins name and each plan's usual build
// time are kept from prev, which only the full read (prev nil) learns afresh.
func (s *Service) RefreshBoard(ctx context.Context, groups []config.PinGroup, prev *Board) (Board, error) {
	ctx = s.boardScope(ctx)
	now := s.Clock.Now()
	b := Board{At: now, Server: s.Server.Alias, URL: s.Server.URL}
	if prev != nil {
		b.Version = prev.Version
	} else if info, err := s.P.ServerInfo(ctx); err == nil {
		b.Version = info.Version
	}
	as, err := s.P.Agents(ctx)
	s.seedAgents(as, err == nil) // the queue reads the same list for its reasons
	if err != nil {
		b.AgentsNote = refusal("the agent list", err)
	} else {
		b.Agents = summarizeAgents(as)
		if ctx.Value(agentScopeKey{}) == true {
			b.Mine = s.myAgents(as)
		}
	}
	b.MineSet = len(s.AgentStarts()) > 0
	var known []PinGroupView
	if prev != nil {
		known = prev.Groups
	}
	b.Groups = s.readPins(ctx, groups, known)
	pinned := map[string]bool{}
	for _, g := range b.Groups {
		for _, p := range g.Pins {
			if p.PlanKey != "" {
				pinned[p.PlanKey] = true
			}
		}
	}
	if q, others, filtered, err := s.BoardQueue(ctx, pinned); err != nil {
		b.QueueNote = refusal("the queue", err)
	} else {
		b.QueueOK = true
		b.Queue, b.QueueOthers, b.QueueFiltered = q, others, filtered
	}
	if err := ctx.Err(); err != nil {
		return Board{}, err
	}
	b.Needs = NeedsYou(b, now)
	return b, nil
}

// refusal is the one line that says why a part of the board is missing.
func refusal(what string, err error) string {
	switch {
	case errs.KindOf(err) == errs.KindAuth:
		return fmt.Sprintf("%s is not shown: Bamboo refused it (it may need an administrator)", what)
	case errors.Is(err, errs.ErrUnsupported):
		return fmt.Sprintf("%s is not shown: this Bamboo does not offer it", what)
	}
	return fmt.Sprintf("%s could not be read: %s", what, err.Error())
}

func summarizeAgents(as []provider.Agent) *AgentSummary {
	sum := &AgentSummary{Total: len(as)}
	for _, a := range as {
		switch {
		case !a.Enabled:
			sum.Disabled++
			sum.Away = append(sum.Away, a)
		case !a.Online:
			sum.Offline++
			sum.Away = append(sum.Away, a)
		default:
			sum.Online++
			if a.Busy {
				sum.Busy++
			}
		}
		// An agent that is both disabled and offline counts once, as disabled.
	}
	return sum
}

// QueueRows lists the builds waiting on the server, first in line first,
// each with the time it has waited, taken from the build's own record. The
// first waitReads builds are read; a build that was not read, or whose record
// gives no time, keeps AtLeast and shows no wait.
func (s *Service) QueueRows(ctx context.Context) ([]QueueRow, error) {
	rows, _, err := s.readQueue(ctx, func([]provider.QueueEntry) []bool { return nil })
	return rows, err
}

// BoardQueue is the queue the board shows: the builds that wait for you, with
// their true place in the whole queue, and how many others wait. A build is
// yours when one of your agents can run one of its jobs, its plan is in
// pinned (the plan keys of the pins), or its project is in projects:. With
// none of the three set, or when ctx asks for the whole server, every build is
// yours. When bam cannot tell which jobs your agents run, a build no pin or
// project names is shown, never hidden. The waits and reasons are read for
// your builds first, wherever they sit, up to waitReads.
func (s *Service) BoardQueue(ctx context.Context, pinned map[string]bool) (rows []QueueRow, others int, filtered bool, err error) {
	filtered = ctx.Value(agentScopeKey{}) != false &&
		(len(s.AgentStarts()) > 0 || len(pinned) > 0 || len(s.ProjectKeys()) > 0)
	if !filtered {
		rows, err = s.QueueRows(ctx)
		return rows, 0, false, err
	}
	rows, others, err = s.readQueue(ctx, func(q []provider.QueueEntry) []bool { return s.mineBuilds(ctx, q, pinned) })
	return rows, others, true, err
}

// mineBuilds says, for each queued entry, whether it is yours.
func (s *Service) mineBuilds(ctx context.Context, q []provider.QueueEntry, pinned map[string]bool) []bool {
	projects := map[string]bool{}
	for _, k := range s.ProjectKeys() {
		projects[strings.ToUpper(k)] = true
	}
	var runs map[string]bool
	known := false
	if len(s.AgentStarts()) > 0 {
		if agents, ok := s.whyAgents(ctx); ok {
			agents = s.scopeAgents(ctx, agents)
			plans := make([]string, 0, len(q))
			for _, e := range q {
				plans = append(plans, e.PlanKey)
			}
			runs, known = s.planRunners(ctx, agents, plans)
		}
	}
	mine := make([]bool, len(q))
	for i, e := range q {
		project, _, _ := strings.Cut(e.PlanKey, "-")
		mine[i] = pinned[e.PlanKey] || projects[strings.ToUpper(project)] || runs[e.PlanKey] ||
			(len(s.AgentStarts()) > 0 && !known)
	}
	return mine
}

// readQueue reads the queue and the wait of up to waitReads builds, yours
// first. mineOf says which entries are yours; nil means all. It returns the
// rows of your builds, each with its place in the whole queue, and how many
// others it left out.
func (s *Service) readQueue(ctx context.Context, mineOf func([]provider.QueueEntry) []bool) ([]QueueRow, int, error) {
	q, err := s.P.Queue(ctx)
	if err != nil {
		return nil, 0, err
	}
	now := s.Clock.Now()
	mine := mineOf(q)
	rows := make([]QueueRow, 0, len(q))
	others := 0
	for i, e := range q {
		if mine != nil && !mine[i] {
			others++
			continue
		}
		rows = append(rows, QueueRow{Position: i + 1, QueueEntry: e, AtLeast: true})
	}
	sem := make(chan struct{}, boardReaders)
	var wg sync.WaitGroup
	for i := range min(len(rows), waitReads) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := &rows[i]
			b, err := s.P.GetBuild(ctx, r.Key)
			if err != nil {
				return
			}
			r.Why = s.WhyWaits(ctx, b, r.Position-1)
			since := b.QueuedAt
			if since.IsZero() {
				since = b.StartedAt
			}
			if !since.IsZero() {
				r.Waited, r.AtLeast = max(now.Sub(since), 0), false
			}
		}()
	}
	wg.Wait()
	return rows, others, nil
}

// readPins reads every pinned pipeline, boardReaders at a time.
// A pin that known holds, resolved and without an error, is not resolved
// again and keeps its usual time.
func (s *Service) readPins(ctx context.Context, groups []config.PinGroup, known []PinGroupView) []PinGroupView {
	out := make([]PinGroupView, len(groups))
	sem := make(chan struct{}, boardReaders)
	var wg sync.WaitGroup
	for gi, g := range groups {
		out[gi] = PinGroupView{Name: g.Name, Pins: make([]PinView, len(g.Pins))}
		for pi, pin := range g.Pins {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				out[gi].Pins[pi] = s.readPin(ctx, pin, findPin(known, pin))
			}()
		}
	}
	wg.Wait()
	return out
}

// findPin is the earlier reading of pin, or nil.
func findPin(groups []PinGroupView, pin string) *PinView {
	for _, g := range groups {
		for i := range g.Pins {
			if g.Pins[i].Pin == pin && g.Pins[i].Err == nil && g.Pins[i].PlanKey != "" {
				return &g.Pins[i]
			}
		}
	}
	return nil
}

func (s *Service) readPin(ctx context.Context, pin string, prev *PinView) PinView {
	v := PinView{Pin: pin}
	if prev != nil {
		v.PlanKey, v.Branch, v.Usual = prev.PlanKey, prev.Branch, prev.Usual
		return s.readPinBuilds(ctx, v, false)
	}
	plan, branch := config.SplitPin(pin)
	if t, ok, _ := s.Cfg.Target(PresetName(s.Cfg, plan)); ok && t.Server != "" && !strings.EqualFold(t.Server, s.Server.Alias) {
		v.Err = errs.Usagef("preset %s is on server %s; bam top shows one server, %s", t.Name, t.Server, s.Server.Alias)
		return v
	}
	ref, err := s.ResolvePlan(ctx, plan, branch)
	if err != nil {
		v.Err = err
		return v
	}
	v.PlanKey, v.Branch = ref.PlanKey, ref.Branch
	return s.readPinBuilds(ctx, v, true)
}

// readPinBuilds reads the builds of v's plan. usual says whether the plan's
// usual build time is read too.
func (s *Service) readPinBuilds(ctx context.Context, v PinView, usual bool) PinView {
	builds, err := s.P.ListBuilds(ctx, v.PlanKey, provider.ListOptions{Limit: PinResults + 1, Unfinished: true})
	if err != nil {
		v.Err = err
		return v
	}
	for _, b := range builds {
		switch {
		case !b.State.Finished():
			if v.Current == nil {
				c := b
				v.Current = &c
			}
		case len(v.Results) < PinResults:
			v.Results = append(v.Results, b)
		}
	}
	if len(v.Results) > 0 {
		v.Last = v.Results[0].Duration
	}
	if usual {
		if med, err := s.historyMedian(ctx, v.PlanKey); err == nil {
			v.Usual = med
		}
	}
	if v.Current != nil && v.Current.State == provider.StateRunning {
		v.Progress, _ = s.Progress(ctx, *v.Current)
	}
	return v
}

// NeedsYou is the Needs you list: an enabled agent that is offline (with
// agents: set, one of yours that is down or disabled), a build
// waiting over NeedsWait, a pinned pipeline whose last build failed, and a
// pinned build running NeedsSlowFactor past its usual time.
func NeedsYou(b Board, now time.Time) []Need {
	var out []Need
	if b.Mine != nil {
		out = append(out, needsAgents(b.Mine)...)
	} else if b.Agents != nil {
		for _, a := range b.Agents.Away {
			if a.Enabled && !a.Online {
				out = append(out, Need{Kind: "agent_offline", Text: fmt.Sprintf("agent %s is offline", a.Name)})
			}
		}
	}
	out = append(out, needsQueue(b.Queue)...)
	for _, g := range b.Groups {
		for _, p := range g.Pins {
			if p.Err != nil {
				continue
			}
			if len(p.Results) > 0 && p.Results[0].State == provider.StateFailed {
				out = append(out, Need{Kind: "pin_failed", Text: fmt.Sprintf("%s: the last build, %s, failed", p.Pin, p.Results[0].Key), Build: p.Results[0].Key})
			}
			if c := p.Current; c != nil && c.State == provider.StateRunning && p.Usual > 0 && !c.StartedAt.IsZero() {
				if run := now.Sub(c.StartedAt); float64(run) > NeedsSlowFactor*float64(p.Usual) {
					out = append(out, Need{Kind: "pin_slow", Text: fmt.Sprintf("%s: %s has run %s, usually %s", p.Pin, c.Key, shortDuration(run), shortDuration(p.Usual)), Build: c.Key})
				}
			}
		}
	}
	return out
}

// needsQueue is the Needs you lines for builds waiting over NeedsWait.
// Builds that wait for the same reason become one line with a count, led by
// the one that has waited longest; a build bam has no wait for is left out.
func needsQueue(q []QueueRow) []Need {
	type group struct {
		why   string
		rows  []QueueRow
		first int
	}
	var groups []*group
	byWhy := map[string]*group{}
	for _, r := range q {
		if r.Waited <= NeedsWait {
			continue
		}
		why := WaitReason(r.Why)
		g := byWhy[why]
		if g == nil {
			g = &group{why: why, first: len(groups)}
			byWhy[why] = g
			groups = append(groups, g)
		}
		g.rows = append(g.rows, r)
	}
	var out []Need
	for _, g := range groups {
		longest := g.rows[0]
		for _, r := range g.rows {
			if r.Waited > longest.Waited {
				longest = r
			}
		}
		text := fmt.Sprintf("%s has waited %s in the queue", longest.Key, shortDuration(longest.Waited))
		if len(g.rows) > 1 {
			text = fmt.Sprintf("%d builds have waited over %s in the queue, the longest %s for %s", len(g.rows), shortDuration(NeedsWait), longest.Key, shortDuration(longest.Waited))
		}
		if g.why != "" {
			text += ": " + g.why
		}
		out = append(out, Need{Kind: "queue_wait", Text: text, Build: longest.Key})
	}
	return out
}

// WaitReason is why a build waits, short enough for a Needs you line: the
// reason without the names of the agents.
func WaitReason(w *provider.Why) string {
	if w == nil {
		return ""
	}
	short := *w
	short.Agents = nil
	return short.Text()
}

// LongWait is d as days and hours, "57d 4h", once it is 48 hours or more.
func LongWait(d time.Duration) (string, bool) {
	if d < 48*time.Hour {
		return "", false
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24), true
}

// shortDuration is d in whole minutes, or seconds under a minute; 48 hours
// and more read as days and hours.
func shortDuration(d time.Duration) string {
	if l, ok := LongWait(d); ok {
		return l
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

// PinGroup is a monitor group, so the UI can name it without importing config.
type PinGroup = config.PinGroup
