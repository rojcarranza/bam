# Build Tree Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam build tree` shows a build and every build it triggered, to any depth, with stages, failed jobs and a pointer to the failure; `--watch`, `--json`, `--failed`, `--depth`.

**Architecture:** `app` finds children by indexing the last 25 builds of every candidate plan by the parent their reason names (`Child of KEY-N`), grows the tree from that index, then reads each node's detail. An optional `children:` config narrows a plan's children and yields `not run` nodes. `view` renders text, live, lines and JSON; `cli` adds the command.

**Tech Stack:** Go, cobra, testify. No new dependency.

**Spec:** `docs/superpowers/specs/2026-10-08-bam-build-tree-design.md`

## Global Constraints

- No new dependency; no change to `provider` or `provider/bamboo` code (a recorded-shape test is added there).
- `app` never prints (`TestAppStaysUIReady`).
- Window: 25 builds per candidate plan. Concurrency: 4 requests at a time. Watch poll: 10 seconds. Three failed refreshes in a row end a watch.
- Reason pattern: `^Child of ([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)$` on `provider.Build.Reason` (plain text).
- Exit codes: 0 success; 1 any node not successful or a configured child not run; a running tree without `--watch` exits 0 with `still running: bam build tree KEY --watch` on stderr; `--depth` below 1 is exit 2 before any network call.
- JSON names (public contract): top level `state`, `warnings`, `root`; node `plan_key`, `status` (`success`…, `not_run`, `loop`), `build` (the `bam build show --json` document, `null` for `not_run`), `children` (always an array).
- Placeholders only: `PROJ`, `OPS`, `LAB`, `jdoe`, `bamboo.example.com`.
- Before each commit: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs`, chained with `&&`; commit `docs/cli/` changes with the task that caused them.
- Commits: Conventional Commits, the owner's identity only, no AI attribution lines.

## Review Focus

1. **A root build on a plan branch** (`PROJ-API12-5`): no crash, no error, children searched on default branches only, so the tree is just the root. (Task 2 `TestBuildTreeRootOnABranch`.)
2. **A `children:` entry naming a plan that does not exist on the server**: a warning, the rest of the tree, and the plan shown `not run`. (Task 2 `TestBuildTreeConfiguredPlanThatCannotBeRead`.)
3. **No projects configured**: candidates are the root's project and configured plans only. (Task 2 `TestBuildTreeWithoutAConfiguredScope`.)
4. **`--failed` on a fully successful tree**: the root line only, exit 0. (Task 4 `TestTreeTextFailedOnlyOnASuccessfulTree`.)
5. **A watch interrupted by ctrl-C**: exit 130 with `still running: …`, no build stopped. (Task 6 `TestBuildTreeWatchInterrupted`.)

---

### Task 1: `children:` in configuration

**Files:**
- Create: `internal/config/children.go`
- Modify: `internal/config/schema.go` (`ProjectFile`, `MachineFile`, `Repo`)
- Test: `internal/config/children_test.go`

**Interfaces:**
- Produces: `func (c *Config) Children() (map[string][]string, error)` — merged project < machine < machine `repos` entry, a later layer replacing a plan's list; validated.

- [ ] **Step 1: Write the failing tests**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildrenMergeAcrossLayers(t *testing.T) {
	cfg, err := loadProject(t,
		"version: 1\nchildren:\n  PROJ-API: [OPS-DEPLOY, PROJ-DOCS]\n  OPS-DEPLOY: [OPS-SMOKE]\n",
		"version: 1\nchildren:\n  OPS-DEPLOY: [OPS-AUDIT]\n")
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Equal(t, map[string][]string{
		"PROJ-API":   {"OPS-DEPLOY", "PROJ-DOCS"},
		"OPS-DEPLOY": {"OPS-AUDIT"},
	}, got, "the machine file replaces the project's list for the same plan")
}

func TestChildrenFromTheReposEntryWin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	writeFile(t, filepath.Join(dir, ".bam.yaml"), "version: 1\nchildren:\n  PROJ-API: [OPS-DEPLOY]\n")
	mpath := filepath.Join(dir, "machine.yaml")
	writeFile(t, mpath, "version: 1\nrepos:\n  "+dir+":\n    children:\n      PROJ-API: [PROJ-DOCS]\n")
	cfg, err := Load(LoadOptions{WorkDir: dir, Home: dir, MachineFile: mpath, Getenv: noEnv})
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Equal(t, map[string][]string{"PROJ-API": {"PROJ-DOCS"}}, got)
}

func TestChildrenValidation(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"children:\n  proj-api: [OPS-DEPLOY]\n", `children: "proj-api" is not a plan key`},
		{"children:\n  PROJ-API: [OPS-DEPLOY-1]\n", `children.PROJ-API: "OPS-DEPLOY-1" is not a plan key`},
		{"children:\n  PROJ-API: [PROJ-API]\n", "children.PROJ-API lists itself"},
	} {
		cfg, err := loadProject(t, "version: 1\n"+tc.yaml, "")
		require.NoError(t, err, tc.yaml)
		_, err = cfg.Children()
		require.Error(t, err, tc.yaml)
		assert.Equal(t, errs.KindConfig, errs.KindOf(err), tc.yaml)
		assert.Contains(t, err.Error(), tc.want, tc.yaml)
		assert.Contains(t, err.Error(), ".bam.yaml", tc.yaml)
	}
}

func TestChildrenEmptyWithoutConfig(t *testing.T) {
	cfg, err := loadProject(t, "version: 1\n", "")
	require.NoError(t, err)
	got, err := cfg.Children()
	require.NoError(t, err)
	require.Empty(t, got)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/config/ -run Children -v`
Expected: build failure — `cfg.Children undefined`.

- [ ] **Step 3: Implement**

`internal/config/schema.go`: add `Children map[string][]string \`yaml:"children,omitempty"\`` as the last field of `ProjectFile`, of `MachineFile` (before `Color`) and of `Repo`.

`internal/config/children.go`:

```go
package config

import (
	"regexp"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// childPlanKeyRe is a plan key, PROJ-PLAN.
var childPlanKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*$`)

