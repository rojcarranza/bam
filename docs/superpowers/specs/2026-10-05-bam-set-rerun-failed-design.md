# bam: `bam set rerun --failed`

**Status:** Implemented | **Date:** 2026-10-05
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future", "Run sets"
**Builds on:** [`2026-09-29-bam-run-sets-core-design.md`](2026-09-29-bam-run-sets-core-design.md) (the record §5.1, resume §5.3, lock §5.4, events §4.3, output §7), [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) §7 (JSON contract), §9 (capabilities)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

A release train fails when one run of a stage fails, often for a reason outside the code: a flaky deploy job, a busy agent, a network blip. Today the record is then `failed`, `bam set resume` refuses it, and the only way on is `bam set run <set>`, which triggers every run from stage 1 again, including the ones that already succeeded.

`bam set rerun --failed` retries only the failed runs of the failed stage and, when they succeed, continues the train through the stages that never started. A failed build is retried with Bamboo's own restart, which re-runs only that build's failed jobs, with the same variables and the same commit, under the same build key.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| How a failed run is retried | Bamboo restarts the failed build (`PUT queue/{key}`) | Only the failed jobs run again, with the variables and commit the build already had; bam never stores variable values, so it could not otherwise reproduce them |
| A run that failed to trigger | Triggered fresh, as resume does | It has no build to restart |
| After the failed stage succeeds | The train continues through the later stages | One command takes a broken train to the end |
| Record | The same record and id, updated | The train's history stays in one place, as with resume |
| Command shape | `--failed` is required | Leaves a bare `bam set rerun` (rerun everything) for later without changing meaning |
| Rejected | Re-triggering fresh builds for failed runs | Runs every job again and needs every variable again |
| Rejected | Stopping after the failed stage, leaving the rest to `resume` | Two commands for the common case, and a new record state |

## 3. What Bamboo does

The queue resource on Bamboo 12.1.8 lists `PUT queue/{projectKey}-{buildKey}-{buildNumber}` with parameters `stage` and `executeAllStages` (`internal/provider/bamboo/testdata/recorded/wadl_queue.txt`). Bamboo documents it as restarting a build: its failed and not-built jobs are queued again, in the same build result.

What it answers, how the build's state moves after the call, and how it refuses a build it cannot restart are not recorded yet. The recorder gains a probe (§8.1) that the owner runs once against the lab; the hand-written fixtures follow the recordings, and `TestRecorded*` keeps them in step. Until then the design assumes:

- The answer is a queued-build object like the trigger's (`buildResultKey`, `buildNumber`, `triggerReason`), naming the same build key.
- For a short time after the call, `GET result/{key}` may still return the old finished result. The build then reads as queued or in progress, and finally finishes again with a new `buildCompletedTime`.
- A build Bamboo will not restart (still running, or not failed) is refused with a 4xx and a message.

Section 4.2's wait does not depend on the first two holding exactly: it only needs the old finished result to go away.

## 4. Architecture

### 4.1 `provider` and `provider/bamboo`

`Provider` gains:

```go
// RestartBuild asks Bamboo to run a finished build's failed jobs again, in
// the same build. It returns the queued build.
RestartBuild(ctx context.Context, buildKey string) (Build, error)
```

`provider/bamboo`:

- `RestartBuild` sends `PUT queue/{key}` with `executeAllStages=true`, and maps the answer as `Trigger` does.
- 405 or 501 (the method is missing) returns an error wrapping `errs.ErrUnsupported`, What `this Bamboo cannot restart a build over REST`, and records the new capability `Rerun: "no"`. 404 is a missing build, as for `StopBuild`: an error wrapping `errs.ErrNotFound`. A success records `Rerun: "yes"`. The capability lives in `Capabilities` beside `Stop`, cached per origin like the others.
- Any other 4xx is an error of kind bamboo, What `Bamboo would not restart PROJ-PLAN-9`, Why Bamboo's message.
- `bam doctor` lists the capability like `stop builds`: `rerun builds`, `confirmed by an earlier rerun`, `not supported` or `not known yet`. Doctor does not probe it, because a probe would restart a build.

`provider/fake`: `RestartResult` / `RestartErr` (with a per-key map, as `TriggerResults`), and `Restarted []string`.

### 4.2 `app`: restarting and waiting

```go
// Restart restarts a finished build in Bamboo and waits until Bamboo has
// requeued it, so a Watch that follows does not see the old result.
func (s *Service) Restart(ctx context.Context, key string) (provider.Build, error)
```

