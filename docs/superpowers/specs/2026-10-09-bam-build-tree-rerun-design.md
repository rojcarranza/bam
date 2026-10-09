# bam: rerun a build tree's failures, and the tree in the terminal UI

**Status:** Implemented | **Date:** 2026-10-09
**Issue:** #50 (follow-up to #27)
**Builds on:** [`2026-10-08-bam-build-tree-design.md`](2026-10-08-bam-build-tree-design.md) (the tree), [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md) (`Service.Restart`), [`2026-10-06-bam-tui-sets-design.md`](2026-10-06-bam-tui-sets-design.md) (a live screen in the terminal UI)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

`bam build tree` shows where a run failed, but retrying means opening each failed build in Bamboo and pressing "Rerun failed jobs" in the right order. Rerunning too much wastes time; rerunning a child before its parent runs it twice. The terminal UI has no tree at all.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| What reruns | Each failed or stopped build whose ancestors all succeeded ("topmost failures") | The owner's choice: when it succeeds, Bamboo triggers its children again, so restarting them too would run them twice |
| How | `Service.Restart` (PUT `queue/{key}`): Bamboo reruns the build's failed jobs, same key, same variables and commit | Already used and recorded by `bam set rerun --failed` |
| Asking first | A `[y/N]` question listing the builds; `--yes` skips it | Restarting builds on a shared server is not undone |
| After the restart | Follow the tree until it finishes; `--no-watch` exits at once | The owner's choice: the point is to see whether the retry worked |
| Several runs of one child | Only the newest counts; older ones are `superseded` | After a rerun the tree holds the old failure and the new run; the old one must not fail the tree for ever |
| One build | `bam build rerun <build>` | The issue asks for it; no question, since the build is named |
| Terminal UI | A tree screen (`t`, `:tree`) with `F` to rerun | The owner's choice: the feature in both places |

## 3. Superseded runs (a change to the tree)

Among a node's children, builds of the same plan are siblings ordered newest first (tree spec §5.3). From this spec on:

- The newest is current; every older one has `Superseded = true`.
- A superseded node and everything under it do not count toward the tree's state (§6.4 of the tree spec), are dropped by `--failed`, and are never rerun.
- Text: the line ends `earlier run` and is dim; its stages and children are not shown.
- JSON: `status` is `superseded`; `build` and `children` are kept as they are. This adds a value to `status`; no field is renamed or removed.
- `not run` nodes are never superseded (they have no build).

## 4. Choosing what reruns: `app`

```go
// RerunCandidates is the topmost failed or stopped builds of a tree: each
// such build whose ancestors all succeeded. Not-run, loop and superseded
// nodes are never candidates, and nothing below a candidate is.
func RerunCandidates(t Tree) []*TreeNode
```

- A node is a candidate when its build state is `failed` or `stopped`, it is not superseded, not a loop, and every ancestor's state is `success` (or `skipped`).
- The walk does not go below a candidate.
- Order: tree order (pre-order).
- The root can be a candidate.

```go
// RerunResult is what happened to one candidate.
type RerunResult struct {
	Key       string
	Restarted bool  // false: running or succeeded after all, so nothing was restarted
	Err       error
}

// RerunTree restarts each candidate with Restart, one at a time, in order.
func (s *Service) RerunTree(ctx context.Context, cands []*TreeNode) []RerunResult
```

One at a time keeps the order and the requeue wait of `Restart`; a tree rarely has more than a few topmost failures. A failed restart does not stop the others.

## 5. Command line

### 5.1 `bam build tree --rerun-failed`

```
bam build tree [BUILD] --rerun-failed [--yes] [--no-watch] [--json] [--depth N]
```

1. Build the tree (`BuildTree`) and its candidates. No candidate: `nothing to rerun` on stderr, then exit as the tree's state says (0 for a successful or running tree, 1 otherwise).
2. A running tree is refused: `PROJ-API-142's tree is still running; rerun when it has finished` (exit 2). Restarting a build while its siblings still run would race them.
3. The question, on stderr:
   ```
   Rerun the failed jobs of 2 builds?
     OPS-DEPLOY-88   ✗ failed    Deploy › Helm upgrade
     PROJ-DOCS-30    ⊘ stopped
   [y/N]
   ```
   Each line: key, state, and the first failed stage › job when there is one. `y` or `yes` (any case) goes on; anything else, or end of input, stops with `nothing restarted` and exit 0.
4. Without a terminal on stdin, or with `--json`, the question cannot be asked: without `--yes`, exit 2 `--rerun-failed needs --yes when it cannot ask`, checked before anything is restarted.
5. `RerunTree`. One line per candidate on stderr: `restarted OPS-DEPLOY-88`, or `OPS-DEPLOY-88 was not restarted: it is running` / `it succeeded`, or the error.
6. Then, unless `--no-watch`, follow the tree exactly as `--watch` does (a fresh `WatchTree`, so restarted builds are read again) and exit as it does.
7. Exit codes: as `--watch` when following; with `--no-watch`, 0 once every candidate was restarted. Any restart that failed: exit 5 after following (or at once with `--no-watch`), the failure named.
8. `--failed` and `--depth` apply to the display only; candidates come from the whole tree.
9. `--json` with `--rerun-failed --yes`: the restart lines go to stderr, the tree NDJSON to stdout as for `--watch --json`.