// Children is the children: map of every layer, project, then machine, then
// the machine's entry for this repository; a later layer's list replaces an
// earlier one for the same plan. Each key and value must be a plan key.
func (c *Config) Children() (map[string][]string, error) {
	out := map[string][]string{}
	layer := func(path, where string, m map[string][]string) error {
		for _, k := range sortedKeys(m) {
			if !childPlanKeyRe.MatchString(k) {
				return errs.Configf("%s: %s: %q is not a plan key", path, where, k).
					WithWhy("children maps a plan key like PROJ-API to the plans it triggers")
			}
			for _, v := range m[k] {
				switch {
				case !childPlanKeyRe.MatchString(v):
					return errs.Configf("%s: %s.%s: %q is not a plan key", path, where, k, v)
				case v == k:
					return errs.Configf("%s: %s.%s lists itself", path, where, k)
				}
			}
			out[k] = append([]string(nil), m[k]...)
		}
		return nil
	}
	if c.Project != nil {
		if err := layer(c.ProjectPath, "children", c.Project.Children); err != nil {
			return nil, err
		}
	}
	if err := layer(c.MachinePath, "children", c.Machine.Children); err != nil {
		return nil, err
	}
	if key, repo, ok := c.repoEntry(); ok {
		if err := layer(c.MachinePath, "repos."+key+".children", repo.Children); err != nil {
			return nil, err
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/config/ -v -run Children`
Expected: PASS, four tests.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/config/
git commit -m "feat: a children map in configuration"
```

---

### Task 2: Build the tree in `app`

**Files:**
- Create: `internal/app/tree.go`
- Test: `internal/app/tree_test.go`, `internal/provider/bamboo/read_test.go` (append one recorded test)

**Interfaces:**
- Consumes: `Config.Children()` (Task 1); `Service.Build`, `Service.ProjectKeys`, `describeErr` (existing).
- Produces:
  - `type TreeOptions struct{ Depth int }`
  - `type TreeNode struct { PlanKey string; Build provider.Build; NotRun, Loop bool; Children []*TreeNode }`
  - `type Tree struct { Root *TreeNode; State provider.State; Warnings []string }`
  - `func (s *Service) BuildTree(ctx context.Context, key string, o TreeOptions) (Tree, error)`
  - `func (s *Service) buildTree(ctx context.Context, key string, o TreeOptions, done map[string]provider.Build) (Tree, error)` (used by Task 3)
  - `func ParentOf(reason string) (string, bool)`
  - `func TreeState(root *TreeNode) provider.State`
  - `func (n *TreeNode) Walk(f func(n *TreeNode, depth int))` — pre-order, root at depth 0

- [ ] **Step 1: Write the failing tests** — `internal/app/tree_test.go`

```go
package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

var treeT0 = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

// tb is a build of key PLAN-N with a reason, started minutes after treeT0.
func tb(key, reason string, st provider.State, minutes int) provider.Build {
	i := strings.LastIndex(key, "-")
	n, _ := strconv.Atoi(key[i+1:])
	return provider.Build{Key: key, PlanKey: key[:i], Number: n, State: st, Reason: reason,
		StartedAt: treeT0.Add(time.Duration(minutes) * time.Minute)}
}

func childOf(parent string) string { return "Child of " + parent }

// treeFake: PROJ-API-142 triggered OPS-DEPLOY-88 and PROJ-DOCS-30;
// OPS-DEPLOY-88 triggered OPS-SMOKE-51. Other builds are noise.
func treeFake() *fake.Provider {
	return &fake.Provider{
		BaseURL: "https://bamboo.example.com",
		Plans: map[string][]provider.Plan{
			"PROJ": {{Key: "PROJ-API"}, {Key: "PROJ-DOCS"}},
			"OPS":  {{Key: "OPS-DEPLOY"}, {Key: "OPS-SMOKE"}},
		},
		History: map[string][]provider.Build{
			"PROJ-API":   {tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0), tb("PROJ-API-141", "Manual run by jdoe", provider.StateSuccess, -60)},
			"PROJ-DOCS":  {tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5)},
			"OPS-DEPLOY": {tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5), tb("OPS-DEPLOY-87", childOf("PROJ-API-141"), provider.StateSuccess, -55)},
			"OPS-SMOKE":  {tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)},
		},
	}
}

func treeService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Projects = []string{"PROJ", "OPS"}
	return svc
}

// shape is a tree as KEY(child,child), not-run nodes as PLAN!, loops as KEY^.
func shape(n *TreeNode) string {
	label := n.Build.Key
	switch {
	case n.NotRun:
		return n.PlanKey + "!"
	case n.Loop:
		return label + "^"
	}
	if len(n.Children) == 0 {
		return label
	}
	parts := make([]string, len(n.Children))
	for i, c := range n.Children {
		parts[i] = shape(c)
	}
	return label + "(" + strings.Join(parts, ",") + ")"
}

func TestBuildTreeFromBuildReasons(t *testing.T) {
	svc := treeService(t, treeFake())
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Equal(t, provider.StateRunning, tr.State, "PROJ-DOCS-30 still runs")
	require.Empty(t, tr.Warnings)
}

func TestParentOf(t *testing.T) {
	p, ok := ParentOf("Child of PROJ-API-142")
	require.True(t, ok)
	require.Equal(t, "PROJ-API-142", p)
	for _, r := range []string{"Child of PROJ-API", "Manual run by jdoe", "Child of PROJ-API-142 and more", "child of PROJ-API-142"} {
		_, ok := ParentOf(r)
		require.False(t, ok, r)
	}
}

func TestBuildTreeDoesNotConfuseBuildNumbers(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"] = append(p.History["PROJ-DOCS"], tb("PROJ-DOCS-29", childOf("PROJ-API-1420"), provider.StateSuccess, 1))
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.NotContains(t, shape(tr.Root), "PROJ-DOCS-29")
}

func TestBuildTreeShowsEveryRunOfAChildNewestFirst(t *testing.T) {
	p := treeFake()
	p.History["OPS-SMOKE"] = []provider.Build{tb("OPS-SMOKE-52", childOf("OPS-DEPLOY-88"), provider.StateSuccess, 20), tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-52,OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
}

func TestBuildTreeConfiguredChildren(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Children = map[string][]string{"PROJ-API": {"OPS-DEPLOY", "OPS-AUDIT"}}
	svc.P.(*fake.Provider).History["OPS-AUDIT"] = []provider.Build{tb("OPS-AUDIT-3", "Manual run by jdoe", provider.StateSuccess, 1)}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-AUDIT!,OPS-DEPLOY-88(OPS-SMOKE-51))", shape(tr.Root),
		"only the listed plans; PROJ-DOCS-30 is left out; OPS-AUDIT never ran as a child")
	require.Equal(t, 1, svc.P.(*fake.Provider).ListCalls("OPS-AUDIT"), "a configured plan is a candidate outside the scope")
	require.Equal(t, provider.StateFailed, tr.State)
}

func TestBuildTreeConfiguredPlanThatCannotBeRead(t *testing.T) {
	p := treeFake()
	svc := treeService(t, p)
	svc.Cfg.Project.Children = map[string][]string{"PROJ-API": {"OPS-GONE"}}
	svc.P = listErrProvider{Provider: p, failing: "OPS-GONE"}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-GONE!)", shape(tr.Root))
	require.Len(t, tr.Warnings, 1)
	require.Contains(t, tr.Warnings[0], "OPS-GONE: its builds could not be read")
}

// listErrProvider fails ListBuilds for one plan.
type listErrProvider struct {
	*fake.Provider
	failing string
}

func (l listErrProvider) ListBuilds(ctx context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	if key == l.failing {
		return nil, errs.Bamboof("plan %s not found", key).Wrap(errs.ErrNotFound)
	}
	return l.Provider.ListBuilds(ctx, key, o)
}

func TestBuildTreeCutsALoop(t *testing.T) {
	p := treeFake()
	p.History["PROJ-API"][0].Reason = childOf("OPS-SMOKE-51")
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51(PROJ-API-142^)),PROJ-DOCS-30)", shape(tr.Root))
}

func TestBuildTreeDepthStopsTheWalkAndTheReads(t *testing.T) {
	svc := treeService(t, treeFake())
	counting := &getCounter{Provider: svc.P.(*fake.Provider)}
	svc.P = counting
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{Depth: 1})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88,PROJ-DOCS-30)", shape(tr.Root))
	require.Zero(t, counting.calls["OPS-SMOKE-51"], "a node past the depth is not read")
	require.Equal(t, 1, counting.calls["OPS-DEPLOY-88"])
}

// getCounter counts GetBuild calls by key; the tree reads nodes in parallel.
type getCounter struct {
	*fake.Provider
	mu    sync.Mutex
	calls map[string]int
}

func (g *getCounter) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]int{}
	}
	g.calls[key]++
	g.mu.Unlock()
	return g.Provider.GetBuild(ctx, key)
}

func TestBuildTreeReadsEachNodesDetail(t *testing.T) {
	p := treeFake()
	detail := tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)
	detail.Stages = []provider.Stage{{Name: "Smoke", State: provider.StateFailed, Jobs: []provider.Job{{Key: "OPS-SMOKE-JOB1-51", Name: "Smoke tests", State: provider.StateFailed}}}}
	detail.FailedTests = []string{"a", "b", "c"}
	p.Sequences = map[string][]provider.Build{"OPS-SMOKE-51": {detail}}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	smoke := tr.Root.Children[0].Children[0]
	require.Equal(t, "Smoke tests", smoke.Build.Stages[0].Jobs[0].Name)
	require.Len(t, smoke.Build.FailedTests, 3)
}

func TestBuildTreeWindowWarning(t *testing.T) {
	p := treeFake()
	var docs []provider.Build
	for i := 0; i < 25; i++ {
		docs = append(docs, tb(fmt.Sprintf("PROJ-DOCS-%d", 100-i), "Manual run by jdoe", provider.StateSuccess, 30+i))
	}
	p.History["PROJ-DOCS"] = docs
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"PROJ-DOCS: only its last 25 builds were searched; older children may be missing"}, tr.Warnings)

	docs[24].StartedAt = treeT0.Add(-time.Hour) // the window reaches past the root
	tr, err = treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Empty(t, tr.Warnings)
}

func TestBuildTreeAProjectThatCannotBeListed(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Projects = []string{"PROJ", "OPS", "LAB"}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Len(t, tr.Warnings, 1)
	require.Contains(t, tr.Warnings[0], "project LAB: its plans could not be listed")
}

func TestBuildTreeWithoutAConfiguredScope(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Projects = nil
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(PROJ-DOCS-30)", shape(tr.Root), "only the root's project is searched")
	require.Zero(t, svc.P.(*fake.Provider).ListCalls("OPS-DEPLOY"))
}

func TestBuildTreeRootOnABranch(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-API12-5": {tb("PROJ-API12-5", "Manual run by jdoe", provider.StateSuccess, 0)}}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API12-5", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API12-5", shape(tr.Root))
	require.Equal(t, provider.StateSuccess, tr.State)
}

func TestBuildTreeUnknownRoot(t *testing.T) {
	_, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-999", TreeOptions{})
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestTreeState(t *testing.T) {
	ok := &TreeNode{Build: provider.Build{State: provider.StateSuccess}}
	require.Equal(t, provider.StateSuccess, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{ok}}))
	require.Equal(t, provider.StateFailed, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{NotRun: true}}}))
	require.Equal(t, provider.StateFailed, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{Build: provider.Build{State: provider.StateStopped}}}}))
	require.Equal(t, provider.StateRunning, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{NotRun: true}, {Build: provider.Build{State: provider.StateQueued}}}}))
}

func TestTreeNodeWalk(t *testing.T) {
	tr, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	var got []string
	tr.Root.Walk(func(n *TreeNode, depth int) { got = append(got, fmt.Sprintf("%d:%s", depth, n.Build.Key)) })
	require.Equal(t, []string{"0:PROJ-API-142", "1:OPS-DEPLOY-88", "2:OPS-SMOKE-51", "1:PROJ-DOCS-30"}, got)
}
```

Append to `internal/provider/bamboo/read_test.go`:

```go
// TestRecordedChildReason checks the reason of a recorded child build maps to
// the plain text the build tree matches on.
func TestRecordedChildReason(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "recorded", "children_child_0.json")); err != nil {
		t.Skip("children_child_0.json not recorded")
	}
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/LAB-CHILD-1": {fixture: "recorded/children_child_0.json"},
	})
	b, err := c.GetBuild(ctx, "LAB-CHILD-1")
	require.NoError(t, err)
	assert.Equal(t, "Child of LAB-PARENT-2", b.Reason)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run 'Tree|ParentOf' -v; go test ./internal/provider/bamboo/ -run TestRecordedChildReason -v`
Expected: app build failure — `undefined: TreeOptions`; the recorded test PASSES already (it pins an existing mapping).

- [ ] **Step 3: Implement `internal/app/tree.go`**

```go
package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// treeWindow is how many recent builds of each candidate plan are searched
// for children; treeFetch is how many requests run at a time.
const (
	treeWindow = 25
	treeFetch  = 4
)

var childOfRe = regexp.MustCompile(`^Child of ([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)$`)

// ParentOf returns the build a build reason names as its parent: Bamboo
// gives a child build the reason "Child of PROJ-API-142".
func ParentOf(reason string) (string, bool) {
	m := childOfRe.FindStringSubmatch(reason)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// TreeOptions shape a build tree.
type TreeOptions struct {
	Depth int // levels of builds below the root; 0 means all
}

// TreeNode is one build of a tree, or a configured child that never ran.
type TreeNode struct {
	PlanKey  string
	Build    provider.Build // zero for a not-run node
	NotRun   bool           // a configured child plan with no build that names the parent
	Loop     bool           // the build already appears above; its children are not followed
	Children []*TreeNode    // by plan key, then build number, newest first
}

// Walk calls f for n and every node below it, parents first; the root is at
// depth 0.
func (n *TreeNode) Walk(f func(n *TreeNode, depth int)) {
	var walk func(n *TreeNode, depth int)
	walk = func(n *TreeNode, depth int) {
		f(n, depth)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
}

// Tree is a build and every build it caused.
type Tree struct {
	Root     *TreeNode
	State    provider.State // TreeState of the root
	Warnings []string       // sorted; never change the result
}

// TreeState is running while any build runs, failed when any build did not
// succeed or a configured child did not run, success otherwise.
func TreeState(root *TreeNode) provider.State {
	running, bad := false, false
	root.Walk(func(n *TreeNode, _ int) {
		switch {
		case n.NotRun:
			bad = true
		case !n.Build.State.Finished():
			running = true
		case n.Build.State != provider.StateSuccess:
			bad = true
		}
	})
	switch {
	case running:
		return provider.StateRunning
	case bad:
		return provider.StateFailed
	}
	return provider.StateSuccess
}

// BuildTree reads build key and every build it caused. It never triggers or
// stops anything.
func (s *Service) BuildTree(ctx context.Context, key string, o TreeOptions) (Tree, error) {
	return s.buildTree(ctx, key, o, nil)
}

// buildTree is BuildTree reusing done: builds already known to have
// finished are not read again.
func (s *Service) buildTree(ctx context.Context, key string, o TreeOptions, done map[string]provider.Build) (Tree, error) {
	root, ok := done[key]
	if !ok {
		var err error
		if root, err = s.Build(ctx, key); err != nil {
			return Tree{}, err
		}
	}
	configured, err := s.Cfg.Children()
	if err != nil {
		return Tree{}, err
	}
	plans, warnings, err := s.treeCandidates(ctx, root.PlanKey, configured)
	if err != nil {
		return Tree{}, err
	}
	idx, windows, more := s.treeIndex(ctx, plans)
	warnings = append(warnings, more...)
	t := Tree{Root: &TreeNode{PlanKey: root.PlanKey, Build: root}}
	treeGrow(t.Root, idx, configured, o.Depth, 0, map[string]bool{root.Key: true})
	warnings = append(warnings, s.treeDetails(ctx, t.Root, done)...)
	warnings = append(warnings, windowWarnings(windows, root)...)
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	sort.Strings(warnings)
	t.Warnings, t.State = warnings, TreeState(t.Root)
	return t, nil
}

// treeCandidates is every plan of the scoped projects and of the root's
// project, and every plan the children map names, sorted.
func (s *Service) treeCandidates(ctx context.Context, rootPlan string, configured map[string][]string) ([]string, []string, error) {
	set := map[string]bool{}
	projects := slices.Clone(s.ProjectKeys())
	if p, _, ok := strings.Cut(rootPlan, "-"); ok && !slices.Contains(projects, p) {
		projects = append(projects, p)
	}
	var warnings []string
	var firstErr error
	for _, p := range projects {
		plans, err := s.P.ListPlans(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
			warnings = append(warnings, fmt.Sprintf("project %s: its plans could not be listed: %s", p, describeErr(err)))
			continue
		}
		for _, pl := range plans {
			set[pl.Key] = true
		}
	}
	for k, vs := range configured {
		set[k] = true
		for _, v := range vs {
			set[v] = true
		}
	}
	if len(set) == 0 && firstErr != nil {
		return nil, nil, firstErr
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, warnings, nil
}

// planWindow is what the search saw of one plan: whether it returned a full
// window, and when its oldest returned build started.
type planWindow struct {
	full   bool
	oldest time.Time
}

// treeIndex reads the recent builds of every plan and indexes each child
// build under the parent its reason names.
func (s *Service) treeIndex(ctx context.Context, plans []string) (map[string][]provider.Build, map[string]planWindow, []string) {
	type result struct {
		builds []provider.Build
		err    error
	}
	results := make([]result, len(plans))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	for i, p := range plans {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.ListBuilds(ctx, p, provider.ListOptions{Limit: treeWindow})
			results[i] = result{builds: b, err: err}
		}(i, p)
	}
	wg.Wait()
	idx := map[string][]provider.Build{}
	windows := map[string]planWindow{}
	var warnings []string
	for i, r := range results {
		if r.err != nil {
			if ctx.Err() == nil {
				warnings = append(warnings, fmt.Sprintf("%s: its builds could not be read: %s", plans[i], describeErr(r.err)))
			}
			continue
		}
		w := planWindow{full: len(r.builds) >= treeWindow}
		for _, b := range r.builds {
			if !b.StartedAt.IsZero() && (w.oldest.IsZero() || b.StartedAt.Before(w.oldest)) {
				w.oldest = b.StartedAt
			}
			if parent, ok := ParentOf(b.Reason); ok {
				idx[parent] = append(idx[parent], b)
			}
		}
		windows[plans[i]] = w
	}
	return idx, windows, warnings
}

// treeGrow adds the children of n from the index, then theirs. path holds
// the builds from the root to n.
func treeGrow(n *TreeNode, idx map[string][]provider.Build, configured map[string][]string, depth, level int, path map[string]bool) {
	if depth > 0 && level >= depth {
		return
	}
	listed, has := configured[n.PlanKey]
	var kids []*TreeNode
	for _, b := range idx[n.Build.Key] {
		if has && !slices.Contains(listed, b.PlanKey) {
			continue
		}
		kids = append(kids, &TreeNode{PlanKey: b.PlanKey, Build: b})
	}
	for _, p := range listed {
		if !slices.ContainsFunc(kids, func(k *TreeNode) bool { return k.PlanKey == p }) {
			kids = append(kids, &TreeNode{PlanKey: p, NotRun: true})
		}
	}
	sort.SliceStable(kids, func(i, j int) bool {
		if kids[i].PlanKey != kids[j].PlanKey {
			return kids[i].PlanKey < kids[j].PlanKey
		}
		return kids[i].Build.Number > kids[j].Build.Number
	})
	n.Children = kids
	for _, k := range kids {
		if k.NotRun {
			continue
		}
		if path[k.Build.Key] {
			k.Loop = true
			continue
		}
		path[k.Build.Key] = true
		treeGrow(k, idx, configured, depth, level+1, path)
		delete(path, k.Build.Key)
	}
}

// treeDetails reads every build node below the root with GetBuild, for its
// stages, jobs and failed tests; done builds are reused.
func (s *Service) treeDetails(ctx context.Context, root *TreeNode, done map[string]provider.Build) []string {
	var nodes []*TreeNode
	root.Walk(func(n *TreeNode, depth int) {
		if depth > 0 && !n.NotRun && !n.Loop {
			nodes = append(nodes, n)
		}
	})
	warnings := make([]string, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	for i, n := range nodes {
		if b, ok := done[n.Build.Key]; ok {
			n.Build = b
			continue
		}
		wg.Add(1)
		go func(i int, n *TreeNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.GetBuild(ctx, n.Build.Key)
			if err != nil {
				if ctx.Err() == nil {
					warnings[i] = fmt.Sprintf("%s: its stages could not be read: %s", n.Build.Key, describeErr(err))
				}
				return
			}
			n.Build = b
		}(i, n)
	}
	wg.Wait()
	return slices.DeleteFunc(warnings, func(w string) bool { return w == "" })
}

// windowWarnings names the plans whose full window started after the root:
// older builds of theirs, unread, may be children.
func windowWarnings(windows map[string]planWindow, root provider.Build) []string {
	if root.StartedAt.IsZero() {
		return nil
	}
	var out []string
	for plan, w := range windows {
		if w.full && w.oldest.After(root.StartedAt) {
			out = append(out, fmt.Sprintf("%s: only its last %d builds were searched; older children may be missing", plan, treeWindow))
		}
	}
	return out
}
```

(`treeT0` builds start at minute 0 for the root; in `TestBuildTreeWindowWarning` every PROJ-DOCS build starts after it, so the warning shows; moving the oldest before the root removes it.)

- [ ] **Step 4: Run them to see them pass**

Run: `go test -race ./internal/app/ -run 'Tree|ParentOf|TestAppStaysUIReady' -v && go test ./internal/provider/bamboo/ -run TestRecordedChildReason -v`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/app/tree.go internal/app/tree_test.go internal/provider/bamboo/read_test.go
git commit -m "feat: build the tree of builds a build triggered"
```

---

### Task 3: Watch the tree

**Files:**
- Modify: `internal/app/tree.go` (append)
- Test: `internal/app/tree_watch_test.go`

**Interfaces:**
- Consumes: `buildTree`, `Tree`, `TreeState`, `TreeNode.Walk` (Task 2).
- Produces:
  - `type TreeEventType string`: `TreeUpdate "update"`, `TreeDone "done"`, `TreeError "error"`
  - `type TreeEvent struct { Type TreeEventType; Time time.Time; Tree Tree; Err error }`
  - `func (s *Service) WatchTree(ctx context.Context, key string, o TreeOptions) <-chan TreeEvent`

- [ ] **Step 1: Write the failing tests**

```go
package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// hookClock is the fake clock, calling hook with the number of each wait
// before it fires.
type hookClock struct {
	*fakeClock
	mu   sync.Mutex
	n    int
	hook func(n int)
}

func (c *hookClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()
	if c.hook != nil {
		c.hook(n)
	}
	return c.fakeClock.After(d)
}

func collectTree(ch <-chan TreeEvent) []TreeEvent {
	var out []TreeEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestWatchTreeFollowsAChildThatAppearsLater(t *testing.T) {
	p := treeFake()
	p.History = map[string][]provider.Build{"PROJ-API": {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)}}
	p.Sequences = map[string][]provider.Build{"PROJ-API-142": {
		tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0),
		tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0),
	}}
	svc := treeService(t, p)
	clock := &hookClock{fakeClock: newClock(), hook: func(n int) {
		if n == 1 { // the parent ends and Bamboo queues its child
			p.History["OPS-DEPLOY"] = []provider.Build{tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5)}
		}
	}}
	svc.Clock = clock
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Len(t, evs, 3)
	require.Equal(t, TreeUpdate, evs[0].Type)
	require.Equal(t, provider.StateRunning, evs[0].Tree.State)
	require.Equal(t, TreeUpdate, evs[1].Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88)", shape(evs[1].Tree.Root))
	require.Equal(t, TreeDone, evs[2].Type, "done after one more refresh found nothing new")
	require.Equal(t, provider.StateSuccess, evs[2].Tree.State)
	require.Equal(t, []time.Duration{10 * time.Second, 10 * time.Second}, clock.Waits())
}

func TestWatchTreeSendsOnlyChanges(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-DOCS-30": {
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateSuccess, 5),
	}}
	svc := treeService(t, p)
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	var types []TreeEventType
	for _, e := range evs {
		types = append(types, e.Type)
	}
	require.Equal(t, []TreeEventType{TreeUpdate, TreeUpdate, TreeDone}, types, "the unchanged second poll sends nothing")
	require.Equal(t, provider.StateFailed, evs[2].Tree.State, "OPS-SMOKE-51 failed")
}

