# bam: run sets in the terminal UI

**Status:** Implemented | **Date:** 2026-10-06
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future", "Run sets" (the run-sets panel)
**Builds on:** [`2026-09-29-bam-run-sets-core-design.md`](2026-09-29-bam-run-sets-core-design.md) (events §4.3, record §5, lock §5.4), [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md), [`2026-10-05-bam-set-matrix-design.md`](2026-10-05-bam-set-matrix-design.md), [`2026-09-23-bam-v0.3-tui-home-design.md`](2026-09-23-bam-v0.3-tui-home-design.md) (Home, `:` commands)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

Release trains run only from the command line. Starting one, watching it, and resuming or rerunning it after a failure means leaving the terminal UI. The UI knows plans, builds and presets, but not sets.

The UI gains a sets view on Home and a set screen that launches and drives a train: run it with optional variables and a dry-run preview, watch it live, resume it, and rerun its failed runs. It adds no behaviour the commands lack: the same `app` use cases decide everything.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Purpose | Launch and drive trains, not only monitor them | The owner's choice: removes the round trip to the command line |
| `--var` values | A small vars prompt: optional rows on run, one required row per recorded name on resume and rerun | Trains that use `--var` stay drivable; values are never stored, as on the CLI |
| Where | A third Home view (`:sets`) and a set screen | Home already switches between plans and presets |
| Shared state model | `app.SetBoard`, moved out of `view` | The CLI block and the UI screen draw from one model and cannot disagree |
| Leaving the screen | The train keeps running in the window | Browsing builds while a train runs is the point |
| Trains run elsewhere | Shown from the record, polled; actions refused | The lock belongs to that process |
| Rejected | Several trains per window | One set screen, one train; a second needs a second window |
| Rejected | The lock `--force` override in the UI | Taking over another process's train is a command-line decision |

## 3. Screens

```
bam ─► Home ── :sets ─► Sets table
                          │ enter on a set
                          ▼
                        Set screen ── enter on a run ─► Panels (that build)
                          │ R / U / F
                          ▼
                        Vars prompt ── ctrl-R ─► Set screen (live)
```

### 3.1 Sets table (Home view)

`:sets` switches Home to the sets view, as `:plans` and `:presets` switch to theirs. `:sets` joins the command table, its completion and the help overlay.

```
 bam · work · 9.6.4 · jdoe · 2 sets · sort set↑
 SET        STAGES  RUNS  LAST RUN
▸release    2       3     ✗ failed      12m
 regions    2       6     ● running     now
 nightly    1       1     –

 enter open  / filter  s sort  R run  : command  ? help  q quit
```

- One row per configured set (`Deps.Sets`, re-read on every load as `Deps.Targets` is).
- RUNS counts runs after matrix expansion.
- LAST RUN is this repository's newest record of the set: its status glyph and title (`running`, `successful`, `failed`, `interrupted`, `timed out`) and the age of its start; `–` when there is none. A train running in this window shows as running.
- `/` filters by set name; `s` sorts by set, last-run status or age.
- `enter` opens the set screen; `R` opens the vars prompt to run the set from the table.

### 3.2 Set screen

```
 release · release-20261006T091500Z · failed                    esc back
 stage 1/2
   ✓ publish-specs                 PROJ-SPEC-12  Success            1m12s
 stage 2/2
   ✓ provision                     OPS-PROV-40   Success            1m30s
 ▸ ✗ deploy[env=prod]              OPS-DEP-9     Failed (rerun 1)     48s

 enter build  o open  y copy  R run  F rerun failed  r refresh  esc back
```

- The board is `app.SetBoard` (section 5) seeded from this repository's newest record of the set, then updated by live events when the train runs in this window. With no record, the stages list their runs as pending.
- The cursor moves over runs. `enter` on a run with a build opens it in the panel layout, as `enter` on a build does (detail, stages, jobs, logs); `esc` from there returns to the set screen. `o` and `y` open and copy the run's build URL.
- `R` runs the set; `U` resumes the newest record when it is `interrupted` or `timed_out`; `F` reruns its failed runs when it is `failed`. A key whose action does not apply does nothing, and the key line lists only the keys that apply.
- `r` reloads the record and each build's live state (`SetStatus`).
- While the train runs in this window, the header reads `running`, rows update live (state, duration, progress bar), and `R`/`U`/`F` are not offered.
- When the train ends, the status bar reads `release succeeded` or `release failed: stage 2 (1 of 2 runs failed)`.

