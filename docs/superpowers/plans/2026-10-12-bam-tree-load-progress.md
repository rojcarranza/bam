# Tree load progress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While a build tree loads, `bam build tree` (plain, `--watch`, `--rerun-failed`) shows a progress line on stderr on a terminal, and the TUI tree screen and plan-screen Tree panel show the same progress in place of their static loading text.

**Architecture:** `app` counts the tree search's requests in an unexported, mutex-guarded `treeTracker` and reports `TreeProgress` snapshots through `TreeOptions.OnProgress` (`BuildTree`) or as `TreeLoading` events on the `WatchTree` channel (first refresh only). `view` owns the one text form (`TreeProgressText`) and a rewritable stderr line (`ProgressLine`); the CLI and TUI only wire them in.

**Tech Stack:** Go, cobra, bubbletea, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-12-bam-tree-load-progress-design.md`

## Global Constraints

- Public repository: placeholders only (`bamboo.example.com`, `PROJ`, `OPS`, `LAB`, `jdoe`).
- `app` never prints, reads stdin or imports cobra; `TestAppStaysUIReady` must pass.
- `--json` output and pipe output are unchanged byte for byte.
- Progress shows only when stdout and stderr are both terminals, `--json` is off, and `TERM` is not `dumb`.
- No test sleeps or touches the network.
- Conventional Commits, each ending `Refs #81`. Commits use Roj's git identity only: no `Co-Authored-By`, no Claude trailer.
- Before each commit: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"` if `go` is not found.

## Review Focus

1. A tree read that fails (a plan, a log, a detail) must still count as done, or the line stalls at `39/40` forever: Task 1 pins it with a failing `ListBuilds`.
2. A watch reader that is slow or gone must never block the tree load on a progress send: Task 2 pins it with a reader that starts only after the load has passed several sends.
3. An interrupted `--watch` must not leave a half-drawn progress line before `still running: …`: Task 3 pins `TreeLive.Stop`; Task 4 calls it before the note.
4. The progress line must never wrap on a narrow terminal (a wrap leaves a stale line behind `\r`): Task 3 pins the cut to width − 1.
5. A progress message from an earlier Tree panel read (another build, or after `r`) must not overwrite the current one: Task 6 pins it.

---

## File structure

| File | Change | Responsibility |
| --- | --- | --- |
| `internal/app/tree_progress.go` | create | `TreeCount`, `TreeProgress`, `treeTracker` |
| `internal/app/tree.go` | modify | `TreeOptions.OnProgress`/`track`, `TreeLoading`, `TreeEvent.Progress`, counting in `treeCandidates`, `treeRead`, `treeDeepen`, `treeParents`, `treeDetails`, `WatchTree` |
| `internal/app/tree_log.go` | modify | counting in `treeLogLinks`, `treeAddProjects`, `treeReadLinks` |
| `internal/app/tree_progress_test.go` | create | tracker and `BuildTree`/`WatchTree` progress tests |
| `internal/view/progressline.go` | create | `ProgressLine` |
| `internal/view/progressline_test.go` | create | its tests |
| `internal/view/tree.go` | modify | `TreeProgressText`, `TreeLive` progress writer and `Stop` |
| `internal/view/tree_test.go`, `internal/view/tree_watch_test.go` | modify | tests; `NewTreeLive` call site |
| `internal/cli/env.go` | modify | `Env.StderrTTY` |
| `internal/cli/tree.go` | modify | `progressOn`, `treeProgress`, `loadTree`, `treeLoop` wiring, help text |
| `internal/cli/harness_test.go` | modify | set `StderrTTY` from `h.tty` |
| `internal/cli/tree_test.go` | modify | CLI progress tests |
| `internal/view/tui/treescreen.go` | modify | `treeScreen.progress`, `TreeLoading`, header |
| `internal/view/tui/treescreen_test.go` | modify | `firstTree` drains progress; new tests |
| `internal/view/tui/msgs.go` | modify | `treePreviewCmd` with progress, `treePreviewProgressMsg` |
| `internal/view/tui/planscreen.go` | modify | `treePreview.progress`, `treePreviewProgressed`, `treeBody` |
| `internal/view/tui/model.go` | modify | route `treePreviewProgressMsg` |
| `internal/view/tui/planscreen_test.go` | modify | panel progress tests |
| `docs/cli/` | regenerate | `make docs` |
| `CHANGELOG.md`, spec | modify | one Unreleased line; spec status |

---

### Task 1: `app` counts a tree load and reports it through `OnProgress`

**Files:**
- Create: `internal/app/tree_progress.go`, `internal/app/tree_progress_test.go`
- Modify: `internal/app/tree.go`, `internal/app/tree_log.go`

**Interfaces:**
- Produces:
  - `type TreeCount struct{ Done, Total int }`
  - `type TreeProgress struct{ Projects, Plans, Builds, Jobs TreeCount }`
  - `TreeOptions.OnProgress func(TreeProgress)`; unexported `TreeOptions.track *treeTracker`
  - `newTreeTracker(report func(TreeProgress)) *treeTracker` (nil for a nil report), `(*treeTracker).add(ph treePhase, n int)`, `(*treeTracker).done(ph treePhase)`; phases `treeProjects`, `treePlans`, `treeBuilds`, `treeJobs`. Both methods are no-ops on a nil tracker.

- [ ] **Step 1: Write the failing tests**

Create `internal/app/tree_progress_test.go`:

```go
package app

import (
	"context"
	"sync"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// progressRec records every snapshot a tree load reports.
type progressRec struct {
	mu  sync.Mutex
	got []TreeProgress
}

func (r *progressRec) on(p TreeProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, p)
}

func (r *progressRec) snapshots() []TreeProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TreeProgress(nil), r.got...)
}

func counts(p TreeProgress) []TreeCount { return []TreeCount{p.Projects, p.Plans, p.Builds, p.Jobs} }

// requireSaneProgress: every count has Done <= Total, nothing ever goes
// down, and the last snapshot has every started phase finished.
func requireSaneProgress(t *testing.T, got []TreeProgress) {
	t.Helper()
	require.NotEmpty(t, got)
	var prev TreeProgress
	for i, p := range got {
		for j, c := range counts(p) {
			was := counts(prev)[j]
			require.LessOrEqual(t, c.Done, c.Total, "snapshot %d phase %d", i, j)
			require.GreaterOrEqual(t, c.Total, was.Total, "snapshot %d phase %d: a total shrank", i, j)
			require.GreaterOrEqual(t, c.Done, was.Done, "snapshot %d phase %d: done went back", i, j)
		}
		prev = p
	}
	for j, c := range counts(prev) {
		require.Equal(t, c.Total, c.Done, "phase %d is finished at the end", j)
	}
}

func TestATreeTrackerIsSafeWhenNil(t *testing.T) {
	require.Nil(t, newTreeTracker(nil))
	var tr *treeTracker
	tr.add(treePlans, 3)
	tr.done(treePlans)
}

func TestBuildTreeReportsEveryRequestItMakes(t *testing.T) {
	var rec progressRec
	_, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	require.Equal(t, TreeProgress{
		Projects: TreeCount{2, 2}, // PROJ and OPS
		Plans:    TreeCount{4, 4}, // PROJ-API, PROJ-DOCS, OPS-DEPLOY, OPS-SMOKE
		Builds:   TreeCount{3, 3}, // OPS-DEPLOY-88, OPS-SMOKE-51, PROJ-DOCS-30
	}, got[len(got)-1])
}

func TestBuildTreeCountsALinkedProjectAndItsLogs(t *testing.T) {
	var rec progressRec
	_, err := treeService(t, logTreeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	last := got[len(got)-1]
	require.Equal(t, TreeCount{3, 3}, last.Projects, "LAB is listed once a log links to it")
	require.Equal(t, TreeCount{8, 8}, last.Plans, "five candidates, then LAB's three plans")
	require.Positive(t, last.Jobs.Total)
	overlap := false
	for _, p := range got {
		if p.Jobs.Total > 0 && p.Plans.Done < p.Plans.Total {
			overlap = true
		}
	}
	require.True(t, overlap, "LAB's builds are read while logs are being counted")
}

func TestBuildTreeCountsAFailedReadAsDone(t *testing.T) {
	p := treeFake()
	svc := treeService(t, p)
	svc.P = &flakyProvider{Provider: p, failBuilds: func(plan string, _ int) bool { return plan == "PROJ-DOCS" }}
	var rec progressRec
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	require.NotEmpty(t, tr.Warnings)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	require.Equal(t, TreeCount{4, 4}, got[len(got)-1].Plans)
}

func TestBuildTreeDoesNotCountBuildsAWatchAlreadyHas(t *testing.T) {
	var rec progressRec
	svc := treeService(t, treeFake())
	done := map[string]provider.Build{"OPS-SMOKE-51": tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)}
	o := TreeOptions{track: newTreeTracker(rec.on)}
	_, _, err := svc.buildTree(context.Background(), "PROJ-API-142", o, done, nil)
	require.NoError(t, err)
	got := rec.snapshots()
	require.Equal(t, TreeCount{2, 2}, got[len(got)-1].Builds, "OPS-SMOKE-51 is reused, not read")
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./internal/app/ -run 'TreeTracker|BuildTreeReports|BuildTreeCounts|BuildTreeDoesNotCount' -v`
Expected: build failure: `undefined: TreeProgress`, `newTreeTracker`, `TreeOptions.OnProgress`.

- [ ] **Step 3: Add the types and the tracker**

Create `internal/app/tree_progress.go`:

```go
package app

import "sync"

// TreeCount is one phase of a tree load: requests finished and requests
// known so far. Total only grows.
type TreeCount struct{ Done, Total int }

// TreeProgress is a snapshot of a tree load.
type TreeProgress struct {
	Projects TreeCount // projects whose plans were listed
	Plans    TreeCount // plan build reads, the deeper reads included
	Builds   TreeCount // build reads: details, restarted builds' parents, builds a log links to
	Jobs     TreeCount // job logs read for the builds a script started
}

// treePhase names a TreeProgress count.
type treePhase int

const (
	treeProjects treePhase = iota
	treePlans
	treeBuilds
	treeJobs
)

// treeTracker counts a tree load's requests and reports every change, one
// call at a time, in order. A nil tracker counts nothing.
type treeTracker struct {
	mu     sync.Mutex
	p      TreeProgress
	report func(TreeProgress)
}

func newTreeTracker(report func(TreeProgress)) *treeTracker {
	if report == nil {
		return nil
	}
	return &treeTracker{report: report}
}

func (t *treeTracker) count(ph treePhase) *TreeCount {
	switch ph {
	case treeProjects:
		return &t.p.Projects
	case treePlans:
		return &t.p.Plans
	case treeBuilds:
		return &t.p.Builds
	}
	return &t.p.Jobs
}

// add raises ph's total by n, before its requests start.
func (t *treeTracker) add(ph treePhase, n int) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count(ph).Total += n
	t.report(t.p)
}

// done counts one request of ph as ended, whether it succeeded or not, so
// the count never stalls short of its total.
func (t *treeTracker) done(ph treePhase) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count(ph).Done++
	t.report(t.p)
}
```

- [ ] **Step 4: Add `OnProgress` and thread the tracker**

In `internal/app/tree.go`, replace `TreeOptions`:

```go
// TreeOptions shape a build tree.
type TreeOptions struct {
	Depth      int                // levels of builds below the root; 0 means all
	OnProgress func(TreeProgress) // BuildTree reports how far it got; calls are serialized and end before it returns
	track      *treeTracker       // set by BuildTree and WatchTree
}
```

and `BuildTree`:

```go
// BuildTree reads build key and every build it caused. It never triggers or
// stops anything.
func (s *Service) BuildTree(ctx context.Context, key string, o TreeOptions) (Tree, error) {
	o.track = newTreeTracker(o.OnProgress)
	t, _, err := s.buildTree(ctx, key, o, nil, nil)
	return t, err
}
```

