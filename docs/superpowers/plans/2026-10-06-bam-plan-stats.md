# bam plan stats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam plan stats <plan|preset> [--last N] [--branch B] [--json]` reports a plan's success rate, durations, streak and failing jobs over its last N builds.

**Architecture:** `app.Service.PlanStats` reads `ListBuilds` once and `GetBuild` per failed build (4 at a time), and computes `app.PlanStats`. `view` renders text and a JSON document. `cli` adds the subcommand under `bam plan`, resolving the plan as `bam build list` does.

**Tech Stack:** Go, cobra, testify.

**Spec:** `docs/superpowers/specs/2026-10-06-bam-plan-stats-design.md`

## Global Constraints

- Placeholders only (`bamboo.example.com`, `PROJ`, `OPS`, `jdoe`).
- The JSON document is new; its field names are those of spec §5.2 exactly.
- `app` never prints. No new dependencies. No provider change. No test sleeps or touches the network.
- Conventional Commits, one per task; the owner's identity only.
- Gate: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` — chained with `&&` so a failure stops the commit.
- `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. Durations must come from successful builds only — `TestPlanStatsDurationsUseSuccessfulBuildsOnly` (Task 1).
2. A failed build whose detail cannot be read must not fail the command — `TestPlanStatsUnreadableFailedBuild` (Task 1).
3. Queued and running builds must be left out of every figure — `TestPlanStatsCounts` (Task 1).
4. `--last 0`/`101` must fail before any network — `TestPlanStatsLastRange` (Task 3).
5. A plan with no finished builds prints one line and exits 0, JSON with nulls — `TestPlanStatsNoFinishedBuilds` (Tasks 2, 3).

---

### Task 1: `app.PlanStats`

**Files:**
- Create: `internal/app/stats.go`, `internal/app/stats_test.go`

**Interfaces:**
- Produces: `PlanStats`, `DurationStats`, `Streak`, `JobFailures`, `(PlanStats).SuccessRate() (float64, bool)`, `(*Service).PlanStats(ctx, ref PlanRef, n int) (PlanStats, error)`, `const UnknownJob = "(unknown)"`.

- [ ] **Step 1: Failing tests** — create `internal/app/stats_test.go`:

```go
package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

var statsAt = time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)

// sb is a build of PROJ-BUILD with number n, state st and duration d,
// started n hours after statsAt.
func sb(n int, st provider.State, d time.Duration, failedJobs ...string) provider.Build {
	start := statsAt.Add(time.Duration(n) * time.Hour)
	b := provider.Build{Key: "PROJ-BUILD-" + strconv.Itoa(n), PlanKey: "PROJ-BUILD", Number: n, State: st,
		StartedAt: start, FinishedAt: start.Add(d), Duration: d}
	if len(failedJobs) > 0 {
		var jobs []provider.Job
		for i, j := range failedJobs {
			jobs = append(jobs, provider.Job{Key: b.Key + "-J" + strconv.Itoa(i), Name: j, State: provider.StateFailed})
		}
		b.Stages = []provider.Stage{{Name: "Test", State: provider.StateFailed, Jobs: jobs}}
	}
	return b
}

// statsService serves builds (newest first) as PROJ-BUILD's history.
func statsService(t *testing.T, builds ...provider.Build) (*Service, *fake.Provider) {
	p := fakeBamboo()
	p.History["PROJ-BUILD"] = builds
	return newService(t, p), p
}

func statsRef() PlanRef { return PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD"} }

func TestPlanStatsCounts(t *testing.T) {
	s, _ := statsService(t,
		sb(9, provider.StateRunning, 0),
		sb(8, provider.StateQueued, 0),
		sb(7, provider.StateSuccess, 3*time.Minute),
		sb(6, provider.StateStopped, time.Minute),
		sb(5, provider.StateFailed, time.Minute, "Integration"),
		sb(4, provider.StateSuccess, 2*time.Minute),
		sb(3, provider.StateNotBuilt, 0),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, 7, st.Builds)
	require.Equal(t, 3, st.First)
	require.Equal(t, 9, st.Last)
	require.Equal(t, 2, st.Passed)
	require.Equal(t, 1, st.Failed)
	require.Equal(t, map[provider.State]int{provider.StateStopped: 1, provider.StateNotBuilt: 1}, st.Other)
	rate, ok := st.SuccessRate()
	require.True(t, ok)
	require.InDelta(t, 2.0/3, rate, 1e-9)
}

func TestPlanStatsDurationsUseSuccessfulBuildsOnly(t *testing.T) {
	s, _ := statsService(t,
		sb(5, provider.StateFailed, 10*time.Second, "Unit"),
		sb(4, provider.StateSuccess, 4*time.Minute),
		sb(3, provider.StateSuccess, 2*time.Minute),
		sb(2, provider.StateSuccess, 9*time.Minute),
		sb(1, provider.StateSuccess, 3*time.Minute),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.NotNil(t, st.Durations)
	require.Equal(t, 4, st.Durations.Of)
	require.Equal(t, 3*time.Minute, st.Durations.Median, "lower middle of 2,3,4,9")
	require.Equal(t, 9*time.Minute, st.Durations.P90)
	require.Equal(t, 9*time.Minute, st.Durations.Max)
	require.Equal(t, 2, st.Durations.MaxBuild)
}

func TestPlanStatsPercentiles(t *testing.T) {
	for _, tc := range []struct {
		n           int
		median, p90 int // minutes
	}{{1, 1, 1}, {2, 1, 2}, {10, 5, 9}, {11, 6, 10}} {
		var builds []provider.Build
		for i := tc.n; i >= 1; i-- {
			builds = append(builds, sb(i, provider.StateSuccess, time.Duration(i)*time.Minute))
		}
		s, _ := statsService(t, builds...)
		st, err := s.PlanStats(bg, statsRef(), 100)
		require.NoError(t, err)
		require.Equal(t, time.Duration(tc.median)*time.Minute, st.Durations.Median, "n=%d", tc.n)
		require.Equal(t, time.Duration(tc.p90)*time.Minute, st.Durations.P90, "n=%d", tc.n)
	}
}

func TestPlanStatsStreakSkipsOtherBuilds(t *testing.T) {
	s, _ := statsService(t,
		sb(6, provider.StateRunning, 0),
		sb(5, provider.StateSuccess, time.Minute),
		sb(4, provider.StateStopped, time.Minute),
		sb(3, provider.StateSuccess, time.Minute),
		sb(2, provider.StateFailed, time.Minute, "Unit"),
		sb(1, provider.StateSuccess, time.Minute),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, &Streak{State: provider.StateSuccess, Count: 2}, st.Streak)
}

func TestPlanStatsFailingJobs(t *testing.T) {
	s, _ := statsService(t,
		sb(6, provider.StateFailed, time.Minute, "Integration"),
		sb(5, provider.StateFailed, time.Minute, "Integration", "Deploy"),
		sb(4, provider.StateSuccess, time.Minute),
		sb(3, provider.StateFailed, time.Minute, "Deploy"),
		sb(2, provider.StateFailed, time.Minute, "Integration"),
	)
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, []JobFailures{
		{Name: "Integration", Failures: 3, LastBuild: 6},
		{Name: "Deploy", Failures: 2, LastBuild: 5},
	}, st.FailingJobs)
	require.Empty(t, st.Warnings)
}

func TestPlanStatsUnreadableFailedBuild(t *testing.T) {
	gone := sb(5, provider.StateFailed, time.Minute)
	s, p := statsService(t, gone, sb(4, provider.StateSuccess, time.Minute))
	// The list knows build 5; reading it alone fails.
	p.Sequences = map[string][]provider.Build{}
	p.History["PROJ-BUILD"] = []provider.Build{gone, sb(4, provider.StateSuccess, time.Minute)}
	s.P = &failingGet{Provider: p, key: gone.Key}
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, []JobFailures{{Name: UnknownJob, Failures: 1, LastBuild: 5}}, st.FailingJobs)
	require.Len(t, st.Warnings, 1)
	require.Contains(t, st.Warnings[0], "could not read PROJ-BUILD-5")
}

func TestPlanStatsNoFinishedBuilds(t *testing.T) {
	s, _ := statsService(t, sb(1, provider.StateRunning, 0))
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	_, ok := st.SuccessRate()
	require.False(t, ok)
	require.Nil(t, st.Durations)
	require.Nil(t, st.Streak)
	require.Empty(t, st.FailingJobs)
}

func TestPlanStatsRange(t *testing.T) {
	s, _ := statsService(t, sb(5, provider.StateSuccess, time.Minute), sb(2, provider.StateSuccess, time.Minute))
	st, err := s.PlanStats(bg, statsRef(), 20)
	require.NoError(t, err)
	require.Equal(t, statsAt.Add(2*time.Hour), st.From)
	require.Equal(t, statsAt.Add(5*time.Hour+time.Minute), st.To)
}
```