func TestWatchTreeGivesUpAfterThreeFailedRefreshes(t *testing.T) {
	svc := treeService(t, treeFake())
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-999", TreeOptions{}))
	require.Len(t, evs, 1)
	require.Equal(t, TreeError, evs[0].Type)
	require.Len(t, svc.Clock.(*fakeClock).Waits(), 2, "three attempts, two waits")
}

func TestWatchTreeStopsWhenCancelled(t *testing.T) {
	svc := treeService(t, treeFake())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, collectTree(svc.WatchTree(ctx, "PROJ-API-142", TreeOptions{})))
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/app/ -run WatchTree -v`
Expected: build failure — `undefined: TreeEvent`.

- [ ] **Step 3: Implement** — append to `internal/app/tree.go` (add `"strconv"` is not needed; `strings` and `time` are already imported):

```go
// treePoll is the time between two refreshes of a watched tree; treeTries
// is how many failed refreshes in a row end the watch.
const (
	treePoll  = 10 * time.Second
	treeTries = 3
)

// TreeEventType names a step of a watched tree.
type TreeEventType string

const (
	TreeUpdate TreeEventType = "update" // the tree changed (always sent for the first refresh)
	TreeDone   TreeEventType = "done"   // every build finished and nothing new appeared; last event
	TreeError  TreeEventType = "error"  // refreshes kept failing; last event
)