1. Read the build. Remember its state and `FinishedAt`. A build that is not finished, or that succeeded, is not restarted: `Restart` returns it as it is, and the caller watches it without counting a rerun.
2. `P.RestartBuild(ctx, key)`. Errors are returned as they are.
3. Poll `s.Build` on `s.Clock`, every 2 seconds, for at most 2 minutes, until the build is not finished, or is finished with a `FinishedAt` other than the remembered one. Return that build.
4. If the 2 minutes pass first: an error of kind bamboo, What `Bamboo did not restart PROJ-PLAN-9`, Why `the build still shows its earlier result`, Try `bam open PROJ-PLAN-9`.

`Restart` does not record the build as the repository's last build: it is not a new trigger. It never prints.

### 4.3 `app`: `RerunSet`

```go
func (s *Service) RerunSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error)
```

Before the first event, as `ResumeSet` does (§5.3 of the run sets design), returning an error and no channel:

1. Load the record. Only `failed` can be rerun. `successful` is a usage error, `set run <id> succeeded; nothing to rerun`. Every other status (`interrupted`, `timed_out`, `running`) is a usage error, `set run <id> was <status>, not failed`, Try `bam set resume <id>`.
2. The same checks as resume, shared with it: the origin, every name in `var_names` given again with `--var`, `PlanSet`, the same shape, and both lock files.
3. Set the status to `running`, clear `finished_at`, save.

Then, in the first stage whose runs did not all succeed:

- A run with a build key and a state other than `Successful` is restarted: `Restart`, then watched as any run is. Its `reruns` count goes up by one, and the record is saved before the restart call, so bam never restarts a build it has not recorded.
- A run with no build key (its trigger failed) is triggered fresh from the `PlanSet` resolution, exactly as resume triggers it, and its `error` is cleared.
- A run that succeeded is left alone and counts as done.

When every run of that stage is done, the train goes on as in §4.2 of the run sets design: the next stage starts only if every run succeeded, and the later stages trigger normally. A run Bamboo cannot restart (`Restart` error) is a failed run with that error, like a failed trigger, and the stage ends failed.

`ResumeSet`'s message for a `failed` record changes to `set run <id> failed; resume continues interrupted runs`, Try `bam set rerun --failed <id>`.

The set timeout, if any, starts again from the rerun, as on resume.

### 4.4 Record

`RunRecord` gains `Reruns int \`json:"reruns"\``: how many times bam restarted this run's build. A fresh trigger of a run that had no build does not count. The record's `version` stays 1; an old record reads as `reruns: 0`.

### 4.5 Events

`SetEvent` gains `Rerun int`: on `run_triggered`, the run's `reruns` after this restart, or 0 for a trigger. No new event type: a restarted run is triggered again, from the train's point of view, and every consumer of `run_triggered` keeps working.

## 5. Command

```
bam set rerun [set|id] --failed [--var k=v]... [--force]
```

- With no argument, this repository's newest `failed` record; with a set name, that set's newest `failed` record for this repository. No such record is a usage error, `no failed run of <set> to rerun`, Try `bam set status <set>`.
- `--failed` is required. Without it: usage error `rerun needs --failed`, checked before any configuration load or network call.
- `<set>` and `id` complete as for `bam set resume`.
- `--server`, `--var`, `--force` and `--json` work as for `bam set resume`.

## 6. Output

### 6.1 Text

The set block of the run sets design §7.1, with a restarted run marked:

```
release  release-20261005T091500Z
stage 1/3
  ✓ publish-specs  PROJ-SPEC-12  Successful  1m12s
stage 2/3
  ✓ provision      OPS-PROV-40   Successful  3m02s
  ● deploy         OPS-DEP-9     Running (rerun 1)   ████░░ 62%
stage 3/3
  · smoke          pending
```

- The live block starts from the record, for `rerun` and for `resume` alike: runs that already have a build show its key and recorded state instead of `pending`.
- A restarted run shows `(rerun N)` after its state title, in the live block and in `bam set status`. Its state glyph is the usual one from `view/style`; no new glyph.
- Without a terminal, the restart prints `stage 2 deploy OPS-DEP-9 rerun 1`.
- Every failed set's summary, from `run`, `resume` or `rerun`, ends with one `try: bam set rerun --failed <id>` after the per-run `try:` lines of §7.1 of the run sets design.

### 6.2 JSON

- NDJSON events gain `rerun` (number) on every event; it is 0 except on a restart's `run_triggered`.
- `bam set status --json` gains `reruns` on each run.

Fields are added; none is renamed or removed. `docs/json.md` lists both.

## 7. Errors and exit codes