### 3.3 Vars prompt

```
 Run release
   env        qa                 
   region     ________           
   + add variable

 enter edit  a add  x remove  d dry-run  ctrl-R run  esc cancel
```

- `R` opens it with no rows; `a` adds a `name=value` row, `x` removes the current one. The rows are the command's `--var` flags, in order.
- `U` and `F` open it with one row per name in the record's `var_names`, marked `required: the run was started with it`. `ctrl-R` refuses while any of them is empty. With no `var_names`, `U` and `F` skip the prompt and act at once.
- `enter` edits a row's value (or, on a new row, its name then its value), like the run form's text fields.
- `d` runs `PlanSet` and shows every run of every stage with its plan, branch and changed variables, secrets masked, in an overlay, as `bam set run --dry-run` prints them. A `PlanSet` error shows in the error overlay (`e` expands it).
- `ctrl-R` calls `RunSet`, `ResumeSet` or `RerunSet` and returns to the set screen, live. An error before the first event (a held lock, a missing variable, a changed set) stays in the prompt as the run form's errors do.
- Values exist only in the prompt and the call; leaving the prompt forgets them.

## 4. Running, leaving and quitting

- The UI reads the train's event channel with a command loop, as it reads a build watch. Events update the window's `SetBoard`; progress-only watch events move the bars.
- At most one train runs per window. While one runs, `R`/`U`/`F` on any set report `release is running in this window` in the status bar.
- Leaving the set screen does not stop the train: it keeps triggering and watching while the user browses. Home's LAST RUN shows it running; reopening the set screen shows its live board.
- `q` (or `:q`) while a train runs asks: `release is running in this window. Quit and stop watching it? Its builds keep running; continue with U or bam set resume.` On yes, the train's context ends: the record becomes `interrupted` (as Ctrl-C on the CLI), no build is stopped, and the UI exits once the train has released its lock. On no, nothing changes.
- Switching server (`S`, `:server`) while a train runs asks the same question first, because the train's service belongs to the server it runs on.

## 5. Trains run elsewhere

- When the newest record is `running` and its lock is held by another process, the set screen reads `running in another bam (pid 4242 on laptop, since 09:15)`, shows the record with each build's live state, and re-reads it every 5 seconds while the screen is open.
- `R`, `U` and `F` there report the same text in the status bar and do nothing. A lock left by a process that is gone is the command line's to override (`--force`).
- A `running` record whose lock is not held is shown as interrupted would be: its process ended without updating it. `U` resumes it, as `bam set resume` does.

## 6. Architecture

### 6.1 `app`

- **`SetBoard`**, moved from `view`'s unexported `setBoard`: the state of a set run built from a plan or record and its events.

  ```go
  type SetBoard struct { /* name, id, stages of rows, final event */ }
  type SetBoardRow struct {
  	Name     string            // run name
  	Build    provider.Build
  	Progress provider.Progress
  	At       time.Time         // when Progress arrived
  	Err      error
  	Rerun    int
  }
  func NewSetBoard(p SetPlan) *SetBoard
  func (b *SetBoard) Seed(rec SetRecord)
  func (b *SetBoard) Apply(e SetEvent, now time.Time)
  func (b *SetBoard) ID() string
  func (b *SetBoard) Stages() [][]SetBoardRow   // copies, safe to render
  func (b *SetBoard) Final() *SetEvent           // set_done or set_error, nil while running
  func (b *SetBoard) Summary() (failedStage, failed, total int, ok bool)
  ```

  `view.SetLive`, `view.SetLines` and the summary use it; their output does not change, and their tests and goldens pass unchanged.
