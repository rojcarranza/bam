# Build Tree Rerun and Tree Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rerun a build tree's topmost failed builds from `bam build tree --rerun-failed`, `bam build rerun`, and a new terminal UI tree screen; only the newest run of a child plan counts toward the tree.

**Architecture:** `app` marks older sibling runs `Superseded`, picks `RerunCandidates`, and restarts them with the existing `Service.Restart` in `RerunTree`. `view` renders superseded runs and the confirmation list. `cli` adds the flags, the `[y/N]` question and `bam build rerun`. `view/tui` adds `screenTree`, fed by `WatchTree`, with `t`, `:tree` and `F`.

**Tech Stack:** Go, cobra, bubbletea, testify. No new dependency.

**Spec:** `docs/superpowers/specs/2026-10-09-bam-build-tree-rerun-design.md`

## Global Constraints

- No new dependency; no change to `provider` or `provider/bamboo`.
- `app` never prints or reads input (`TestAppStaysUIReady`); the question lives in `cli`.
- Candidates: state `failed` or `stopped`, not superseded, not a loop, not `not run`, every ancestor `success` or `skipped`; nothing below a candidate; tree order.
- Restarts are sequential, through `Service.Restart`; a failed restart does not stop the others.
- CLI exit codes: no candidate → `nothing to rerun`, exit by the tree's state; a running tree with candidates → exit 2; no terminal or `--json` without `--yes` → exit 2 before any request that restarts; an answer other than `y`/`yes` → `nothing restarted`, exit 0; a failed restart → exit 5 after following (or at once with `--no-watch`).
- `bam build rerun`: running or succeeded → exit 2 `… nothing to rerun`; no question.
- JSON: `status` gains `superseded`; nothing renamed or removed.
- TUI: `t` is new; `F` keeps its meaning; `TestNoKeyIsBoundTwice` stays green; the tree screen's key line fits 80 columns.
- Placeholders only (`PROJ`, `OPS`, `LAB`, `jdoe`, `bamboo.example.com`, `bamboo.lab.example`).
- Before each commit: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs`, chained with `&&`; commit `docs/cli/` with the task that changed it.
- Commits: Conventional Commits, the owner's identity only, no AI attribution lines.

## Review Focus

1. **A superseded run's own children** (the old `OPS-SMOKE-51` had children): they must not count toward the tree or be rerun, though JSON keeps them. (Task 1 `TestSupersededSubtreeDoesNotCount`.)
2. **A candidate whose restart Bamboo refuses as running** (it started again meanwhile): reported as "was not restarted", not an error, and following goes on. (Task 3 `TestRerunFailedReportsABuildThatWasNotRestarted`.)
3. **End of input on the question** (stdin closed, e.g. `</dev/null` with a TTY flag): `nothing restarted`, exit 0, no restart. (Task 3 `TestRerunFailedEndOfInputRestartsNothing`.)
4. **`F` pressed twice quickly in the TUI**: the second press while restarting must not restart again. (Task 6 `TestTreeScreenRerunIgnoresASecondF`.)
5. **Closing the tree screen while a rerun is in flight**: the late result must not reopen the screen. (Task 6 `TestTreeScreenLateRerunResultIsDropped`.)

---

### Task 1: Superseded runs, candidates and `RerunTree` in `app`

**Files:**
- Modify: `internal/app/tree.go` (`TreeNode.Superseded`, `treeGrow`, `TreeState`)
- Create: `internal/app/rerun.go`
- Test: `internal/app/rerun_test.go`

**Interfaces:**
- Consumes: `Tree`, `TreeNode`, `TreeFinished`, `Service.Restart(ctx, key) (provider.Build, bool, error)`; test helpers `treeFake`, `treeService`, `tb`, `childOf`, `shape` from `tree_test.go`, `restartable` from `sets_test.go`.
- Produces:
  - `TreeNode.Superseded bool`
  - `func RerunCandidates(t Tree) []*TreeNode`
  - `type RerunResult struct { Key string; Restarted bool; Err error }`
  - `func (s *Service) RerunTree(ctx context.Context, cands []*TreeNode) []RerunResult`

- [ ] **Step 1: Write the failing tests** — `internal/app/rerun_test.go`

```go
package app

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func keysOf(ns []*TreeNode) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Build.Key)
	}
	return out
}

func TestOlderRunsOfAChildAreSuperseded(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSuccess
	p.History["OPS-SMOKE"] = []provider.Build{
		tb("OPS-SMOKE-52", childOf("OPS-DEPLOY-88"), provider.StateSuccess, 20),
		tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9),
	}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	smokes := tr.Root.Children[0].Children
	require.Equal(t, []string{"OPS-SMOKE-52", "OPS-SMOKE-51"}, keysOf(smokes))
	require.False(t, smokes[0].Superseded)
	require.True(t, smokes[1].Superseded)
	require.Equal(t, provider.StateSuccess, tr.State, "the earlier failure no longer fails the tree")
}

func TestSupersededSubtreeDoesNotCount(t *testing.T) {
	old := &TreeNode{PlanKey: "OPS-SMOKE", Build: provider.Build{Key: "OPS-SMOKE-51", State: provider.StateFailed}, Superseded: true,
		Children: []*TreeNode{{PlanKey: "OPS-AUDIT", Build: provider.Build{Key: "OPS-AUDIT-3", State: provider.StateRunning}}}}
	root := &TreeNode{PlanKey: "PROJ-API", Build: provider.Build{Key: "PROJ-API-142", State: provider.StateSuccess}, Children: []*TreeNode{old}}
	require.Equal(t, provider.StateSuccess, TreeState(root))
	require.Empty(t, RerunCandidates(Tree{Root: root}))
}

func TestRerunCandidatesAreTheTopmostFailures(t *testing.T) {
	st := func(key string, s provider.State, kids ...*TreeNode) *TreeNode {
		return &TreeNode{PlanKey: key, Build: provider.Build{Key: key + "-1", State: s}, Children: kids}
	}
	root := st("PROJ-API", provider.StateSuccess,
		st("OPS-DEPLOY", provider.StateFailed, st("OPS-SMOKE", provider.StateFailed)),
		st("PROJ-DOCS", provider.StateStopped),
		st("PROJ-LINT", provider.StateSkipped, st("PROJ-FMT", provider.StateFailed)),
		st("PROJ-SLOW", provider.StateRunning, st("PROJ-SLOWER", provider.StateFailed)),
		&TreeNode{PlanKey: "OPS-AUDIT", NotRun: true},
	)
	loop := st("PROJ-LOOP", provider.StateFailed)
	loop.Loop = true
	root.Children = append(root.Children, loop)
	require.Equal(t, []string{"OPS-DEPLOY-1", "PROJ-DOCS-1", "PROJ-FMT-1"}, keysOf(RerunCandidates(Tree{Root: root})),
		"below a failure, below a running build, not-run and loop nodes are left out; a skipped parent is fine")

	failedRoot := st("PROJ-API", provider.StateFailed, st("OPS-DEPLOY", provider.StateFailed))
	require.Equal(t, []string{"PROJ-API-1"}, keysOf(RerunCandidates(Tree{Root: failedRoot})), "the root can be a candidate")
}

