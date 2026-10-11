# bam: the build tree (`bam build tree`)

**Status:** Implemented | **Date:** 2026-10-08
**Issue:** #27 (this spec covers the tree; rerunning failed nodes is the next spec)
**Builds on:** [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) (§2.2 argument resolution, §5 exit codes, §7 JSON)
**Evidence:** the recordings `internal/provider/bamboo/testdata/recorded/children_*` (PR #38)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

A parent plan triggers child plans when it succeeds; each child can trigger its own. Every child is a separate build with its own stages, jobs and tests. To answer "did my run work?", a developer opens the parent in Bamboo, works out which child plans it started, opens each child, then each stage and job, to find the failure. Bamboo shows no single view of the run.

`bam build tree` shows the parent build and every build it caused, to any depth, with each node's state, and points at the failure.

## 2. What Bamboo offers (from the spike)

- The REST API has no list of a plan's child plans. `dependency/search/{plan}/child|parent` is the UI's plan picker: it needs a search term and returns any plan matching it.
- Neither the parent plan nor the parent build result mentions a child.
- The server-wide `/result` list holds only each plan's newest build.
- A child build names its parent in its build reason: `Child of <a href="…/browse/PROJ-API-142">PROJ-API-142</a>`. `provider.Build.Reason` already holds it as plain text: `Child of PROJ-API-142`.

So bam finds a build's children by reading the recent builds of candidate plans and keeping those whose reason names it.

## 3. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Where children are looked for | The plans of the scoped projects, the root's project, and any plan named in the optional `children:` config | The owner's choice: works with no setup; config narrows the search and makes a child that never ran visible |
| How | One index per refresh: the last 25 builds of every candidate plan, keyed by the parent their reason names | Deeper levels cost no extra requests |
| Window | 25 builds per candidate plan | One page per plan; enough for a recent run. A note says when the window may miss children |
| Branches | Each candidate plan's default branch | Children of branch builds go to the child plan's branch only when Bamboo is configured for it; later work |
| Node detail | One `GetBuild` per node, 4 at a time | Stages, jobs and failed tests are not in the build list |
| Rerun of failed nodes | Not in this spec | The owner's choice: the tree ships first, `--rerun-failed` is the next spec |
| Command name | `bam build tree` | The issue's proposal; a subcommand of `build`, like `show` |

## 4. Configuration: `children`

Optional. A map from a plan key to the plan keys of its children:

```yaml
children:
  PROJ-API: [OPS-DEPLOY, PROJ-DOCS]
  OPS-DEPLOY: [OPS-SMOKE]
```

- Allowed in the project file, the machine file and a machine `repos` entry. Layers merge per plan key with the precedence targets have: project, then machine, then the `repos` entry; a later layer's list replaces an earlier one for the same plan.
- Every key and every listed value must be a plan key (`PROJ-PLAN`); otherwise a config error (exit 3) names the file and the entry. A plan listing itself is a config error.
- For a plan with an entry, only the listed plans count as its children. A listed plan with no build that names the parent becomes a `not run` node. A plan without an entry takes its children from the index (§5).
- The listed plans are always candidates, whatever the scope.
- `docs/configuration.md` documents it.

## 5. Finding the tree: `app`

```go
type TreeOptions struct {
	Depth int // levels below the root to show; 0 means all
}

type TreeNode struct {
	PlanKey  string
	Build    provider.Build // zero for a not-run node
	NotRun   bool           // configured child with no build that names the parent
	Loop     bool           // this build already appears above it; its children are not followed
	Children []*TreeNode    // in plan key order, then build number, newest first
}

type Tree struct {
	Root     *TreeNode
	State    provider.State // §6.4
	Warnings []string       // §5.4
}

func (s *Service) BuildTree(ctx context.Context, key string, o TreeOptions) (Tree, error)
func (s *Service) WatchTree(ctx context.Context, key string, o TreeOptions) <-chan TreeEvent
```

### 5.1 Candidates

The candidate plans are, without duplicates, sorted:

1. every plan of `s.ProjectKeys()` (`ListPlans` per project);
2. every plan of the root build's project;
3. every plan named anywhere in `children:` (keys and values).

### 5.2 Index

For every candidate plan, `ListBuilds(plan, Limit: 25, Unfinished: true)`, at most 4 requests at a time. `Unfinished` matters: without `includeAllStates=true`, Bamboo's result list holds finished builds only, so a queued or running child would never reach the tree, and a watch would end as soon as the root finished. Each build whose reason matches `^Child of ([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)$` is indexed under that parent key. A build that cannot be read for one plan (an error other than cancellation) leaves that plan out and adds a warning; the tree still renders.

A restart rewrites a child's reason to `Rebuilt by <user>`, so a restarted child no longer names its parent in the list. For each build whose reason matches `^Rebuilt\s+by\b`, one `GetBuild` (4 at a time) reads the parent from the build's metadata (`dependency.parent.0`, exposed as `provider.Build.Parent`), and the build is indexed under it. A build's parent never changes, so a watch asks each restarted build once; one whose parent cannot be read adds a warning and is asked again on the next refresh. While a restarted build's parent is unknown, a watch does not settle (§6.3), since the build may belong to the tree. After 3 failed reads in a row bam gives up on it: it is left out of the tree, and every later refresh warns `KEY: its parent could not be read, so it is left out`.

### 5.3 Building the tree

From the root build, recursively: a node's children are the indexed builds that name it; with a `children:` entry for the node's plan, only those of the listed plans, plus a `not run` node for each listed plan with none. Several builds of one child plan that name the same parent (a rerun of the child) all appear, newest first.

A build that already appears on the path from the root is a `loop` node and is not followed. `Depth` stops the walk below that many levels; nodes past it are not fetched.

Each node then gets `GetBuild` (stages, jobs, failed tests), 4 at a time. A node whose `GetBuild` fails keeps the build from the list and adds a warning.

### 5.4 Warnings

- **Window.** A candidate plan that returned a full 25 builds, all started after the root build started, may hold older builds that are children: `PROJ-DOCS: only its last 25 builds were searched; older children may be missing`. A plan with fewer than 25 builds, or whose oldest fetched build started before the root, gets no warning.
- **Unreadable plan or node**, as above.
- Warnings go to stderr in text and to `warnings` in JSON. They never change the exit code.

### 5.5 Errors

- The root key resolves exactly as `bam build show` resolves its argument (§2.2 of the MVP design): a build key, a plan or preset (its latest build, with `--branch`), or `--last`.
- A root build that does not exist: the error `GetBuild` gives today (not found).
- No candidate plan can be listed at all: a Bamboo error (exit 5).

## 6. Command

```
bam build tree [<build>|<plan>|<preset>] [--last] [--branch B] [--watch] [--failed] [--depth N] [--json]
```

### 6.1 Text

```
PROJ-API-142            ✗ failed     12m03s
├─ stage Build          ✓ success     2m10s
├─ stage Test           ✓ success     4m55s
├─ OPS-DEPLOY-88        ✓ success     3m12s
│  ├─ stage Deploy      ✓ success     3m12s
│  └─ OPS-SMOKE-51      ✗ failed      1m40s
│     └─ stage Smoke    ✗ failed      1m40s   Smoke tests: 3 tests failed
├─ PROJ-DOCS-30         ⟳ running     0m45s
└─ OPS-AUDIT            · not run
next: bam logs OPS-SMOKE-51 --failed
```

- A build line: key (a link where links are on), state glyph and title from `view/style`, duration (elapsed for a running build).
- A stage line under its build: the stage's state and duration; a failed stage names its failed jobs and, when Bamboo reports them, the count of failed tests.
- Children follow their build's stages.
- `next:` lines: one `bam logs KEY --failed` per failed build that has a failed job, in tree order.
- A loop node: `PROJ-API-142   ↺ already shown above`.

### 6.2 Flags

- `--failed`: keep only nodes on a path to a failed, stopped or not-run node; stages are shown for those nodes only.
- `--depth N`: levels of builds below the root, `N ≥ 1`; 0 or negative is a usage error (exit 2), checked before any network call.
- `--json`: §7.
- `--watch`: §6.3.

### 6.3 Watching

`WatchTree` emits a `TreeEvent{Type: TreeUpdate|TreeDone|TreeError, Tree, Err}`:

- The first tree at once, then every 10 seconds: the index is read again, and `GetBuild` runs only for nodes that have not finished and for new nodes.
- An update is sent only when a node's state, its stages' states or the set of nodes changed.
- It ends with `TreeDone` once every build node has finished and one more refresh after the last one finished found no new node: Bamboo queues children when their parent ends.
- A refresh that fails is retried; three in a row end with `TreeError`.
- Ending the context stops watching; no build is stopped.
- Text on a TTY redraws the tree in place; on a pipe, one timestamped line per node whose state changed; `--json` writes one tree document per update (NDJSON).

### 6.4 Exit codes

The tree's state: `success` when every build node succeeded and there is no `not run` node; otherwise `failed`. A running node makes the state `running` without `--watch`.

- 0: success.
- 1: any node did not succeed, or a configured child did not run.
- Without `--watch`, a running tree exits 0 and says `still running: bam build tree KEY --watch`.
- 2, 3, 4, 5 as for every command; 130 when interrupted.

## 7. JSON

`bam build tree --json`:

```json
{
  "state": "failed",
  "warnings": [],
  "root": {
    "plan_key": "PROJ-API",
    "status": "failed",
    "build": { "...": "the BuildDoc of bam build show --json" },
    "children": [
      {"plan_key": "OPS-AUDIT", "status": "not_run", "build": null, "children": []}
    ]
  }
}
```

- `status`: the build's state, or `not_run`, or `loop`.
- `build`: exactly the `bam build show --json` document (`BuildDoc`, without `progress`); `null` for `not_run`; for `loop`, the build again, with `children` empty.
- `children`: always an array.
- `--watch --json`: one such document per line, each a full tree.

These names are a public contract from the first release; `docs/json.md` documents them.

## 8. Architecture

- `config`: `Children map[string][]string` on `ProjectFile`, `MachineFile` and `Repo`; `Config.Children() (map[string][]string, error)` merges and validates (§4).
- `app`: `tree.go` with §5; it reuses `ResolveBuild`, `ListPlans`, `ListBuilds`, `GetBuild`. Never prints.
- `view`: `tree.go` — text, live, lines and JSON renderers.
- `cli`: `newBuildTreeCmd` under `bam build`.
- No new dependency; no change to `provider` or `provider/bamboo`.
- The terminal UI is not changed; `BuildTree` and `WatchTree` are what a later panel uses.

## 9. Testing

Test first. The fake provider, the fake clock, no network.

- **`config`**: `children` in each layer, the merge, and each validation error.
- **`app`**:
  - a three-level tree from reasons; children of children; the order of children;
  - a child plan rerun twice: both builds, newest first;
  - a `children:` entry: only its plans; a `not run` node; listed plans become candidates outside the scope;
  - a loop is cut;
  - `Depth`: nodes past it are not fetched (the fake records `GetBuild` calls);
  - the window warning, and no warning when the window reaches past the root;
  - one candidate plan that fails to list: a warning, the rest of the tree;
  - the reason regex: `Child of PROJ-API-142` matches, `Child of PROJ-API-1420` names another build, other reasons do not match;
  - `WatchTree`: updates only on change; a child that appears after its parent finished; done after the extra refresh; three failed refreshes end with an error.
- **`provider/bamboo`**: `TestRecorded*` checks the recorded child reason maps to `Child of LAB-PARENT-2`.
- **`view`**: the text tree (golden), `--failed`, a loop line, a not-run line, the `next:` lines; the JSON document.
- **`cli`** (harness): exit codes 0 and 1; `--depth 0` is exit 2 before any request; a running tree without `--watch` exits 0 with the hint; `--json`.

## 10. Documentation

- `docs/configuration.md`: `children`.
- `docs/json.md`: the tree document.
- `docs/cli/` regenerated.
- `CHANGELOG.md` `[Unreleased]`: one line.
- Issue #27: link this spec; the rerun part stays open.

## 11. Out of scope

- Rerunning failed nodes (`--rerun-failed`, `bam build rerun`): the next spec.
- Children on plan branches.
- Children started by a "trigger plan" task or a script: their reason is not `Child of …`.
- A terminal UI panel.
- A window larger than 25 builds, or a flag to set it.
