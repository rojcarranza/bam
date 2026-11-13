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
	Queue      []QueueRow
	QueueOK    bool // the queue was read; an empty Queue is then really empty
	QueueNote  string
	Groups     []PinGroupView
	Needs      []Need
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
}

// Board reads the board for groups. It never fails for a refused agent list
// or queue, or for a pipeline it cannot read: those are noted on the board.
// Only an interrupt is an error.
func (s *Service) Board(ctx context.Context, groups []config.PinGroup) (Board, error) {
	now := s.Clock.Now()
	b := Board{At: now, Server: s.Server.Alias, URL: s.Server.URL}
	if info, err := s.P.ServerInfo(ctx); err == nil {
		b.Version = info.Version
	}
	if as, err := s.P.Agents(ctx); err != nil {
		b.AgentsNote = refusal("the agent list", err)
	} else {
		b.Agents = summarizeAgents(as)
	}
	if q, err := s.QueueRows(ctx); err != nil {
		b.QueueNote = refusal("the queue", err)
	} else {
		b.QueueOK = true
		b.Queue = q
	}
	b.Groups = s.readPins(ctx, groups)
	if err := ctx.Err(); err != nil {
		return Board{}, err
	}
	b.Needs = needsYou(b, now)
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
// first waitReads builds are read; a build whose record gives no time keeps
// AtLeast and counts from now, when bam first saw it.
func (s *Service) QueueRows(ctx context.Context) ([]QueueRow, error) {
	q, err := s.P.Queue(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now()
	rows := make([]QueueRow, len(q))
	sem := make(chan struct{}, boardReaders)
	var wg sync.WaitGroup
	for i, e := range q {
		rows[i] = QueueRow{Position: i + 1, QueueEntry: e, AtLeast: true}
		if i >= waitReads {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.GetBuild(ctx, e.Key)
			if err != nil {
				return
			}
			since := b.QueuedAt
			if since.IsZero() {
				since = b.StartedAt
			}
			if !since.IsZero() {
				rows[i].Waited, rows[i].AtLeast = max(now.Sub(since), 0), false
			}
		}()
	}
	wg.Wait()
	return rows, nil
}

// readPins reads every pinned pipeline, boardReaders at a time.
func (s *Service) readPins(ctx context.Context, groups []config.PinGroup) []PinGroupView {
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
				out[gi].Pins[pi] = s.readPin(ctx, pin)
			}()
		}
	}
	wg.Wait()
	return out
}

func (s *Service) readPin(ctx context.Context, pin string) PinView {
	v := PinView{Pin: pin}
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
	builds, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: PinResults + 1, Unfinished: true})
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
	if med, err := s.historyMedian(ctx, ref.PlanKey); err == nil {
		v.Usual = med
	}
	if v.Current != nil && v.Current.State == provider.StateRunning {
		v.Progress, _ = s.Progress(ctx, *v.Current)
	}
	return v
}

// needsYou is the Needs you list: an enabled agent that is offline, a build
// waiting over NeedsWait, a pinned pipeline whose last build failed, and a
// pinned build running NeedsSlowFactor past its usual time.
func needsYou(b Board, now time.Time) []Need {
	var out []Need
	if b.Agents != nil {
		for _, a := range b.Agents.Away {
			if a.Enabled && !a.Online {
				out = append(out, Need{"agent_offline", fmt.Sprintf("agent %s is offline", a.Name)})
			}
		}
	}
	for _, r := range b.Queue {
		if r.Waited > NeedsWait {
			out = append(out, Need{"queue_wait", fmt.Sprintf("%s has waited %s in the queue", r.Key, shortDuration(r.Waited))})
		}
	}
	for _, g := range b.Groups {
		for _, p := range g.Pins {
			if p.Err != nil {
				continue
			}
			if len(p.Results) > 0 && p.Results[0].State == provider.StateFailed {
				out = append(out, Need{"pin_failed", fmt.Sprintf("%s: the last build, %s, failed", p.Pin, p.Results[0].Key)})
			}
			if c := p.Current; c != nil && c.State == provider.StateRunning && p.Usual > 0 && !c.StartedAt.IsZero() {
				if run := now.Sub(c.StartedAt); float64(run) > NeedsSlowFactor*float64(p.Usual) {
					out = append(out, Need{"pin_slow", fmt.Sprintf("%s: %s has run %s, usually %s", p.Pin, c.Key, shortDuration(run), shortDuration(p.Usual))})
				}
			}
		}
	}
	return out
}

// shortDuration is d in whole minutes, or seconds under a minute.
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}