Also append to the same file the helpers it uses:

```go
// failingGet fails GetBuild for one key.
type failingGet struct {
	*fake.Provider
	key string
}

func (f *failingGet) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	if key == f.key {
		return provider.Build{}, errs.Bamboof("server error reading %s", key)
	}
	return f.Provider.GetBuild(ctx, key)
}
```

(with imports `context`, `strconv` and `internal/errs` added.)

- [ ] **Step 2: Run** `go test ./internal/app/ -run PlanStats 2>&1 | tail -2` — expected: compile error `s.PlanStats undefined`.

- [ ] **Step 3: Implement** — create `internal/app/stats.go`:

```go
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
	Builds          int // builds read
	First, Last     int // build numbers; 0 without builds
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
	builds, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: n})
	if err != nil {
		return PlanStats{}, err
	}
	st := PlanStats{PlanKey: ref.PlanKey, Branch: ref.Branch, Builds: len(builds), Other: map[provider.State]int{}}
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
		if !b.State.Finished() {
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
		wg.Add(1)
		sem <- struct{}{}
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
```

- [ ] **Step 4: Run** `go test -race ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git add internal/app && git commit -m "feat: compute a plan's statistics from its last builds"`

---

### Task 2: `view` — text and JSON

**Files:**
- Create: `internal/view/stats.go`, `internal/view/stats_test.go`

**Interfaces:**
- Consumes: `app.PlanStats` (Task 1).
- Produces: `func PlanStatsView(o Out, s app.PlanStats) error`; `type PlanStatsDoc`; `func PlanStatsJSON(s app.PlanStats) PlanStatsDoc`.

- [ ] **Step 1: Failing tests** — create `internal/view/stats_test.go`:

```go
package view

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var statsFrom = time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

func fullStats() app.PlanStats {
	return app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: 20, First: 381, Last: 400,
		From: statsFrom, To: statsFrom.Add(6 * 24 * time.Hour),
		Passed: 16, Failed: 4, Other: map[provider.State]int{provider.StateStopped: 1},
		Durations: &app.DurationStats{Median: 250 * time.Second, P90: 362 * time.Second, Max: 580 * time.Second, MaxBuild: 388, Of: 16},
		Streak:    &app.Streak{State: provider.StateSuccess, Count: 3},
		FailingJobs: []app.JobFailures{{Name: "Integration tests", Failures: 3, LastBuild: 397},
			{Name: "Deploy", Failures: 1, LastBuild: 384}}}
}

func TestPlanStatsViewFull(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, fullStats()))
	out := buf.String()
	require.Contains(t, out, "PROJ-DEPLOY  last 20 builds (#381–#400, 6 days)\n")
	require.Regexp(t, `Success\s+80%\s+\(16 ✓\s+4 ✗\s+1 stopped\)\s+streak ✓ 3`, out)
	require.Regexp(t, `Duration\s+median 4m10s\s+p90 6m2s\s+max 9m40s \(#388\)\s+of 16 successful`, out)
	require.Regexp(t, `Integration tests\s+3 of 4 failures\s+last #397`, out)
	require.Regexp(t, `Deploy\s+1 of 4 failures\s+last #384`, out)
}

func TestPlanStatsViewBranchAndEmptyParts(t *testing.T) {
	s := fullStats()
	s.Branch, s.PlanKey = "develop", "PROJ-DEPLOY12"
	s.Durations, s.FailingJobs, s.Failed, s.Passed = nil, nil, 0, 0
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, s))
	out := buf.String()
	require.Contains(t, out, "branch develop (PROJ-DEPLOY12)")
	require.Contains(t, out, "no successful builds")
	require.Regexp(t, `Failing jobs\s+none`, out)
}