- **`SetStore.Holder(set string) (pid int, host string, since time.Time, held bool)`**: reads the set's lock file without taking it. A holder whose pid is this process's own counts as not held elsewhere.
- **`Service.LatestSetRun(set string) (SetRecord, bool, error)`**: this repository's newest record of the set (`SetStore.Latest`).
- `PlanSet`, `RunSet`, `ResumeSet`, `RerunSet` and `SetStatus` are used as they are.

### 6.2 `tui`

- New files: `sets.go` (the Home sets view and its loading), `setscreen.go` (the set screen, its keys, the live loop), `setvars.go` (the vars prompt and dry-run overlay). `model.go` gains only the routing: a `screenSet` and `screenSetVars`, the `homeSets` view, and the train's state on `Model` (its cancel function, channel, board and generation counter, as the build watch has).
- `Deps` gains `Sets func() ([]app.SetInfo, error)`; `cli/ui.go` fills it like `Targets`, re-reading the configuration.
- New key bindings: `U` (resume), `F` (rerun failed) and `x` (remove a row in the vars prompt). In the prompt, `a` (add a row) reuses the existing `a` binding (all logs on the log screen), as the run form reuses `f`: `TestNoKeyIsBoundTwice` forbids a second binding on one key. `R`, `d`, `r`, `o`, `y`, `enter`, `esc` keep their meanings on the new screens. The help overlay and the README key table list the new keys; `TestEveryBindingAppearsInHelp` and `TestNoKeyIsBoundTwice` keep passing.
- Layering is unchanged: `tui` imports `app`, `provider`, `errs` and `view/style`, never `config` or `view`.

## 7. Errors

- Before a train starts: errors stay in the vars prompt (`e` expands them), and nothing triggers.
- While it runs: a run's failure is a failed row, as on the CLI; a `set_error` (set timeout, record write failure) puts its What in the status bar, `e` expands it.
- Loading sets or records: an error in the status bar; the table keeps its last rows.

## 8. Testing

Test first. The UI is tested through `Model.Update` with the fake provider, the instant clock and a temporary set store; no test sleeps or touches the network.

- **`app.SetBoard`**: seed from a record (names, states, reruns, errors), apply each event type, matrix names, summary counts; the existing `view` set tests and goldens unchanged.
- **`app.SetStore.Holder`** and **`LatestSetRun`**: held, not held, own pid, no record.
- **Sets table**: rows, RUNS after expansion, LAST RUN per status and none, filter, sort, `:sets` and its completion; golden `sets-80x24`.
- **Set screen**: seeded board, live updates from events, `enter` opens the build, `o`/`y`, keys offered per record status; goldens `set-running-80x24`, `set-failed-80x24`, `set-elsewhere-80x24`.
- **Vars prompt**: add/remove/edit rows; required rows block `ctrl-R`; `d` shows the dry run masked; a `PlanSet` error stays in the prompt; `ctrl-R` calls the right use case with the rows as `--var`.
- **Lifecycle**: the train keeps running after `esc` and after opening a build; `q` and `S` ask while it runs; answering yes leaves the record `interrupted` and stops no build; one train per window.
- **Elsewhere**: a held lock shows the banner, polls every 5 seconds on the fake clock, and refuses `R`/`U`/`F`; a stale `running` record offers `U`.

## 9. Documentation

- README: the sets view and set screen in the terminal UI section, and the key table rows for `U`, `F`, and the prompt keys.
- `docs/configuration.md` is unchanged. `make docs` regenerates `docs/cli/` only if help text changes.
- The run sets design §12 points here for the terminal UI panel.

## 10. Acceptance

Against the local Bamboo:

1. `:sets` lists the sets with their last run; `enter` opens one.
2. `R`, a variable, `d` shows the dry run, `ctrl-R` runs it; the board updates live; `esc`, browse a build, return: the train is still running.
3. A failed train: `F`, the required variable, `ctrl-R`: only the failed run restarts.
4. `q` while it runs asks; yes leaves it interrupted; `bam set resume` continues it.
5. A train started with `bam set run` in another terminal shows as running elsewhere and refuses `R`.
6. `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.

## 11. Out of scope

- Editing sets from the UI.
- More than one train per window.
- Cancelling a set's builds from the set screen (`C` on a build in the panels still works).
- The lock `--force` override in the UI.
- Batch files and plan statistics.
