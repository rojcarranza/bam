# Local duration estimate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When Bamboo reports build progress as unsupported, estimate a running build's progress from the median duration of its plan branch's last successful builds.

**Architecture:** `provider.Progress` gains `Source`. `app` gains a history estimate (`estimate.go`) with an in-memory median cache on `Service`; `Service.Progress` takes the build and falls back to it on `ErrUnsupported`; `Watch` does the same once unsupported. `view` adds `source` to the progress JSON.

**Tech Stack:** Go, testify.

**Spec:** `docs/superpowers/specs/2026-10-07-bam-local-estimate-design.md`

## Global Constraints

- Bamboo's estimate always wins; history only on `errs.ErrUnsupported`.
- JSON fields are added only: `source` on progress, omitted on a zero progress.
- Text output unchanged; goldens must not change.
- `app` never prints. No new dependencies. No test sleeps or touches the network.
- Conventional Commits, one per task; the owner's identity only.
- Gate chained before each commit: `go test -race ./... && make lint && make check-fixtures && make docs`.
- `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. A supported server's estimate is used and history never read — `TestProgressPrefersBamboo` (Task 2).
2. Failed builds must not enter the median — `TestProgressFromHistory` (fixture has a short failed build) (Task 2).
3. Fewer than 3 successful builds → no estimate — `TestProgressNeedsThreeSuccessfulBuilds` (Task 2).
4. History is read once per plan per process — `TestProgressReadsHistoryOncePerPlan` (Task 2).
5. A watch on an unsupported server carries history progress — `TestWatchUsesHistoryWhenUnsupported` (Task 2).

---

### Task 1: `Progress.Source`

**Files:**
- Modify: `internal/provider/types.go` (`Progress.Source`), `internal/provider/bamboo/progress.go` (`mapProgress`), `internal/view/json.go` (`ProgressDoc.Source`)
- Test: `internal/provider/bamboo/progress_test.go`, `internal/view/json_test.go`

**Interfaces:**
- Produces: `provider.Progress.Source string` (`"bamboo"`, `"history"`, or `""`); `view.ProgressDoc.Source` (`json:"source,omitempty"`).

- [ ] **Step 1: Failing tests**

In `internal/provider/bamboo/progress_test.go`, in the test that decodes the running fixture into a valid progress (the one asserting `Valid` and `Average`), add `assert.Equal(t, "bamboo", p.Source)` — use the variable name that test uses for the progress.

Append to `internal/view/json_test.go`:

```go
func TestProgressJSONNamesItsSource(t *testing.T) {
	b := provider.Build{Key: "PROJ-BUILD-45", State: provider.StateRunning}
	data, err := json.Marshal(BuildJSONWithProgress(b, provider.Progress{Valid: true, Average: time.Minute, Elapsed: 30 * time.Second,
		Remaining: 30 * time.Second, Percent: 0.5, Source: "history"}))
	require.NoError(t, err)
	require.Contains(t, string(data), `"source":"history"`)

	data, err = json.Marshal(BuildJSONWithProgress(b, provider.Progress{}))
	require.NoError(t, err)
	require.NotContains(t, string(data), `"source"`)
}
```

(Add `"time"` to the imports if missing.)

- [ ] **Step 2: Run** `go test ./internal/provider/bamboo/ ./internal/view/ -run 'Progress' 2>&1 | tail -3` — expected: compile error `unknown field Source`.

- [ ] **Step 3: Implement**
  - `internal/provider/types.go`, `Progress`, after `Stage`: `Source    string        // "bamboo" or "history"; empty without an estimate`.
  - `internal/provider/bamboo/progress.go`, `mapProgress`'s literal: `Source:  "bamboo",`.
  - `internal/view/json.go`, `ProgressDoc` gains `Source string \`json:"source,omitempty"\`` after `Stage`, filled wherever a `ProgressDoc` is built from a `provider.Progress` (`Source: p.Source`).

- [ ] **Step 4: Run** `go test ./internal/provider/... ./internal/view/...` — PASS; `git status --short internal/view/testdata` shows nothing.

- [ ] **Step 5: Gate and commit** — `go test -race ./... && make lint && git add internal && git commit -m "feat: say where a progress estimate comes from"`

---

### Task 2: the history estimate in `app`

