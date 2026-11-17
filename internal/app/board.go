package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	// BranchWindow is how recently a plan branch must have built for a pin to
	// list it, and PinBranches how many branches a pin lists.
	BranchWindow = 7 * 24 * time.Hour
	PinBranches  = 10

	boardReaders = 6  // reads in flight at once
	waitReads    = 25 // queued builds whose own record bam queue reads for the time waited
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
	// Work is the build each busy one of your agents runs, by agent name, and
	// WorkNote a line saying why some are missing; empty without Mine.
	Work     AgentWork
	WorkNote string
	// QueueLength is how many builds wait in the server's queue, read as one
	// list: the board reads no queued build. QueueOK is false when the list
	// could not be read, and QueueNote says why.
	QueueLength int
	QueueOK     bool
	QueueNote   string
	Groups      []PinGroupView
	Needs       []Need
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
	// Name is the plan's name, set for a pin written as a plan key; a preset
	// pin keeps the preset's name.
	Name string
	// Results are the last finished builds, newest first.
	Results []provider.Build
	// Current is the build queued or running now, or nil.
	Current  *provider.Build
	Progress provider.Progress
	// Branches are the plan branches that built in the last BranchWindow,
	// newest first, at most PinBranches, each read like a pin of its own;
	// only a pin written as a plan key without a branch has them.
	// BranchesMore counts the branches left out.
	Branches     []PinView
	BranchesMore int
	// follow is true for a pin that lists its branches.
	follow bool
	// Tree is the pipelines the pin's build started, nested: the tree of the
	// build running now, else of the newest finished one. Nil when it could
	// not be read; use HasChildren to know whether there is anything to draw.
	Tree *Tree
	// treeRoot is the build whose tree was asked for last.
	treeRoot string
	// Last is how long the newest finished build took; Usual is the plan's
	// usual build time, 0 when its history is too short.
	Last, Usual time.Duration
	Err         error
}

// Label is how the board names the pipeline: its plan's name, with the
// branch when the pin has one, or the pin as written when no name is known.
func (p PinView) Label() string {
	switch {
	case p.Name == "":
		return p.Pin
	case p.Branch != "":
		return p.Name + "@" + p.Branch
	}
	return p.Name
}

// Rows is the group's pipelines as the board lists them: each pin, then the
// branches under it.
func (g PinGroupView) Rows() []PinView {
	var out []PinView
	for _, p := range g.Pins {
		out = append(out, p)
		out = append(out, p.Branches...)
	}
	return out
}

// Need is one line of the Needs you list.
type Need struct {
	Kind string // agent_offline, queue_wait (a pinned pipeline's build), pin_failed, pin_child_failed or pin_slow
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
	if q, err := s.P.Queue(ctx); err != nil {
		b.QueueNote = refusal("the queue", err)
	} else {
		b.QueueOK, b.QueueLength = true, len(q)
	}
	if b.Mine != nil {
		b.Work = s.boardWork(ctx, as, b.Groups, prev)
		b.WorkNote = b.Work.Note
		s.markWork(b.Mine, b.Work)
	}
	if err := ctx.Err(); err != nil {
		return Board{}, err
	}
	b.Needs = NeedsYou(b, now)
	return b, nil
}

// boardWork is what the busy ones of your agents build. Every read asks
// Bamboo's Build activity list, one request, while an agent is busy. Where
// the server does not give that list, the full read (prev nil) looks the
// slow way and the quick read keeps the answer of the board on screen, so
// Bamboo is asked once a minute.
func (s *Service) boardWork(ctx context.Context, as []provider.Agent, groups []PinGroupView, prev *Board) AgentWork {
	var busy []provider.Agent
	for _, a := range OnlyAgents(as, s.AgentStarts()) {
		if a.Busy && a.Online && a.Enabled {
			busy = append(busy, a)
		}
	}
	if prev != nil {
		if w, ok := s.LiveAgentWork(ctx, busy, groups); ok {
			return w
		}
		return prev.Work
	}
	return s.LookAgentWork(ctx, busy, groups)
}