In `buildTree`, add `tr := o.track` after the `configured` read and pass `tr` as the second argument of every helper below. The call sites become:

```go
plans, projects, more, err := s.treeCandidates(ctx, tr, root.PlanKey, configured)
...
got, failed := s.treeRead(ctx, tr, scan.plans, limit)
...
depths = s.treeDeepen(ctx, tr, got, root)
...
warnings = append(warnings, s.treeParents(ctx, tr, scan)...)
...
warnings = append(warnings, s.treeDetails(ctx, tr, t.Root, done)...)
warnings = append(warnings, s.treeLogLinks(ctx, tr, t.Root, scan, configured, o.Depth, done)...)
```

Counting in each helper (add `tr *treeTracker` as the second parameter of each signature):

`treeCandidates`: before the `for _, p := range projects` loop add `tr.add(treeProjects, len(projects))`; directly after `plans, err := s.P.ListPlans(ctx, p)` add `tr.done(treeProjects)`.

`treeDeepen`: `more, _ := s.treeRead(ctx, tr, short, read)`.

`treeRead`: before `for i, p := range plans` add `tr.add(treePlans, len(plans))`; in the goroutine, after the `ListBuilds` line add `tr.done(treePlans)`.

`treeParents`: before `for i, k := range keys` add `tr.add(treeBuilds, len(keys))`; in the goroutine after `b, err := s.P.GetBuild(ctx, k)` add `tr.done(treeBuilds)`.

`treeDetails`: before the goroutine loop add

```go
	reads := 0
	for _, n := range nodes {
		if _, ok := done[n.Build.Key]; !ok {
			reads++
		}
	}
	tr.add(treeBuilds, reads)
```

and in the goroutine after `b, err := s.P.GetBuild(ctx, n.Build.Key)` add `tr.done(treeBuilds)`.

In `internal/app/tree_log.go`:

`treeLogLinks(ctx context.Context, tr *treeTracker, root *TreeNode, …)`: call `s.treeReadLinks(ctx, tr, open, sc)`; before `for i, f := range todo { if b, ok := done[...]` add

```go
		reads := 0
		for _, f := range todo {
			if _, ok := done[f.link.key]; !ok {
				reads++
			}
		}
		tr.add(treeBuilds, reads)
```

and in its goroutine change the read to

```go
				builds[i], readErrs[i] = s.P.GetBuild(ctx, key)
				tr.done(treeBuilds)
```

Then `s.treeAddProjects(ctx, tr, sc, fetched, root.Build)` and `s.treeDetails(ctx, tr, kid, done)`.

`treeAddProjects(ctx context.Context, tr *treeTracker, …)`: just before `ps, err := s.P.ListPlans(ctx, project)` add `tr.add(treeProjects, 1)`, and directly after it `tr.done(treeProjects)`; then `s.treeRead(ctx, tr, plans, treeWindow)` and `s.treeDeepen(ctx, tr, got, root)`.

`treeReadLinks(ctx context.Context, tr *treeTracker, …)`: before `for i, j := range jobs { if k, ok := sc.logLinks[...]` add

```go
	reads := 0
	for _, j := range jobs {
		if _, ok := sc.logLinks[j.job.Key]; !ok {
			reads++
		}
	}
	tr.add(treeJobs, reads)
```

and in the goroutine after `chunk, err := s.P.FetchLog(ctx, j.job.Key, provider.LogOptions{})` add `tr.done(treeJobs)`.

`WatchTree`'s `s.buildTree(ctx, key, o, done, scan)` is unchanged in this task (its `o.track` is nil).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/app/ -v -run 'TreeTracker|BuildTreeReports|BuildTreeCounts|BuildTreeDoesNotCount'`
Expected: PASS. If `TestBuildTreeReportsEveryRequestItMakes` reports different totals, re-check the counting against the fixture before touching the expectation: each number in the comment is one request.

Run: `go test -race ./internal/app/`
Expected: PASS, including `TestAppStaysUIReady`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/tree_progress.go internal/app/tree_progress_test.go internal/app/tree.go internal/app/tree_log.go
git commit -m "feat: count a build tree's reads and report them as it loads

Refs #81"
```

---

### Task 2: `WatchTree` sends `TreeLoading` during its first refresh

**Files:**
- Modify: `internal/app/tree.go`, `internal/app/tree_progress_test.go`

**Interfaces:**
- Consumes: `newTreeTracker`, `TreeOptions.track` (Task 1)
- Produces: `const TreeLoading TreeEventType = "loading"`; `TreeEvent.Progress TreeProgress`

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/tree_progress_test.go` (add `"github.com/r0jjames/bam-cli/internal/provider/fake"` to its imports):

```go
// finishedTreeFake is treeFake with every build finished, so a watch ends.
func finishedTreeFake() *fake.Provider {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSuccess
	return p
}

func TestWatchTreeReportsItsFirstRefreshOnly(t *testing.T) {
	svc := treeService(t, finishedTreeFake())
	called := false
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142",
		TreeOptions{OnProgress: func(TreeProgress) { called = true }}))
	require.False(t, called, "a watch reports on its channel only")
	first := -1
	loading := 0
	for i, e := range evs {
		if e.Type == TreeLoading {
			require.Equal(t, -1, first, "event %d: no progress after the first tree", i)
			loading++
			continue
		}
		if first < 0 {
			first = i
		}
	}
	require.Positive(t, loading)
	require.Equal(t, TreeUpdate, evs[first].Type)
	require.Equal(t, TreeDone, evs[len(evs)-1].Type)
}

// signalProvider closes hit once GetBuild has read key.
type signalProvider struct {
	*fake.Provider
	key  string
	once sync.Once
	hit  chan struct{}
}

func (p *signalProvider) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	b, err := p.Provider.GetBuild(ctx, key)
	if key == p.key {
		p.once.Do(func() { close(p.hit) })
	}
	return b, err
}

func TestWatchTreeProgressNeverBlocksTheLoad(t *testing.T) {
	p := finishedTreeFake()
	svc := treeService(t, p)
	sp := &signalProvider{Provider: p, key: "OPS-SMOKE-51", hit: make(chan struct{})}
	svc.P = sp
	ch := svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{})
	// Nobody reads yet. Listing and reading builds report more snapshots
	// than the channel holds; a blocking send would never reach the details.
	<-sp.hit
	evs := collectTree(ch)
	require.Equal(t, TreeDone, evs[len(evs)-1].Type)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'WatchTreeReports|WatchTreeProgress' -v`
