# bam: run sets core (release trains)

**Status:** Implemented | **Date:** 2026-09-29
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future", "Run sets"
**Builds on:** [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) (config layers, targets, exit codes, JSON contract), [`2026-09-20-bam-v0.3-estimator-design.md`](2026-09-20-bam-v0.3-estimator-design.md) (watch events and progress)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

A release often means several different Bamboo plans run in a fixed order: publish the specs, then provision and deploy. Today that is one `bam run` per plan, typed by hand, with the user watching each one and deciding when the next may start.

A **run set** (a "release train") names those runs once, in the configuration, grouped into ordered **stages**. `bam set run release` triggers stage 1, waits until every build in it has finished, and starts stage 2 only when all of stage 1 succeeded. The whole train reports one result.

The PRD's "Run sets" line lists more than this: matrix expansion, batch files, `rerun --failed`, plan statistics and a terminal UI panel. This spec covers the core only (§12); each of the rest gets its own spec and builds on the record defined in §5.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Use case | Different plans run together as a release train | The owner's use case; a matrix over one plan is later work |
| Ordering | Ordered stages; runs inside a stage run in parallel | Covers "all in parallel" as a one-stage set; a dependency graph is more than a release needs |
| Where a set lives | Named, in the configuration, next to `targets:` | A release train repeats; config gets layering, validation and review for free |
| Interrupted train | Recorded in the state directory; `bam set status` and `bam set resume` | bam orders the stages, so a sleeping laptop must not lose the train |
| A run fails | The stage finishes; later stages do not start; nothing is cancelled | Only `bam build cancel` stops builds; stopping a provision half-way leaves debris |
| Orchestration | An `app` use case that emits events on a channel | Keeps `app` UI-ready; the future run-sets panel consumes the same events |
| Rejected | A loop in `cli` | The terminal UI would have to copy it |
| Rejected | Bamboo child plans or plan dependencies | Plan editing is out of scope, and the train could not be chosen per release |

## 3. Configuration

### 3.1 Schema

A `sets:` map, allowed in the same three layers as `targets:`: the project `.bam.yaml`, the machine file's top level, and the machine file's `repos.<key>` entry.

```yaml
sets:
  release:
    timeout: 2h                 # optional; the whole train
    stages:
      - [publish-specs]
      - - provision
        - target: deploy
          vars:
            env: qa
            deploy_token: "${DEPLOY_TOKEN}"
```

```go
// Set is a named release train: stages run in order, runs inside a stage
// run in parallel.
type Set struct {
	Stages  []Stage  `yaml:"stages"`
	Timeout Duration `yaml:"timeout,omitempty"`
}

// Stage is one step of a set. UnmarshalYAML accepts a bare scalar as
// shorthand for SetRun{Target: name}.
type Stage []SetRun

// SetRun is one preset run inside a stage, with variables that override the
// preset's defaults for this set only.
type SetRun struct {
	Target string    `yaml:"target"`
	Vars   StringMap `yaml:"vars,omitempty"`
}
```

`ProjectFile`, `MachineFile` and `Repo` each gain `Sets map[string]Set \`yaml:"sets,omitempty"\``.

### 3.2 Layering

A set is replaced whole, never merged: when a higher layer defines `release`, its stages replace the lower layer's. Merging stage lists would hide which stages run. The layer order is the one targets use: project, machine top level, machine `repos` entry. `config.Sets()` returns `map[string]ResolvedSet`, where `ResolvedSet` carries `Name`, the `Set` and `DefinedIn` (the file path of the winning layer, and of every layer it replaced, lowest precedence first).

### 3.3 Checks when the configuration loads

These need no network and fail as config errors (exit 3), each naming the set and the file:

- The set name matches the target name pattern (`^[a-z][a-z0-9_-]*$`).
- `stages` has at least one stage; every stage has at least one run.
- Every `target` names a preset that exists after the targets merge.
- A preset appears at most once per stage. It may appear again in a later stage.
- The presets that name an explicit `server` all name the same one.
- `timeout` parses as a duration.
- `vars` values are single scalars; `${ENV}` references are allowed, as in `defaults`.

### 3.4 Checks before anything triggers

These need Bamboo and run in `app.PlanSet` (§4.1):

