package app

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// statsReaders is how many failed builds PlanStats reads at once.
const statsReaders = 4

// UnknownJob groups the failed builds whose failed jobs could not be read.
const UnknownJob = "(unknown)"

// PlanStats is what a plan's last builds say about it.
type PlanStats struct {
	PlanKey, Branch string
	MasterKey       string // the plan; PlanKey is its branch plan when a branch is chosen
	Builds          int    // builds read
	First, Last     int    // build numbers; 0 without builds
	From, To        time.Time
	Passed, Failed  int
	Other           map[provider.State]int // stopped, not built, skipped
	Durations       *DurationStats         // nil without passed builds
	Streak          *Streak                // nil without a passed or failed build
	FailingJobs     []JobFailures
	Warnings        []string
}

// DurationStats are over successful builds only: failed builds often stop
// early and would pull the figures down.
type DurationStats struct {
	Median, P90, Max time.Duration
	MaxBuild, Of     int
}

type Streak struct {
	State provider.State
	Count int
}

type JobFailures struct {
	Name      string
	Failures  int
	LastBuild int
}

// SuccessRate is passed ÷ (passed + failed), and false without either.
func (p PlanStats) SuccessRate() (float64, bool) {
	d := p.Passed + p.Failed
	if d == 0 {
		return 0, false
	}
	return float64(p.Passed) / float64(d), true
}

// PlanStats reads the last n builds of ref and what failed in the failed
// ones. Queued and running builds are left out of every figure.
func (s *Service) PlanStats(ctx context.Context, ref PlanRef, n int) (PlanStats, error) {
	builds, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: n, Unfinished: true})
	if err != nil {
		return PlanStats{}, err
	}
	st := PlanStats{PlanKey: ref.PlanKey, MasterKey: ref.MasterKey, Branch: ref.Branch, Builds: len(builds), Other: map[provider.State]int{}}
	type dur struct {
		d time.Duration
		n int
	}
	var durs []dur
	var failed []provider.Build
	streakBroken := false // the streak ended at the first verdict that differs
	for _, b := range builds {
		if st.First == 0 || b.Number < st.First {
			st.First = b.Number
		}
		if b.Number > st.Last {
			st.Last = b.Number
		}
		start := b.StartedAt
		if start.IsZero() {
			start = b.QueuedAt
		}
		if !start.IsZero() && (st.From.IsZero() || start.Before(st.From)) {
			st.From = start
		}
		if !settled(b.State) {
			continue
		}
		if b.FinishedAt.After(st.To) {
			st.To = b.FinishedAt
		}
		switch b.State {
		case provider.StateSuccess:
			st.Passed++
			durs = append(durs, dur{b.Duration, b.Number})
		case provider.StateFailed:
			st.Failed++
			failed = append(failed, b)
		default:
			st.Other[b.State]++
		}
		if b.State == provider.StateSuccess || b.State == provider.StateFailed {
			switch {
			case st.Streak == nil:
				st.Streak = &Streak{State: b.State, Count: 1}
			case st.Streak.State == b.State && !streakBroken:
				st.Streak.Count++
			default:
				streakBroken = true
			}
		}
	}
	if len(durs) > 0 {
		sort.SliceStable(durs, func(i, j int) bool { return durs[i].d < durs[j].d })
		max := durs[len(durs)-1]
		st.Durations = &DurationStats{
			Median: durs[(len(durs)-1)/2].d,
			P90:    durs[int(math.Ceil(0.9*float64(len(durs))))-1].d,
			Max:    max.d, MaxBuild: max.n, Of: len(durs),
		}
	}
	st.FailingJobs, st.Warnings = s.failingJobs(ctx, failed)
	// An interrupt during the reads is an interrupt: its failed reads are
	// not "could not read" warnings, and partial figures are not stats.
	if err := ctx.Err(); err != nil {
		return PlanStats{}, err
	}
	return st, nil
}

// failingJobs reads each failed build, statsReaders at a time, and groups
// its failed jobs by name.
func (s *Service) failingJobs(ctx context.Context, failed []provider.Build) ([]JobFailures, []string) {
	type read struct {
		jobs []string
		err  error
	}
	reads := make([]read, len(failed))
	sem := make(chan struct{}, statsReaders)
	var wg sync.WaitGroup
	for i, b := range failed {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil, nil
		}
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			d, err := s.P.GetBuild(ctx, key)
			if err != nil {
				reads[i].err = err
				return
			}
			for _, j := range d.FailedJobs() {
				reads[i].jobs = append(reads[i].jobs, j.Name)
			}
		}(i, b.Key)
	}
	wg.Wait()
	byName := map[string]*JobFailures{}
	var warnings []string
	note := func(name string, n int) {
		f := byName[name]
		if f == nil {
			f = &JobFailures{Name: name}
			byName[name] = f
		}
		f.Failures++
		f.LastBuild = max(f.LastBuild, n)
	}
	for i, b := range failed {
		r := reads[i]
		switch {
		case r.err != nil:
			warnings = append(warnings, "could not read "+b.Key+": "+describeErr(r.err))
			note(UnknownJob, b.Number)
		case len(r.jobs) == 0:
			note(UnknownJob, b.Number)
		default:
			seen := map[string]bool{}
			for _, j := range r.jobs {
				if !seen[j] {
					seen[j] = true
					note(j, b.Number)
				}
			}
		}
	}
	out := make([]JobFailures, 0, len(byName))
	for _, f := range byName {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Failures != b.Failures {
			return a.Failures > b.Failures
		}
		if a.LastBuild != b.LastBuild {
			return a.LastBuild > b.LastBuild
		}
		return a.Name < b.Name
	})
	return out, warnings
}

// settled is whether a build's state is its last for statistics: finished,
// or skipped, which Finished leaves out because a skipped build can be
// skipped before it ever starts, but which never changes again either.
func settled(s provider.State) bool {
	return s.Finished() || s == provider.StateSkipped
}