### 5.2 `bam build rerun`

```
bam build rerun <build> [--watch]
```

- `<build>` resolves as for `bam build show` (a key, a plan or preset's latest build, `--last`).
- No question.
- Refused with exit 2 when the build is running (`PROJ-DOCS-30 is running; nothing to rerun`) or succeeded (`… succeeded; nothing to rerun`).
- Prints `restarted KEY` and `watch: bam watch KEY`, or with `--watch` follows it exactly as `bam watch` does and exits as it does.

## 6. Terminal UI

### 6.1 Opening the tree screen

- `t` on a build row of the Builds panel, or on a build's detail, opens the tree screen for that build.
- `:tree KEY` from the command bar opens it for any build key; `:tree` alone uses the selected build.
- A new screen, `screenTree`, alongside `screenSet`. `esc` and `q` close it and return where it was opened; closing stops watching only.

### 6.2 The screen

- A header: the root key, the tree's state, and a count line such as `2 failed · 1 running · 4 succeeded`.
- One row per build and per stage, indented with the same box drawing as the CLI; `not run` and superseded rows as in the CLI.
- It follows `WatchTree` while the screen is open; each update redraws, keeping the cursor on the same build when it is still there.
- Warnings go to the status bar, the first with a count (`2 warnings: PROJ-DOCS: only its last 25 builds …`).

### 6.3 Keys

| Key | On the tree screen |
| --- | --- |
| `j` `k` `g` `G` | move between build rows (stage rows are skipped) |
| `enter` | the selected build's detail; `esc` returns to the tree |
| `l` / `a` | the selected build's failed logs / all logs |
| `o` / `y` | open / copy the selected build's URL |
| `r` | refresh at once |
| `F` | rerun the failed builds (§6.4) |
| `esc` `q` | close the screen |

`t` and `F` are existing-style single keys: `t` is new; `F` already means "rerun the failed runs" on the set screen and takes the same meaning here. The help overlay and the key line list both.

### 6.4 `F`

- With no candidate: status bar `nothing to rerun`.
- A running tree: status bar `the tree is still running; rerun when it has finished`.
- Otherwise the confirmation overlay: `Rerun the failed jobs of 2 builds? OPS-DEPLOY-88, PROJ-DOCS-30`. Yes runs `RerunTree` in the background; the candidates' rows show `restarting…`. When it returns, the status bar reports `restarted 2 builds` or the failures, and the screen starts a fresh `WatchTree`.

## 7. Architecture

- `app`: `TreeNode.Superseded`, set in `treeGrow`; `TreeState`, `RerunCandidates`, `RerunTree`. `app` never reads input or prints.
- `view`: superseded lines in text and JSON; `RerunList(o, cands)` for the question's lines.
- `cli`: `--rerun-failed`, `--yes`, `--no-watch` on `build tree`; `newBuildRerunCmd`; the question reads `Env.Stdin`.
- `view/tui`: `screenTree`, its model, keys, the `t` and `:tree` entry points, `F`.
- No new dependency; no change to `provider`.

## 8. Testing

Test first; the fake provider's restart (`RestartResults`, `Sequences`), the fake clock, no network.

- **`app`**:
  - superseded: the older of two runs of one plan, its subtree ignored by `TreeState`; `not run` never superseded;
  - candidates: topmost only (parent ✓ → deploy ✗ → smoke ✗ gives deploy); stopped counts; root as candidate; loop, not-run and superseded excluded; skipped ancestors allowed;
  - `RerunTree`: order, a refused restart (running), an error does not stop the others.
- **`view`**: a superseded line (golden), its JSON status, the question's lines.
- **`cli`**:
  - `--rerun-failed --yes` restarts the candidate and follows to success (exit 0);
  - `y` on stdin with a terminal; `n` restarts nothing (exit 0);
  - no terminal and no `--yes`: exit 2, nothing restarted;
  - a running tree: exit 2;
  - nothing to rerun;
  - `--no-watch`;
  - a failed restart: exit 5;
  - `bam build rerun`: a failed build restarted; a running or succeeded one refused (exit 2); `--watch`.
- **`view/tui`**: `t` opens the screen on the selected build; `:tree KEY`; the screen's rows (golden); cursor on build rows only; `enter` and back; `F` with no candidate, with a running tree, and confirm → restart command and a fresh watch; `TestNoKeyIsBoundTwice` and the key line still fit 80 columns.

## 9. Documentation

- README: the Build trees section gains `--rerun-failed` and `bam build rerun`; the Terminal UI section gains the tree screen.
- `docs/json.md`: `superseded`.
- `docs/cli/` regenerated.
- `CHANGELOG.md` `[Unreleased]`: one line for the rerun, one for the tree screen.
- Issue #50 closed by the implementation PR.

## 10. Out of scope

- Rerunning a `not run` child (it has no build; triggering its plan is `bam run`).
- Rerunning builds below a failure directly.
- Restarting several builds in parallel.
- Rerun in the TUI for a single chosen build (the CLI's `bam build rerun` covers it).