// TreeEvent is one step of a watched tree.
type TreeEvent struct {
	Type TreeEventType
	Time time.Time
	Tree Tree
	Err  error
}

// WatchTree refreshes the tree of key every treePoll and emits it when it
// changed. It ends once every build has finished and one more refresh
// found nothing new, since Bamboo queues children when their parent ends.
// Ending ctx stops watching; no build is stopped.
func (s *Service) WatchTree(ctx context.Context, key string, o TreeOptions) <-chan TreeEvent {
	ch := make(chan TreeEvent, 4)
	go func() {
		defer close(ch)
		send := func(e TreeEvent) bool {
			e.Time = s.Clock.Now()
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		done := map[string]provider.Build{}
		var last, settled string
		fails := 0
		for {
			t, err := s.buildTree(ctx, key, o, done)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fails++
				if fails >= treeTries {
					send(TreeEvent{Type: TreeError, Err: err})
					return
				}
			} else {
				fails = 0
				t.Root.Walk(func(n *TreeNode, _ int) {
					if !n.NotRun && !n.Loop && n.Build.State.Finished() {
						done[n.Build.Key] = n.Build
					}
				})
				sig := treeSignature(t)
				if sig != last {
					if !send(TreeEvent{Type: TreeUpdate, Tree: t}) {
						return
					}
					last = sig
				}
				if t.State == provider.StateRunning {
					settled = ""
				} else if settled == sig {
					send(TreeEvent{Type: TreeDone, Tree: t})
					return
				} else {
					settled = sig
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.Clock.After(treePoll):
			}
		}
	}()
	return ch
}

// treeSignature is what a watcher compares between refreshes: every node,
// its state and its stages' states.
func treeSignature(t Tree) string {
	var b strings.Builder
	t.Root.Walk(func(n *TreeNode, depth int) {
		fmt.Fprintf(&b, "%d %s %s %s %t %t", depth, n.PlanKey, n.Build.Key, n.Build.State, n.NotRun, n.Loop)
		for _, st := range n.Build.Stages {
			fmt.Fprintf(&b, " %s=%s", st.Name, st.State)
		}
		b.WriteByte('\n')
	})
	return b.String()
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test -race ./internal/app/ -run 'WatchTree|Tree' -v`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/app/
git commit -m "feat: watch a build tree until every build has finished"
```

---

### Task 4: Render the tree as text and JSON

**Files:**
- Create: `internal/view/tree.go`
- Test: `internal/view/tree_test.go`, golden `internal/view/testdata/tree.golden`, `internal/view/testdata/tree_failed.golden`

**Interfaces:**
- Consumes: `app.Tree`, `app.TreeNode`, `app.TreeState` (Task 2); `BuildJSON` (existing).
- Produces:
  - `func TreeText(o Out, t app.Tree, failedOnly bool) error`
  - `type TreeDoc struct { State string \`json:"state"\`; Warnings []string \`json:"warnings"\`; Root TreeNodeDoc \`json:"root"\` }`
  - `type TreeNodeDoc struct { PlanKey string \`json:"plan_key"\`; Status string \`json:"status"\`; Build *BuildDoc \`json:"build"\`; Children []TreeNodeDoc \`json:"children"\` }`
  - `func TreeJSON(t app.Tree, failedOnly bool) TreeDoc`

- [ ] **Step 1: Write the failing tests**

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

func tnode(key string, st provider.State, d time.Duration, kids ...*app.TreeNode) *app.TreeNode {
	i := strings.LastIndex(key, "-")
	return &app.TreeNode{PlanKey: key[:i], Build: provider.Build{Key: key, PlanKey: key[:i], State: st, Duration: d,
		URL: "https://bamboo.example.com/browse/" + key}, Children: kids}
}

func stage(name string, st provider.State, d time.Duration, failedJobs ...string) provider.Stage {
	s := provider.Stage{Name: name, State: st, Duration: d}
	for _, j := range failedJobs {
		s.Jobs = append(s.Jobs, provider.Job{Name: j, State: provider.StateFailed, Key: "X-" + j})
	}
	return s
}

// sampleTree is the tree of the spec's §6.1.
func sampleTree() app.Tree {
	smoke := tnode("OPS-SMOKE-51", provider.StateFailed, 100*time.Second)
	smoke.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, 100*time.Second, "Smoke tests")}
	smoke.Build.FailedTests = []string{"a", "b", "c"}
	deploy := tnode("OPS-DEPLOY-88", provider.StateSuccess, 192*time.Second, smoke)
	deploy.Build.Stages = []provider.Stage{stage("Deploy", provider.StateSuccess, 192*time.Second)}
	docs := tnode("PROJ-DOCS-30", provider.StateRunning, 0)
	docs.Build.StartedAt = fixedNow.Add(-45 * time.Second)
	audit := &app.TreeNode{PlanKey: "OPS-AUDIT", NotRun: true}
	root := tnode("PROJ-API-142", provider.StateFailed, 723*time.Second, deploy, audit, docs)
	root.Build.Stages = []provider.Stage{stage("Build", provider.StateSuccess, 130*time.Second), stage("Test", provider.StateSuccess, 295*time.Second)}
	return app.Tree{Root: root, State: app.TreeState(root)}
}