func TestRerunTreeRestartsInOrderAndGoesOnAfterAnError(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{}
	restartable(p, "OPS-DEPLOY-88", provider.StateSuccess)
	restartable(p, "PROJ-DOCS-30", provider.StateSuccess)
	p.RestartErrs = map[string]error{"OPS-DEPLOY-88": errs.Bamboof("Bamboo answered 409")}
	p.Sequences["OPS-SMOKE-51"] = []provider.Build{{Key: "OPS-SMOKE-51", State: provider.StateRunning}}
	svc := treeService(t, p)
	cands := []*TreeNode{
		{Build: provider.Build{Key: "OPS-DEPLOY-88"}},
		{Build: provider.Build{Key: "OPS-SMOKE-51"}},
		{Build: provider.Build{Key: "PROJ-DOCS-30"}},
	}
	res := svc.RerunTree(context.Background(), cands)
	require.Len(t, res, 3)
	require.Equal(t, "OPS-DEPLOY-88", res[0].Key)
	require.Error(t, res[0].Err)
	require.False(t, res[1].Restarted, "running: nothing to restart")
	require.NoError(t, res[1].Err)
	require.True(t, res[2].Restarted)
	require.Equal(t, []string{"OPS-DEPLOY-88", "PROJ-DOCS-30"}, p.Restarted, "the refused one was still asked")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'Superseded|RerunCandidates|RerunTree' 2>&1 | tail -3`
Expected: build failure — `unknown field Superseded`, `undefined: RerunCandidates`.

- [ ] **Step 3: Implement**

`internal/app/tree.go`:

In `TreeNode`, after `Loop`: `Superseded bool // an older run of the same child plan; it does not count toward the tree`.

In `treeGrow`, after `sort.SliceStable(...)` and before `n.Children = kids`:

```go
	// The newest run of a child plan is current; older ones are superseded.
	for i := 1; i < len(kids); i++ {
		if !kids[i].NotRun && kids[i].PlanKey == kids[i-1].PlanKey {
			kids[i].Superseded = true
		}
	}
```

Replace `TreeState`'s body walk so a superseded node and its subtree are skipped:

```go
func TreeState(root *TreeNode) provider.State {
	running, bad := false, false
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.Superseded {
			return
		}
		switch {
		case n.NotRun:
			bad = true
		case !TreeFinished(n.Build.State):
			running = true
		case n.Build.State != provider.StateSuccess && n.Build.State != provider.StateSkipped:
			bad = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	switch {
	case running:
		return provider.StateRunning
	case bad:
		return provider.StateFailed
	}
	return provider.StateSuccess
}
```

`internal/app/rerun.go`:

```go
package app

import (
	"context"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// RerunCandidates is the topmost failed or stopped builds of a tree: each
// such build whose ancestors all succeeded (or were skipped), in tree order.
// When one succeeds again Bamboo triggers its children itself, so nothing
// below a candidate is one. Not-run, loop and superseded nodes never are.
func RerunCandidates(t Tree) []*TreeNode {
	var out []*TreeNode
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.NotRun || n.Loop || n.Superseded {
			return
		}
		switch n.Build.State {
		case provider.StateFailed, provider.StateStopped:
			out = append(out, n)
		case provider.StateSuccess, provider.StateSkipped:
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	if t.Root != nil {
		walk(t.Root)
	}
	return out
}

// RerunResult is what happened to one candidate.
type RerunResult struct {
	Key       string
	Restarted bool // false: it was running or had succeeded, so nothing was restarted
	Err       error
}

// RerunTree restarts each candidate's failed jobs with Restart, one at a
// time, in order. A failed restart does not stop the others; ending ctx does.
func (s *Service) RerunTree(ctx context.Context, cands []*TreeNode) []RerunResult {
	out := make([]RerunResult, 0, len(cands))
	for _, n := range cands {
		if ctx.Err() != nil {
			break
		}
		_, restarted, err := s.Restart(ctx, n.Build.Key)
		out = append(out, RerunResult{Key: n.Build.Key, Restarted: restarted, Err: err})
	}
	return out
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test -race ./internal/app/ -run 'Superseded|RerunCandidates|RerunTree|Tree|ParentOf' -v 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `ok`, every existing tree test still passing.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/app/
git commit -m "feat: older runs of a child are superseded; pick and restart a tree's failures"
```

---

### Task 2: Superseded runs and the rerun list in `view`

**Files:**
- Modify: `internal/view/tree.go` (`treeBad`, `pruneFailed`, `TreeText`, `treeRow`, `treeNodeDoc`)
- Create: `internal/view/rerun.go`
- Test: `internal/view/rerun_test.go`, golden `internal/view/testdata/tree_superseded.golden`

**Interfaces:**
- Consumes: `app.TreeNode.Superseded` (Task 1); `tnode`, `stage`, `testOut`, `golden` test helpers.
- Produces: `func RerunList(o Out, cands []*app.TreeNode) error`.

- [ ] **Step 1: Write the failing tests** — `internal/view/rerun_test.go`

```go
package view

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func supersededTree() app.Tree {
	newer := tnode("OPS-SMOKE-52", provider.StateSuccess, time.Minute)
	older := tnode("OPS-SMOKE-51", provider.StateFailed, 100*time.Second, tnode("OPS-AUDIT-3", provider.StateFailed, time.Second))
	older.Superseded = true
	older.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, 100*time.Second, "Smoke tests")}
	older.Build.FailedTests = []string{"a"}
	root := tnode("OPS-DEPLOY-88", provider.StateSuccess, 3*time.Minute, newer, older)
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTextSupersededRun(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, supersededTree(), false))
	out := buf.String()
	require.Contains(t, out, "earlier run")
	require.NotContains(t, out, "stage Smoke", "an earlier run's stages are not shown")
	require.NotContains(t, out, "OPS-AUDIT-3", "nor its children")
	require.NotContains(t, out, "next: bam logs OPS-SMOKE-51", "nor a next line")
	golden(t, "tree_superseded", out)
}

func TestTreeTextFailedOnlyDropsSupersededRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, supersededTree(), true))
	require.Equal(t, 1, strings.Count(buf.String(), "\n"), "nothing current failed: the root line only")
}

func TestTreeJSONSupersededStatus(t *testing.T) {
	data, err := json.Marshal(TreeJSON(supersededTree(), false))
	require.NoError(t, err)
	var doc struct {
		Root struct {
			Children []struct {
				Status   string         `json:"status"`
				Build    map[string]any `json:"build"`
				Children []any          `json:"children"`
			} `json:"children"`
		} `json:"root"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	old := doc.Root.Children[1]
	require.Equal(t, "superseded", old.Status)
	require.Equal(t, "OPS-SMOKE-51", old.Build["key"])
	require.Len(t, old.Children, 1, "its children are kept")
}