- Every run's server, chosen by `config.SelectServer` exactly as `bam run <preset>` chooses it, resolves to the same origin. Otherwise a config error names the first two presets that differ and their origins. One origin means one credential and one `Connect`.
- Each run's variables resolve with this precedence, lowest first: plan values < preset `defaults` < the set run's `vars` < command-line `--var`. A `--var` on `bam set run` applies to every run whose plan or preset declares that name. A name no run declares warns on stderr, as `bam run` does, and is sent to every run: it may override a Bamboo global variable.
- Then the existing checks per run: unset `${ENV}` references, `required`, and `options`.
- Every error across every run is collected. If there is any, `PlanSet` returns them together as one usage error, and nothing triggers.

### 3.5 Behaviour of a preset inside a set

- Every run is watched. A preset's `watch: false` is ignored inside a set.
- A preset's `timeout` applies to that run (§4.4).
- A preset's `branch` applies, as it does for `bam run`.
- Secrets are masked everywhere, as today: names matching `password|secret|passphrase|sshkey`, values Bamboo returns as `********`, and `${ENV}`-resolved values.

## 4. Orchestration (`internal/app/sets.go`)

### 4.1 API

```go
// SetOptions are the command-line inputs to a set run.
type SetOptions struct {
	Vars   []string // --var k=v, in the order given
	Server string   // --server, so each run's server is chosen as bam run would
}

// SetPlan is a set with every run resolved and checked. RunSet does no
// further resolving.
type SetPlan struct {
	Name     string
	Origin   string
	Timeout  time.Duration
	VarNames []string       // names given with --var, for the record
	Stages   [][]SetRunPlan
	Warnings []string       // each run's VarSet warnings, prefixed with the preset name; the CLI prints them
}

// SetRunPlan is one run of a stage, ready to trigger.
type SetRunPlan struct {
	Target  string
	Ref     PlanRef
	Vars    VarSet
	Timeout time.Duration   // the preset's; zero means none
}

func (s *Service) PlanSet(ctx context.Context, name string, o SetOptions) (SetPlan, error)
func (s *Service) RunSet(ctx context.Context, p SetPlan, lock SetLockOptions) (id string, events <-chan SetEvent, err error)
func (s *Service) ResumeSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error)
```

`RunSet` also returns the record id, which the CLI prints on an interrupt. `RunSet` and `ResumeSet` return an error, and no channel, only for failures before the first event: a held lock (§5.4), a record that cannot be written, or a record that cannot be resumed (§5.3).

### 4.2 Flow of one stage

1. Emit `stage_start`.
2. Trigger each run of the stage with the existing `Service.Run`, one after another, in the order the stage lists them. Sequential triggers keep the error order stable and avoid a burst of requests.
3. A trigger that fails makes that run failed (`run_done` with the error). The stage goes on: every run that did trigger is still watched.
4. Watch every triggered build at once: one `Service.Watch` goroutine each, fanned into the set's channel. Each inner `Event` is sent as a `run_event` carrying its stage index and preset name.
5. When a build's watch ends, emit `run_done` with the build's final state.
6. When every run of the stage is done, emit `stage_done`. If every run is `Successful`, the next stage starts. Otherwise the set ends: `set_done` with state `Failed`, and no later stage triggers.
7. After the last stage succeeds, emit `set_done` with state `Successful`.

A watch that ends with `EventError` (polling failed) makes that run failed with that error; the build itself is not touched.

### 4.3 Events

```go
type SetEventType string

const (
	SetStageStart   SetEventType = "stage_start"
	SetRunTriggered SetEventType = "run_triggered"
	SetRunEvent     SetEventType = "run_event"  // one inner watch Event
	SetRunDone      SetEventType = "run_done"
	SetStageDone    SetEventType = "stage_done"
	SetDone         SetEventType = "set_done"   // last event of a finished set
	SetError        SetEventType = "set_error"  // last event of a set that could not go on
)

// SetEvent is one change in a set run. Stage is zero-based.
type SetEvent struct {
	Type   SetEventType
	Time   time.Time
	Set    string
	ID     string
	Stage  int
	Target string          // preset name; empty for stage and set events
	Build  provider.Build  // run_triggered, run_event, run_done
	Event  *Event          // run_event only: the watch event, unchanged
	State  provider.State  // run_done, stage_done, set_done
	Err    error
}
```