func TestTreeTextGolden(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, sampleTree(), false))
	golden(t, "tree", buf.String())
}

func TestTreeTextFailedOnly(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, sampleTree(), true))
	out := buf.String()
	require.NotContains(t, out, "PROJ-DOCS-30", "a running branch with no failure is left out")
	require.Contains(t, out, "OPS-SMOKE-51")
	require.Contains(t, out, "OPS-AUDIT")
	golden(t, "tree_failed", out)
}

func TestTreeTextFailedOnlyOnASuccessfulTree(t *testing.T) {
	root := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, tnode("OPS-DEPLOY-88", provider.StateSuccess, time.Minute))
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, app.Tree{Root: root, State: provider.StateSuccess}, true))
	require.Equal(t, 1, strings.Count(buf.String(), "\n"), "the root line only")
}

func TestTreeTextLoopAndNext(t *testing.T) {
	loop := tnode("PROJ-API-142", provider.StateFailed, 0)
	loop.Loop = true
	smoke := tnode("OPS-SMOKE-51", provider.StateFailed, time.Minute, loop)
	smoke.Build.Stages = []provider.Stage{stage("Smoke", provider.StateFailed, time.Minute, "Smoke tests")}
	root := tnode("PROJ-API-142", provider.StateFailed, time.Minute, smoke)
	o, buf := testOut(false)
	require.NoError(t, TreeText(o, app.Tree{Root: root, State: provider.StateFailed}, false))
	out := buf.String()
	require.Contains(t, out, "↺ already shown above")
	require.Contains(t, out, "next: bam logs OPS-SMOKE-51 --failed\n")
	require.NotContains(t, out, "next: bam logs PROJ-API-142", "PROJ-API-142 has no failed job")
}

