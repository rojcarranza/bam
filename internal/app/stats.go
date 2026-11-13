package app

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
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
	// Wait is the jobs' wait for an agent; nil when no job of the window
	// could be read. Earlier is the same over the window before it, nil when
	// -n left no room for one or it had nothing to compare.
	Wait         *WaitStats
	Earlier      *WaitStats
	SlowStages   []Slowest // by median run time of successful runs, slowest first
	SlowJobs     []Slowest
	UnreadBuilds int // finished builds whose jobs could not be read
	Warnings     []string
}

// maxSlowest is how many stages and jobs the slowest lists hold.
const maxSlowest = 3

// WaitStats is the time jobs waited for an agent. Share is the part of all
// job time (waiting plus running) spent waiting.
type WaitStats struct {
	Median, P90 time.Duration
	Share       float64
	Jobs        int // jobs counted
}

// Slowest is a stage or job and its median run time over successful runs.
// Earlier is the same median over the window before; 0 when there is none.
type Slowest struct {
	Name    string
	Median  time.Duration
	Runs    int
	Earlier time.Duration
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

// PlanStats reads the last n builds of ref, in every state, and what failed
// in the failed ones. Builds and the range count every build read; queued and
// running builds are left out of every other figure. Not-built builds, which
// Bamboo lists only when asked for every state, count under Other.
func (s *Service) PlanStats(ctx context.Context, ref PlanRef, n int) (PlanStats, error) {
	// When twice n fits in the 100 builds -n allows, read the window before
	// as well, to compare with.
	limit := n
	if 2*n <= 100 {
		limit = 2 * n
	}
	all, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: limit, Unfinished: true})
	if err != nil {
		return PlanStats{}, err
	}
	builds, before := all, []provider.Build(nil)
	if len(all) > n {
		builds, before = all[:n], all[n:]
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
	cur, prev := ranBuilds(builds), ranBuilds(before)
	reads := s.readBuilds(ctx, append(append([]provider.Build{}, cur...), prev...))
	got := func(bs []provider.Build) []provider.Build {
		var out []provider.Build
		for _, b := range bs {
			if r := reads[b.Key]; r.err == nil {
				out = append(out, r.b)
			}
		}
		return out
	}
	st.FailingJobs, st.Warnings = failingJobs(failed, reads)
	for _, b := range cur {
		if r := reads[b.Key]; r.err != nil {
			st.UnreadBuilds++
			if b.State != provider.StateFailed { // failed ones warn in failingJobs
				st.Warnings = append(st.Warnings, "could not read "+b.Key+": "+describeErr(r.err))
			}
		}
	}
	curRead, prevRead := got(cur), got(prev)
	st.Wait = waitStats(curRead)
	st.Earlier = waitStats(prevRead)
	st.SlowStages, st.SlowJobs = slowest(curRead, prevRead)
	// An interrupt during the reads is an interrupt: its failed reads are
	// not "could not read" warnings, and partial figures are not stats.
	if err := ctx.Err(); err != nil {
		return PlanStats{}, err
	}
	return st, nil
}

// buildRead is one finished build read in full.
type buildRead struct {
	b   provider.Build
	err error
}

// ranBuilds keeps the builds that ran to a verdict: their jobs have times.
func ranBuilds(bs []provider.Build) []provider.Build {
	var out []provider.Build
	for _, b := range bs {
		if b.State == provider.StateSuccess || b.State == provider.StateFailed {
			out = append(out, b)
		}
	}
	return out
}

// readBuilds reads each build, statsReaders at a time, keyed by build key.
func (s *Service) readBuilds(ctx context.Context, bs []provider.Build) map[string]buildRead {
	reads := make([]buildRead, len(bs))
	sem := make(chan struct{}, statsReaders)
	var wg sync.WaitGroup
	for i, b := range bs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return nil
		}
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			reads[i].b, reads[i].err = s.P.GetBuild(ctx, key)
		}(i, b.Key)
	}
	wg.Wait()
	out := make(map[string]buildRead, len(bs))
	for i, b := range bs {
		out[b.Key] = reads[i]
	}
	return out
}