func TestPlanStatsNoFinishedBuilds(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, PlanStatsView(o, app.PlanStats{PlanKey: "PROJ-DEPLOY", Builds: 20, Other: map[provider.State]int{}}))
	require.Equal(t, "no finished builds of PROJ-DEPLOY in the last 20\n", buf.String())

	data, err := json.Marshal(PlanStatsJSON(app.PlanStats{PlanKey: "PROJ-DEPLOY", Other: map[provider.State]int{}}))
	require.NoError(t, err)
	require.JSONEq(t, `{"plan_key":"PROJ-DEPLOY","branch":"","builds":0,"first":0,"last":0,"from":null,"to":null,
	  "passed":0,"failed":0,"other":{},"success_rate":null,"duration":null,"streak":null,"failing_jobs":[],"warnings":[]}`, string(data))
}

func TestPlanStatsJSON(t *testing.T) {
	data, err := json.Marshal(PlanStatsJSON(fullStats()))
	require.NoError(t, err)
	require.JSONEq(t, `{"plan_key":"PROJ-DEPLOY","branch":"","builds":20,"first":381,"last":400,
	  "from":"2026-09-30T08:00:00Z","to":"2026-10-06T08:00:00Z",
	  "passed":16,"failed":4,"other":{"stopped":1},"success_rate":0.8,
	  "duration":{"median_ms":250000,"p90_ms":362000,"max_ms":580000,"max_build":388,"of":16},
	  "streak":{"state":"success","count":3},
	  "failing_jobs":[{"name":"Integration tests","failures":3,"last_build":397},{"name":"Deploy","failures":1,"last_build":384}],
	  "warnings":[]}`, string(data))
}
```

(`view.Duration` formats 362s; if it prints `6m02s` rather than `6m2s`, use its real output in the regexp — the helper is the source of truth.)

- [ ] **Step 2: Run** `go test ./internal/view/ -run PlanStats 2>&1 | tail -2` — expected: compile error.

- [ ] **Step 3: Implement** — create `internal/view/stats.go`:

```go
package view

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// PlanStatsView is bam plan stats.
func PlanStatsView(o Out, s app.PlanStats) error {
	if s.Passed+s.Failed+otherCount(s) == 0 {
		fmt.Fprintf(o.W, "no finished builds of %s in the last %d\n", s.PlanKey, s.Builds)
		return nil
	}
	head := s.PlanKey
	if s.Branch != "" {
		head = fmt.Sprintf("%s  branch %s (%s)", s.PlanKey, s.Branch, s.PlanKey)
	}
	fmt.Fprintf(o.W, "%s  last %d builds (#%d–#%d, %s)\n", style.Bold(o.Style, head), s.Builds, s.First, s.Last, span(s.To.Sub(s.From)))

	ok, bad := style.StateGlyph(o.Style, provider.StateSuccess), style.StateGlyph(o.Style, provider.StateFailed)
	counts := fmt.Sprintf("%d %s  %d %s", s.Passed, ok, s.Failed, bad)
	for _, st := range sortedStates(s.Other) {
		counts += fmt.Sprintf("  %d %s", s.Other[st], strings.ToLower(style.Title(st)))
	}
	rate := "–"
	if r, has := s.SuccessRate(); has {
		rate = fmt.Sprintf("%d%%", int(math.Round(r*100)))
	}
	line := fmt.Sprintf("%-9s %s  (%s)", "Success", rate, counts)
	if s.Streak != nil {
		line += fmt.Sprintf("   streak %s %d", style.StateGlyph(o.Style, s.Streak.State), s.Streak.Count)
	}
	fmt.Fprintln(o.W, line)

	if d := s.Durations; d != nil {
		fmt.Fprintf(o.W, "%-9s median %s  p90 %s  max %s (#%d)   of %d successful\n", "Duration",
			Duration(d.Median), Duration(d.P90), Duration(d.Max), d.MaxBuild, d.Of)
	} else {
		fmt.Fprintf(o.W, "%-9s no successful builds\n", "Duration")
	}

	if len(s.FailingJobs) == 0 {
		fmt.Fprintf(o.W, "%-12s none\n", "Failing jobs")
		return nil
	}
	fmt.Fprintln(o.W, "Failing jobs")
	t := Table{Indent: "  ", Headers: []string{"", "", ""}}
	for _, j := range s.FailingJobs {
		t.Rows = append(t.Rows, []string{j.Name, fmt.Sprintf("%d of %d failures", j.Failures, s.Failed), fmt.Sprintf("last #%d", j.LastBuild)})
	}
	return RenderRows(o, t)
}