**Files:**
- Create: `internal/app/estimate.go`, `internal/app/estimate_test.go`
- Modify: `internal/app/service.go` (`Service.estimates`, `Progress(ctx, b)`), `internal/app/run.go` (`Watch`), `internal/cli/browse.go` (two callers), `internal/view/tui/msgs.go` (one caller), and any test calling `Progress(ctx, key)`

**Interfaces:**
- Consumes: `provider.Progress.Source` (Task 1); fake `ProgressErr`, `ListCalls`.
- Produces: `func (s *Service) Progress(ctx context.Context, b provider.Build) (provider.Progress, error)`; `const historySample = 10`, `const minHistory = 3`.

- [ ] **Step 1: Failing tests** — create `internal/app/estimate_test.go`:

```go
package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// historyBamboo has no progress endpoint and three successful PROJ-BUILD
// builds of 6, 4 and 5 minutes, plus a 1-minute failure.
func historyBamboo() *fake.Provider {
	p := fakeBamboo()
	p.ProgressErr = errs.Bamboof("this Bamboo does not report build progress").Wrap(errs.ErrUnsupported)
	p.History["PROJ-BUILD"] = []provider.Build{
		{Key: "PROJ-BUILD-9", PlanKey: "PROJ-BUILD", Number: 9, State: provider.StateSuccess, Duration: 6 * time.Minute},
		{Key: "PROJ-BUILD-8", PlanKey: "PROJ-BUILD", Number: 8, State: provider.StateFailed, Duration: time.Minute},
		{Key: "PROJ-BUILD-7", PlanKey: "PROJ-BUILD", Number: 7, State: provider.StateSuccess, Duration: 4 * time.Minute},
		{Key: "PROJ-BUILD-6", PlanKey: "PROJ-BUILD", Number: 6, State: provider.StateSuccess, Duration: 5 * time.Minute},
	}
	return p
}

// runningFor is PROJ-BUILD-10, started d ago by s's clock.
func runningFor(s *Service, d time.Duration) provider.Build {
	return provider.Build{Key: "PROJ-BUILD-10", PlanKey: "PROJ-BUILD", Number: 10, State: provider.StateRunning,
		StartedAt: s.Clock.Now().Add(-d),
		Stages: []provider.Stage{{Name: "Build", State: provider.StateSuccess}, {Name: "Test", State: provider.StateRunning}}}
}

func TestProgressFromHistory(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, runningFor(s, 2*time.Minute))
	require.NoError(t, err)
	require.True(t, pr.Valid)
	require.Equal(t, "history", pr.Source)
	require.Equal(t, 5*time.Minute, pr.Average, "median of 4, 5, 6; the failure is not counted")
	require.Equal(t, 2*time.Minute, pr.Elapsed)
	require.Equal(t, 3*time.Minute, pr.Remaining)
	require.InDelta(t, 0.4, pr.Percent, 1e-9)
	require.Equal(t, "Test", pr.Stage)
}

func TestProgressPastTheMedian(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, runningFor(s, 7*time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1.0, pr.Percent)
	require.Zero(t, pr.Remaining)
}

func TestProgressNeedsThreeSuccessfulBuilds(t *testing.T) {
	p := historyBamboo()
	p.History["PROJ-BUILD"] = p.History["PROJ-BUILD"][:2] // one success, one failure
	s := newService(t, p)
	pr, err := s.Progress(bg, runningFor(s, time.Minute))
	require.NoError(t, err)
	require.False(t, pr.Valid)
}

func TestProgressNotRunningHasNone(t *testing.T) {
	s := newService(t, historyBamboo())
	pr, err := s.Progress(bg, provider.Build{Key: "PROJ-BUILD-10", PlanKey: "PROJ-BUILD", State: provider.StateQueued})
	require.NoError(t, err)
	require.False(t, pr.Valid)
}

func TestProgressPrefersBamboo(t *testing.T) {
	p := historyBamboo()
	p.ProgressErr = nil
	p.Progressions = map[string][]provider.Progress{"PROJ-BUILD-10": {{Valid: true, Average: time.Hour, Source: "bamboo"}}}
	s := newService(t, p)
	pr, err := s.Progress(bg, runningFor(s, time.Minute))
	require.NoError(t, err)
	require.Equal(t, "bamboo", pr.Source)
	require.Equal(t, 0, p.ListCalls("PROJ-BUILD"), "history is never read when Bamboo answers")
}

func TestProgressReadsHistoryOncePerPlan(t *testing.T) {
	p := historyBamboo()
	s := newService(t, p)
	for i := 0; i < 3; i++ {
		_, err := s.Progress(bg, runningFor(s, time.Minute))
		require.NoError(t, err)
	}
	require.Equal(t, 1, p.ListCalls("PROJ-BUILD"))
}

func TestWatchUsesHistoryWhenUnsupported(t *testing.T) {
	p := historyBamboo()
	s := newService(t, p)
	run := runningFor(s, time.Minute)
	done := run
	done.State = provider.StateSuccess
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD-10": {run, run, done}}
	var sawHistory bool
	for e := range s.Watch(bg, "PROJ-BUILD-10") {
		if e.Progress.Valid && e.Progress.Source == "history" {
			sawHistory = true
		}
	}
	require.True(t, sawHistory)
	require.Equal(t, 1, p.ListCalls("PROJ-BUILD"))
	require.Equal(t, 1, p.ProgressCalls("PROJ-BUILD-10"), "Bamboo asked once, then never again")
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'Progress|WatchUsesHistory' 2>&1 | tail -3` — expected: compile error (`Progress` takes a key).