func TestTreeJSON(t *testing.T) {
	data, err := json.Marshal(TreeJSON(sampleTree(), false))
	require.NoError(t, err)
	var doc struct {
		State    string   `json:"state"`
		Warnings []string `json:"warnings"`
		Root     struct {
			PlanKey  string         `json:"plan_key"`
			Status   string         `json:"status"`
			Build    map[string]any `json:"build"`
			Children []struct {
				PlanKey  string         `json:"plan_key"`
				Status   string         `json:"status"`
				Build    map[string]any `json:"build"`
				Children []any          `json:"children"`
			} `json:"children"`
		} `json:"root"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Equal(t, "running", doc.State)
	require.NotNil(t, doc.Warnings, "always an array")
	require.Equal(t, "PROJ-API", doc.Root.PlanKey)
	require.Equal(t, "failed", doc.Root.Status)
	require.Equal(t, "PROJ-API-142", doc.Root.Build["key"])
	require.Len(t, doc.Root.Children, 3)
	audit := doc.Root.Children[1]
	require.Equal(t, "not_run", audit.Status)
	require.Nil(t, audit.Build)
	require.NotNil(t, audit.Children)
	require.NotContains(t, string(data), `"progress"`)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/ -run 'Tree' -v`
Expected: build failure — `undefined: TreeText`.

- [ ] **Step 3: Implement `internal/view/tree.go`**

```go
package view

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
)

// treeBad reports whether a node failed, was stopped or did not run.
func treeBad(n *app.TreeNode) bool {
	return n.NotRun || (n.Build.State.Finished() && n.Build.State != provider.StateSuccess)
}

// pruneFailed keeps the nodes on a path to a bad node; the root always stays.
func pruneFailed(n *app.TreeNode) *app.TreeNode {
	c := *n
	c.Children = nil
	for _, k := range n.Children {
		if p := pruneFailed(k); treeBad(k) || len(p.Children) > 0 {
			c.Children = append(c.Children, p)
		}
	}
	return &c
}

// TreeText prints a build tree: a line per build, its stages under it, then
// its children; then one next: line per failed build with a failed job.
func TreeText(o Out, t app.Tree, failedOnly bool) error {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	showStages := !failedOnly || len(root.Children) > 0 || treeBad(root)
	tb := Table{Headers: []string{"", "", "", ""}}
	tb.Rows = append(tb.Rows, treeRow(o, root, ""))
	var next []string
	if root.Build.State == provider.StateFailed && len(root.Build.FailedJobs()) > 0 {
		next = append(next, root.Build.Key)
	}
	var add func(n *app.TreeNode, indent string, stages bool)
	add = func(n *app.TreeNode, indent string, stages bool) {
		type item struct {
			stage *provider.Stage
			node  *app.TreeNode
		}
		var items []item
		if stages && !n.NotRun && !n.Loop {
			for i := range n.Build.Stages {
				items = append(items, item{stage: &n.Build.Stages[i]})
			}
		}
		for _, k := range n.Children {
			items = append(items, item{node: k})
		}
		for i, it := range items {
			branch, more := "├─ ", "│  "
			if i == len(items)-1 {
				branch, more = "└─ ", "   "
			}
			if it.stage != nil {
				tb.Rows = append(tb.Rows, stageRow(o, n.Build, *it.stage, indent+branch))
				continue
			}
			tb.Rows = append(tb.Rows, treeRow(o, it.node, indent+branch))
			if !it.node.NotRun && !it.node.Loop && it.node.Build.State == provider.StateFailed && len(it.node.Build.FailedJobs()) > 0 {
				next = append(next, it.node.Build.Key)
			}
			add(it.node, indent+more, true)
		}
	}
	add(root, "", showStages)
	if err := RenderRows(o, tb); err != nil {
		return err
	}
	for _, k := range next {
		if _, err := fmt.Fprintf(o.W, "next: bam logs %s --failed\n", k); err != nil {
			return err
		}
	}
	return nil
}

func treeRow(o Out, n *app.TreeNode, prefix string) []string {
	switch {
	case n.NotRun:
		return []string{prefix + n.PlanKey, style.Pending(o.Style) + " not run", "", ""}
	case n.Loop:
		return []string{prefix + o.Key(n.Build.Key, n.Build.URL), "↺ already shown above", "", ""}
	}
	extra := ""
	if c := len(n.Build.FailedTests); c > 0 {
		extra = fmt.Sprintf("%d tests failed", c)
	}
	return []string{prefix + o.Key(n.Build.Key, n.Build.URL), o.State(n.Build.State), Duration(elapsed(o, n.Build)), extra}
}

func stageRow(o Out, b provider.Build, s provider.Stage, prefix string) []string {
	extra := ""
	if s.State == provider.StateFailed {
		var names []string
		for _, j := range s.Jobs {
			if j.State == provider.StateFailed {
				names = append(names, j.Name)
			}
		}
		extra = strings.Join(names, ", ")
	}
	return []string{prefix + "stage " + s.Name, o.State(s.State), Duration(s.Duration), extra}
}

type TreeDoc struct {
	State    string      `json:"state"`
	Warnings []string    `json:"warnings"`
	Root     TreeNodeDoc `json:"root"`
}

type TreeNodeDoc struct {
	PlanKey  string        `json:"plan_key"`
	Status   string        `json:"status"`
	Build    *BuildDoc     `json:"build"`
	Children []TreeNodeDoc `json:"children"`
}

// TreeJSON is bam build tree --json.
func TreeJSON(t app.Tree, failedOnly bool) TreeDoc {
	root := t.Root
	if failedOnly {
		root = pruneFailed(root)
	}
	return TreeDoc{State: string(t.State), Warnings: nonNil(t.Warnings), Root: treeNodeDoc(root)}
}

func treeNodeDoc(n *app.TreeNode) TreeNodeDoc {
	d := TreeNodeDoc{PlanKey: n.PlanKey, Status: string(n.Build.State), Children: []TreeNodeDoc{}}
	switch {
	case n.NotRun:
		d.Status = "not_run"
		return d
	case n.Loop:
		d.Status = "loop"
	}
	b := BuildJSON(n.Build)
	d.Build = &b
	if n.Loop {
		return d
	}
	for _, k := range n.Children {
		d.Children = append(d.Children, treeNodeDoc(k))
	}
	return d
}
```

(`BuildJSON` returns a `BuildDoc` with `Progress` nil, so `progress` is absent. `style.Pending` is the dim `·`.)

- [ ] **Step 4: Write the goldens and review them**

Run: `go test ./internal/view/ -run 'TestTreeText(Golden|FailedOnly)$' -update && cat internal/view/testdata/tree.golden internal/view/testdata/tree_failed.golden`
Expected `tree.golden`: the root line `PROJ-API-142  ✗ failed  12m03s`; `├─ stage Build`, `├─ stage Test`; `├─ OPS-AUDIT  · not run`; `├─ OPS-DEPLOY-88` with `│  ├─ stage Deploy` and `│  └─ OPS-SMOKE-51 … 3 tests failed`, then `│     └─ stage Smoke … Smoke tests`; `└─ PROJ-DOCS-30  ▸ running  45s`; the last line `next: bam logs OPS-SMOKE-51 --failed`. Children are in the order the test built them (the app sorts; the view does not). `tree_failed.golden` has no `PROJ-DOCS-30` line. Fix the renderer, not the golden, if a line is wrong.

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/view/ -v -run Tree`
Expected: PASS.

- [ ] **Step 6: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/view/
git commit -m "feat: render a build tree as text and JSON"
```

---

### Task 5: Watch renderers for the tree

**Files:**
- Modify: `internal/view/tree.go` (append)
- Test: `internal/view/tree_watch_test.go`

**Interfaces:**
- Consumes: `app.TreeEvent` (Task 3); `TreeText`, `TreeJSON` (Task 4).
- Produces: `type TreeRenderer interface{ Event(e app.TreeEvent) }`; `NewTreeLive(o Out, failedOnly bool) *TreeLive`; `NewTreeLines(o Out, failedOnly bool) *TreeLines`; `NewTreeNDJSON(o Out, failedOnly bool) *TreeNDJSON`.

- [ ] **Step 1: Write the failing tests**

```go
package view

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var treeAt = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func treeEv(typ app.TreeEventType, t app.Tree) app.TreeEvent {
	return app.TreeEvent{Type: typ, Time: treeAt, Tree: t}
}

func twoTrees() (app.Tree, app.Tree) {
	running := tnode("PROJ-API-142", provider.StateRunning, 0)
	r := app.Tree{Root: running, State: provider.StateRunning}
	child := tnode("OPS-DEPLOY-88", provider.StateSuccess, time.Minute)
	done := tnode("PROJ-API-142", provider.StateSuccess, time.Minute, child)
	return r, app.Tree{Root: done, State: provider.StateSuccess}
}

func TestTreeLinesPrintsChangesThenTheTree(t *testing.T) {
	first, last := twoTrees()
	o, buf := testOut(false)
	l := NewTreeLines(o, false)
	l.Event(treeEv(app.TreeUpdate, first))
	l.Event(treeEv(app.TreeUpdate, last))
	l.Event(treeEv(app.TreeDone, last))
	out := buf.String()
	require.True(t, strings.HasPrefix(out, "2026-10-08T09:00:00Z PROJ-API-142 running\n"+
		"2026-10-08T09:00:00Z PROJ-API-142 success\n"+
		"2026-10-08T09:00:00Z OPS-DEPLOY-88 success\n"), out)
	require.Contains(t, out, "└─ OPS-DEPLOY-88", "the final tree follows")
}

func TestTreeLiveRedrawsInPlace(t *testing.T) {
	first, last := twoTrees()
	o, buf := testOut(true)
	l := NewTreeLive(o, false)
	l.Event(treeEv(app.TreeUpdate, first))
	l.Event(treeEv(app.TreeUpdate, last))
	require.Contains(t, buf.String(), "\x1b[1A\x1b[J", "the one-line first tree is redrawn")
	require.True(t, strings.HasSuffix(buf.String(), "OPS-DEPLOY-88  ✓ success  1m00s\n"), buf.String())
}

func TestTreeNDJSONWritesATreePerUpdate(t *testing.T) {
	first, last := twoTrees()
	var b bytes.Buffer
	n := NewTreeNDJSON(Out{W: &b}, false)
	n.Event(treeEv(app.TreeUpdate, first))
	n.Event(treeEv(app.TreeUpdate, last))
	n.Event(treeEv(app.TreeDone, last))
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	require.Len(t, lines, 2, "done repeats the last update, so it is not written")
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &doc))
	require.Equal(t, "success", doc["state"])
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/view/ -run 'TreeLines|TreeLive|TreeNDJSON' -v`
Expected: build failure — `undefined: NewTreeLines`.

- [ ] **Step 3: Implement** — append to `internal/view/tree.go` (add `"time"` to its imports):

```go
// TreeRenderer shows a watched tree.
type TreeRenderer interface {
	Event(e app.TreeEvent)
}

// TreeLive redraws the tree in place on a TTY.
type TreeLive struct {
	o      Out
	failed bool
	lines  int
}

func NewTreeLive(o Out, failedOnly bool) *TreeLive { return &TreeLive{o: o, failed: failedOnly} }

func (l *TreeLive) Event(e app.TreeEvent) {
	if e.Type == app.TreeError {
		return
	}
	var b strings.Builder
	o := l.o
	o.W = &b
	_ = TreeText(o, e.Tree, l.failed)
	text := b.String()
	if l.lines > 0 {
		fmt.Fprintf(l.o.W, "\x1b[%dA\x1b[J", l.lines)
	}
	fmt.Fprint(l.o.W, text)
	l.lines = strings.Count(text, "\n")
}

// TreeLines prints one timestamped line per node whose state changed, for
// pipes and CI logs, and the whole tree at the end.
type TreeLines struct {
	o      Out
	failed bool
	seen   map[string]string
}

func NewTreeLines(o Out, failedOnly bool) *TreeLines {
	return &TreeLines{o: o, failed: failedOnly, seen: map[string]string{}}
}

func (l *TreeLines) Event(e app.TreeEvent) {
	switch e.Type {
	case app.TreeUpdate:
		ts := e.Time.UTC().Format(time.RFC3339)
		e.Tree.Root.Walk(func(n *app.TreeNode, _ int) {
			key, state := n.Build.Key, string(n.Build.State)
			switch {
			case n.NotRun:
				key, state = n.PlanKey, "not run"
			case n.Loop:
				return
			}
			if l.seen[key] == state {
				return
			}
			l.seen[key] = state
			fmt.Fprintf(l.o.W, "%s %s %s\n", ts, key, state)
		})
	case app.TreeDone:
		_ = TreeText(l.o, e.Tree, l.failed)
	}
}

// TreeNDJSON writes one tree document per update.
type TreeNDJSON struct {
	o      Out
	failed bool
}

func NewTreeNDJSON(o Out, failedOnly bool) *TreeNDJSON { return &TreeNDJSON{o: o, failed: failedOnly} }

func (n *TreeNDJSON) Event(e app.TreeEvent) {
	if e.Type == app.TreeUpdate {
		_ = WriteNDJSON(n.o.W, TreeJSON(e.Tree, n.failed))
	}
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/view/ -v -run Tree`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/view/
git commit -m "feat: live, line and NDJSON renderers for a watched tree"
```

---

### Task 6: `bam build tree`

**Files:**
- Create: `internal/cli/tree.go`
- Modify: `internal/cli/browse.go` (`addBrowse`: add `newBuildTreeCmd(r)` to the `build` group)
- Test: `internal/cli/tree_test.go`
- Regenerated: `docs/cli/`

**Interfaces:**
- Consumes: `Service.BuildTree`, `Service.WatchTree`, `TreeOptions`, `TreeEvent` (Tasks 2–3); `view.TreeText`, `view.TreeJSON`, the renderers (Tasks 4–5); `runtime.resolveBuildArg`, `resultError`, `view.PrintWarnings` (existing).

- [ ] **Step 1: Write the failing tests**

```go
package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeHarness adds PROJ-PROV-9, a child of PROJ-BUILD-482, ending in final.
func treeHarness(t *testing.T, final provider.State) *harness {
	t.Helper()
	h := newHarness(t)
	child := provider.Build{Key: "PROJ-PROV-9", URL: workOrigin + "/browse/PROJ-PROV-9", PlanKey: "PROJ-PROV", Number: 9,
		State: final, Reason: "Child of PROJ-BUILD-482", StartedAt: fixedNow.Add(-10 * time.Minute)}
	if final == provider.StateFailed {
		child.Stages = []provider.Stage{{Name: "Apply", State: provider.StateFailed,
			Jobs: []provider.Job{{Key: "PROJ-PROV-TF-9", Name: "Terraform", State: provider.StateFailed}}}}
	}
	h.fake.History["PROJ-PROV"] = []provider.Build{child}
	return h
}

func TestBuildTreeExitCodes(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stdout.String(), "PROJ-BUILD-482")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9")

	h = treeHarness(t, provider.StateFailed)
	assert.Equal(t, 1, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stdout.String(), "next: bam logs PROJ-PROV-9 --failed")
}

func TestBuildTreeJSON(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "success", doc["state"])
	kids := doc["root"].(map[string]any)["children"].([]any)
	require.Len(t, kids, 1)
	assert.Equal(t, "PROJ-PROV", kids[0].(map[string]any)["plan_key"])
}