Expected: build failure: `undefined: TreeLoading`.

- [ ] **Step 3: Implement**

In `internal/app/tree.go`, extend the event constants and struct:

```go
const (
	TreeLoading TreeEventType = "loading" // the first refresh is still reading; Progress says how far
	TreeUpdate  TreeEventType = "update"  // the tree changed (always sent for the first refresh)
	TreeDone    TreeEventType = "done"    // every build finished and nothing new appeared; last event
	TreeError   TreeEventType = "error"   // refreshes kept failing; last event
)

// TreeEvent is one step of a watched tree.
type TreeEvent struct {
	Type     TreeEventType
	Time     time.Time
	Tree     Tree
	Progress TreeProgress // for TreeLoading
	Err      error
}
```

In `WatchTree`, replace `t, next, err := s.buildTree(ctx, key, o, done, scan)` with:

```go
			ro := o
			ro.track = nil
			if scan == nil {
				// The first refresh is the slow one: say how far it got. A
				// full channel drops the snapshot rather than hold up the
				// load; a newer one follows.
				ro.track = newTreeTracker(func(p TreeProgress) {
					select {
					case ch <- TreeEvent{Type: TreeLoading, Time: s.Clock.Now(), Progress: p}:
					default:
					}
				})
			}
			t, next, err := s.buildTree(ctx, key, ro, done, scan)
```

Update `WatchTree`'s doc comment: add the sentence "Until the first tree, it sends TreeLoading events saying how far the search got; o.OnProgress is not called."

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/app/`
Expected: PASS (the existing watch tests ignore events they do not look for; if one counts events, skip `TreeLoading` there and say so in the commit body).

- [ ] **Step 5: Commit**

```bash
git add internal/app/tree.go internal/app/tree_progress_test.go
git commit -m "feat: send tree load progress on a watched tree's first refresh