As `bam set resume`:

| Case | Exit | What / Try |
| --- | --- | --- |
| No `--failed` | 2 | `rerun needs --failed` |
| No failed record | 2 | `no failed run of <set> to rerun` / `bam set status <set>` |
| Record `successful` | 2 | `set run <id> succeeded; nothing to rerun` |
| Record `interrupted` or `timed_out` | 2 | `set run <id> was interrupted, not failed` / `bam set resume <id>` |
| A `var_names` entry not given | 2 | as resume |
| Lock held | 2 | as resume |
| Origin or shape changed | 3 | as resume |
| A run cannot be restarted | 1 | the run fails with `this Bamboo cannot restart a build over REST` / `bam set run <set>`, or `Bamboo would not restart …` |
| Bamboo never requeued the build | 1 | the run fails with `Bamboo did not restart …` / `bam open <key>` |
| The train still fails | 1 | as `set run` |
| Set timeout | 6 | as `set run` |
| Interrupted | 130 | the record is `interrupted`; `bam set resume` continues it |

## 8. Testing

Test first. No test sleeps or touches the network.

### 8.1 Recorder probe

`go run ./tools/record -probe-rerun -target <preset>` (and `make record ARGS='-probe-rerun …'`): triggers the plan, waits for it to finish, and, if it failed, restarts it with `PUT queue/{key}` and records `queue_restart.json` (the answer), then `result_restart_*.json` at each state change until it finishes again. It also restarts a build that succeeded and records the refusal as `queue_restart_refused.json` and `.status`. A plan that does not fail cannot exercise the restart; the probe says so and records only the refusal. The probe is run by the owner; recordings are scrubbed as all others are.

### 8.2 Unit tests

- **`provider/bamboo`**: `PUT` to the right path with `executeAllStages=true`; the answer maps to the build; 405/501 is `ErrUnsupported` and records `Rerun: "no"`; 404 is `ErrNotFound`; any 2xx counts as accepted, whatever its body; success records `"yes"`; a 400 with a message is a bamboo error naming it. Hand-written fixtures `queue_restart.json`, `queue_restart_refused.json`; `TestRecordedRestart*` checks them against the recordings when present.
- **`app` `Restart`**: returns once the old result is gone (old result twice, then queued); a build finishing again with a new `FinishedAt` counts as restarted; never requeued gives the error after 2 minutes of fake time, with the polls on the fake clock; `RestartBuild` errors pass through; `--last` untouched.
- **`app` `RerunSet`**: refuses `successful`, `interrupted`, `timed_out`; the shared resume checks; restarts only the failed runs of the failed stage and leaves succeeded runs alone; triggers a run with no build key; `reruns` saved before the restart call; continues to later stages; a restart error fails the run and stops the train; the record ends `successful` or `failed`; `ResumeSet`'s new message for `failed`.
- **`cli`** (harness): `--failed` required (exit 2, no network); newest failed record picked with no argument; text block with `rerun 1`; NDJSON `rerun` field; exit 1 when the train still fails; `set status --json` has `reruns`.
- **`view`**: explicit expected strings for the header, the `rerun N` mark and the plain line; goldens only where a renderer changed.
- **e2e** (`//go:build e2e`): with `-set-rerun-plan KEY` naming a plan that fails, run it as a one-stage set, then `bam set rerun --failed`, and check that the same build key ends with `reruns: 1` in `bam set status --json`.

## 9. Documentation

- `make docs` regenerates `docs/cli/` with `bam set rerun`.
- README, Release trains section: a line for `bam set rerun --failed`.
- `docs/json.md`: `rerun` on set events and `reruns` on status runs.
- The run sets design §12 points here for `rerun --failed`.

## 10. Acceptance

Against the local Bamboo, with a set whose second stage has a plan that fails on its first run and passes on its second:

1. `bam set run release` ends failed, exit 1.
2. `bam set rerun --failed` restarts that build under the same key, the stage succeeds, the later stage runs, exit 0; `bam set status --json` shows `reruns: 1`.
3. `bam set rerun` without `--failed` exits 2; `bam set rerun --failed` on the now successful record exits 2.
4. `bam doctor` reports `rerun builds` as confirmed.
5. `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.

## 11. Out of scope

- Re-triggering fresh builds for failed runs (a `--fresh` flag).
- A bare `bam set rerun` that runs every run again.
- Rerunning a single build outside a set (`bam build rerun`).
- Rerun in the terminal UI; it belongs with the run-sets panel.
- Restarting only some stages of a build (Bamboo's `stage` parameter).