- [ ] **Step 3: Implement** — create `internal/app/estimate.go`:

```go
package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// historySample is how many successful builds a local estimate's median is
// taken from; minHistory is how many it needs at least.
const (
	historySample = 10
	minHistory    = 3
)

// estimateCache keeps each plan branch's median for the life of the
// process, so a plan's history is read at most once.
type estimateCache struct {
	mu sync.Mutex
	m  map[string]time.Duration // plan key → median; 0 with too little history
}

// historyMedian is the median duration of planKey's last successful builds:
// the lower middle value of an even count, always a real build's duration.
func (s *Service) historyMedian(ctx context.Context, planKey string) (time.Duration, error) {
	s.estimates.mu.Lock()
	if d, ok := s.estimates.m[planKey]; ok {
		s.estimates.mu.Unlock()
		return d, nil
	}
	s.estimates.mu.Unlock()
	builds, err := s.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: historySample, State: provider.StateSuccess})
	if err != nil {
		return 0, err
	}
	var ds []time.Duration
	for _, b := range builds {
		if b.State == provider.StateSuccess && b.Duration > 0 {
			ds = append(ds, b.Duration)
		}
	}
	var med time.Duration
	if len(ds) >= minHistory {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		med = ds[(len(ds)-1)/2]
	}
	s.estimates.mu.Lock()
	if s.estimates.m == nil {
		s.estimates.m = map[string]time.Duration{}
	}
	s.estimates.m[planKey] = med
	s.estimates.mu.Unlock()
	return med, nil
}

// historyProgress estimates a running build from its plan branch's history,
// for a server that reports no progress. A build that is not running, or a
// plan with too little history, gets no estimate.
func (s *Service) historyProgress(ctx context.Context, b provider.Build) provider.Progress {
	if b.State != provider.StateRunning || b.StartedAt.IsZero() {
		return provider.Progress{}
	}
	med, err := s.historyMedian(ctx, b.PlanKey)
	if err != nil || med <= 0 {
		return provider.Progress{}
	}
	elapsed := max(s.Clock.Now().Sub(b.StartedAt), 0)
	p := provider.Progress{Valid: true, Average: med, Elapsed: elapsed, Percent: 1, Source: "history", Stage: runningStage(b)}
	if elapsed < med {
		p.Remaining = med - elapsed
		p.Percent = float64(elapsed) / float64(med)
	}
	return p
}

// runningStage is the stage of b that is running now, or "".
func runningStage(b provider.Build) string {
	for _, st := range b.Stages {
		if st.State == provider.StateRunning {
			return st.Name
		}
	}
	return ""
}
```

`internal/app/service.go`: `Service` gains, after `Getenv`:

```go
	estimates estimateCache // medians of plan histories, for servers without progress
```

and `Progress` becomes:

