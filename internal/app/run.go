package app

import (
	"context"
	"errors"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// Polling bounds (spec §5.4).
const (
	MinPoll = 2 * time.Second
	MaxPoll = 10 * time.Second
)

// Stopping a build whose revision Bamboo ignored: a build that was only just
// queued may have no job to stop yet, so a not-found answer is retried.
const (
	stopRetryEvery = 2 * time.Second
	stopRetryFor   = 30 * time.Second
)

// Run triggers the plan with the variables that differ from the plan's
// values and records the build as this repository's last build. When a
// revision was asked for and Bamboo ignored it, the build (which runs the
// newest commit) is stopped and Run fails.
func (s *Service) Run(ctx context.Context, ref PlanRef, vs VarSet) (provider.Build, error) {
	b, err := s.P.Trigger(ctx, provider.TriggerRequest{PlanKey: ref.PlanKey, Variables: vs.Changed(), Secret: vs.Secret(), Revision: ref.Revision})
	if err != nil {
		if ref.Revision != "" && b.Key != "" && errors.Is(err, errs.ErrUnsupported) {
			return b, s.stopIgnoredRevision(ctx, b.Key)
		}
		return provider.Build{}, s.explainDisabled(ctx, ref.PlanKey, err)
	}
	rec := LastRecord{BuildKey: b.Key, Origin: s.Origin, PlanKey: ref.PlanKey, TriggeredAt: s.Clock.Now()}
	if ref.Target != nil {
		rec.Target = ref.Target.Name
	}
	// The build is already running; failing to remember it only loses --last.
	_ = s.State.SetLast(s.Cfg.RepoRoot, rec)
	return b, nil
}

// explainDisabled names the cause when Bamboo refused to start a plan that
// is disabled, so the error does not depend on how Bamboo words its refusal.
func (s *Service) explainDisabled(ctx context.Context, key string, err error) error {
	var e *errs.Error
	if !errors.As(err, &e) || e.Kind != errs.KindBamboo || errors.Is(err, errs.ErrNotFound) {
		return err
	}
	if p, perr := s.P.GetPlan(ctx, key); perr == nil && p.Disabled {
		e.Why, e.Try = key+" is disabled in Bamboo", "enable it in Bamboo: "+p.URL
	}
	return err
}

// DisabledWarning returns a warning when Bamboo would refuse to start key
// because it is disabled, so a dry run says so before the real run fails;
// otherwise "". A failed lookup is not a warning: the dry run still holds.
func (s *Service) DisabledWarning(ctx context.Context, key string) string {
	if p, err := s.P.GetPlan(ctx, key); err == nil && p.Disabled {
		return key + " is disabled in Bamboo, so a real run would be refused; enable it in Bamboo: " + p.URL
	}
	return ""
}

// stopIgnoredRevision stops a build that runs the newest commit instead of
// the revision the user chose, and returns the error that says so.
func (s *Service) stopIgnoredRevision(ctx context.Context, key string) error {
	deadline := s.Clock.Now().Add(stopRetryFor)
	err := s.P.StopBuild(ctx, key)
	for err != nil && errors.Is(err, errs.ErrNotFound) && s.Clock.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-s.Clock.After(stopRetryEvery):
			err = s.P.StopBuild(ctx, key)
		}
	}
	if err != nil {
		return errs.Bamboof("Bamboo ignored the revision; %s is building the newest commit", key).
			WithWhy("this Bamboo does not support building a chosen revision over REST").
			WithTry("bam build cancel " + key).
			Wrap(err)
	}
	return errs.Bamboof("Bamboo ignored the revision; %s was stopped", key).
		WithWhy("this Bamboo does not support building a chosen revision over REST").
		WithTry("run without --revision, or use Run customised in Bamboo")
}

// EventType names what changed between two polls.
type EventType string

const (
	EventState    EventType = "state"    // the build's state changed (always sent for the first poll)
	EventStage    EventType = "stage"    // a stage's state changed
	EventJob      EventType = "job"      // a job's state changed
	EventProgress EventType = "progress" // only the server's estimate changed
	EventDone     EventType = "done"     // the build finished; last event
	EventError    EventType = "error"    // polling failed; last event
)