The channel closes after `set_done` or `set_error`, or, when `ctx` ends, after the last event it managed to send (§4.5).

### 4.4 Timeouts

- **Preset timeout.** When a run's own timeout expires, bam stops watching it and the run is failed with a timeout error. The build keeps running in Bamboo. The stage then ends as a failed stage, as in §4.2.
- **Set timeout.** When the set's timeout expires, every watch stops, the record's status becomes `timed_out`, and the set ends with `set_error` carrying an `errs` timeout error (exit 6). No build is cancelled. A timed-out set can be resumed (§5.3).

### 4.5 Interrupt

When `ctx` ends (Ctrl-C), every watch stops and nothing more triggers. The record's status becomes `interrupted`, with every build key known so far. The channel closes without `set_done`; the CLI exits 130. Builds already running keep running: interrupting never stops a build.

### 4.6 `--last`

`Service.Run` is unchanged. It still records each triggered build as the repository's last build, so `bam build show --last` and friends point at the train's latest trigger.

### 4.7 UI-readiness

`sets.go` never prints, reads stdin or imports cobra. It uses only the injected `Clock`. `TestAppStaysUIReady` covers it.

## 5. State

### 5.1 The record

Each set run writes `$XDG_STATE_HOME/bam/sets/<id>.json` (file 0600, directory 0700), atomically: write `<id>.json.tmp`, then rename, as `StateStore` does. The id is `<set>-<UTC start time>`, for example `release-20260929T101500Z`. The `SetStore` type owns the directory; its path is injected the same way `StateStore.Path` is.

```json
{
  "version": 1,
  "id": "release-20260929T101500Z",
  "set": "release",
  "origin": "https://bamboo.example.com",
  "repo": "/home/jdoe/src/app",
  "started_at": "2026-09-29T10:15:00Z",
  "finished_at": "",
  "status": "running",
  "var_names": ["env"],
  "stages": [
    { "runs": [
      { "target": "publish-specs", "plan_key": "PROJ-SPEC",
        "build_key": "PROJ-SPEC-12", "state": "Successful", "error": "" }
    ] },
    { "runs": [
      { "target": "provision", "plan_key": "OPS-PROV",
        "build_key": "", "state": "", "error": "" }
    ] }
  ]
}
```

- `status` is one of `running`, `successful`, `failed`, `interrupted`, `timed_out`.
- The record holds no variable value, masked or not. `var_names` holds only the names given with `--var`, so resume can ask for them again.
- `error` holds the run's error message (`What` only), for a trigger failure or a watch error.

### 5.2 When it is written

- When `RunSet` starts, with every run listed and no build keys.
- After every `run_triggered`, `run_done` and `stage_done`.
- When the set ends: `successful`, `failed`, `interrupted` or `timed_out`, with `finished_at`.

If a write fails before a trigger, the set ends with `set_error` and that trigger never happens: bam never triggers a build it cannot record. A write that fails after the last event is reported as a warning on stderr by the CLI; the builds are already done.

### 5.3 Resume

`ResumeSet(id)`:

1. Loads the record. Only `interrupted` and `timed_out` can be resumed. `failed` is a usage error: "set run <id> failed; resume continues interrupted runs", with a try line of `bam set rerun --failed <id>`. `successful` is a usage error: "nothing to resume". `running` is handled by the lock (§5.4).
2. Checks that the current server's origin equals the record's origin, and that the set's stages in the configuration still list the same presets, in the same stages and order, as the record. Otherwise a config error, with a try line of `bam set run <set>`.
3. Checks that every name in `var_names` is given again with `--var`. A missing one is a usage error naming it; values are never stored.
4. Runs `PlanSet` again to resolve the variables of the runs that have not triggered.
5. In the first stage that is not finished, watches each run that has a build key (a build that finished while bam was away yields its `run_done` at once) and triggers each run that has none. The following stages run as in §4.2.
6. The set timeout, if any, starts again from the resume.

The record keeps its id; resume emits events with that id and updates the same file.

### 5.4 Lock

`RunSet` and `ResumeSet` create `<id>.lock` in the same directory with `O_CREATE|O_EXCL`; it holds the pid, the host name and the start time. `RunSet` also creates `<set>.lock`, so two runs of the same set cannot start at once in different terminals; `ResumeSet` takes both.