func TestRerunList(t *testing.T) {
	deploy := tnode("OPS-DEPLOY-88", provider.StateFailed, time.Minute)
	deploy.Build.Stages = []provider.Stage{stage("Deploy", provider.StateFailed, time.Minute, "Helm upgrade")}
	docs := tnode("PROJ-DOCS-30", provider.StateStopped, time.Minute)
	o, buf := testOut(false)
	require.NoError(t, RerunList(o, []*app.TreeNode{deploy, docs}))
	require.Equal(t, [][]string{
		{"Rerun", "the", "failed", "jobs", "of", "2", "builds?"},
		{"OPS-DEPLOY-88", "✗", "failed", "Deploy", "›", "Helm", "upgrade"},
		{"PROJ-DOCS-30", "⊘", "stopped"},
	}, fields(buf.String()))
	o, buf = testOut(false)
	require.NoError(t, RerunList(o, []*app.TreeNode{docs}))
	require.True(t, strings.HasPrefix(buf.String(), "Rerun the failed jobs of 1 build?\n"))
}
```

Also add, at the end of `rerun_test.go`:

```go
// fields is each line of s split on whitespace.
func fields(s string) [][]string {
	var out [][]string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		out = append(out, strings.Fields(l))
	}
	return out
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/ -run 'Superseded|RerunList' 2>&1 | tail -3`
Expected: build failure — `undefined: RerunList`.

- [ ] **Step 3: Implement**

`internal/view/tree.go`:

- `pruneFailed`: skip superseded children before keeping one:

```go
	for _, k := range n.Children {
		if k.Superseded {
			continue
		}
		if p := pruneFailed(k); treeBad(k) || len(p.Children) > 0 {
			c.Children = append(c.Children, p)
		}
	}
```

- In `TreeText`'s `add`, stages are added only `if stages && !n.NotRun && !n.Loop && !n.Superseded`; after appending a child's row, skip its `next` line and its recursion when it is superseded:

```go
			tb.Rows = append(tb.Rows, treeRow(o, it.node, indent+branch))
			if it.node.Superseded {
				continue
			}
```

  (the existing `next` append and `add(it.node, …)` follow this check).

- `treeRow`: a new first case:

```go
	case n.Superseded:
		return []string{prefix + style.Dim(o.Style, n.Build.Key), style.Dim(o.Style, style.Glyph(n.Build.State)+" "+style.Label(n.Build.State)),
			style.Dim(o.Style, Duration(n.Build.Duration)), style.Dim(o.Style, "earlier run")}
```

- `treeNodeDoc`: after the `NotRun` and `Loop` cases, `case n.Superseded: d.Status = "superseded"` (the build and children are kept as for a current node).

`internal/view/rerun.go`:

```go
package view

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
)

// RerunList is the question bam build tree --rerun-failed asks: what it
// will restart, with the first failed stage and job of each.
func RerunList(o Out, cands []*app.TreeNode) error {
	noun := "builds"
	if len(cands) == 1 {
		noun = "build"
	}
	if _, err := fmt.Fprintf(o.W, "Rerun the failed jobs of %d %s?\n", len(cands), noun); err != nil {
		return err
	}
	tb := Table{Indent: "  ", Headers: []string{"", "", ""}}
	for _, n := range cands {
		where := ""
		for _, s := range n.Build.Stages {
			for _, j := range s.Jobs {
				if where == "" && j.State == n.Build.State {
					where = s.Name + " › " + j.Name
				}
			}
		}
		tb.Rows = append(tb.Rows, []string{o.Key(n.Build.Key, n.Build.URL), o.State(n.Build.State), where})
	}
	return RenderRows(o, tb)
}
```

- [ ] **Step 4: Write the golden and review it**

Run: `go test ./internal/view/ -run TestTreeTextSupersededRun -update && cat internal/view/testdata/tree_superseded.golden`
Expected: `OPS-DEPLOY-88 ✓ success 3m00s`, `├─ OPS-SMOKE-52 ✓ success 1m00s`, `└─ OPS-SMOKE-51 ✗ failed 1m40s earlier run`; no stage or child under it; no `next:` line.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/view/ -v -run 'Tree|Rerun' 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `ok`.

- [ ] **Step 6: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/view/
git commit -m "feat: show earlier runs in a tree and the list a rerun asks about"
```

---

### Task 3: `bam build tree --rerun-failed`

**Files:**
- Modify: `internal/cli/tree.go`
- Test: `internal/cli/rerun_test.go`
- Regenerated: `docs/cli/`

**Interfaces:**
- Consumes: `app.RerunCandidates`, `Service.RerunTree`, `app.RerunResult` (Task 1); `view.RerunList` (Task 2); `treeHarness`, `runtime.treeLoop`, `runtime.treeResult` (existing).

- [ ] **Step 1: Write the failing tests** — `internal/cli/rerun_test.go`

```go
package cli

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rerunnable makes PROJ-PROV-9 restart and then end in final: read failed
// by the tree and by Restart, then queued, then final.
func rerunnable(h *harness, final provider.State) {
	child := h.fake.History["PROJ-PROV"][0]
	queued, done := child, child
	queued.State, done.State = provider.StateQueued, final
	done.Stages = nil
	h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV-9": {child, child, queued, done}}
}

func TestRerunFailedWithYesRestartsAndFollows(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
	assert.Contains(t, h.stderr.String(), "restarted PROJ-PROV-9")
	assert.Contains(t, h.stdout.String(), "PROJ-PROV-9 success")
}

func TestRerunFailedAsksOnATerminal(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	h.tty, h.stdin = true, "y\n"
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "Rerun the failed jobs of 1 build?")
	assert.Contains(t, h.stderr.String(), "Apply › Terraform")
	assert.Contains(t, h.stderr.String(), "[y/N]")
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
}

func TestRerunFailedNoRestartsNothing(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.tty, h.stdin = true, "n\n"
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "nothing restarted")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedEndOfInputRestartsNothing(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	h.tty, h.stdin = true, ""
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "nothing restarted")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNeedsYesWithoutATerminal(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed"))
	assert.Contains(t, h.stderr.String(), "--rerun-failed needs --yes when it cannot ask")
	assert.Empty(t, h.fake.Restarted)
	h.tty = true
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--json"))
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedRefusesARunningTree(t *testing.T) {
	h := treeHarness(t, provider.StateFailed) // root succeeded, PROJ-PROV-9 failed
	// A running child of another plan: a second PROJ-PROV run would
	// supersede PROJ-PROV-9 and leave no candidate.
	h.fake.History["PROJ-BUILD"] = append(h.fake.History["PROJ-BUILD"], provider.Build{Key: "PROJ-BUILD-483", PlanKey: "PROJ-BUILD",
		Number: 483, State: provider.StateRunning, Reason: "Child of PROJ-BUILD-482"})
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "tree is still running")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNothingToRerun(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "nothing to rerun")
	assert.Empty(t, h.fake.Restarted)
}

func TestRerunFailedNoWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes", "--no-watch"))
	assert.Contains(t, h.stderr.String(), "restarted PROJ-PROV-9")
	assert.NotContains(t, h.stdout.String(), "PROJ-PROV-9 success", "nothing is followed")
}

func TestRerunFailedRestartErrorExits5(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	h.fake.RestartErrs = map[string]error{"PROJ-PROV-9": errs.Bamboof("Bamboo answered 409")}
	assert.Equal(t, 5, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 was not restarted: Bamboo answered 409")
}

func TestRerunFailedReportsABuildThatWasNotRestarted(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	child := h.fake.History["PROJ-PROV"][0]
	running := child
	running.State = provider.StateRunning
	h.fake.Sequences = map[string][]provider.Build{"PROJ-PROV-9": {child, running, running}}
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--rerun-failed", "--yes", "--no-watch"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 was not restarted: it is running or has succeeded")
	require.Empty(t, h.fake.Restarted)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run RerunFailed 2>&1 | grep -E '^(--- |ok|FAIL)' | head`