// markWork sets Build or Deploy on each of your building agents the lookup
// found a build or a deployment for, and clears them on the others.
func (s *Service) markWork(m *MyAgents, w AgentWork) {
	for i := range m.Pools {
		for j := range m.Pools[i].Agents {
			a := &m.Pools[i].Agents[j]
			a.Build, a.Deploy = nil, nil
			if a.State != AgentBuilding {
				continue
			}
			if b, ok := w.BuildOf(a.Name); ok {
				a.Build = &b
			} else if d, ok := w.DeployOf(a.Name); ok {
				a.Deploy = &d
			}
		}
	}
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
		v.PlanKey, v.Branch, v.Name, v.Usual, v.follow = prev.PlanKey, prev.Branch, prev.Name, prev.Usual, prev.follow
		v = s.readPinBuilds(ctx, v, false, prev)
		return s.followBranches(ctx, v, prev)
	}
	plan, branch := config.SplitPin(pin)
	cfg := s.Config()
	if t, ok, _ := cfg.Target(PresetName(cfg, plan)); ok && t.Server != "" && !strings.EqualFold(t.Server, s.Server.Alias) {
		v.Err = errs.Usagef("preset %s is on server %s; bam top shows one server, %s", t.Name, t.Server, s.Server.Alias)
		return v
	}
	ref, err := s.ResolvePlan(ctx, plan, branch)
	if err != nil {
		v.Err = err
		return v
	}
	v.PlanKey, v.Branch = ref.PlanKey, ref.Branch
	if IsPlanKey(plan) {
		v.Name, _ = s.PlanName(ctx, ref.MasterKey)
	}
	v.follow = IsPlanKey(plan) && ref.Branch == ""
	v = s.readPinBuilds(ctx, v, true, nil)
	return s.followBranches(ctx, v, nil)
}

// followBranches fills the branches of a pin that lists them. The full read
// (prev nil) lists the plan's branches and reads the builds of each, one
// request per branch; the quick read reads again only the branches prev
// listed. A branch that cannot be read is left out.
func (s *Service) followBranches(ctx context.Context, v PinView, prev *PinView) PinView {
	if !v.follow || v.Err != nil {
		return v
	}
	var targets []provider.Branch
	if prev != nil {
		for _, b := range prev.Branches {
			targets = append(targets, provider.Branch{Key: b.PlanKey, ShortName: b.Branch})
		}
		v.BranchesMore = prev.BranchesMore
	} else {
		bs, err := s.P.ListBranches(ctx, v.PlanKey)
		if err != nil {
			return v
		}
		targets = bs
	}
	read := make([]PinView, len(targets))
	usual := make([]time.Duration, len(targets))
	if prev != nil {
		for i, b := range prev.Branches {
			usual[i] = b.Usual
		}
	}
	sem := make(chan struct{}, boardReaders)
	var wg sync.WaitGroup
	for i, br := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			name := v.Name
			if name == "" {
				name = v.Pin
			}
			read[i] = s.readPinBuilds(noPinTrees(ctx), PinView{Pin: v.Pin, PlanKey: br.Key, Branch: br.ShortName, Name: name, Usual: usual[i]}, false, nil)
		}()
	}
	wg.Wait()
	cutoff := s.Clock.Now().Add(-BranchWindow)
	type dated struct {
		view PinView
		at   time.Time
	}
	var recent []dated
	for _, b := range read {
		if b.Err != nil {
			continue
		}
		if at := branchActivity(b); at.After(cutoff) {
			recent = append(recent, dated{b, at})
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].at.After(recent[j].at) })
	if prev == nil {
		v.BranchesMore = max(len(recent)-PinBranches, 0)
	}
	for i, r := range recent {
		if i == PinBranches {
			break
		}
		v.Branches = append(v.Branches, r.view)
	}
	// A branch is slow against its own usual time, learned once, and only
	// when it runs: the plan's own history may be empty.
	for i, b := range v.Branches {
		if b.Usual == 0 && b.Current != nil && b.Current.State == provider.StateRunning {
			if med, err := s.historyMedian(ctx, b.PlanKey); err == nil {
				v.Branches[i].Usual = med
			}
		}
	}
	return v
}