- Finding a lock is a usage error: "set run in progress (pid 4242 on laptop, since 10:15)", with a try line that says to pass `--force` if that process is gone.
- `--force` replaces the lock files.
- Both files are removed when the command ends, on every path, including errors and interrupts.

### 5.5 Status

`bam set status [set|id]` reads a record and fetches each recorded build's current state from Bamboo. With no argument it shows this repository's newest record; with a set name, that set's newest record for this repository. It takes no lock and never triggers.

### 5.6 Pruning

When `RunSet` writes a new record, it deletes that set's records beyond the newest 20. Records of other sets are untouched.

## 6. Commands

A new `bam set` noun, laid out like `bam target`:

| Command | Does | `--json` |
| --- | --- | --- |
| `bam set list` | Sets, stage count, where each is defined | array |
| `bam set show <set>` | Stages and their presets, with each run's set `vars` (secrets masked) | object |
| `bam set run <set> [--var k=v]... [--dry-run] [--force]` | `PlanSet`, then `RunSet` | NDJSON |
| `bam set status [set\|id]` | The record with each build's live state | object |
| `bam set resume [set\|id] [--var k=v]... [--force]` | `ResumeSet`; with no argument, this repository's newest resumable record | NDJSON |

- `<set>` completes from the configuration; `id` completes from the records.
- `--server` works as everywhere else and must match the origin the set resolves to; otherwise a config error.
- `bam set run --dry-run` runs `PlanSet` and prints each stage's runs with plan, branch and changed variables (secrets masked), then exits 0 without triggering or writing a record.

## 7. Output

### 7.1 Text

`bam set run` and `bam set resume` show one block: a header with set and id, then one line per run under its stage.

```
release  release-20260929T101500Z
stage 1/2
  ✓ publish-specs  PROJ-SPEC-12  Successful  1m12s
stage 2/2
  ● provision      OPS-PROV-40   Running     ████░░ 62%  ~1m remaining
  ✗ deploy         OPS-DEP-9     Failed      48s
set failed: stage 2 (1 of 2 runs failed)
  try: bam logs OPS-DEP-9 --failed
```

- On a terminal the block redraws in place, as the watch block does; states, glyphs and colours come from `view/style` only.
- Without a terminal, each change prints one line: `stage 2 deploy OPS-DEP-9 Failed 48s`.
- Stages not started yet show their presets with a pending glyph and no build key.
- On failure, a `try:` line per failed build suggests `bam logs <key> --failed`; a run that failed to trigger shows its error instead.

`list`, `show` and `status` are tables in the style of `bam target list` and `bam build show`.

### 7.2 JSON

`bam set run --json` and `bam set resume --json` print NDJSON, one `SetEvent` per line:

```json
{"type":"run_event","time":"2026-09-29T10:16:02Z","set":"release","id":"release-20260929T101500Z","stage":1,"target":"deploy","build":{...},"event":{...}}
```

Fields: `type`, `time`, `set`, `id`, `stage` (zero-based), `target`, `build` (the existing build object), `event` (for `run_event`: the existing watch event object, unchanged), `state`, `error` (an object: `kind`, `what`, `why`, `try`). The last line is `{"type":"set_done",...}` or `{"type":"set_error",...}`.

`bam set status --json` prints the record's fields, with each run's `state` replaced by the live state and a `build` object added where a build key exists. `list` and `show` print the resolved configuration, with secret values masked.

These field names join the public contract (MVP spec §7): later work adds fields, never renames or removes them. `docs/json.md` gets a section for them.

## 8. Errors and exit codes

Mapped by `cli.exitCode`, unchanged:

| Code | When |
| --- | --- |
| 0 | Every run succeeded; `--dry-run`; `list`, `show`, `status` |
| 1 | The set failed (a result error, like a watched build that did not succeed) |
| 2 | Usage: bad `--var`, `PlanSet` errors, a missing `--var` on resume, a held lock, a record that cannot be resumed |
| 3 | Config: §3.3 checks, runs on different origins, a set changed since its record, `--server` not matching |
| 4 | Auth |
| 5 | Bamboo or network failure outside a run (a failure inside a run makes that run failed, not the command) |
| 6 | The set timeout expired |
| 130 | Interrupted; the record stays resumable |