Expected: FAIL — `unknown flag: --rerun-failed`.

- [ ] **Step 3: Implement** — in `internal/cli/tree.go`:

Add flags and the check at the top of `RunE`:

```go
	var rerun, yes, noWatch bool
```

```go
			if rerun && !yes && (!r.env.StdinTTY || r.flags.json) {
				return errs.Usagef("--rerun-failed needs --yes when it cannot ask").WithTry("add --yes")
			}
```

After `o := app.TreeOptions{Depth: depth}`:

```go
			if rerun {
				return r.rerunTree(ctx, svc, key, failed, yes, noWatch)
			}
```

```go
	f.BoolVar(&rerun, "rerun-failed", false, "restart the failed builds whose parents succeeded, then follow the tree")
	f.BoolVar(&yes, "yes", false, "with --rerun-failed, do not ask first")
	f.BoolVar(&noWatch, "no-watch", false, "with --rerun-failed, restart and exit without following")
```

Add:

```go
// rerunTree restarts the topmost failed builds of key's tree, after asking,
// then follows the tree unless noWatch.
func (r *runtime) rerunTree(ctx context.Context, svc *app.Service, key string, failed, yes, noWatch bool) error {
	t, err := svc.BuildTree(ctx, key, app.TreeOptions{})
	if err != nil {
		return err
	}
	cands := app.RerunCandidates(t)
	if len(cands) == 0 {
		r.note("nothing to rerun", "")
		return r.treeResult(t, key)
	}
	if t.State == provider.StateRunning {
		return errs.Usagef("%s's tree is still running; rerun when it has finished", key).
			WithTry("bam build tree " + key + " --watch")
	}
	if !yes {
		o, err := r.out()
		if err != nil {
			return err
		}
		o.W = r.env.Stderr
		if err := view.RerunList(o, cands); err != nil {
			return err
		}
		fmt.Fprint(r.env.Stderr, "[y/N] ")
		answer, _ := bufio.NewReader(r.env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			r.note("nothing restarted", "")
			return nil
		}
	}
	var restartErr error
	for _, res := range svc.RerunTree(ctx, cands) {
		switch {
		case res.Err != nil:
			fmt.Fprintf(r.env.Stderr, "%s was not restarted: %s\n", res.Key, whatOf(res.Err))
			if restartErr == nil {
				restartErr = res.Err
			}
		case !res.Restarted:
			fmt.Fprintf(r.env.Stderr, "%s was not restarted: it is running or has succeeded\n", res.Key)
		default:
			fmt.Fprintf(r.env.Stderr, "restarted %s\n", res.Key)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if noWatch {
		return restartErr
	}
	err = r.treeLoop(ctx, svc, key, app.TreeOptions{}, failed)
	if restartErr != nil {
		return restartErr
	}
	return err
}
```

Add the helper the loop uses (the package has none):

```go
// whatOf is an error's What, the one line a list of problems shows.
func whatOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.What
	}
	return err.Error()
}
```

(imports: `bufio`, `errors`, `strings`.)

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/cli/ -v -run 'RerunFailed|BuildTree' 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `ok`.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/cli/ docs/cli/
git commit -m "feat: bam build tree --rerun-failed restarts a tree's failures after asking"
```

---

### Task 4: `bam build rerun`

**Files:**
- Create: `internal/cli/rerun.go`
- Modify: `internal/cli/browse.go` (`addBrowse`: add `newBuildRerunCmd(r)`)
- Test: `internal/cli/rerun_test.go` (append)
- Regenerated: `docs/cli/`

**Interfaces:**
- Consumes: `Service.Build`, `Service.Restart`, `runtime.resolveBuildArg`, `runtime.watchLoop(ctx, svc, key, limit, revision)` (existing).

- [ ] **Step 1: Write the failing tests** (append)

```go
func TestBuildRerunRestartsOneBuild(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Equal(t, []string{"PROJ-PROV-9"}, h.fake.Restarted)
	assert.Contains(t, h.stdout.String(), "restarted PROJ-PROV-9")
	assert.Contains(t, h.stdout.String(), "watch: bam watch PROJ-PROV-9")
}

func TestBuildRerunWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	rerunnable(h, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "rerun", "PROJ-PROV-9", "--watch"))
	assert.Contains(t, h.stdout.String(), "✓ Success")
}

func TestBuildRerunRefusesARunningOrSuccessfulBuild(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 2, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 succeeded; nothing to rerun")
	h = treeHarness(t, provider.StateRunning)
	assert.Equal(t, 2, h.run("build", "rerun", "PROJ-PROV-9"))
	assert.Contains(t, h.stderr.String(), "PROJ-PROV-9 is running; nothing to rerun")
	assert.Empty(t, h.fake.Restarted)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run BuildRerun 2>&1 | grep -E '^(--- |ok|FAIL)' | head`
Expected: FAIL — `unknown command "rerun" for "bam build"`.

- [ ] **Step 3: Implement** — `internal/cli/rerun.go`:

```go
package cli

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/spf13/cobra"
)