func TestBuildTreeDepthIsCheckedFirst(t *testing.T) {
	h := treeHarness(t, provider.StateSuccess)
	assert.Equal(t, 2, h.run("build", "tree", "PROJ-BUILD-482", "--depth", "0"))
	assert.Contains(t, h.stderr.String(), "--depth takes 1 or more")
	assert.Empty(t, h.connectOpts, "no connection is made")
}

func TestBuildTreeStillRunning(t *testing.T) {
	h := treeHarness(t, provider.StateRunning)
	assert.Equal(t, 0, h.run("build", "tree", "PROJ-BUILD-482"))
	assert.Contains(t, h.stderr.String(), "still running: bam build tree PROJ-BUILD-482 --watch")
}

func TestBuildTreeWatch(t *testing.T) {
	h := treeHarness(t, provider.StateFailed)
	assert.Equal(t, 1, h.run("build", "tree", "PROJ-BUILD-482", "--watch"))
	assert.Contains(t, h.stdout.String(), "PROJ-PROV-9 failed")
	assert.Contains(t, h.stdout.String(), "└─ PROJ-PROV-9", "the final tree")
}

func TestBuildTreeWatchInterrupted(t *testing.T) {
	h := treeHarness(t, provider.StateRunning)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, 130, h.runCtx(ctx, "build", "tree", "PROJ-BUILD-482", "--watch"))
	assert.Contains(t, h.stderr.String(), "still running: bam build tree PROJ-BUILD-482 --watch")
	assert.Empty(t, h.fake.Stopped)
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run BuildTree -v`
Expected: FAIL — `unknown command "tree" for "bam build"` (exit 2 where 0 is expected).

- [ ] **Step 3: Implement**

`internal/cli/browse.go` `addBrowse`: `AddCommand(newBuildListCmd(r), newBuildShowCmd(r), newBuildTreeCmd(r))`.

`internal/cli/tree.go`:

```go
package cli