```go
// Progress returns how far a running build has got: Bamboo's estimate, or,
// when the server reports no progress, one from the plan's recent
// successful builds. A build that is not running, or one with no estimate
// to be had, yields a zero Progress and no error; only a real transport
// failure is returned.
func (s *Service) Progress(ctx context.Context, b provider.Build) (provider.Progress, error) {
	p, err := s.P.BuildProgress(ctx, b.Key)
	if err != nil {
		if errors.Is(err, errs.ErrUnsupported) {
			return s.historyProgress(ctx, b), nil
		}
		return provider.Progress{}, err
	}
	return p, nil
}
```

`internal/app/run.go`, in `Watch`, replace the progress block:

```go
			var pr provider.Progress
			if !noProgress && !b.State.Finished() {
				got, err := s.P.BuildProgress(ctx, key)
				switch {
				case err == nil:
					pr = got
				case errors.Is(err, errs.ErrUnsupported):
					noProgress = true
				}
			}
```

with:

```go
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
```

Callers: `internal/cli/browse.go` — `svc.Progress(ctx, b.Key)` → `svc.Progress(ctx, b)` in `runningProgress`, and in `build show` `svc.Progress(cmd.Context(), key)` → `svc.Progress(cmd.Context(), b)`; `internal/view/tui/msgs.go` `reloadBuildCmd` — `svc.Progress(ctx, key)` → `svc.Progress(ctx, b)`. Fix any test that calls `Progress(ctx, "<key>")` the same way (pass a `provider.Build{Key: …}`).

- [ ] **Step 4: Run** `go vet ./... && go test -race ./internal/app/ ./internal/cli/ ./internal/view/...` — PASS, no golden changes. If `go vet` reports a lock copy of `Service`, find the copy and take a pointer instead.

- [ ] **Step 5: Gate and commit** — `go test -race ./... && make lint && git add internal && git commit -m "feat: estimate progress from history when Bamboo has none"`

---

### Task 3: end to end, docs

**Files:**
- Test: `internal/cli/progress_test.go`
- Modify: `docs/json.md`, `docs/superpowers/specs/2026-09-20-bam-v0.3-estimator-design.md` (§2 pointer), spec status

- [ ] **Step 1: Test** — append to `internal/cli/progress_test.go`:

```go
// A server without progress still gets a bar, from the plan's history.
func TestBuildShowEstimatesFromHistory(t *testing.T) {
	h := newHarness(t)
	h.fake.ProgressErr = errs.Bamboof("this Bamboo does not report build progress").Wrap(errs.ErrUnsupported)
	ok := func(n int, d time.Duration) provider.Build {
		return provider.Build{Key: fmt.Sprintf("PROJ-BUILD-%d", n), PlanKey: "PROJ-BUILD", Number: n, State: provider.StateSuccess, Duration: d}
	}
	running := provider.Build{Key: "PROJ-BUILD-490", URL: workOrigin + "/browse/PROJ-BUILD-490", PlanKey: "PROJ-BUILD", Number: 490,
		State: provider.StateRunning, StartedAt: fixedNow.Add(-time.Minute)}
	h.fake.History["PROJ-BUILD"] = []provider.Build{running, ok(489, 4*time.Minute), ok(488, 4*time.Minute), ok(487, 4*time.Minute)}
	require.Equal(t, 0, h.run("build", "show", "PROJ-BUILD-490", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"source": "history"`)
}
```

(Add `"fmt"` and the `errs` import if missing. This test passes once Task 2 is in; it is the end-to-end check.)

- [ ] **Step 2: Run** `go test ./internal/cli/ -run TestBuildShowEstimatesFromHistory -v` — PASS.

- [ ] **Step 3: Docs**
  - `docs/json.md`, the progress paragraph (the one starting `` `percent` is a number between 0 and 1 ``): append `` `source` says where the estimate comes from: `bamboo`, or `history` when the server reports no progress and bam estimates from the median of the plan branch's last 10 successful builds (at least 3). ``
  - Estimator design §2, end of "Rejected alternative": `` A server without the status endpoint now gets a local estimate: see [`2026-10-07-bam-local-estimate-design.md`](2026-10-07-bam-local-estimate-design.md). ``
  - Spec status → `Implemented`.

- [ ] **Step 4: Gate and commit** — `go test -race ./... && make lint && make check-fixtures && make docs && git add internal docs && git commit -m "test: cover the history estimate end to end and document it"`