func newBuildRerunCmd(r *runtime) *cobra.Command {
	var last, watch bool
	var branch string
	cmd := &cobra.Command{
		Use:   "rerun [<build>|<plan>|<target>]",
		Short: "Rerun a failed build's failed jobs",
		Long: `Rerun a failed or stopped build's failed jobs, as Bamboo's "Rerun failed jobs"
does: same build key, same variables and commit. A build that is running or
succeeded is refused.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			b, err := svc.Build(ctx, key)
			if err != nil {
				return err
			}
			switch {
			case b.State == provider.StateSuccess:
				return errs.Usagef("%s succeeded; nothing to rerun", key)
			case !b.State.Finished():
				return errs.Usagef("%s is %s; nothing to rerun", key, b.State)
			}
			_, restarted, err := svc.Restart(ctx, key)
			if err != nil {
				return err
			}
			if !restarted {
				return errs.Usagef("%s is running or has succeeded; nothing to rerun", key)
			}
			if !watch {
				fmt.Fprintf(r.env.Stdout, "restarted %s\n  watch: bam watch %s\n", key, key)
				return nil
			}
			return r.watchLoop(ctx, svc, key, 0, "")
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or target is given")
	f.BoolVar(&watch, "watch", false, "follow the build until it finishes")
	return cmd
}
```

`internal/cli/browse.go` `addBrowse`: `AddCommand(newBuildListCmd(r), newBuildShowCmd(r), newBuildTreeCmd(r), newBuildRerunCmd(r))`.

(A running build: `b.State` is `running`, so the message reads `PROJ-PROV-9 is running; nothing to rerun`.)

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/cli/ -v -run 'BuildRerun|RerunFailed' 2>&1 | grep -E '^(--- FAIL|ok|FAIL)'`
Expected: `ok`.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/cli/ docs/cli/
git commit -m "feat: bam build rerun restarts one build's failed jobs"
```

---

### Task 5: The tree screen in the terminal UI

**Files:**
- Create: `internal/view/tui/treescreen.go`
- Modify: `internal/view/tui/model.go` (screen constant, fields, `Update` cases, `dispatchKey`, `View`, `back`, `quit`, `selectionURL`, the columns key switch), `internal/view/tui/keys.go` (`Tree` binding, help rows), `internal/view/tui/panels.go` (`keyLine`), `internal/view/tui/command.go` (`optional`, `:tree`)
- Test: `internal/view/tui/treescreen_test.go`, golden `internal/view/tui/testdata/golden/tree-80x24.txt`

**Interfaces:**
- Consumes: `app.Service.WatchTree`, `app.TreeEvent`, `app.Tree`, `app.TreeNode`, `app.TreeFinished` (existing); `testService`, `goldenModel`, `send`, `mkKey`, `requireGolden` test helpers.
- Produces (for Task 6):
  - `screenTree` constant; `Model.buildTree treeScreen`; `Model.treeGen int`; `Model.treeCancel context.CancelFunc`; `Model.treeCh <-chan app.TreeEvent`; `Model.fromTree bool`
  - `type treeScreen struct { root string; tree app.Tree; have bool; cursor int; from screen; restarting map[string]bool }`
  - `func (m Model) openTree(key string) (tea.Model, tea.Cmd)`, `func (m Model) watchTree() (tea.Model, tea.Cmd)`, `func (m *Model) stopTree()`
  - `func (m Model) treeSelected() (*app.TreeNode, bool)`
  - `type treeEventMsg struct{ Gen int; Event app.TreeEvent }`, `type treeClosedMsg struct{ Gen int }`

- [ ] **Step 1: Write the failing tests** — `internal/view/tui/treescreen_test.go`

```go
package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// instantClock fires every wait at once, so a watch or restart never sleeps.
type instantClock struct{}

func (instantClock) Now() time.Time                         { return goldenNow }
func (instantClock) After(time.Duration) <-chan time.Time   { ch := make(chan time.Time, 1); ch <- goldenNow; return ch }

// treeTUIModel is the panels on PROJ-BUILD-44 (failed) with a failed child
// PROJ-PROV-7, the Builds panel focused.
func treeTUIModel(t *testing.T) (Model, *fake.Provider) {
	t.Helper()
	m := goldenModel(80, 24)
	m.svc = testService()
	m.svc.Clock = instantClock{}
	f := m.svc.P.(*fake.Provider)
	f.History["PROJ-PROV"] = []provider.Build{{Key: "PROJ-PROV-7", PlanKey: "PROJ-PROV", Number: 7, State: provider.StateFailed,
		Reason: "Child of PROJ-BUILD-44", Duration: 40 * time.Second, URL: labOrigin + "/browse/PROJ-PROV-7",
		Stages: []provider.Stage{{Name: "Apply", State: provider.StateFailed, Duration: 40 * time.Second,
			Jobs: []provider.Job{{Key: "PROJ-PROV-TF-7", Name: "Terraform", State: provider.StateFailed}}}}}}
	m.focus = focusBuilds
	return m, f
}

// firstTree opens the screen with t and delivers the first tree.
func firstTree(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.True(t, m.buildTree.have)
	return m
}

func TestTOpensTheTreeOfTheSelectedBuild(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	require.Equal(t, "PROJ-BUILD-44", m.buildTree.root)
	require.Equal(t, "PROJ-BUILD-44(PROJ-PROV-7)", tuiShape(m.buildTree.tree.Root))
}

func tuiShape(n *app.TreeNode) string {
	s := n.Build.Key
	if len(n.Children) == 0 {
		return s
	}
	s += "("
	for i, c := range n.Children {
		if i > 0 {
			s += ","
		}
		s += tuiShape(c)
	}
	return s + ")"
}

func TestTreeScreenGolden(t *testing.T) {
	m, _ := treeTUIModel(t)
	requireGolden(t, "tree-80x24", firstTree(t, m).View())
}

func TestTreeScreenCursorMovesOverBuildsOnly(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	n, ok := m.treeSelected()
	require.True(t, ok)
	require.Equal(t, "PROJ-BUILD-44", n.Build.Key)
	m, _ = send(m, mkKey("j"))
	n, _ = m.treeSelected()
	require.Equal(t, "PROJ-PROV-7", n.Build.Key, "the stage rows are skipped")
	m, _ = send(m, mkKey("j"))
	n, _ = m.treeSelected()
	require.Equal(t, "PROJ-PROV-7", n.Build.Key, "the last build stays selected")
}

func TestTreeScreenEnterAndBack(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenColumns, m.screen)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen, "esc from a tree's build returns to the tree")
	require.NotNil(t, m.treeCh, "and watches it again")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenColumns, m.screen, "esc from the tree returns where it was opened")
	require.Nil(t, m.treeCh, "closing the screen stops watching")
}

func TestTreeCommandOpensAnyBuild(t *testing.T) {
	m, _ := treeTUIModel(t)
	next, cmd := m.runCommandLine("tree PROJ-BUILD-44")
	m = next.(Model)
	require.Equal(t, screenTree, m.screen)
	require.NotNil(t, cmd)
	next, _ = m.runCommandLine("tree")
	require.Equal(t, screenTree, next.(Model).screen, ":tree alone uses the selected build")
}

func TestTreeScreenURLAndKeyLine(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("j"))
	require.Equal(t, labOrigin+"/browse/PROJ-PROV-7", m.selectionURL())
	require.Contains(t, m.statusBar(80), "enter build")
	require.Contains(t, m.statusBar(80), "esc back")
}

func TestTreeScreenIgnoresAStaleEvent(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	before := m.buildTree.tree
	m, _ = send(m, treeEventMsg{Gen: m.treeGen - 1, Event: app.TreeEvent{Type: app.TreeUpdate, Tree: app.Tree{Root: &app.TreeNode{}}}})
	require.Equal(t, before, m.buildTree.tree)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/tui/ -run 'Tree' 2>&1 | tail -3`
Expected: build failure — `undefined: screenTree`.

- [ ] **Step 3: Implement**

`internal/view/tui/keys.go`:
- `keyMap`: add `Tree key.Binding` (its own line after `Command`).
- `defaultKeys`: `Tree: b("t", "t", "build tree"),`.
- `allBindings`: append `k.Tree`.
- `helpRows`: add `{"VIEW", "t", "the build tree of the selected build: every build it triggered"}` after the `r` row; change the `F` row to `{"RUN", "F", "rerun the failed runs (set screen) or builds (tree screen)"}`; add `:tree` to the `:` row's command list.

`internal/view/tui/command.go`:
- `command` gains `optional bool // the argument may be left out`.
- In `runCommandLine`, the first case becomes `case c.arg != "" && arg == "" && !c.optional:`.
- In `commands()`, before `quit`:

```go
		{name: "tree", arg: "build", optional: true, desc: "the build tree of a build, or of the selected build", run: Model.commandTree},
```

- Add:

```go
func (m Model) commandTree(arg string) (tea.Model, tea.Cmd, error) {
	key := strings.ToUpper(strings.TrimSpace(arg))
	if key == "" {
		b, ok := m.currentBuild()
		if !ok {
			return m, nil, fmt.Errorf("no build selected; :tree KEY")
		}
		key = b.Key
	}
	next, cmd := m.openTree(key)
	return next, cmd, nil
}
```

`internal/view/tui/model.go`:
- `screen` constants: add `screenTree // one build's tree: every build it triggered` after `screenSetVars`.
- `Model` fields, after `setDry`: `buildTree treeScreen`, `fromTree bool // the panels were opened from the tree screen; esc returns there`; after `projectsGen int` in the generations block: `treeGen int`; after `watchCh`: `treeCancel context.CancelFunc` and `treeCh <-chan app.TreeEvent`.
- `Update`, before `case setLoadedMsg:`:

```go
	case treeEventMsg:
		return m.handleTreeEvent(msg)
	case treeClosedMsg:
		return m, nil
	case treeRerunMsg:
		return m.handleTreeRerun(msg)
```

  (`treeRerunMsg` and `handleTreeRerun` come in Task 6; until then add them as `type treeRerunMsg struct{ Gen int; Results []app.RerunResult }` and `func (m Model) handleTreeRerun(treeRerunMsg) (tea.Model, tea.Cmd) { return m, nil }` in `treescreen.go`, replaced in Task 6.)
- `dispatchKey`: after the `screenSetVars` check: `if m.screen == screenTree { return m.handleTreeKey(msg) }`.
- The columns key switch, after `case key.Matches(msg, keys.Logs):`'s neighbours: `case key.Matches(msg, keys.Tree): if b, ok := m.currentBuild(); ok { return m.openTree(b.Key) }`.
- `View`: `case screenTree: return m.overlayView(m.treeScreenView())`.
- `back()`: before `case m.screen == screenSet:`:

```go
	case m.screen == screenColumns && m.fromTree:
		m.fromTree = false
		m.stopWatch()
		m.screen = screenTree
		return m.watchTree() // watching paused while the build was open
	case m.screen == screenTree:
		return m.closeTree()
```

- `quit()`: in the plain path, `m.stopTree()` next to `m.stopWatch()`.
- `selectionURL()`: after the set screen check: `if m.screen == screenTree { return m.treeSelectionURL() }`.

`internal/view/tui/panels.go` `keyLine`, after the `screenSet` block:

```go
	if m.screen == screenTree {
		parts := []string{"enter build", "l logs"}
		if m.treeRerunnable() {
			parts = append(parts, "F rerun")
		}
		return strings.Join(append(parts, "r refresh", "esc back"), "  ")
	}
```

`internal/view/tui/treescreen.go`:

```go
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
)

// treeScreen is one build's tree: what the watch last sent, the cursor over
// its build rows, and where esc returns.
type treeScreen struct {
	root       string
	tree       app.Tree
	have       bool
	cursor     int // over build rows only
	from       screen
	restarting map[string]bool // builds a rerun is restarting now
}

type treeEventMsg struct {
	Gen   int
	Event app.TreeEvent
}

type treeClosedMsg struct{ Gen int }

func treeCmd(ch <-chan app.TreeEvent, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return treeClosedMsg{Gen: gen}
		}
		return treeEventMsg{Gen: gen, Event: e}
	}
}

// openTree opens the tree screen on key and starts watching it.
func (m Model) openTree(key string) (tea.Model, tea.Cmd) {
	if m.svc == nil || key == "" {
		return m, nil
	}
	from := m.screen
	if from == screenTree {
		from = m.buildTree.from
	}
	m.stopWatch()
	m.buildTree = treeScreen{root: key, from: from}
	m.screen = screenTree
	return m.watchTree()
}

// watchTree starts a fresh watch on the screen's root, so builds a rerun
// restarted are read again.
func (m Model) watchTree() (tea.Model, tea.Cmd) {
	m.stopTree()
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.treeCancel = cancel
	m.treeCh = m.svc.WatchTree(ctx, m.buildTree.root, app.TreeOptions{})
	return m, treeCmd(m.treeCh, m.treeGen)
}

func (m *Model) stopTree() {
	if m.treeCancel != nil {
		m.treeCancel()
		m.treeCancel = nil
	}
	m.treeCh = nil
	m.treeGen++
}

func (m Model) closeTree() (tea.Model, tea.Cmd) {
	m.stopTree()
	m.screen = m.buildTree.from
	if m.screen == screenTree {
		m.screen = screenColumns
	}
	return m, nil
}

func (m Model) handleTreeEvent(msg treeEventMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.treeGen {
		return m, nil
	}
	e := msg.Event
	switch e.Type {
	case app.TreeError:
		m.err = e.Err
		return m, nil
	case app.TreeUpdate, app.TreeDone:
		selected := ""
		if n, ok := m.treeSelected(); ok {
			selected = n.Build.Key
		}
		m.buildTree.tree, m.buildTree.have = e.Tree, true
		m.buildTree.cursor = 0
		for i, l := range m.treeBuildRows() {
			if l.node.Build.Key == selected {
				m.buildTree.cursor = i
			}
		}
		if w := e.Tree.Warnings; len(w) > 0 {
			m.status = fmt.Sprintf("%d warning(s): %s", len(w), w[0])
		}
	}
	if e.Type == app.TreeUpdate {
		return m, treeCmd(m.treeCh, m.treeGen)
	}
	return m, nil
}

// treeLine is one row of the screen: a build, a stage, or a not-run child.
type treeLine struct {
	text string
	node *app.TreeNode // nil for a stage row
}

// treeLines is the tree as the CLI draws it, one row per build and stage.
func (m Model) treeLines() []treeLine {
	t := m.buildTree.tree
	if t.Root == nil {
		return nil
	}
	out := []treeLine{{text: m.treeNodeText(t.Root), node: t.Root}}
	var add func(n *app.TreeNode, indent string)
	add = func(n *app.TreeNode, indent string) {
		type item struct {
			stage *provider.Stage
			node  *app.TreeNode
		}
		var items []item
		if !n.NotRun && !n.Loop && !n.Superseded {
			for i := range n.Build.Stages {
				items = append(items, item{stage: &n.Build.Stages[i]})
			}
			for _, c := range n.Children {
				items = append(items, item{node: c})
			}
		}
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.stage != nil {
				out = append(out, treeLine{text: indent + branch + stageText(*it.stage)})
				continue
			}
			out = append(out, treeLine{text: indent + branch + m.treeNodeText(it.node), node: it.node})
			add(it.node, indent+more)
		}
	}
	add(t.Root, "")
	return out
}

func (m Model) treeNodeText(n *app.TreeNode) string {
	switch {
	case n.NotRun:
		return dimStyle.Render("· " + n.PlanKey + "  not run")
	case n.Loop:
		return "↺ " + n.Build.Key + "  already shown above"
	case n.Superseded:
		return dimStyle.Render(stateGlyph(n.Build.State) + " " + n.Build.Key + "  earlier run")
	}
	b := n.Build
	d := b.Duration
	if !app.TreeFinished(b.State) && !b.StartedAt.IsZero() {
		d = m.now().Sub(b.StartedAt)
	}
	text := b.Key + "  " + stateCell(b.State) + "  " + view.Duration(d)
	if c := len(b.FailedTests); c > 0 {
		text += fmt.Sprintf("  %d tests failed", c)
	}
	if m.buildTree.restarting[b.Key] {
		text += "  restarting…"
	}
	return text
}

func stageText(s provider.Stage) string {
	text := stateStyle(s.State).Render(stateGlyph(s.State)) + " stage " + s.Name + "  " + view.Duration(s.Duration)
	if s.State == provider.StateFailed {
		var names []string
		for _, j := range s.Jobs {
			if j.State == provider.StateFailed {
				names = append(names, j.Name)
			}
		}
		if len(names) > 0 {
			text += "  " + strings.Join(names, ", ")
		}
	}
	return text
}

// treeBuildRows is the rows the cursor moves over: every build, no stage,
// no not-run child.
func (m Model) treeBuildRows() []treeLine {
	var out []treeLine
	for _, l := range m.treeLines() {
		if l.node != nil && !l.node.NotRun {
			out = append(out, l)
		}
	}
	return out
}

func (m Model) treeSelected() (*app.TreeNode, bool) {
	rows := m.treeBuildRows()
	if m.buildTree.cursor >= len(rows) {
		return nil, false
	}
	return rows[m.buildTree.cursor].node, true
}

func (m Model) treeSelectionURL() string {
	n, ok := m.treeSelected()
	if !ok {
		return ""
	}
	if n.Build.URL != "" {
		return n.Build.URL
	}
	if m.svc != nil {
		return m.svc.P.URL(n.Build.Key)
	}
	return ""
}

// treeRerunnable reports whether F would act: a finished tree with a
// failure to restart, and no rerun in flight.
func (m Model) treeRerunnable() bool {
	t := m.buildTree.tree
	return m.buildTree.have && len(m.buildTree.restarting) == 0 && t.State != provider.StateRunning && len(app.RerunCandidates(t)) > 0
}

func (m Model) handleTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.treeBuildRows()
	switch {
	case key.Matches(msg, keys.Quit), key.Matches(msg, keys.Back):
		return m.closeTree()
	case key.Matches(msg, keys.Down):
		m.buildTree.cursor = min(m.buildTree.cursor+1, max(len(rows)-1, 0))
	case key.Matches(msg, keys.Up):
		m.buildTree.cursor = max(m.buildTree.cursor-1, 0)
	case key.Matches(msg, keys.Top):
		m.buildTree.cursor = 0
	case key.Matches(msg, keys.Bottom):
		m.buildTree.cursor = max(len(rows)-1, 0)
	case key.Matches(msg, keys.Enter):
		return m.openTreeBuild(false, false)
	case key.Matches(msg, keys.Logs):
		return m.openTreeBuild(true, false)
	case key.Matches(msg, keys.AllLogs):
		return m.openTreeBuild(true, true)
	case key.Matches(msg, keys.Open):
		return m.openSelection()
	case key.Matches(msg, keys.Copy):
		return m.copySelection()
	case key.Matches(msg, keys.Refresh):
		return m.watchTree()
	case key.Matches(msg, keys.RerunFailed):
		return m.askTreeRerun()
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
	}
	return m, nil
}

// openTreeBuild shows the selected build in the panels, or its logs; esc
// there comes back to the tree. Watching the tree pauses meanwhile.
func (m Model) openTreeBuild(logs, all bool) (tea.Model, tea.Cmd) {
	n, ok := m.treeSelected()
	if !ok || m.svc == nil {
		return m, nil
	}
	b := n.Build
	m.stopTree()
	m.fromTree = true
	m.screen = screenColumns
	m.focus = focusMain
	m.detail = &b
	m.progress = provider.Progress{}
	m.expanded = defaultExpanded(b)
	m.treeCursor = 0
	if logs {
		return m.openLogs("", all)
	}
	return m.startWatch(b.Key)
}

func (m Model) treeScreenView() string {
	t := m.buildTree.tree
	head := " " + m.buildTree.root
	if m.buildTree.have {
		head += " · " + stateCell(t.State)
	} else {
		head += " · loading"
	}
	lines := []string{head, " " + m.treeCounts()}
	rows := m.treeLines()
	sel := -1
	if n, ok := m.treeSelected(); ok {
		for i, l := range rows {
			if l.node == n {
				sel = i
			}
		}
	}
	for i, l := range rows {
		text := " " + l.text
		if i == sel {
			text = selectedRow(text, m.width, true)
		}
		lines = append(lines, text)
	}
	body := m.height - 1
	if len(lines) > body {
		lines = lines[:body]
	}
	for len(lines) < body {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(append(lines, m.statusBar(m.width)), "\n")
}

// treeCounts is "2 failed · 1 running · 4 succeeded", over current builds.
func (m Model) treeCounts() string {
	var failed, running, ok, notRun int
	var walk func(n *app.TreeNode)
	walk = func(n *app.TreeNode) {
		if n.Superseded {
			return
		}
		switch {
		case n.NotRun:
			notRun++
		case n.Loop:
		case !app.TreeFinished(n.Build.State):
			running++
		case n.Build.State == provider.StateSuccess || n.Build.State == provider.StateSkipped:
			ok++
		default:
			failed++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if m.buildTree.tree.Root != nil {
		walk(m.buildTree.tree.Root)
	}
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{failed, "failed"}, {running, "running"}, {ok, "succeeded"}, {notRun, "not run"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	return strings.Join(parts, " · ")
}

// askTreeRerun is F; Task 6 fills it in.
func (m Model) askTreeRerun() (tea.Model, tea.Cmd) { return m, nil }

type treeRerunMsg struct {
	Gen     int
	Results []app.RerunResult
}

func (m Model) handleTreeRerun(treeRerunMsg) (tea.Model, tea.Cmd) { return m, nil }
```

(`defaultExpanded`, `selectedRow`, `truncate`, `stateCell`, `stateStyle`, `stateGlyph`, `dimStyle`, `openLogs`, `startWatch`, `openSelection`, `copySelection`, `openHelp` already exist in the package. `TreeFinished` was added to `app` with #48's skipped fix.)

- [ ] **Step 4: Write the golden and review it**

Run: `go test ./internal/view/tui/ -run TestTreeScreenGolden -update && cat internal/view/tui/testdata/golden/tree-80x24.txt`
Expected: a header ` PROJ-BUILD-44 · ✗ failed`, a count line ` 2 failed`, the root row highlighted, its stage rows if the fake build has stages, `└─ PROJ-PROV-7  ✗ failed  40s`, `   └─ ✗ stage Apply  40s  Terraform`, blank lines to the bottom, and the status bar with `enter build  l logs  F rerun  r refresh  esc back`.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test -race ./internal/view/tui/ 2>&1 | tail -3`
Expected: `ok` — the new tests and every existing TUI test, including `TestNoKeyIsBoundTwice` and the helpRows coverage test.

- [ ] **Step 6: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/view/tui/
git commit -m "feat: a build tree screen in the terminal UI (t, :tree)"
```

---

### Task 6: `F` reruns from the tree screen

**Files:**
- Modify: `internal/view/tui/treescreen.go` (replace `askTreeRerun`, `handleTreeRerun`)
- Test: `internal/view/tui/treescreen_test.go` (append)

**Interfaces:**
- Consumes: Task 5's screen; `app.RerunCandidates`, `Service.RerunTree` (Task 1); `Model.ask` (existing confirm overlay).

- [ ] **Step 1: Write the failing tests** (append)

```go
// restartableChild makes the root succeed, so PROJ-PROV-7 is the one
// candidate, and makes PROJ-PROV-7 restart and then succeed.
func restartableChild(f *fake.Provider) {
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	child := f.History["PROJ-PROV"][0]
	queued, done := child, child
	queued.State, done.State = provider.StateQueued, provider.StateSuccess
	done.Stages = nil
	f.Sequences = map[string][]provider.Build{"PROJ-PROV-7": {child, child, queued, done}}
}

func TestTreeScreenRerunAsksThenRestartsAndWatchesAgain(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Contains(t, m.confirm.Prompt, "Rerun the failed jobs of 1 build? PROJ-PROV-7")
	m, cmd := send(m, mkKey("y"))
	require.True(t, m.buildTree.restarting["PROJ-PROV-7"])
	require.NotNil(t, cmd)
	m, cmd = send(m, cmd())
	require.Empty(t, m.buildTree.restarting)
	require.Contains(t, m.status, "restarted")
	require.NotNil(t, cmd, "a fresh watch starts")
	require.NotNil(t, m.treeCh)
}

func TestTreeScreenRerunNothingToRerun(t *testing.T) {
	m, f := treeTUIModel(t)
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	f.History["PROJ-PROV"][0].State = provider.StateSuccess
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, "nothing to rerun", m.status)
}

func TestTreeScreenRerunRefusesARunningTree(t *testing.T) {
	m, f := treeTUIModel(t)
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	f.Plans["PROJ"] = append(f.Plans["PROJ"], provider.Plan{Key: "PROJ-DOCS", ProjectKey: "PROJ"})
	f.History["PROJ-DOCS"] = []provider.Build{{Key: "PROJ-DOCS-3", PlanKey: "PROJ-DOCS", Number: 3,
		State: provider.StateRunning, Reason: "Child of PROJ-BUILD-44"}}
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay)
	require.Contains(t, m.status, "still running")
}

func TestTreeScreenRerunIgnoresASecondF(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, _ = send(m, mkKey("y"))
	m, cmd := send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay, "no second question while restarting")
	require.Nil(t, cmd)
}

func TestTreeScreenLateRerunResultIsDropped(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("y"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenColumns, m.screen)
	m, _ = send(m, cmd())
	require.Equal(t, screenColumns, m.screen, "a result after closing does not reopen the screen")
	require.Nil(t, m.treeCh)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/tui/ -run 'TreeScreenRerun|LateRerun' 2>&1 | grep -E '^(--- FAIL|ok|FAIL)' | head`
Expected: FAIL — no overlay opens on `F`.

- [ ] **Step 3: Implement** — replace the two stubs in `treescreen.go`:

```go
// askTreeRerun is F: it says why it cannot act, or asks before restarting
// the tree's topmost failures.
func (m Model) askTreeRerun() (tea.Model, tea.Cmd) {
	t := m.buildTree.tree
	if !m.buildTree.have || len(m.buildTree.restarting) > 0 || m.svc == nil {
		return m, nil
	}
	cands := app.RerunCandidates(t)
	switch {
	case len(cands) == 0:
		m.status = "nothing to rerun"
		return m, nil
	case t.State == provider.StateRunning:
		m.status = "the tree is still running; rerun when it has finished"
		return m, nil
	}
	var keys []string
	for _, n := range cands {
		keys = append(keys, n.Build.Key)
	}
	noun := "builds"
	if len(cands) == 1 {
		noun = "build"
	}
	prompt := fmt.Sprintf("Rerun the failed jobs of %d %s? %s", len(cands), noun, strings.Join(keys, ", "))
	return m.ask(prompt, func(m Model) (tea.Model, tea.Cmd) {
		m.buildTree.restarting = map[string]bool{}
		for _, k := range keys {
			m.buildTree.restarting[k] = true
		}
		m.stopTree()
		gen, svc, ctx := m.treeGen, m.svc, m.baseCtx()
		return m, func() tea.Msg { return treeRerunMsg{Gen: gen, Results: svc.RerunTree(ctx, cands)} }
	})
}

// handleTreeRerun reports the restarts and watches the tree afresh, so the
// restarted builds are read again. A result for a screen since closed or
// reopened is dropped.
func (m Model) handleTreeRerun(msg treeRerunMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.treeGen || m.screen != screenTree {
		return m, nil
	}
	m.buildTree.restarting = nil
	restarted := 0
	var problems []string
	for _, r := range msg.Results {
		switch {
		case r.Err != nil:
			problems = append(problems, r.Key+": "+errorWhat(r.Err))
		case r.Restarted:
			restarted++
		default:
			problems = append(problems, r.Key+" was not restarted")
		}
	}
	m.status = fmt.Sprintf("restarted %d build(s)", restarted)
	if len(problems) > 0 {
		m.status += "; " + strings.Join(problems, "; ")
	}
	return m.watchTree()
}
```

(`errorWhat` is the TUI's helper the status bar uses for an error. `stopTree` in the confirm action bumps `treeGen`, so the message carries the generation the screen expects; closing the screen bumps it again, so a late result is dropped.)

- [ ] **Step 4: Run them to see them pass**

Run: `go test -race ./internal/view/tui/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/view/tui/
git commit -m "feat: F reruns a tree's failed builds from the tree screen"
```

---

### Task 7: Documentation

**Files:**
- Modify: `README.md`, `docs/json.md`, `CHANGELOG.md`, `docs/superpowers/specs/2026-10-09-bam-build-tree-rerun-design.md` (Status)

- [ ] **Step 1: README** — in "Build trees", after the flags list, add:

```markdown
To retry the failures, `bam build tree PROJ-API-142 --rerun-failed` lists the failed builds whose parents succeeded and asks before restarting them. Bamboo reruns only their failed jobs, under the same build keys, and triggers their children again when they succeed; bam then follows the tree until it has finished. `--yes` skips the question (required without a terminal), `--no-watch` restarts and exits. `bam build rerun OPS-SMOKE-51` restarts one build. Only the newest run of a child plan counts: an older one shows as `earlier run`.
```

  In "Everyday commands", add a row `| retry the failed parts of a run | \`bam build tree --last --rerun-failed\` |`. In "Terminal UI", add a paragraph: `t` on a build (or `:tree KEY`) opens its tree, live; `enter` opens a build, `l` its logs; `F` asks, then reruns the failed builds and keeps watching. Add `t` to the keys table.

- [ ] **Step 2: `docs/json.md`** — in "Build tree", add `superseded` to the `status` values: "an older run of the same child plan; its build and children are kept, and it does not count toward `state`".

- [ ] **Step 3: `CHANGELOG.md`** — under `## [Unreleased]` → `### Added`:

```markdown
- `bam build tree --rerun-failed` and `bam build rerun` restart failed builds; only the newest run of a child plan counts toward the tree.
- The terminal UI shows a build's tree (`t`, `:tree`) and reruns its failed builds with `F`.
```

- [ ] **Step 4: Spec status** — `**Status:** Implemented`.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add README.md docs/ CHANGELOG.md
git commit -m "docs: rerunning a build tree and the tree screen"
```