import (
	"context"
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func newBuildTreeCmd(r *runtime) *cobra.Command {
	var last, watch, failed bool
	var branch string
	var depth int
	cmd := &cobra.Command{
		Use:   "tree [<build>|<plan>|<target>]",
		Short: "Show a build and every build it triggered, as a tree",
		Long: `Show a build and every build it triggered, as a tree.

bam finds a child build by its reason, "Child of <parent>", in the last 25
builds of each plan of the configured projects and of the build's project.
A children: map in the configuration narrows a plan's children and shows a
configured child that never ran. Interrupting --watch never stops a build.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("depth") && depth < 1 {
				return errs.Usagef("--depth takes 1 or more, not %d", depth)
			}
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			o := app.TreeOptions{Depth: depth}
			if watch {
				return r.treeLoop(ctx, svc, key, o, failed)
			}
			t, err := svc.BuildTree(ctx, key, o)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, t.Warnings)
			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, view.TreeJSON(t, failed)); err != nil {
					return err
				}
			} else {
				out, err := r.out()
				if err != nil {
					return err
				}
				if err := view.TreeText(out, t, failed); err != nil {
					return err
				}
			}
			return r.treeResult(t, key)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or target is given")
	f.BoolVar(&watch, "watch", false, "follow the tree until every build has finished")
	f.BoolVar(&failed, "failed", false, "show only the branches that lead to a failure")
	f.IntVar(&depth, "depth", 0, "levels of builds to show below the root (default: all)")
	return cmd
}

// treeResult maps a tree to the exit: 0 when it succeeded or still runs,
// 1 when any build did not succeed or a configured child did not run.
func (r *runtime) treeResult(t app.Tree, key string) error {
	switch t.State {
	case provider.StateSuccess:
		return nil
	case provider.StateRunning:
		r.note("still running: bam build tree "+key+" --watch", "")
		return nil
	}
	return &resultError{State: provider.StateFailed}
}

// treeLoop renders a watched tree. Interrupts stop watching only.
func (r *runtime) treeLoop(ctx context.Context, svc *app.Service, key string, o app.TreeOptions, failed bool) error {
	var rend view.TreeRenderer
	if r.flags.json {
		rend = view.NewTreeNDJSON(view.Out{W: r.env.Stdout}, failed)
	} else {
		out, err := r.out()
		if err != nil {
			return err
		}
		if out.TTY {
			rend = view.NewTreeLive(out, failed)
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
	fmt.Fprintf(r.env.Stderr, "still running: bam build tree %s --watch\n", key)
	return ctx.Err()
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/cli/ -v -run 'BuildTree|Help|Root'`
Expected: PASS.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add internal/cli/ docs/cli/
git commit -m "feat: bam build tree shows a build and every build it triggered"
```

---

### Task 7: Documentation

**Files:**
- Modify: `docs/configuration.md`, `docs/json.md`, `CHANGELOG.md`, `docs/superpowers/specs/2026-10-08-bam-build-tree-design.md` (Status)

- [ ] **Step 1: `docs/configuration.md`** — add a `children` section:

````markdown
### `children`

`bam build tree` finds the builds a build triggered by their reason, "Child of <build>", in the last 25 builds of each plan of your projects. A `children` map narrows that for a plan and shows a configured child that never ran:

```yaml
children:
  PROJ-API: [OPS-DEPLOY, PROJ-DOCS]
  OPS-DEPLOY: [OPS-SMOKE]
```

- Keys and values are plan keys. A plan with an entry gets only the listed plans as children; a listed plan without a matching build shows as `not run` and makes the tree fail.
- Listed plans are searched even outside your projects.
- The project file, the machine file and a machine `repos` entry may each set it; a later one replaces a plan's list (project, then machine, then `repos`).
````

- [ ] **Step 2: `docs/json.md`** — add:

````markdown
## `bam build tree --json`

```json
{"state": "failed", "warnings": [], "root": {"plan_key": "PROJ-API", "status": "failed", "build": {...}, "children": [
  {"plan_key": "OPS-AUDIT", "status": "not_run", "build": null, "children": []}
]}}
```

- `state`: `success`, `failed` or `running`.
- `status`: the build's state, `not_run` for a configured child that never ran, or `loop` for a build already shown above (its `children` is empty).
- `build`: the `bam build show --json` document, without `progress`; `null` for `not_run`.
- `--watch --json` writes one such document per change, one per line.
````

- [ ] **Step 3: `CHANGELOG.md`** — under `## [Unreleased]`:

```markdown
### Added

- `bam build tree` shows a build and every build it triggered, with their stages and failures, and `--watch` follows them to the end.
```

- [ ] **Step 4: Spec status** — set `**Status:** Implemented`.

- [ ] **Step 5: Gate and commit**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH" && go test -race ./... && make lint && make check-fixtures && make docs
git add docs/ CHANGELOG.md
git commit -m "docs: the build tree"
```