func otherCount(s app.PlanStats) int {
	n := 0
	for _, c := range s.Other {
		n += c
	}
	return n
}

func sortedStates(m map[provider.State]int) []provider.State {
	out := make([]provider.State, 0, len(m))
	for st, n := range m {
		if n > 0 {
			out = append(out, st)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// span is how long the builds covered: days from one day on, else a
// duration.
func span(d time.Duration) string {
	if d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return Duration(d)
}

type PlanStatsDoc struct {
	PlanKey     string            `json:"plan_key"`
	Branch      string            `json:"branch"`
	Builds      int               `json:"builds"`
	First       int               `json:"first"`
	Last        int               `json:"last"`
	From        *string           `json:"from"`
	To          *string           `json:"to"`
	Passed      int               `json:"passed"`
	Failed      int               `json:"failed"`
	Other       map[string]int    `json:"other"`
	SuccessRate *float64          `json:"success_rate"`
	Duration    *DurationStatsDoc `json:"duration"`
	Streak      *StreakDoc        `json:"streak"`
	FailingJobs []JobFailuresDoc  `json:"failing_jobs"`
	Warnings    []string          `json:"warnings"`
}

type DurationStatsDoc struct {
	MedianMS int64 `json:"median_ms"`
	P90MS    int64 `json:"p90_ms"`
	MaxMS    int64 `json:"max_ms"`
	MaxBuild int   `json:"max_build"`
	Of       int   `json:"of"`
}

type StreakDoc struct {
	State string `json:"state"`
	Count int    `json:"count"`
}

type JobFailuresDoc struct {
	Name      string `json:"name"`
	Failures  int    `json:"failures"`
	LastBuild int    `json:"last_build"`
}

func PlanStatsJSON(s app.PlanStats) PlanStatsDoc {
	d := PlanStatsDoc{PlanKey: s.PlanKey, Branch: s.Branch, Builds: s.Builds, First: s.First, Last: s.Last,
		Passed: s.Passed, Failed: s.Failed, Other: map[string]int{},
		FailingJobs: []JobFailuresDoc{}, Warnings: nonNil(s.Warnings)}
	if !s.From.IsZero() {
		d.From = timePtr(s.From)
	}
	if !s.To.IsZero() {
		d.To = timePtr(s.To)
	}
	for st, n := range s.Other {
		d.Other[string(st)] = n
	}
	if r, ok := s.SuccessRate(); ok {
		d.SuccessRate = &r
	}
	if x := s.Durations; x != nil {
		d.Duration = &DurationStatsDoc{MedianMS: x.Median.Milliseconds(), P90MS: x.P90.Milliseconds(),
			MaxMS: x.Max.Milliseconds(), MaxBuild: x.MaxBuild, Of: x.Of}
	}
	if s.Streak != nil {
		d.Streak = &StreakDoc{State: string(s.Streak.State), Count: s.Streak.Count}
	}
	for _, j := range s.FailingJobs {
		d.FailingJobs = append(d.FailingJobs, JobFailuresDoc{Name: j.Name, Failures: j.Failures, LastBuild: j.LastBuild})
	}
	return d
}
```

  (`timePtr`, `nonNil` and `ms` already exist in `view`; reuse them. If `provider.State`'s string value for stopped is not `"stopped"`, map it through the same helper `BuildJSON` uses for `state`.)

- [ ] **Step 4: Run** `go test ./internal/view/` — PASS; no golden changed.

- [ ] **Step 5: Commit** `git add internal/view && git commit -m "feat: render plan statistics as text and JSON"`

---

### Task 3: `bam plan stats`, docs and the gate

**Files:**
- Modify: `internal/cli/browse.go` (`newPlanStatsCmd`, registered in `addBrowse`)
- Create: `internal/cli/stats_test.go`
- Modify: `README.md`, `docs/json.md`; regenerate `docs/cli/`; spec status

- [ ] **Step 1: Failing tests** — create `internal/cli/stats_test.go`:

```go
package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func statsHarness(t *testing.T) *harness {
	h := newHarness(t)
	at := fixedNow.Add(-48 * time.Hour)
	b := func(n int, st provider.State) provider.Build {
		return provider.Build{Key: "PROJ-BUILD-" + itoaStats(n), PlanKey: "PROJ-BUILD", Number: n, State: st,
			StartedAt: at.Add(time.Duration(n) * time.Hour), FinishedAt: at.Add(time.Duration(n)*time.Hour + time.Minute), Duration: time.Minute}
	}
	h.fake.History["PROJ-BUILD"] = []provider.Build{b(3, provider.StateSuccess), b(2, provider.StateSuccess), b(1, provider.StateFailed)}
	return h
}

func itoaStats(n int) string { return string(rune('0' + n)) }

func TestPlanStatsCommand(t *testing.T) {
	h := statsHarness(t)
	require.Equal(t, 0, h.run("plan", "stats", "build"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "PROJ-BUILD")
	require.Contains(t, h.stdout.String(), "67%")

	h = statsHarness(t)
	require.Equal(t, 0, h.run("plan", "stats", "build", "--json"), h.stderr.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.Equal(t, float64(2), doc["passed"])
	require.Equal(t, "PROJ-BUILD", doc["plan_key"])
}

func TestPlanStatsLastRange(t *testing.T) {
	for _, n := range []string{"0", "101"} {
		h := statsHarness(t)
		require.Equal(t, 2, h.run("plan", "stats", "build", "--last", n))
		require.Contains(t, h.stderr.String(), "--last takes 1 to 100, not "+n)
		require.Empty(t, h.connectOpts, "no network before the check")
	}
}

func TestPlanStatsUsesThePresetsBranch(t *testing.T) {
	h := newHarness(t)
	h.fake.History["PROJ-PROV12"] = []provider.Build{{Key: "PROJ-PROV12-8", PlanKey: "PROJ-PROV12", Number: 8, State: provider.StateSuccess, Duration: time.Minute}}
	require.Equal(t, 0, h.run("plan", "stats", "provision-lab", "--json"), h.stderr.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	require.Equal(t, "PROJ-PROV12", doc["plan_key"])
	require.Equal(t, "develop", doc["branch"])
}
```

- [ ] **Step 2: Run** `go test ./internal/cli/ -run PlanStats 2>&1 | tail -3` — expected: FAIL (`unknown command "stats"`, exit 2 where 0 is expected).

- [ ] **Step 3: Implement** — in `internal/cli/browse.go`, register `newPlanStatsCmd(r)` in the `plan` subgroup's `AddCommand` list and add:

```go
func newPlanStatsCmd(r *runtime) *cobra.Command {
	var branch string
	var last int
	cmd := &cobra.Command{
		Use:               "stats <plan>",
		Short:             "Success rate, durations and failing jobs over a plan's last builds",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			// Checked before any config or network.
			if last < 1 || last > 100 {
				return errs.Usagef("--last takes 1 to 100, not %d", last)
			}
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], branch)
			if err != nil {
				return err
			}
			st, err := svc.PlanStats(ctx, ref, last)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, st.Warnings)
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.PlanStatsJSON(st))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.PlanStatsView(o, st)
		}),
	}
	f := cmd.Flags()
	f.IntVar(&last, "last", 20, "how many of the newest builds to read (1 to 100)")
	f.StringVar(&branch, "branch", "", "plan branch to read")
	return cmd
}
```

  and update the `plan` subgroup's description to `"Plans: list, show, variables, branches, stats"`.

- [ ] **Step 4: Run** `go test ./internal/cli/ && go test ./...` — PASS.

- [ ] **Step 5: Docs** — `make docs` (adds `docs/cli/bam_plan_stats.md`, updates `bam_plan.md`). README everyday commands table, after the `see recent builds` row: `| is this plan flaky? | \`bam plan stats provision-lab\` |`. `docs/json.md`: a `## Plan statistics` section after `## Plan`, with the §5.2 example and its null rules. Spec status → `Implemented`.

- [ ] **Step 6: Gate and commit**

```bash
go test -race ./... && make lint && make check-fixtures && make docs && \
git add internal/cli README.md docs && git commit -m "feat: add bam plan stats"
```
