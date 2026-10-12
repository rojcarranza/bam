# bam: progress while a build tree loads

**Status:** Implemented | **Date:** 2026-10-12
**Issue:** #81
**Builds on:** [`2026-10-08-bam-build-tree-design.md`](2026-10-08-bam-build-tree-design.md) (the tree search) and [`2026-10-10-bam-tui-plan-screen-design.md`](2026-10-10-bam-tui-plan-screen-design.md) (the Tree panel)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

A deep tree takes a while to read. bam lists the candidate plans, reads each plan's builds (deeper for a busy plan), reads the parents of restarted builds, reads each node's detail, then each job's log for builds a script started. A log link to a new project goes back to listing plans and reading builds. Through all of it:

- `bam build tree` prints nothing until it is done.
- `bam build tree --watch` prints nothing until the first refresh ends.
- The tree screen's header says `PROJ-API-142 · loading`; the plan screen's Tree panel says `reading the tree…`.

A slow load looks like a hang.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Which CLI loads show progress | Every tree load: plain `bam build tree`, the first refresh of `--watch`, and `--rerun-failed`'s read before it asks | The owner's choice; all three wait on the same search |
| Later `--watch` refreshes | No progress | They read 5 builds a plan and skip finished builds; they are quick |
| How `app` reports | A callback, `TreeOptions.OnProgress`, plus a `TreeLoading` event on the `WatchTree` channel | `BuildTree` callers stay synchronous; the tree screen already reads the watch channel. A channel-only `LoadTree` would add a second way to read a tree; polling a counter would add tickers in two UIs |
| What a report holds | A snapshot of four counters, each `Done/Total` | Phases overlap: a log link to a new project reads builds while logs are still being counted |
| Totals | Grow as the search learns more; never a percentage | Deeper reads and log links add work mid-load |
| When the CLI shows it | Stdout and stderr are both terminals, no `--json`, `TERM` is not `dumb` | `bam build tree X \| less` and `2>log` stay clean; issue #81 asks for nothing on a pipe or with `--json` |
| Where the CLI writes it | One line on stderr, rewritten in place, cleared before anything else prints | stdout carries only the tree |

## 3. `app`: reporting progress

### 3.1 API

```go
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

type TreeOptions struct {
	Depth      int
	OnProgress func(TreeProgress) // nil reports nothing
}

const TreeLoading TreeEventType = "loading" // the first refresh is still reading; Progress says how far

type TreeEvent struct {
	Type     TreeEventType
	Time     time.Time
	Tree     Tree
	Progress TreeProgress // for TreeLoading
	Err      error
}
```

### 3.2 Counting

An unexported `treeTracker` (in a new `internal/app/tree_progress.go`) holds the snapshot behind a mutex:

- `add(phase, n)` raises a phase's `Total` by `n` before its requests start.
- `done(phase)` raises its `Done` after each request ends, whether it succeeded, failed or was cancelled, so the count never stalls short of the total.
- Every change calls `OnProgress` with a copy, while still holding the lock, so calls are serialized and arrive in order.
- A nil tracker, or one with a nil callback, does nothing; existing callers pay nothing.

Where each phase is counted:

| Phase | `add` | `done` |
| --- | --- | --- |
| `Projects` | `treeCandidates` and `treeAddProjects`, one per project listed | after each `ListPlans` |
| `Plans` | each `treeRead` call (first window, each deeper read, `treeAddProjects`), one per plan | after each `ListBuilds` |
| `Builds` | `treeParents`, each `treeDetails` call and each round of builds a log links to, one per build actually read (not one reused from `done`) | after each `GetBuild` |
| `Jobs` | each `treeReadLinks` round, one per job log actually read (not one cached in `logLinks`) | after each `FetchLog` |

Reading the root build itself is one request and is not counted.

### 3.3 `BuildTree` and `WatchTree`

`buildTree` takes the tracker as a parameter and threads it to the helpers above.

- `BuildTree` builds a tracker from `o.OnProgress`. Every call happens before `BuildTree` returns.
- `WatchTree` ignores `o.OnProgress`. For its first refresh only, it builds a tracker whose callback sends `TreeEvent{Type: TreeLoading, Progress: p}` without blocking: when the channel is full the snapshot is dropped, since a newer one follows. Later refreshes get a nil tracker. `TreeUpdate`, `TreeDone` and `TreeError` are unchanged, and no `TreeLoading` is sent after the first `TreeUpdate`.

`app` still prints nothing; `TestAppStaysUIReady` holds.

## 4. `view`: describing progress

In `internal/view/tree.go`:

- `TreeProgressText(p app.TreeProgress) string` names every phase that has started (`Total > 0`) and is not finished (`Done < Total`), in the order projects, plans, builds, jobs, joined with ` · `. When every started phase is finished it names the last started one; with nothing started it is `reading the tree`.

  | Phase | Text |
  | --- | --- |
  | Projects | `listing plans 1/2 projects` |
  | Plans | `reading builds 12/40 plans` |
  | Builds | `reading details 5/8 builds` |
  | Jobs | `reading logs 3/9 jobs` |

  Example while a log link pulls in a new project: `reading builds 2/6 plans · reading logs 3/9 jobs`.

- `ProgressLine` writes one rewritable line: `\r\x1b[K` and the text, cut to `Out.Width` so it never wraps. `Show(text)` writes only when the text changed. `Clear()` writes `\r\x1b[K` once, only if a line is shown.

- `TreeLive` takes a second writer for progress, `NewTreeLive(o Out, progress io.Writer, failedOnly bool)`. A `TreeLoading` event shows `TreeProgressText` on a `ProgressLine` on that writer; the next `TreeUpdate`, `TreeDone` or `TreeError` clears it first. A nil writer drops `TreeLoading`.

- `TreeLines` and `TreeNDJSON` ignore `TreeLoading`: pipe and `--json` output are unchanged byte for byte.

## 5. CLI

- `Env.StderrTTY bool`, set with `term.IsTerminal` on stderr like `StdoutTTY`.
- `runtime.treeProgress() *view.ProgressLine` returns a line on stderr when `StdoutTTY && StderrTTY && !--json && TERM != dumb`, else nil. A nil `*ProgressLine` is valid and does nothing.
- Plain `bam build tree`: `TreeOptions.OnProgress` shows `TreeProgressText` on that line; the line is cleared when `BuildTree` returns, before warnings, the tree or an error print.
- `--watch`: `treeLoop` hands the same stderr writer to `NewTreeLive` when progress is on, nil otherwise.
- `--rerun-failed`: its first `BuildTree` shows progress like the plain tree; the line is cleared before the rerun list and the prompt. The follow after restarting goes through `treeLoop` and shows progress as `--watch` does.
- `bam build tree --help` gains one sentence: on a terminal, bam shows on stderr what it is reading while the tree loads. `make docs` regenerates `docs/cli/`.

## 6. TUI

### 6.1 Tree screen

- `treeScreen` gains `progress app.TreeProgress`.
- `handleTreeEvent` on `TreeLoading` stores the snapshot and keeps reading the channel.
- Until the first tree arrives the header is ` PROJ-API-142 · ` plus `TreeProgressText`, where it said `loading`.

### 6.2 Plan screen Tree panel

- `treePreviewCmd` starts `BuildTree` in a goroutine. Its `OnProgress` writes into a one-slot channel, replacing an unread snapshot; the result goes into a second channel.
- The command returned waits on both and returns either `treePreviewProgressMsg{Gen, Key, Progress, wait}`, whose handler stores the snapshot and returns `wait` to keep listening, or the existing `treePreviewMsg`.
- The handler drops a progress message whose `Gen` or `Key` is stale, as `treePreviewLoaded` does. A cancelled context ends the goroutine with the load.
- `treePreview` gains `progress app.TreeProgress`; while loading the panel shows `reading the tree… ` plus the text, cut to the panel width, where it said `reading the tree…`.

## 7. Testing

- `app`:
  - A fake-provider tree with several plans, a busy plan read deeper, a restarted build and a script-linked build in a new project: snapshots are delivered in order, each count has `Done ≤ Total`, totals never shrink, and the last snapshot has `Done == Total` for every started phase.
  - A failing `ListBuilds` still counts as done.
  - A nil `OnProgress` is safe.
  - `WatchTree` sends `TreeLoading` only before the first `TreeUpdate`, ignores `o.OnProgress`, and a reader that never drains the channel does not block the load.
  - `TestAppStaysUIReady` passes.
- `view`: expected strings for `TreeProgressText` (none started, one phase, two overlapping, all done); `ProgressLine` skips an unchanged text, cuts to the width, and `Clear` is a no-op when nothing is shown; `TreeLive` clears the progress line before the first tree.
- CLI, through the harness:
  - Both TTY flags set: stderr carries `\r\x1b[K` lines naming a phase and ends with a clear; stdout matches today's output.
  - Stdout a pipe, stderr a pipe, or `--json`: stderr carries no `\x1b[K`.
  - `--watch` on a TTY shows progress on stderr before the first tree.
- TUI: the tree screen header and the Tree panel while loading; a stale `treePreviewProgressMsg` is dropped.
- `go test -race ./...`: the tracker and the preview goroutine are race-free.

## 8. Out of scope

- Making the load faster: more parallelism, caching across runs (#81).
- Progress for other slow reads (`plan stats`, set boards).
- A progress bar or time estimate.

## 9. Changelog

Under `## [Unreleased]`: `bam build tree` and the terminal UI's tree views show what bam is reading while a tree loads.