// branchActivity is when a branch last had a build: the newest of the one
// running now and its last result; zero when it never built.
func branchActivity(b PinView) time.Time {
	var at time.Time
	for _, c := range append([]*provider.Build{b.Current}, resultPtrs(b.Results)...) {
		if c == nil {
			continue
		}
		for _, t := range []time.Time{c.FinishedAt, c.StartedAt, c.QueuedAt} {
			if t.After(at) {
				at = t
			}
		}
	}
	return at
}

func resultPtrs(bs []provider.Build) []*provider.Build {
	out := make([]*provider.Build, len(bs))
	for i := range bs {
		out[i] = &bs[i]
	}
	return out
}

// readPinBuilds reads the builds of v's plan. usual says whether the plan's
// usual build time is read too.
func (s *Service) readPinBuilds(ctx context.Context, v PinView, usual bool, prev *PinView) PinView {
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
	if c := v.Current; c != nil && c.State == provider.StateQueued && c.QueuedAt.IsZero() {
		// The list gives no time the build began waiting; its own record does.
		if b, err := s.P.GetBuild(ctx, c.Key); err == nil {
			c.QueuedAt = b.QueuedAt
			if c.QueuedAt.IsZero() {
				c.QueuedAt = b.StartedAt
			}
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
	return s.readPinTree(ctx, v, usual, prev)
}

// readPinTree adds the pin's tree. The quick refresh keeps the tree it has
// while the pin shows the same build; the full refresh reads it again.
func (s *Service) readPinTree(ctx context.Context, v PinView, full bool, prev *PinView) PinView {
	root, ok := v.PinRoot()
	if !ok || ctx.Value(pinTreeKey{}) != true {
		return v
	}
	if !full && prev != nil && prev.treeRoot == root.Key && !treeRootMoved(prev, root) {
		v.Tree, v.treeRoot = prev.Tree, root.Key // also a tree that could not be read: asked again on the full refresh
		return v
	}
	v.treeRoot = root.Key
	v.Tree = s.PinTree(ctx, root)
	return v
}

// NeedsYou is the Needs you list: an enabled agent that is offline (with
// agents: set, one of yours that is down or disabled), a build of a pinned
// pipeline waiting over NeedsWait, a pinned pipeline whose last build failed, a child
// pipeline of a pin that failed, and a
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
	for _, g := range b.Groups {
		for _, p := range g.Rows() {
			if p.Err != nil {
				continue
			}
			if len(p.Results) > 0 && p.Results[0].State == provider.StateFailed {
				out = append(out, Need{Kind: "pin_failed", Text: fmt.Sprintf("%s: the last build, %s, failed", p.Label(), p.Results[0].Key), Build: p.Results[0].Key})
			}
			for _, k := range failedChildren(p.Tree) {
				out = append(out, Need{Kind: "pin_child_failed", Text: fmt.Sprintf("%s: the child pipeline %s failed", p.Label(), k.Build.Key), Build: k.Build.Key})
			}
			if c := p.Current; c != nil && c.State == provider.StateQueued && !c.QueuedAt.IsZero() {
				if wait := now.Sub(c.QueuedAt); wait > NeedsWait {
					out = append(out, Need{Kind: "queue_wait", Text: fmt.Sprintf("%s has waited %s for an agent", c.Key, shortDuration(wait)), Build: c.Key})
				}
			}
			if c := p.Current; c != nil && c.State == provider.StateRunning && p.Usual > 0 && !c.StartedAt.IsZero() {
				if run := now.Sub(c.StartedAt); float64(run) > NeedsSlowFactor*float64(p.Usual) {
					out = append(out, Need{Kind: "pin_slow", Text: fmt.Sprintf("%s: %s has run %s, usually %s", p.Label(), c.Key, shortDuration(run), shortDuration(p.Usual)), Build: c.Key})
				}
			}
		}
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

// treeRootMoved says the build of a pin's tree has ended since the tree was
// read, so the quick refresh reads it again rather than keep a tree that still
// calls it running.
func treeRootMoved(prev *PinView, root provider.Build) bool {
	return prev.Tree != nil && prev.Tree.Root != nil && TreeFinished(root.State) != TreeFinished(prev.Tree.Root.Build.State)
}