// failingJobs groups the failed jobs of the failed builds by name.
func failingJobs(failed []provider.Build, reads map[string]buildRead) ([]JobFailures, []string) {
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
	for _, b := range failed {
		r := reads[b.Key]
		var names []string
		for _, j := range r.b.FailedJobs() {
			names = append(names, j.Name)
		}
		switch {
		case r.err != nil:
			warnings = append(warnings, "could not read "+b.Key+": "+describeErr(r.err))
			note(UnknownJob, b.Number)
		case len(names) == 0:
			note(UnknownJob, b.Number)
		default:
			seen := map[string]bool{}
			for _, j := range names {
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

// ranJobs are the jobs of bs that ran to a verdict.
func ranJobs(bs []provider.Build) (jobs []provider.Job) {
	for _, b := range bs {
		for _, s := range b.Stages {
			for _, j := range s.Jobs {
				if j.State == provider.StateSuccess || j.State == provider.StateFailed {
					jobs = append(jobs, j)
				}
			}
		}
	}
	return jobs
}

// waitStats is the wait for an agent over the jobs of bs, nil without jobs.
func waitStats(bs []provider.Build) *WaitStats {
	jobs := ranJobs(bs)
	if len(jobs) == 0 {
		return nil
	}
	waits := make([]time.Duration, len(jobs))
	var wait, total time.Duration
	for i, j := range jobs {
		waits[i] = j.QueueDuration
		wait += j.QueueDuration
		total += j.QueueDuration + j.Duration
	}
	w := &WaitStats{Jobs: len(jobs)}
	w.Median, w.P90 = percentiles(waits)
	if total > 0 {
		w.Share = float64(wait) / float64(total)
	}
	return w
}

// percentiles are the median and 90th percentile, the way PlanStats counts
// them for durations.
func percentiles(ds []time.Duration) (median, p90 time.Duration) {
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)-1)/2], sorted[int(math.Ceil(0.9*float64(len(sorted))))-1]
}

// slowest lists the stages and jobs with the longest median run time over
// the successful runs in cur, with the same median over prev. Jobs are told
// apart by stage, since jobs in different stages often share a name; a job
// whose name is in more than one stage is shown as "Stage / Job".
func slowest(cur, prev []provider.Build) (stages, jobs []Slowest) {
	type key struct{ stage, name string }
	medians := func(bs []provider.Build) (stage, job map[key][]time.Duration) {
		stage, job = map[key][]time.Duration{}, map[key][]time.Duration{}
		for _, b := range bs {
			for _, s := range b.Stages {
				if s.State == provider.StateSuccess {
					stage[key{name: s.Name}] = append(stage[key{name: s.Name}], s.Duration)
				}
				for _, j := range s.Jobs {
					if j.State == provider.StateSuccess {
						k := key{s.Name, j.Name}
						job[k] = append(job[k], j.Duration)
					}
				}
			}
		}
		return stage, job
	}
	pick := func(now, before map[key][]time.Duration) []Slowest {
		stagesOf := map[string]int{}
		for k := range now {
			stagesOf[k.name]++
		}
		var out []Slowest
		for k, ds := range now {
			m, _ := percentiles(ds)
			name := k.name
			if stagesOf[k.name] > 1 {
				name = k.stage + " / " + k.name
			}
			e := Slowest{Name: name, Median: m, Runs: len(ds)}
			if b := before[k]; len(b) > 0 {
				e.Earlier, _ = percentiles(b)
			}
			out = append(out, e)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Median != out[j].Median {
				return out[i].Median > out[j].Median
			}
			return out[i].Name < out[j].Name
		})
		return out[:min(len(out), maxSlowest)]
	}
	cs, cj := medians(cur)
	ps, pj := medians(prev)
	return pick(cs, ps), pick(cj, pj)
}