// Event is one change seen while watching. Build is the snapshot of that poll,
// and Progress the server's estimate for it; a zero Progress means none.
type Event struct {
	Type     EventType
	Time     time.Time
	Build    provider.Build
	Progress provider.Progress
	Name     string // stage or job name
	Key      string // job result key for job events
	State    provider.State
	Err      error
}

// Watch polls a build until it finishes and emits its changes. Ending ctx
// stops watching only; the build keeps running.
func (s *Service) Watch(ctx context.Context, key string) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		send := func(e Event) bool {
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var prev *provider.Build
		interval := MinPoll
		noProgress := false
		var sent provider.Progress // the estimate the last event carried
		for {
			// The provider ignores ctx, so check here before every poll:
			// ending ctx must stop watching, not keep polling forever.
			select {
			case <-ctx.Done():
				return
			default:
			}
			b, err := s.Build(ctx, key)
			if err != nil {
				if ctx.Err() == nil {
					send(Event{Type: EventError, Time: s.Clock.Now(), Err: err})
				}
				return
			}
			now := s.Clock.Now()
			// A missing estimate is decoration lost, never a reason to stop
			// watching, so every progress error becomes a zero Progress.
			var pr provider.Progress
			if !b.State.Finished() {
				if !noProgress {
					got, err := s.P.BuildProgress(ctx, key)
					switch {
					case err == nil:
						pr = got
					case errors.Is(err, errs.ErrUnsupported):
						noProgress = true
					}
				}
				// A server without progress gets the plan's history instead;
				// its median is read once and kept.
				if noProgress {
					pr = s.historyProgress(ctx, b)
				}
			}
			changes := diff(prev, b)
			// A poll that changes nothing still brings a fresh estimate; send
			// it on its own so the bar moves. It does not reset the backoff.
			moved := len(changes) == 0 && pr.Valid && pr != sent
			if moved {
				changes = []Event{{Type: EventProgress, State: b.State}}
			}
			for _, e := range changes {
				e.Time, e.Build, e.Progress = now, b, pr
				if !send(e) {
					return
				}
			}
			if b.State.Finished() {
				send(Event{Type: EventDone, Time: now, Build: b, State: b.State})
				return
			}
			sent = pr
			if len(changes) > 0 && !moved {
				interval = MinPoll
			} else {
				interval = min(interval*3/2, MaxPoll)
			}
			prev = &b
			select {
			case <-ctx.Done():
				return
			case <-s.Clock.After(interval):
			}
		}
	}()
	return ch
}

// diff lists the changes from prev to cur. The first poll reports only the
// build state.
func diff(prev *provider.Build, cur provider.Build) []Event {
	if prev == nil {
		return []Event{{Type: EventState, State: cur.State}}
	}
	var out []Event
	if prev.State != cur.State {
		out = append(out, Event{Type: EventState, State: cur.State})
	}
	oldStages := map[string]provider.State{}
	oldJobs := map[string]provider.State{}
	for _, st := range prev.Stages {
		oldStages[st.Name] = st.State
		for _, j := range st.Jobs {
			oldJobs[j.Key] = j.State
		}
	}
	for _, st := range cur.Stages {
		if was, ok := oldStages[st.Name]; (ok && was != st.State) || (!ok && st.State != provider.StateNotBuilt) {
			out = append(out, Event{Type: EventStage, Name: st.Name, State: st.State})
		}
		for _, j := range st.Jobs {
			if was, ok := oldJobs[j.Key]; (ok && was != j.State) || (!ok && j.State != provider.StateNotBuilt) {
				out = append(out, Event{Type: EventJob, Name: j.Name, Key: j.Key, State: j.State})
			}
		}
	}
	return out
}

// BuildRevision is the commit of key's default repository, for reusing it
// with a new run. A build that never started has none, which is the caller's
// input to fix, not a Bamboo fault.
func (s *Service) BuildRevision(ctx context.Context, key string) (string, error) {
	b, err := s.P.GetBuild(ctx, key)
	if err != nil {
		return "", err
	}
	if b.Revision == "" {
		return "", errs.Usagef("%s has no revision to reuse", key).WithTry("pass a commit with --revision")
	}
	return b.Revision, nil
}