Refs #81"
```

---

### Task 3: `view` describes progress and draws a rewritable line

**Files:**
- Create: `internal/view/progressline.go`, `internal/view/progressline_test.go`
- Modify: `internal/view/tree.go`, `internal/view/tree_test.go`, `internal/view/tree_watch_test.go`

**Interfaces:**
- Consumes: `app.TreeProgress`, `app.TreeCount`, `app.TreeLoading` (Tasks 1–2)
- Produces:
  - `func TreeProgressText(p app.TreeProgress) string`
  - `func NewProgressLine(w io.Writer, width int) *ProgressLine`; `(*ProgressLine).Show(text string)`; `(*ProgressLine).Clear()`; both nil-safe
  - `func NewTreeLive(o Out, progress io.Writer, failedOnly bool) *TreeLive`; `(*TreeLive).Stop()` (nil-safe)

- [ ] **Step 1: Write the failing tests**

Create `internal/view/progressline_test.go`:

```go
package view

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgressLineRewritesOnlyAChangedText(t *testing.T) {
	var b bytes.Buffer
	l := NewProgressLine(&b, 80)
	l.Show("reading builds 1/4 plans")
	l.Show("reading builds 1/4 plans")
	l.Show("reading builds 2/4 plans")
	l.Clear()
	l.Clear()
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans\r\x1b[Kreading builds 2/4 plans\r\x1b[K", b.String())
}

func TestProgressLineNeverWraps(t *testing.T) {
	var b bytes.Buffer
	NewProgressLine(&b, 12).Show("reading builds 12/40 plans")
	require.Equal(t, "\r\x1b[Kreading bu…", b.String(), "cut to one column short of the width")
}

func TestProgressLineClearIsANoOpWhenNothingShows(t *testing.T) {
	var b bytes.Buffer
	NewProgressLine(&b, 80).Clear()
	require.Empty(t, b.String())
	var nilLine *ProgressLine
	nilLine.Show("x")
	nilLine.Clear()
}
```

Append to `internal/view/tree_test.go`:

```go
func TestTreeProgressText(t *testing.T) {
	c := func(d, n int) app.TreeCount { return app.TreeCount{Done: d, Total: n} }
	for _, tc := range []struct {
		p    app.TreeProgress
		want string
	}{
		{app.TreeProgress{}, "reading the tree"},
		{app.TreeProgress{Projects: c(1, 2)}, "listing plans 1/2 projects"},
		{app.TreeProgress{Projects: c(2, 2), Plans: c(12, 40)}, "reading builds 12/40 plans"},
		{app.TreeProgress{Projects: c(3, 3), Plans: c(2, 6), Builds: c(4, 4), Jobs: c(3, 9)}, "reading builds 2/6 plans · reading logs 3/9 jobs"},
		{app.TreeProgress{Projects: c(2, 2), Plans: c(4, 4), Builds: c(3, 3)}, "reading details 3/3 builds"},
	} {
		require.Equal(t, tc.want, TreeProgressText(tc.p))
	}
}
```

Append to `internal/view/tree_watch_test.go` (it has `twoTrees()`, `treeEv` and `testOut(tty bool) (Out, *bytes.Buffer)`):

```go
func loadingEv(done, total int) app.TreeEvent {
	return app.TreeEvent{Type: app.TreeLoading, Progress: app.TreeProgress{Plans: app.TreeCount{Done: done, Total: total}}}
}

func TestTreeLiveShowsProgressOnItsOwnWriterAndClearsIt(t *testing.T) {
	first, _ := twoTrees()
	o, out := testOut(true)
	var prog bytes.Buffer
	l := NewTreeLive(o, &prog, false)
	l.Event(loadingEv(1, 4))
	require.Empty(t, out.String(), "no tree yet")
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans", prog.String())
	l.Event(treeEv(app.TreeUpdate, first))
	require.Equal(t, "\r\x1b[Kreading builds 1/4 plans\r\x1b[K", prog.String(), "cleared before the tree")
	require.NotEmpty(t, out.String())
}

// An interrupted watch ends without a tree: Stop must clear the line before
// the CLI prints its "still running" note.
func TestTreeLiveStopClearsAShownLine(t *testing.T) {
	o, _ := testOut(true)
	var prog bytes.Buffer
	l := NewTreeLive(o, &prog, false)
	l.Event(loadingEv(1, 4))
	l.Stop()
	require.True(t, strings.HasSuffix(prog.String(), "\r\x1b[K"), "%q", prog.String())
	var none *TreeLive
	none.Stop()
}

func TestTreeLiveWithoutAProgressWriterDropsLoading(t *testing.T) {
	var out bytes.Buffer
	l := NewTreeLive(Out{W: &out, TTY: true, Width: 80}, nil, false)
	l.Event(app.TreeEvent{Type: app.TreeLoading})
	l.Stop()
	require.Empty(t, out.String())
}

func TestPipeAndNDJSONRenderersIgnoreLoading(t *testing.T) {
	var b bytes.Buffer
	o := Out{W: &b}
	NewTreeLines(o, false).Event(app.TreeEvent{Type: app.TreeLoading})
	NewTreeNDJSON(o, false).Event(app.TreeEvent{Type: app.TreeLoading})
	require.Empty(t, b.String())
}
```

Change the existing `NewTreeLive(o, false)` at `internal/view/tree_watch_test.go:46` to `NewTreeLive(o, nil, false)`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/ -run 'ProgressLine|TreeProgressText|TreeLive|IgnoreLoading' -v`
Expected: build failure: `undefined: NewProgressLine`, `TreeProgressText`, too many arguments to `NewTreeLive`.

- [ ] **Step 3: Implement `ProgressLine`**

Create `internal/view/progressline.go`:

```go
package view

import (
	"fmt"
	"io"
)

// ProgressLine is one line on a terminal, rewritten in place: what a slow
// read is doing. A nil line shows nothing.
type ProgressLine struct {
	w     io.Writer
	width int
	shown string
}

func NewProgressLine(w io.Writer, width int) *ProgressLine {
	return &ProgressLine{w: w, width: width}
}

// Show replaces the line with text, cut one column short of the width so
// the terminal never wraps it. An unchanged text is not written again.
func (l *ProgressLine) Show(text string) {
	if l == nil {
		return
	}
	if l.width > 1 {
		text = cut(text, l.width-1)
	}
	if text == l.shown {
		return
	}
	fmt.Fprint(l.w, "\r\x1b[K"+text)
	l.shown = text
}

// Clear wipes the line, if one shows, before anything else is printed.
func (l *ProgressLine) Clear() {
	if l == nil || l.shown == "" {
		return
	}
	fmt.Fprint(l.w, "\r\x1b[K")
	l.shown = ""
}
```

- [ ] **Step 4: Implement `TreeProgressText` and the `TreeLive` changes**

In `internal/view/tree.go` add (with `io` in the imports):

```go
// TreeProgressText says what a tree load is reading: every phase that has
// started and not finished, or the last one started once all have.
func TreeProgressText(p app.TreeProgress) string {
	phases := []struct {
		c          app.TreeCount
		verb, unit string
	}{
		{p.Projects, "listing plans", "projects"},
		{p.Plans, "reading builds", "plans"},
		{p.Builds, "reading details", "builds"},
		{p.Jobs, "reading logs", "jobs"},
	}
	var open []string
	last := ""
	for _, ph := range phases {
		if ph.c.Total == 0 {
			continue
		}
		s := fmt.Sprintf("%s %d/%d %s", ph.verb, ph.c.Done, ph.c.Total, ph.unit)
		last = s
		if ph.c.Done < ph.c.Total {
			open = append(open, s)
		}
	}
	switch {
	case len(open) > 0:
		return strings.Join(open, " · ")
	case last != "":
		return last
	}
	return "reading the tree"
}
```

Replace `TreeLive`, `NewTreeLive` and the start of `Event`:

```go
// TreeLive redraws the tree in place on a TTY, and shows the first
// refresh's progress on its own line until the tree arrives.
type TreeLive struct {
	o        Out
	failed   bool
	lines    int
	progress *ProgressLine
}

// NewTreeLive draws on o. Progress goes to progress, usually stderr; nil
// shows none.
func NewTreeLive(o Out, progress io.Writer, failedOnly bool) *TreeLive {
	l := &TreeLive{o: o, failed: failedOnly}
	if progress != nil {
		l.progress = NewProgressLine(progress, o.Width)
	}
	return l
}

// Stop clears the progress line, for a watch that ends without a tree.
func (l *TreeLive) Stop() {
	if l != nil {
		l.progress.Clear()
	}
}

func (l *TreeLive) Event(e app.TreeEvent) {
	if e.Type == app.TreeLoading {
		l.progress.Show(TreeProgressText(e.Progress))
		return
	}
	l.progress.Clear()
	if e.Type == app.TreeError {
		return
	}
	// … the rest of Event is unchanged
```

`TreeLines` and `TreeNDJSON` already ignore types they do not handle; leave them.

- [ ] **Step 5: Fix the one production caller so the module builds**

In `internal/cli/tree.go`, change `rend = view.NewTreeLive(out, failed)` to `rend = view.NewTreeLive(out, nil, failed)` (Task 4 wires the writer).

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/view/... ./internal/cli/`
Expected: PASS, goldens unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/view/progressline.go internal/view/progressline_test.go internal/view/tree.go internal/view/tree_test.go internal/view/tree_watch_test.go internal/cli/tree.go
git commit -m "feat: describe tree load progress and draw it on one rewritable line

Refs #81"
```

---

### Task 4: `bam build tree` shows progress on a terminal

**Files:**
- Modify: `internal/cli/env.go`, `internal/cli/tree.go`, `internal/cli/harness_test.go`, `internal/cli/tree_test.go`
- Regenerate: `docs/cli/` (`make docs`)

**Interfaces:**
- Consumes: `app.TreeOptions.OnProgress`, `view.TreeProgressText`, `view.NewProgressLine`, `view.NewTreeLive(o, progress, failed)`, `(*view.TreeLive).Stop` (Tasks 1–3)
- Produces: `Env.StderrTTY bool`; `(*runtime).progressOn() bool`, `(*runtime).treeProgress() *view.ProgressLine`, `(*runtime).loadTree(ctx, svc, key, o) (app.Tree, error)`

- [ ] **Step 1: Make the harness set `StderrTTY`**

In `internal/cli/harness_test.go`, in both `run` and `runCtx`, after `env.StdinTTY = h.tty` add `env.StderrTTY = h.tty`. (It will not compile until Step 4.)

- [ ] **Step 2: Write the failing tests**

Append to `internal/cli/tree_test.go` (add `"strings"` to its imports):

```go
const clearLine = "\r\x1b[K"

func TestBuildTreeShowsProgressOnATerminal(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	h.tty = true
	require.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stderr.String(), clearLine+"reading builds")
	assert.True(t, strings.HasSuffix(h.stderr.String(), clearLine), "the line is cleared: %q", h.stderr.String())
	assert.NotContains(t, h.stdout.String(), "\x1b[K", "stdout carries only the tree")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9")
}

func TestBuildTreeShowsNoProgressOnAPipeOrWithJSON(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	require.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.NotContains(t, h.stderr.String(), "\x1b[K")

	h.tty = true
	require.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--json"))
	assert.NotContains(t, h.stderr.String(), "\x1b[K")

	h.vars["TERM"] = "dumb"
	require.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.NotContains(t, h.stderr.String(), "\x1b[K")
}

func TestBuildTreeWatchShowsProgressOnStderr(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.tty = true
	assert.Equal(t, 1, h.run("build", "tree", "PROJ-BUILD-482", "--watch"))
	assert.Contains(t, h.stderr.String(), clearLine+"reading")
	assert.NotContains(t, h.stdout.String(), clearLine+"reading", "progress is not drawn into the tree")
}

func TestAnInterruptedWatchClearsItsProgressFirst(t *testing.T) {
	h := treeHarness(t, provider.StateRunning)
	h.tty = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, 130, h.runCtx(ctx, "build", "tree", "PROJ-BUILD-482", "--watch"))
	stderr := h.stderr.String()
	i := strings.Index(stderr, "still running")
	require.GreaterOrEqual(t, i, 0)
	if j := strings.LastIndex(stderr[:i], "\r"); j >= 0 {
		assert.Equal(t, clearLine, stderr[j:i], "a shown line is cleared before the note")
	}
}
```

(`newHarness` always sets `h.vars`, with `TERM=xterm-256color`.) The interrupt test is a wiring check: with an already-cancelled context the watch may end before any progress shows, so `TestTreeLiveStopClearsAShownLine` (Task 3) is what pins the clear itself.

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'Progress|InterruptedWatch' -v`
Expected: build failure: `env.StderrTTY undefined`.

- [ ] **Step 4: Add `StderrTTY`**

In `internal/cli/env.go`, add `StderrTTY bool` after `StdoutTTY bool` in `Env`, and in `SystemEnv` after `StdoutTTY: term.IsTerminal(out),` add:

```go
		StderrTTY: term.IsTerminal(int(os.Stderr.Fd())),
```

- [ ] **Step 5: Wire progress into the tree command**

In `internal/cli/tree.go`, add:

```go
// progressOn reports whether a slow read may draw a progress line on
// stderr: both outputs are terminals and the output is not JSON.
func (r *runtime) progressOn() bool {
	return r.env.StdoutTTY && r.env.StderrTTY && !r.flags.json && r.env.Getenv("TERM") != "dumb"
}

// treeProgress is the line a tree load reports on, or nil when progressOn
// is false.
func (r *runtime) treeProgress() *view.ProgressLine {
	if !r.progressOn() {
		return nil
	}
	return view.NewProgressLine(r.env.Stderr, r.env.Width())
}

// loadTree reads key's tree, showing on a terminal what it is reading, and
// clears that line before returning.
func (r *runtime) loadTree(ctx context.Context, svc *app.Service, key string, o app.TreeOptions) (app.Tree, error) {
	if pl := r.treeProgress(); pl != nil {
		o.OnProgress = func(p app.TreeProgress) { pl.Show(view.TreeProgressText(p)) }
		defer pl.Clear()
	}
	return svc.BuildTree(ctx, key, o)
}
```

In `newBuildTreeCmd`'s `RunE`, change `t, err := svc.BuildTree(ctx, key, o)` to `t, err := r.loadTree(ctx, svc, key, o)`. In `rerunTree`, change `t, err := svc.BuildTree(ctx, key, app.TreeOptions{})` to `t, err := r.loadTree(ctx, svc, key, app.TreeOptions{})`.

In `treeLoop`, replace the renderer setup and the end of the function:

```go
	var rend view.TreeRenderer
	var live *view.TreeLive
	if r.flags.json {
		rend = view.NewTreeNDJSON(view.Out{W: r.env.Stdout}, failed)
	} else {
		out, err := r.out()
		if err != nil {
			return err
		}
		if out.TTY {
			var progress io.Writer
			if r.progressOn() {
				progress = r.env.Stderr
			}
			live = view.NewTreeLive(out, progress, failed)
			rend = live
		} else {
			rend = view.NewTreeLines(out, failed)
		}
	}
	for e := range svc.WatchTree(ctx, key, o) {
		rend.Event(e)
		switch e.Type {
		case app.TreeDone:
			view.PrintWarnings(r.env.Stderr, e.Tree.Warnings)
			return r.treeResult(e.Tree, key)
		case app.TreeError:
			return e.Err
		}
	}
	live.Stop()
	fmt.Fprintf(r.env.Stderr, "still running: bam build tree %s --watch\n", key)
	return ctx.Err()
```

Add `"io"` to the imports. Add this sentence as the last line of the command's `Long` text (before the closing backtick), on its own line after "Interrupting --watch never stops a build.":

```
On a terminal, bam shows on stderr what it is reading while the tree loads.
```

- [ ] **Step 6: Run the tests and regenerate the docs**

Run: `go test ./internal/cli/`
Expected: PASS. Existing tests with `h.tty = true` on `build tree` (e.g. `TestRerunFailedNeedsYesWithoutATerminal`) only use `Contains`; if one compares stderr exactly, it now sees progress — assert on the part it meant instead and name it in the commit body.

Run: `make docs && git status --short docs/cli`
Expected: the `bam_build_tree` page changed by the new sentence only.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/env.go internal/cli/tree.go internal/cli/harness_test.go internal/cli/tree_test.go docs/cli
git commit -m "feat: show what bam build tree is reading while it loads

Refs #81"
```

---

### Task 5: The TUI tree screen shows progress until its first tree

**Files:**
- Modify: `internal/view/tui/treescreen.go`, `internal/view/tui/treescreen_test.go`

**Interfaces:**
- Consumes: `app.TreeLoading`, `app.TreeEvent.Progress`, `view.TreeProgressText` (Tasks 2–3)
- Produces: `treeScreen.progress app.TreeProgress`

- [ ] **Step 1: Make `firstTree` skip progress, and write the failing test**

In `internal/view/tui/treescreen_test.go`, replace `firstTree`:

```go
// firstTree opens the screen with t and delivers events until the first
// tree; the progress before it is delivered too.
func firstTree(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	for !m.buildTree.have {
		require.NotNil(t, cmd)
		m, cmd = send(m, cmd())
	}
	return m
}
```

Append:

```go
func TestTreeScreenShowsProgressUntilTheFirstTree(t *testing.T) {
	m, _ := treeTUIModel(t)
	m, _ = send(m, mkKey("t"))
	loading := func(gen, done int) treeEventMsg {
		return treeEventMsg{Gen: gen, Event: app.TreeEvent{Type: app.TreeLoading,
			Progress: app.TreeProgress{Plans: app.TreeCount{Done: done, Total: 40}}}}
	}
	m, cmd := send(m, loading(m.treeGen, 12))
	require.NotNil(t, cmd, "the screen keeps reading the watch")
	require.False(t, m.buildTree.have)
	require.Contains(t, m.View(), "PROJ-BUILD-44 · reading builds 12/40 plans")

	m, _ = send(m, loading(m.treeGen-1, 39))
	require.Contains(t, m.View(), "12/40", "a stale watch's progress is dropped")
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/view/tui/ -run 'TreeScreenShowsProgress' -v`
Expected: FAIL: the header reads `PROJ-BUILD-44 · loading` and `cmd` is nil for an unhandled event type.

- [ ] **Step 3: Implement**

In `internal/view/tui/treescreen.go`, add to `treeScreen` after `have bool`:

```go
	progress   app.TreeProgress // how far the first refresh got, until have
```

In `handleTreeEvent`, add before `case app.TreeUpdate, app.TreeDone:`:

```go
	case app.TreeLoading:
		m.buildTree.progress = e.Progress
		return m, treeCmd(m.treeCh, m.treeGen)
```

In `treeScreenView`, replace `head += " · loading"` with:

```go
		head += " · " + view.TreeProgressText(m.buildTree.progress)
```

- [ ] **Step 4: Run the TUI tests**

Run: `go test ./internal/view/tui/`
Expected: PASS. If a golden captured a loading tree screen, it now shows `reading the tree` instead of `loading`; regenerate with `go test ./internal/view/tui/ -update`, review the diff (only that word may change), and commit the golden.

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui/treescreen.go internal/view/tui/treescreen_test.go internal/view/tui/testdata
git commit -m "feat: show tree load progress on the terminal UI's tree screen

Refs #81"
```

---

### Task 6: The plan screen's Tree panel shows progress while it reads

**Files:**
- Modify: `internal/view/tui/msgs.go`, `internal/view/tui/planscreen.go`, `internal/view/tui/model.go`, `internal/view/tui/planscreen_test.go`

**Interfaces:**
- Consumes: `app.TreeOptions.OnProgress`, `view.TreeProgressText` (Tasks 1, 3)
- Produces: `type treePreviewProgressMsg struct{ Gen int; Key string; Progress app.TreeProgress; next tea.Cmd }`; `func treePreviewProgress(ch <-chan app.TreeProgress, key string, gen int) tea.Cmd`; `(Model).treePreviewProgressed(msg treePreviewProgressMsg) (Model, tea.Cmd)`; `treePreview.progress app.TreeProgress`

Design note: the read starts when the command is built (in `Update`), and the command is a batch of a result command and a progress command. The result still arrives as the existing `treePreviewMsg` from its own command, so `planWithTree` and every existing test that looks for it in `runBatch` keeps working; the progress command delivers at most the latest snapshot and re-arms until the read ends.

- [ ] **Step 1: Write the failing tests**

Append to `internal/view/tui/planscreen_test.go` (add `"context"`, `tea "github.com/charmbracelet/bubbletea"` and `"github.com/r0jjames/bam-cli/internal/app"` to imports if missing):

```go
func noMsg() tea.Msg { return nil }

func TestTreePanelShowsProgressWhileReading(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	m, _ = send(m, mkKey("r"))
	require.True(t, m.plan.tree.loading)
	m, cmd := send(m, treePreviewProgressMsg{Gen: m.treePreviewGen, Key: m.plan.tree.key,
		Progress: app.TreeProgress{Plans: app.TreeCount{Done: 3, Total: 4}}, next: noMsg})
	require.NotNil(t, cmd, "it keeps listening")
	require.Contains(t, m.treeBody(80, 10), "reading the tree… reading builds 3/4 plans")
}

func TestAStaleTreeProgressIsDropped(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	m, _ = send(m, mkKey("r"))
	p := app.TreeProgress{Plans: app.TreeCount{Done: 3, Total: 4}}
	m, cmd := send(m, treePreviewProgressMsg{Gen: m.treePreviewGen - 1, Key: m.plan.tree.key, Progress: p, next: noMsg})
	require.Nil(t, cmd)
	m, cmd = send(m, treePreviewProgressMsg{Gen: m.treePreviewGen, Key: "PROJ-BUILD-1", Progress: p, next: noMsg})
	require.Nil(t, cmd)
	require.Equal(t, app.TreeProgress{}, m.plan.tree.progress)
}

func TestTreePreviewCmdDeliversItsResultAndProgress(t *testing.T) {
	m, _ := treeTUIModel(t)
	var result *treePreviewMsg
	var prog *treePreviewProgressMsg
	for _, msg := range runBatch(treePreviewCmd(context.Background(), m.svc, "PROJ-BUILD-44", 7)) {
		switch v := msg.(type) {
		case treePreviewMsg:
			result = &v
		case treePreviewProgressMsg:
			prog = &v
		}
	}
	require.NotNil(t, result)
	require.NoError(t, result.Err)
	require.NotNil(t, prog, "the read's last snapshot is waiting")
	require.Equal(t, 7, prog.Gen)
	require.Positive(t, prog.Progress.Plans.Total)
	require.Nil(t, prog.next(), "the progress ends with the read")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/tui/ -run 'TreePanelShowsProgress|StaleTreeProgress|TreePreviewCmdDelivers' -v`
Expected: build failure: `undefined: treePreviewProgressMsg`.

- [ ] **Step 3: Implement the command**

In `internal/view/tui/msgs.go`, replace `treePreviewCmd` and add the message and progress command:

```go
// treePreviewProgressMsg is how far the Tree panel's read of Key has got;
// next waits for the snapshot after it.
type treePreviewProgressMsg struct {
	Gen      int
	Key      string
	Progress app.TreeProgress
	next     tea.Cmd
}

// treePreviewCmd reads what build key triggered, one level down. It is read
// once, not watched: the plan screen reads it again on r and when the open
// build finishes. The read starts at once; its result comes from the first
// command of the batch and its progress from the second, which keeps only
// the newest snapshot and ends with the read.
func treePreviewCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd {
	progress := make(chan app.TreeProgress, 1)
	result := make(chan treePreviewMsg, 1)
	go func() {
		defer close(progress)
		t, err := svc.BuildTree(ctx, key, app.TreeOptions{Depth: 1, OnProgress: func(p app.TreeProgress) {
			// Calls are serialized, so after the drain there is room.
			select {
			case <-progress:
			default:
			}
			progress <- p
		}})
		result <- treePreviewMsg{Gen: gen, Key: key, Tree: t, Err: err}
	}()
	return tea.Batch(func() tea.Msg { return <-result }, treePreviewProgress(progress, key, gen))
}

// treePreviewProgress waits for the next snapshot of a Tree panel read; it
// returns nil once the read has ended.
func treePreviewProgress(ch <-chan app.TreeProgress, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return treePreviewProgressMsg{Gen: gen, Key: key, Progress: p, next: treePreviewProgress(ch, key, gen)}
	}
}
```

- [ ] **Step 4: Handle the message and draw it**

In `internal/view/tui/planscreen.go`, add to `treePreview` after `loading bool`:

```go
	progress app.TreeProgress // how far the read got, while loading
```

Add after `treePreviewLoaded`:

```go
// treePreviewProgressed shows how far the Tree panel's read has got, if it
// is still this panel's read.
func (m Model) treePreviewProgressed(msg treePreviewProgressMsg) (Model, tea.Cmd) {
	if msg.Gen != m.treePreviewGen || m.plan.key == "" || msg.Key != m.plan.tree.key || !m.plan.tree.loading {
		return m, nil
	}
	m.plan.tree.progress = msg.Progress
	return m, msg.next
}
```

In `treeBody`, replace

```go
	case t.loading:
		return dimStyle.Render("reading the tree…")
```

with

```go
	case t.loading:
		text := "reading the tree…"
		if t.progress != (app.TreeProgress{}) {
			text += " " + view.TreeProgressText(t.progress)
		}
		return dimStyle.Render(cut(text, width-4))
```

(add the `view` import to `planscreen.go` if missing).

In `internal/view/tui/model.go`, after the `case treePreviewMsg:` branch add:

```go
	case treePreviewProgressMsg:
		return m.treePreviewProgressed(msg)
```

- [ ] **Step 5: Run the TUI tests with the race detector**

Run: `go test -race ./internal/view/tui/`
Expected: PASS, goldens unchanged (a loading panel with no progress still reads `reading the tree…`).

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui/msgs.go internal/view/tui/planscreen.go internal/view/tui/model.go internal/view/tui/planscreen_test.go
git commit -m "feat: show tree load progress in the plan screen's Tree panel

Refs #81"
```

---

### Task 7: Changelog, spec status, full verification

**Files:**
- Modify: `CHANGELOG.md`, `docs/superpowers/specs/2026-10-12-bam-tree-load-progress-design.md`

- [ ] **Step 1: Changelog**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md`, add as the last bullet:

```markdown
- `bam build tree` and the terminal UI's tree views show what bam is reading while a tree loads.
```

- [ ] **Step 2: Spec status**

In the spec's header, change `**Status:** Draft` to `**Status:** Implemented`.

- [ ] **Step 3: Full checks**

Run, in order, each expected to pass with no output beyond the usual:

```bash
go test -race ./...
make lint
make check-fixtures
make docs && git diff --exit-code docs/cli
```

- [ ] **Step 4: Try it by hand (optional, needs a configured Bamboo)**

```bash
make build && ./bin/bam build tree <a deep build key>
```

Expected: one stderr line counting up (`listing plans …`, `reading builds …`, `reading details …`, `reading logs …`), gone before the tree prints; `./bin/bam build tree <key> | cat` and `--json` show no progress.

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/superpowers/specs/2026-10-12-bam-tree-load-progress-design.md
git commit -m "docs: note tree load progress in the changelog

Refs #81"
```

- [ ] **Step 6: Open the pull request**

Per `CLAUDE.md`: push the branch and open a PR against `main` whose body says `Closes #81`, with no Claude attribution lines.