## 9. Architecture

| Package | Change |
| --- | --- |
| `config` | `Set`, `Stage`, `SetRun`, `ResolvedSet`; `Sets` fields on the three layers; `Config.Sets()`, `Config.Set(name)`; §3.3 checks |
| `provider/fake` | `TriggerResults map[string]provider.Build` and `TriggerErrs map[string]error`, keyed by plan key; `TriggerResult` and `TriggerErr` still apply when a key is missing |
| `app` | `sets.go` (`PlanSet`, `RunSet`, `ResumeSet`, `SetEvent`), `setstore.go` (`SetStore`, record, lock, pruning) |
| `view` | set list, show, status and dry-run renderers; the set watch block (text and NDJSON) |
| `cli` | `bam set` and its five subcommands; `SetStore` path from `Env` alongside the state path |

No new dependency. The package import table is unchanged: `app` imports only `provider`, `config`, `credential`, `errs`.

## 10. Testing

No test sleeps or touches the network outside the e2e suite; `go test -race ./...` passes.

- **`config`** (tables, temp files): the bare-name shorthand and the `{target, vars}` form; a higher layer replaces a set whole; each §3.3 error; `${ENV}` accepted in `vars`.
- **`app`** (`fake.Provider`, instant fake clock, temp state directory):
  - `PlanSet` collects every error at once; variable precedence plan < `defaults` < set `vars` < `--var`; an undeclared `--var` warns and reaches every run; runs on different origins are a config error.
  - Happy path: event order `stage_start`, `run_triggered` per run, `run_event`s, `run_done` per run, `stage_done`, … `set_done` `Successful`; stage 2 triggers only after every stage 1 build finished (`fake.Triggered` order and the event order agree).
  - A failed run: the rest of its stage is still watched, no later stage triggers, `set_done` `Failed`, `fake.Stopped` is empty.
  - A trigger error fails that run and the stage goes on.
  - A preset timeout fails that run; the set timeout ends with `set_error` and a timeout error, and stops nothing.
  - Cancelling `ctx` mid-stage: the record is `interrupted` with the build keys so far; the channel closes without `set_done`.
  - The record at each step; it contains none of the variable values the test used (read the file and search for each).
  - A record write failure before a trigger ends the set and that trigger never happens.
  - Resume: watches keyed runs, triggers unkeyed ones; a missing `--var` from `var_names`; a changed set; a `successful` or `failed` record is refused.
  - Lock: a second holder is refused; `--force` replaces it; both lock files are gone after an error.
  - Pruning keeps 20 records of the set and none of another set is removed.
  - `TestAppStaysUIReady` still passes.
- **`view`**: golden files for the set block in progress, failed and successful, at 80 columns and without a terminal; explicit expected strings for `list`, `show`, `status` and `--dry-run`; a test pins the NDJSON field names.
- **`cli`** (in-process harness): each exit code in §8 through the fake backend; `--dry-run` never calls `Trigger` and writes no record; `--json` prints valid NDJSON ending in `set_done`; set name and id completion.
- **e2e** (`//go:build e2e`, the owner's local Bamboo): a two-stage set with the publish-specs plan in stage 1 and the provision plan in stage 2. Check the record, `status` and `--json`; interrupt once during stage 2 and `resume`. Afterwards always run the decommission plan with the same cluster variables and confirm it succeeded.

## 11. Documentation

- `make docs` regenerates `docs/cli/bam_set*.md`.
- `README.md` gains a "Release trains" section with the §3.1 example and the §7.1 block.
- `docs/configuration.md` documents `sets:`, layering and the §3.3 rules.
- `docs/json.md` documents the §7.2 documents.
- `docs/prd.md` §7 marks the run sets core as done and keeps the rest under Future.

## 12. Out of scope

Each of these is later work with its own spec:

- `bam set rerun --failed`: done, see [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md).
- Matrix expansion and batch files.
- Plan statistics and set-level history views.
- The terminal UI run-sets panel (it will consume §4.3 events).
- An ad hoc command-line form of a set.
- Runs on more than one Bamboo server in one set.
- Dependencies between runs other than stage order.
- Cancelling builds from a set. `bam build cancel` stays the only way.
