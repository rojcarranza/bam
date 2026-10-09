# bam: live stage logs in the terminal UI, searchable while they stream

**Status:** Draft | **Date:** 2026-10-10
**Issue:** #53
**Builds on:** [`2026-09-18-bam-v0.2-tui-navigate-design.md`](2026-09-18-bam-v0.2-tui-navigate-design.md) §3.1 (the log screen) and the follow plumbing it describes

All hosts, keys, user names and paths are placeholders.

## 1. Problem

The log screen answers "why did it fail" but not "what is it doing". On a running build:

- `l` opens failed jobs only, so it says `no failed job` until something has already broken.
- `f` follows one job. A screen of several jobs refuses it (`follow needs one job`), and a followed job ends the follow when it finishes; the next job or stage is not picked up.
- `/` matches once, against the lines on screen at that moment. Lines that stream in later are never matched, matches are not highlighted, and there is no count.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| What "live" shows | A whole stage: every job in it, then the next stage, until the build finishes | The owner's choice; a stage is how Bamboo groups parallel work |
| How parallel jobs read | Every line of a multi-job screen is prefixed `[JOB1] ` | Parallel jobs interleave chunk by chunk; a block header per job cannot survive interleaving |
| Opening a stage | `l` on a stage row of the build detail tree | Enter on a stage row already expands and collapses it; `l` already means "logs for the selection" (owner's choice) |
| `l` on a running build | Opens the current stage, already following | The failed-only default is useless before anything fails |
| Search while streaming | Highlight every match, count `match 3/12`, match new lines as they arrive | The owner's choice over a grep-style filter |
| Where following lives | A new `app.FollowLogs`, one build poll per tick for all followed jobs | N single-job follows would poll the build N times a tick and put stage logic in the UI |
| `bam logs --follow` | Unchanged; keeps `FollowLog` | Out of scope (#53); it can move to `FollowLogs` later |

## 3. Following several jobs: `app`

### 3.1 API

```go
// LogEventKind tells the consumer what a LogEvent carries.
type LogEventKind int

const (
	LogLines   LogEventKind = iota // new lines of one job
	StageStart                     // following moved on to a new stage
	LogStatus                      // the followed stage's or the build's state changed
)

// LogEvent is one thing FollowLogs saw.
type LogEvent struct {
	Kind  LogEventKind
	Stage string         // the stage the event belongs to
	Job   provider.Job   // LogLines only
	Lines []string       // LogLines only
	Build provider.Build // LogStatus and StageStart: the poll's snapshot
}

// FollowLogs emits new log lines of the jobs in from (job result key →
// offset) until every one of them has finished. With advance set, while the
// build is still running it then follows the next stage's jobs from offset 0,
// emitting StageStart first. It polls the build once per tick.
func (s *Service) FollowLogs(ctx context.Context, buildKey string, from map[string]int, advance bool, emit func(LogEvent)) error
```

`LogsOptions` gains `Stage string`: when set, `selectJobs` returns that stage's jobs (all of them, regardless of `Failed`). An unknown stage is a usage error that lists the stage names, the same shape as an unknown `Job`. `JobLog.Next` already carries each job's offset for `from`.

### 3.2 One tick

1. `s.Build(ctx, buildKey)`: the only build request this tick.
2. If the followed stage's state or the build's state differs from the last tick (or this is the first tick), emit `LogStatus` with the snapshot.
3. For each followed job, look up its state in the snapshot:
   - `skipped`, `not_built`, or not in the snapshot: drop the job without a log request; it will never have a log.
   - `queued` while the build is not finished: no log request this tick.
   - `running`: `FetchLog` from its offset. `errs.ErrNotFound` means Bamboo has no log yet; treat it as no lines. Emit `LogLines` when there are lines; store `chunk.Next`.
   - finished, or any state once the build itself is finished: one last `FetchLog` from its offset (`ErrNotFound` again counts as no lines), emit any lines, and drop the job. This is what ends a follow on a build that was stopped before some jobs ran.
4. If no job is left:
   - `advance` off, or the build is finished: return `nil`.
   - otherwise find the next stage after the last followed one in `b.Stages` order. If there is one, emit `StageStart` with its name and the snapshot, follow all its jobs from offset 0, and go to step 3 for them now. If the build lists no later stage, return `nil`.
5. Wait `MinPoll` on `s.Clock`, or return `ctx.Err()`.

A failed stage needs no special case: Bamboo fails the build, the build is finished, and step 4 returns. Any other error from `Build` or `FetchLog` is returned as is.

`FollowLogs` never prints and never stops a build. `TestAppStaysUIReady` still passes.

## 4. Terminal UI

### 4.1 Opening

| Where | Key | Opens |
| --- | --- | --- |
| Build detail tree, cursor on a stage row | `l` | That stage's jobs, all lines; following with `advance` if the build is running or queued |
| Build detail tree, cursor on a job row | `l` | That job, the same as Enter |
| Anywhere else, running or queued build | `l` | The current stage, following with `advance` |
| Anywhere else, finished build | `l` / `a` | Unchanged: failed jobs / all jobs |

The current stage is the first `running` stage, or else the first stage that has not finished. A queued build whose current stage has no running job yet opens empty with the line `waiting for jobs to start` (not part of `lines`, so search never matches it) and follows.

Enter on a stage row keeps expanding and collapsing it. The tree screen (`t`) is unchanged.

`loadLogsCmd` returns an offset per job (`Offsets map[string]int`) instead of the single `Offset`, plus `Stage` when the screen is a stage screen. `logState` keeps both: `offsets`, `stage`, `advance`.

### 4.2 Rendering

- A screen showing more than one job prefixes every line with `[` + `app.ShortJobKey` + `] `. No padding: a later stage can bring longer keys.
- A stage begins with a `== <stage name> ==` line, both on the initial load and on `StageStart`.
- This replaces the `== Job Name ==` block headers on the `a` and failed multi-job screens. That is a deliberate change to their golden: block headers break once lines interleave, and one format for every multi-job screen keeps `n`, search and the prefix rule the same everywhere.
- A single-job screen keeps bare lines, as today.

The header:

```
PROJ-PLAN-44 · Integration · running · match 3/12                   120/1842
```

Source title, then the followed stage (stage screens only), then its state from the last `LogStatus` (live screens only), then `match i/n` when a query is set. The position stays on the right.

### 4.3 Follow and pinning

- `f` toggles follow on any screen whose jobs are not all finished. On a multi-job screen it follows the jobs on screen, with `advance` only on a stage screen. The `follow needs one job` refusal goes away.
- The TUI uses `FollowLogs` for every follow, single-job included; `followCmd` adapts its `emit` into a channel of `LogEvent` exactly as it adapts `FollowLog` today, and `drainFollowCmd` turns each into a `logEventMsg`.
- `logState` gets an explicit `pinned bool`, true when a screen opens:
  - `G` sets it.
  - a search, `n` and `N` clear it.
  - any other viewport key sets it to `vp.AtBottom()` after the viewport moves.
  - `appendLines` scrolls to the bottom only while `pinned`.

  Today's "pin if it was at the bottom" rule is replaced because a match on the last screen of the log is also "at the bottom" and would be scrolled away by the next chunk.
- Esc, switching build or server, reopening, and the end of the follow all go through `stopFollow`, which bumps `followGen`; late events are dropped. Nothing calls `StopBuild`: leaving the screen never stops the build.

### 4.4 Search while streaming

- `search(q)` works as today over `lines`, then sets `pinned = false`.
- `appendLines` with a query set scans only the new lines and appends their indexes to `matches`; the count in the header grows, the current match does not move.
- Highlighting happens when lines are turned into viewport content. `logState` keeps `rendered []string`, rebuilt in full when the query changes and extended for appended lines only. A match is wrapped in a `matchStyle` (reverse video; defined in `theme.go`).
- The matching rule is unchanged: `strings.Contains(strings.ToLower(line), strings.ToLower(q))`. Positions to highlight come from an ASCII-only case fold of the line and the query, which keeps byte offsets valid. If the rule matches a line and the ASCII fold finds no position (a non-ASCII case difference), the whole line is highlighted.
- Esc in the search input, or submitting an empty query, clears `query`, `matches` and the highlight.
- The key line reads `n next match` when a query is set and `n next failure` otherwise. With no query, `n` keeps going to the next failure.

### 4.5 Help

The help overlay's `l` line becomes `logs for the selection: a stage, a job, or failed jobs (a running build: its current stage, live)`; `f` becomes `follow the jobs on the log screen`; `n` becomes `next match, or next failure with no search`.

## 5. Testing

`app` (`logs_test.go`), with `fake.Provider` scripted per poll and the instant fake clock:

- two jobs in one stage, both running: lines from each come out tagged with their job; offsets advance per job.
- one `Build` call per tick, counted on the fake, with three jobs followed.
- a queued job is not fetched until it runs; a skipped job is never fetched and does not keep the follow alive.
- a build stopped while a job is still queued: that job gets one read, and the follow returns.
- `ErrNotFound` from a running job's log is no lines, not an error; from any other call it is returned.
- `advance`: stage one finishes, `StageStart` for stage two, its jobs from offset 0, return when the build finishes.
- a failed stage fails the build: no `StageStart`, return `nil`.
- `LogStatus` only on a state change.
- `ctx` cancelled mid-wait returns `ctx.Err()`.
- `LogsOptions{Stage}` selects the stage's jobs; an unknown stage is a usage error naming the stages.

`view/tui` (model tests, as `logs_test.go` and `model_test.go` do today):

- `l` on a stage row of a running build opens the stage following; on a finished build, not following.
- `l` on a running build with no row selected opens the current stage; on a queued build with nothing running, the waiting line.
- prefixes on a multi-job screen, none on a single job; `== stage ==` on `StageStart`.
- stale events after switching build are dropped.
- `pinned`: an appended chunk scrolls when pinned, not after `n`; `G` re-pins.
- a query set before new lines arrive: the count grows, the current match stays.
- `n` with and without a query; the key line names which.
- the ASCII-fold fallback highlights the whole line.
- goldens: a stage screen with prefixes, a stage separator, a highlighted match and the header count; the regenerated `a` / failed multi-job golden, reviewed.

`go test -race ./...` passes.

## 6. Docs and changelog

- `CHANGELOG.md` under `[Unreleased]`: "The terminal UI streams a running build's stage logs live (`l` on a stage, or on a running build) and search highlights and counts matches as lines arrive."
- [`2026-09-18-bam-v0.2-tui-navigate-design.md`](2026-09-18-bam-v0.2-tui-navigate-design.md) §3.1 gets a one-line note that this spec supersedes its follow and search behaviour.

## 7. Out of scope

- A split view with the tree and the log side by side.
- A filter-only (grep) mode and regex search.
- `bam logs --follow` on the CLI.
- Stage logs from the build tree screen (`t`).
