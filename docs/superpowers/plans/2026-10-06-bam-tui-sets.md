# Run sets in the terminal UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `:sets` view on Home and a set screen in the terminal UI that run, watch, resume and rerun release trains, with a vars prompt and a dry-run preview.

**Architecture:** The set board model moves from `view` to `app` (`app.SetBoard`) so the CLI and the UI share it. `app` gains a lock-holder read and a newest-record lookup. The UI adds a sets Home view (`homeSets`, `focusSets`), a set screen (`screenSet`) that seeds a board from the record and feeds it live events from a train held on `Model`, and a vars prompt (`screenSetVars`). Quit and server switch ask while a train runs.

**Tech Stack:** Go, bubbletea, lipgloss, bubbles, testify.

**Spec:** `docs/superpowers/specs/2026-10-06-bam-tui-sets-design.md`

## Global Constraints

- `tui` imports `app`, `provider`, `errs`, `view/style` only — never `config` or `view`. `app` never prints.
- CLI set output must not change: `view` set tests and goldens pass unchanged after Task 1.
- Interrupting or quitting never stops a build; only the train's watching ends, leaving the record `interrupted`.
- Values typed into the vars prompt live only in the prompt and the call.
- `TestEveryBindingAppearsInHelp` and `TestNoKeyIsBoundTwice` keep passing; `a` in the prompt reuses `keys.AllLogs`.
- No test sleeps or touches the network: UI set tests use an instant `app.Clock` and a temp `SetStore`.
- Conventional Commits, one per task; the owner's identity only.
- Gate: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs`. `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. A train must keep running after `esc` from the set screen and after opening one of its builds — `TestSetTrainSurvivesLeavingTheScreen` (Task 5).
2. `ctrl-R` must refuse while a required var row is empty — `TestSetVarsRequiredRowsBlockRun` (Task 5).
3. Quitting while a train runs must ask, and yes must leave the record `interrupted` with no build stopped — `TestQuitWhileATrainRunsAsks` (Task 6).
4. A lock held by another process must refuse `R`/`U`/`F` — `TestSetScreenRefusesWhenHeldElsewhere` (Task 4).
5. Opening a build from the set screen and pressing `esc` must return to the set screen, not Home — `TestSetScreenBuildReturnsToTheSet` (Task 4).

---

### Task 1: `app.SetBoard` — the set state model, shared by CLI and UI

**Files:**
- Create: `internal/app/setboard.go`, `internal/app/setboard_test.go`
- Modify: `internal/view/setwatch.go` (use `app.SetBoard`; delete `setRow`, `setBoard`, `newBoard`, `row`, `apply`, `runOf`, `planRunName`'s board use — keep `planRunName` if `sets.go` still uses it)

**Interfaces:**
- Produces:

```go
type SetBoardRow struct {
	Name     string
	Build    provider.Build
	Progress provider.Progress
	At       time.Time // when Progress arrived
	Err      error
	Rerun    int
}
func NewSetBoard(p SetPlan) *SetBoard
func (b *SetBoard) Seed(rec SetRecord)
func (b *SetBoard) Apply(e SetEvent, now time.Time)
func (b *SetBoard) Name() string
func (b *SetBoard) ID() string
func (b *SetBoard) Stages() [][]SetBoardRow
func (b *SetBoard) Final() *SetEvent
func EventRun(e SetEvent) string   // e.Run, or e.Target for events without one
func PlanRunName(r SetRunPlan) string // r.Name, or r.Target
```

- [ ] **Step 1: Failing tests** — create `internal/app/setboard_test.go`:

```go
package app

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var boardAt = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func boardPlan() SetPlan {
	return SetPlan{Name: "release", Stages: [][]SetRunPlan{
		{{Name: "publish-specs", Target: "publish-specs"}},
		{{Name: "deploy[env=qa]", Target: "deploy"}, {Target: "smoke"}},
	}}
}

func TestSetBoardStartsPendingFromThePlan(t *testing.T) {
	b := NewSetBoard(boardPlan())
	require.Equal(t, "release", b.Name())
	st := b.Stages()
	require.Len(t, st, 2)
	require.Equal(t, "deploy[env=qa]", st[1][0].Name)
	require.Equal(t, "smoke", st[1][1].Name, "a plan run without a name is named after its preset")
	require.Empty(t, st[1][0].Build.Key)
}

func TestSetBoardSeedsFromARecord(t *testing.T) {
	b := NewSetBoard(boardPlan())
	b.Seed(SetRecord{ID: "release-1", Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", BuildKey: "PROJ-SPEC-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{{Target: "deploy", Name: "deploy[env=qa]", BuildKey: "OPS-DEP-9", State: provider.StateFailed, Reruns: 1},
			{Target: "smoke", Error: "queue full"}}},
	}})
	require.Equal(t, "release-1", b.ID())
	st := b.Stages()
	require.Equal(t, provider.StateFailed, st[1][0].Build.State)
	require.Equal(t, 1, st[1][0].Rerun)
	require.EqualError(t, st[1][1].Err, "queue full")
}

func TestSetBoardAppliesEvents(t *testing.T) {
	b := NewSetBoard(boardPlan())
	ev := func(typ SetEventType, run string, bld provider.Build, st provider.State) SetEvent {
		return SetEvent{Type: typ, ID: "release-1", Set: "release", Stage: 1, Target: "deploy", Run: run, Build: bld, State: st}
	}
	b.Apply(ev(SetRunTriggered, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, ""), boardAt)
	inner := Event{Type: EventState, Progress: provider.Progress{Valid: true, Percent: 0.5}}
	e := ev(SetRunEvent, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateRunning}, "")
	e.Event = &inner
	b.Apply(e, boardAt)
	row := b.Stages()[1][0]
	require.Equal(t, provider.StateRunning, row.Build.State)
	require.InDelta(t, 0.5, row.Progress.Percent, 1e-9)
	require.Equal(t, boardAt, row.At)

	done := ev(SetRunDone, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9"}, provider.StateFailed)
	done.Err = errs.Bamboof("boom")
	b.Apply(done, boardAt)
	row = b.Stages()[1][0]
	require.Equal(t, provider.StateFailed, row.Build.State)
	require.False(t, row.Progress.Valid)
	require.Error(t, row.Err)

	restart := ev(SetRunTriggered, "deploy[env=qa]", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	restart.Rerun = 2
	b.Apply(restart, boardAt)
	require.Equal(t, 2, b.Stages()[1][0].Rerun)
	require.NoError(t, b.Stages()[1][0].Err)

	require.Nil(t, b.Final())
	b.Apply(SetEvent{Type: SetDone, ID: "release-1", Stage: 1, State: provider.StateSuccess}, boardAt)
	require.Equal(t, SetDone, b.Final().Type)
}

func TestSetBoardStagesAreCopies(t *testing.T) {
	b := NewSetBoard(boardPlan())
	st := b.Stages()
	st[0][0].Name = "changed"
	require.Equal(t, "publish-specs", b.Stages()[0][0].Name)
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run SetBoard 2>&1 | tail -2` — expected: compile error `undefined: NewSetBoard`.

- [ ] **Step 3: Implement** — create `internal/app/setboard.go`:

```go
package app

import (
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// SetBoardRow is one run of a set as a display knows it.
type SetBoardRow struct {
	Name     string
	Build    provider.Build
	Progress provider.Progress
	At       time.Time // when Progress arrived
	Err      error
	Rerun    int // how many times its build was restarted
}

// SetBoard is what is known about a set run so far: its runs per stage,
// built from a plan or a record and moved on by events. The command line's
// set block and the terminal UI's set screen both draw from it. It never
// prints.
type SetBoard struct {
	name, id string
	stages   [][]*SetBoardRow
	final    *SetEvent
}

// NewSetBoard starts from the plan, so stages that have not started list
// their runs as pending.
func NewSetBoard(p SetPlan) *SetBoard {
	b := &SetBoard{name: p.Name}
	for _, st := range p.Stages {
		rows := make([]*SetBoardRow, 0, len(st))
		for _, r := range st {
			rows = append(rows, &SetBoardRow{Name: PlanRunName(r)})
		}
		b.stages = append(b.stages, rows)
	}
	return b
}

// PlanRunName is a planned run's name: its matrix name, or its preset.
func PlanRunName(r SetRunPlan) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Target
}

// EventRun is the run an event is about: its name, or for events from
// before run names existed, its preset.
func EventRun(e SetEvent) string {
	if e.Run != "" {
		return e.Run
	}
	return e.Target
}

func (b *SetBoard) row(stage int, name string) *SetBoardRow {
	for len(b.stages) <= stage {
		b.stages = append(b.stages, nil)
	}
	for _, r := range b.stages[stage] {
		if r.Name == name {
			return r
		}
	}
	r := &SetBoardRow{Name: name}
	b.stages[stage] = append(b.stages[stage], r)
	return r
}

// Seed fills in what a record already knows, so a resumed or rerun set shows
// the runs that already have a build instead of pending ones.
func (b *SetBoard) Seed(rec SetRecord) {
	b.id = rec.ID
	if b.name == "" {
		b.name = rec.Set
	}
	for i, st := range rec.Stages {
		for _, run := range st.Runs {
			if run.BuildKey == "" && run.Error == "" {
				continue
			}
			r := b.row(i, run.RunName())
			r.Build = provider.Build{Key: run.BuildKey, State: run.State}
			r.Rerun = run.Reruns
			if run.Error != "" {
				r.Err = recordedError(run.Error)
			}
		}
	}
}

// recordedError is an error read back from a record, where only its text
// was kept.
type recordedError string

func (e recordedError) Error() string { return string(e) }

// Apply moves the board on by one event; now stamps the progress it carries.
func (b *SetBoard) Apply(e SetEvent, now time.Time) {
	b.id = e.ID
	if b.name == "" {
		b.name = e.Set
	}
	switch e.Type {
	case SetRunTriggered:
		r := b.row(e.Stage, EventRun(e))
		r.Build, r.Err = e.Build, nil
		if e.Rerun > 0 {
			r.Rerun = e.Rerun
		}
	case SetRunEvent:
		r := b.row(e.Stage, EventRun(e))
		r.Build = e.Build
		if e.Event != nil {
			r.Progress, r.At = e.Event.Progress, now
		}
	case SetRunDone:
		r := b.row(e.Stage, EventRun(e))
		if e.Build.Key != "" {
			r.Build = e.Build
		}
		r.Build.State, r.Err, r.Progress = e.State, e.Err, provider.Progress{}
	case SetDone, SetError:
		ev := e
		b.final = &ev
	}
}

func (b *SetBoard) Name() string { return b.name }
func (b *SetBoard) ID() string   { return b.id }

// Final is the set_done or set_error event, nil while the set runs.
func (b *SetBoard) Final() *SetEvent { return b.final }

// Stages is a copy of the rows, safe to read while the board moves on.
func (b *SetBoard) Stages() [][]SetBoardRow {
	out := make([][]SetBoardRow, len(b.stages))
	for i, st := range b.stages {
		out[i] = make([]SetBoardRow, len(st))
		for j, r := range st {
			out[i][j] = *r
		}
	}
	return out
}
```

`internal/view/setwatch.go`: delete `setRow`, `setBoard`, `newBoard`, `(*setBoard).row`, `(*setBoard).apply`, `runOf` and `planRunName`; replace every use:

- `newBoard(p)` → `app.NewSetBoard(p)`; `&setBoard{}` → `app.NewSetBoard(app.SetPlan{})`.
- `b.apply(e, t)` / `l.b.apply(…)` → `.Apply(e, t)`.
- `SetLive.Seed` becomes `func (l *SetLive) Seed(rec app.SetRecord) { l.b.Seed(rec) }`.
- `summary` becomes `func summary(o Out, b *app.SetBoard)` reading `b.Final()`, `b.Stages()`, `b.ID()` and the row fields `Name`, `Build`, `Err` (formerly `target`, `build`, `err`).
- `SetLive.draw` iterates `l.b.Stages()` and reads `r.Name`, `r.Build`, `r.Progress`, `r.At`, `r.Rerun`, `r.Err`; the header reads `l.b.Name()` and `l.b.ID()`.
- `SetLines.Event` uses `app.EventRun(e)` for the head; `SetLines` holds `b *app.SetBoard` created with `app.NewSetBoard(app.SetPlan{})`.
- `sets.go`'s `SetDryRun` and `SetPlanJSON` call `app.PlanRunName(r)` instead of `planRunName(r)`.
- The `SetLive`/`SetLines` structs' `b` field type becomes `*app.SetBoard`.

- [ ] **Step 4: Run** `go test ./internal/app/ ./internal/view/... ./internal/cli/` — expected PASS with **no golden changes** (`git status --short internal/view/testdata` prints nothing).

- [ ] **Step 5: Commit** `git add internal/app internal/view && git commit -m "refactor: move the set board into app so the UI can share it"`

---

### Task 2: `app` — who holds a set, and its newest run

**Files:**
- Modify: `internal/app/setstore.go` (`Holder`), `internal/app/sets.go` (`LatestSetRun`)
- Test: `internal/app/setstore_test.go`, `internal/app/sets_test.go`

**Interfaces:**
- Produces: `func (s *SetStore) Holder(set string) (pid int, host string, since time.Time, held bool)`; `func (s *Service) LatestSetRun(set string) (SetRecord, bool, error)`.

- [ ] **Step 1: Failing tests** — append to `internal/app/setstore_test.go` (reuse its existing store helper if it has one; otherwise build `&SetStore{Dir: t.TempDir(), Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}`):

```go
func TestSetStoreHolder(t *testing.T) {
	dir := t.TempDir()
	mine := &SetStore{Dir: dir, Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
	other := &SetStore{Dir: dir, Host: func() (string, error) { return "desk", nil }, PID: func() int { return 77 }}
	at := time.Date(2026, 10, 6, 9, 15, 0, 0, time.UTC)

	_, _, _, held := mine.Holder("release")
	require.False(t, held, "no lock file")

	l, err := other.Lock([]string{"release", "release-1"}, false, at)
	require.NoError(t, err)
	pid, host, since, held := mine.Holder("release")
	require.True(t, held)
	require.Equal(t, 77, pid)
	require.Equal(t, "desk", host)
	require.True(t, since.Equal(at))
	l.Release()

	l, err = mine.Lock([]string{"release", "release-2"}, false, at)
	require.NoError(t, err)
	_, _, _, held = mine.Holder("release")
	require.False(t, held, "this process's own lock is not held elsewhere")
	l.Release()
}
```

Append to `internal/app/sets_test.go`:

```go
func TestLatestSetRunIsThisRepositorysNewest(t *testing.T) {
	svc := setService(t, fakeBamboo())
	_, ok, err := svc.LatestSetRun("release")
	require.NoError(t, err)
	require.False(t, ok)
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: twoStages("PROJ-BUILD-1")})
	got, ok, err := svc.LatestSetRun("release")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, rec.ID, got.ID)
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'Holder|LatestSetRun' 2>&1 | tail -2` — expected: compile errors.

- [ ] **Step 3: Implement**

`internal/app/setstore.go` (next to `Lock`; `lockInfo` is the type `Lock` writes):

```go
// Holder reads a set's lock without taking it: who runs the set now, if
// anyone other than this process.
func (s *SetStore) Holder(set string) (pid int, host string, since time.Time, held bool) {
	data, err := os.ReadFile(filepath.Join(s.Dir, set+".lock"))
	if err != nil {
		return 0, "", time.Time{}, false
	}
	var info lockInfo
	if json.Unmarshal(data, &info) != nil {
		return 0, "", time.Time{}, false
	}
	me, _ := s.Host()
	if info.PID == s.PID() && info.Host == me {
		return 0, "", time.Time{}, false
	}
	return info.PID, info.Host, info.Since, true
}
```

`internal/app/sets.go`:

```go
// LatestSetRun is this repository's newest record of set.
func (s *Service) LatestSetRun(set string) (SetRecord, bool, error) {
	return s.Sets.Latest(s.Cfg.RepoRoot, set, nil)
}
```

(Check `SetStore.Lock`'s lock file naming: it writes `<name>.lock` for each name; if the file name differs, use the same helper `Lock` uses.)

- [ ] **Step 4: Run** `go test -race ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git add internal/app && git commit -m "feat: read who holds a set and its newest run"`

---

### Task 3: The sets view on Home (`:sets`)

**Files:**
- Create: `internal/view/tui/sets.go`, `internal/view/tui/sets_test.go`
- Modify: `internal/view/tui/tui.go` (`Deps.Sets`), `internal/view/tui/model.go` (`focusSets`, `sets` list, `setsGen`, `streamSets`, movement/filter cases, `setsLoadedMsg` case), `internal/view/tui/home.go` (`homeSets` view, header, table, keys, sort), `internal/view/tui/command.go` (`:sets`), `internal/view/tui/msgs.go` (stream), `internal/cli/ui.go` (`Sets`)

**Interfaces:**
- Produces: `Deps.Sets func() ([]app.SetInfo, error)`; `type setItem struct { Info app.SetInfo; Last *app.SetRecord }`; `homeSets` view; `focusSets`; `Model.sets listState[setItem]`; `loadSetsCmd(d Deps, svc *app.Service, gen int) tea.Cmd`; `setsLoadedMsg{Gen int; Items []setItem}`; `Model.showSets()`.

- [ ] **Step 1: Failing tests** — create `internal/view/tui/sets_test.go`:

```go
package tui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func setInfos() []app.SetInfo {
	return []app.SetInfo{
		{Name: "release", DefinedIn: []string{".bam.yaml"}, Stages: [][]app.SetRunInfo{
			{{Target: "smoke", Runs: []string{"smoke"}}},
			{{Target: "build", Runs: []string{"build[env=qa]", "build[env=prod]"}}},
		}},
		{Name: "nightly", DefinedIn: []string{".bam.yaml"}, Stages: [][]app.SetRunInfo{{{Target: "build", Runs: []string{"build"}}}}},
	}
}

// setsModel is Home on the sets view with release failed 12 minutes ago and
// nightly never run.
func setsModel(t *testing.T) Model {
	m := goldenModel(80, 24)
	m.screen = screenHome
	m.svc = testService()
	m.svc.Sets = &app.SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
	last := app.SetRecord{ID: "release-20261006T084800Z", Set: "release", Status: app.SetFailed,
		StartedAt: goldenNow.Add(-12 * time.Minute)}
	m.home.view = homeSets
	m.focus = focusSets
	m.sets.setItems([]setItem{{Info: setInfos()[0], Last: &last}, {Info: setInfos()[1]}})
	return m
}

func TestSetsGolden80x24(t *testing.T) {
	requireGolden(t, "sets-80x24", setsModel(t).View())
}

func TestSetsTableCountsExpandedRunsAndLastRun(t *testing.T) {
	v := setsModel(t).View()
	require.Regexp(t, `release\s+2\s+3\s+✗ failed\s+12m`, v)
	require.Regexp(t, `nightly\s+1\s+1\s+–`, v)
}

func TestSetsCommandShowsTheSetsView(t *testing.T) {
	m := goldenModel(80, 24)
	m.screen = screenHome
	m.deps.Sets = func() ([]app.SetInfo, error) { return setInfos(), nil }
	m.svc = testService()
	m.svc.Sets = &app.SetStore{Dir: t.TempDir()}
	next, cmd, err := m.runCommand("sets")
	require.NoError(t, err)
	m = next.(Model)
	require.Equal(t, homeSets, m.home.view)
	require.Equal(t, focusSets, m.focus)
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.Equal(t, 2, m.sets.len())
}

func TestSetsFilterAndMove(t *testing.T) {
	m := setsModel(t)
	m, _ = send(m, mkKey("j"))
	s, _ := m.sets.selected()
	require.Equal(t, "nightly", s.Info.Name)
	m.applyFilter("rel")
	require.Equal(t, 1, m.sets.len())
}

func TestSetStatusCell(t *testing.T) {
	require.Equal(t, "–", setStatusCell(nil))
	for st, want := range map[app.SetStatus]string{
		app.SetRunning: "running", app.SetSuccessful: "successful", app.SetFailed: "failed",
		app.SetInterrupted: "interrupted", app.SetTimedOut: "timed out",
	} {
		require.Contains(t, setStatusCell(&app.SetRecord{Status: st}), want)
	}
	_ = provider.StateSuccess
}
```

(If `runCommand` is not the name of the `:` bar's dispatcher, use the function `command.go` exposes for running a command line by name — the same one its tests call.)

- [ ] **Step 2: Run** `go test ./internal/view/tui/ -run 'Sets|SetStatusCell' 2>&1 | tail -3` — expected: compile errors.

- [ ] **Step 3: Implement**

- `tui.go` `Deps` gains `Sets func() ([]app.SetInfo, error) // re-read on every load, as Targets is`.
- `cli/ui.go` fills it like `Targets`:

```go
		Sets: func() ([]app.SetInfo, error) {
			fresh, err := r.loadConfig()
			if err != nil {
				return nil, err
			}
			return app.DescribeSets(fresh, r.env.Getenv)
		},
```

- `model.go`: `focusSets` appended to the `focus` constants (not to `tabOrder`); `sets listState[setItem]` on `Model`, created in `New` with `newList(func(s setItem) string { return s.Info.Name })`; `setsGen int` with the other generations; `moveFocused`, `jumpFocused` and `applyFilter` gain a `focusSets` case on `m.sets`; `currentStream` gains `streamSets` → `m.setsGen`; `Update` gains:

```go
	case setsLoadedMsg:
		if msg.Gen != m.setsGen {
			return m, nil
		}
		m.sets.loading = false
		m.sets.setItems(msg.Items)
		return m, nil
```

- `msgs.go`: `streamSets` in the stream constants.
- `home.go`: `homeSets` after `homePresets`; `homeColNames[homeSets] = {"set", "state", "age"}`; `homeFocus` returns `focusSets` for `homeSets`; `homeHeader` uses `countOf(m.sets.len(), "set")` and the sets sort for `homeSets`; `homeView` draws `m.setTable(m.width, body)` for `homeSets`; `back()`'s Home case also clears `m.sets` query; `handleHomeKey` for `homeSets`: `enter` → `m.openSet()` (Task 4; until then a no-op returning `m, nil, true`), `R` → `m.openSetVars(setActionRun)` (Task 5; no-op until then), `s` → next sort over 3 columns, `r` → reload sets; panel keys (`1`/`2`/`3`, `tab`) are ignored on this view.
- `command.go`: `{"sets", "", "the sets table", func(m Model, _ string) (tea.Model, tea.Cmd, error) { next, cmd := m.showSets(); return next, cmd, nil }}` after `presets`.
- `sets.go`:

```go
package tui

import (
	"cmp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// setItem is one row of the sets table: a configured set and this
// repository's newest record of it.
type setItem struct {
	Info app.SetInfo
	Last *app.SetRecord
}

type setsLoadedMsg struct {
	Gen   int
	Items []setItem
}

// loadSetsCmd reads the sets from the configuration and each one's newest
// record. Both are files, so the load is cheap and needs no server.
func loadSetsCmd(d Deps, svc *app.Service, gen int) tea.Cmd {
	return func() tea.Msg {
		if d.Sets == nil {
			return setsLoadedMsg{Gen: gen}
		}
		infos, err := d.Sets()
		if err != nil {
			return errMsg{Err: err, Where: "sets", Stream: streamSets, Gen: gen}
		}
		items := make([]setItem, 0, len(infos))
		for _, in := range infos {
			it := setItem{Info: in}
			if svc != nil && svc.Sets != nil {
				if rec, ok, err := svc.LatestSetRun(in.Name); err == nil && ok {
					it.Last = &rec
				}
			}
			items = append(items, it)
		}
		return setsLoadedMsg{Gen: gen, Items: items}
	}
}

func (m *Model) loadSets() tea.Cmd {
	m.setsGen++
	m.sets.loading = true
	return loadSetsCmd(m.deps, m.svc, m.setsGen)
}

// showSets picks Home's sets table (:sets).
func (m Model) showSets() (tea.Model, tea.Cmd) {
	m.home.view = homeSets
	load := m.loadSets()
	next, home := m.goHome()
	return next, tea.Batch(load, home)
}

// runCount is how many runs a set has after matrix expansion.
func runCount(in app.SetInfo) int {
	n := 0
	for _, st := range in.Stages {
		for _, r := range st {
			n += max(len(r.Runs), 1)
		}
	}
	return n
}

// setStatusState maps a record's status onto the build state whose glyph
// and colour it shares.
func setStatusState(s app.SetStatus) provider.State {
	switch s {
	case app.SetRunning:
		return provider.StateRunning
	case app.SetSuccessful:
		return provider.StateSuccess
	case app.SetFailed:
		return provider.StateFailed
	}
	return provider.StateStopped
}

// setStatusCell is the LAST RUN cell: the status's glyph and its words.
func setStatusCell(rec *app.SetRecord) string {
	if rec == nil {
		return "–"
	}
	glyph := strings.SplitN(stateCell(setStatusState(rec.Status)), " ", 2)[0]
	return glyph + " " + strings.ReplaceAll(string(rec.Status), "_", " ")
}

func setColumns(width int, rows []setItem) []tableCol {
	nameW := len("SET")
	for _, s := range rows {
		nameW = max(nameW, len(s.Info.Name))
	}
	cols := []tableCol{
		{title: "SET", width: min(nameW, 24)},
		{title: "STAGES", width: 6, right: true},
		{title: "RUNS", width: 4, right: true},
		{title: "LAST RUN"},
		{title: "AGE", width: 5},
	}
	return fillWidth(cols, width, "LAST RUN")
}

func (m Model) setTable(width, height int) []string {
	rows := m.sets.rows()
	cols := setColumns(width, rows)
	start, end := m.sets.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, s := range rows[start:end] {
		age := "–"
		if s.Last != nil {
			age = ageShort(m.now().Sub(s.Last.StartedAt))
		}
		status := setStatusCell(s.Last)
		if m.train.running() && m.train.set == s.Info.Name {
			status, age = setStatusCell(&app.SetRecord{Status: app.SetRunning}), "now"
		}
		row := make([]string, len(cols))
		for i, c := range cols {
			switch c.title {
			case "SET":
				row[i] = s.Info.Name
			case "STAGES":
				row[i] = strconv.Itoa(len(s.Info.Stages))
			case "RUNS":
				row[i] = strconv.Itoa(runCount(s.Info))
			case "LAST RUN":
				row[i] = status
			case "AGE":
				row[i] = age
			}
		}
		cells = append(cells, row)
	}
	return tableLines(cols, cells, m.sets.cursor-start)
}

// sortSets orders the sets by the sets sort: name, last-run status, or age.
func (m *Model) sortSets() {
	s := m.home.setSort
	keep, had := m.sets.selected()
	items := m.sets.items
	rank := func(it setItem) int {
		if it.Last == nil {
			return 9
		}
		return stateRank(&provider.BuildSummary{State: setStatusState(it.Last.Status)})
	}
	started := func(it setItem) time.Time {
		if it.Last == nil {
			return time.Time{}
		}
		return it.Last.StartedAt
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		var c int
		switch s.col {
		case colName:
			c = cmp.Compare(rank(a), rank(b))
		case colState:
			c = compareAge(started(a), started(b))
		default:
			c = strings.Compare(a.Info.Name, b.Info.Name)
		}
		if c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return a.Info.Name < b.Info.Name
	})
	m.sets.refilter()
	if had {
		m.sets.selectFirst(func(it setItem) bool { return it.Info.Name == keep.Info.Name })
	}
}
```

  (`homeState` gains `setSort homeSort`. The sets table's columns map to `homeCol` as set=`colKey`, state=`colName`, age=`colState`; `s` steps through these three, wrapping after `colState`. `m.train` is introduced in Task 5 — until then, add `train trainState` with `func (t trainState) running() bool { return false }` in `sets.go` so this compiles; Task 5 replaces it.)

- [ ] **Step 4: Run** `go test ./internal/view/tui/ -run 'Sets|SetStatusCell' -update`, review `testdata/golden/sets-80x24.txt` (the header names 2 sets; the table shows release `2 3 ✗ failed 12m` and nightly `1 1 – –`), then `go test ./internal/view/tui/ ./internal/cli/` — PASS, with no other golden changed.

- [ ] **Step 5: Commit** `git add internal/view/tui internal/cli && git commit -m "feat: list run sets on the terminal UI's Home"`

---

### Task 4: The set screen

**Files:**
- Create: `internal/view/tui/setscreen.go`, `internal/view/tui/setscreen_test.go`
- Modify: `internal/view/tui/model.go` (`screenSet`, `View`, `dispatchKey`, `back`, `fromSet`), `internal/view/tui/keys.go` (`Resume` `U`, `RerunFailed` `F`, help rows), `internal/view/tui/panels.go` (`keyLine` for `screenSet`)

**Interfaces:**
- Consumes: `app.SetBoard`, `app.NewSetBoard`, `app.PlanRunName` (Task 1); `Service.LatestSetRun`, `SetStore.Holder` (Task 2); `setItem`, `m.sets` (Task 3).
- Produces: `screenSet`; `type setScreen struct { info app.SetInfo; rec *app.SetRecord; board *app.SetBoard; cursor int; elsewhere string }` on `Model` as `setView`; `Model.openSet() (tea.Model, tea.Cmd)`; `setLoadedMsg{Gen int; Rec *app.SetRecord; Elsewhere string}`; `setGen int`; `keys.Resume`, `keys.RerunFailed`; `planOf(info app.SetInfo) app.SetPlan`.

- [ ] **Step 1: Failing tests** — create `internal/view/tui/setscreen_test.go`:

```go
package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func failedRec() *app.SetRecord {
	return &app.SetRecord{ID: "release-20261006T084800Z", Set: "release", Status: app.SetFailed,
		StartedAt: goldenNow.Add(-12 * time.Minute), Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "smoke", Name: "smoke", BuildKey: "PROJ-PROV12-8", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{
				{Target: "build", Name: "build[env=qa]", BuildKey: "PROJ-BUILD-44", State: provider.StateFailed, Reruns: 1},
				{Target: "build", Name: "build[env=prod]", BuildKey: "PROJ-BUILD-45", State: provider.StateSuccess},
			}}}}
}

// setScreenModel is the set screen on release with rec loaded.
func setScreenModel(t *testing.T, rec *app.SetRecord, elsewhere string) Model {
	m := setsModel(t)
	next, _ := m.openSet()
	m = next.(Model)
	m, _ = send(m, setLoadedMsg{Gen: m.setGen, Rec: rec, Elsewhere: elsewhere})
	return m
}

func TestSetScreenGoldens(t *testing.T) {
	requireGolden(t, "set-failed-80x24", setScreenModel(t, failedRec(), "").View())
	requireGolden(t, "set-elsewhere-80x24", setScreenModel(t, &app.SetRecord{ID: "release-20261006T091500Z", Set: "release",
		Status: app.SetRunning, StartedAt: goldenNow}, "pid 77 on desk, since 09:15").View())
	requireGolden(t, "set-new-80x24", setScreenModel(t, nil, "").View())
}

func TestSetScreenOffersOnlyTheKeysThatApply(t *testing.T) {
	failed := setScreenModel(t, failedRec(), "")
	require.Contains(t, failed.keyLine(), "F rerun failed")
	require.NotContains(t, failed.keyLine(), "U resume")
	interrupted := failedRec()
	interrupted.Status = app.SetInterrupted
	m := setScreenModel(t, interrupted, "")
	require.Contains(t, m.keyLine(), "U resume")
	require.NotContains(t, m.keyLine(), "F rerun")
	require.Contains(t, setScreenModel(t, nil, "").keyLine(), "R run")
}

func TestSetScreenBuildReturnsToTheSet(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("j")) // build[env=qa], the failed run
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenColumns, m.screen)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenSet, m.screen, "esc from a set's build returns to the set")
}

func TestSetScreenRefusesWhenHeldElsewhere(t *testing.T) {
	m := setScreenModel(t, &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetRunning}, "pid 77 on desk, since 09:15")
	for _, k := range []string{"R", "U", "F"} {
		next, _ := send(m, mkKey(k))
		require.Equal(t, screenSet, next.screen, k)
		require.Contains(t, next.status, "running in another bam (pid 77 on desk, since 09:15)", k)
	}
}

func TestSetScreenEscReturnsToTheSetsTable(t *testing.T) {
	m := setScreenModel(t, failedRec(), "")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homeSets, m.home.view)
}
```

- [ ] **Step 2: Run** `go test ./internal/view/tui/ -run SetScreen 2>&1 | tail -3` — expected: compile errors.

- [ ] **Step 3: Implement**

`keys.go`: `Resume: b("U", "U", "resume the set run")`, `RerunFailed: b("F", "F", "rerun the failed runs")` on `keyMap`, in `allBindings`, and help rows under RUN: `{"RUN", "U", "resume the set's interrupted run (set screen)"}`, `{"RUN", "F", "rerun the set's failed runs (set screen)"}`; the COMMANDS section picks up `:sets` from the command table.

`model.go`: `screenSet` and `screenSetVars` (Task 5 uses it) in the `screen` constants; `setView setScreen`, `setGen int` and `fromSet bool` on `Model`; `View` routes `screenSet` to `m.overlayView(m.setScreenView())`; `dispatchKey` routes `screenSet` to `m.handleSetKey(msg)` after the overlay check; `Update` gains the `setLoadedMsg` case below; `back()` gains, before the `m.focus != focusPlans` case:

```go
	case m.screen == screenColumns && m.fromSet:
		m.fromSet = false
		m.stopWatch()
		m.screen = screenSet
		return m, nil
	case m.screen == screenSet:
		return m.showSets()
```

`setscreen.go`:

```go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/key"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// setScreen is the set screen's state: the set, its newest record, the
// board drawn from them, and whether another bam holds the set.
type setScreen struct {
	info      app.SetInfo
	rec       *app.SetRecord
	board     *app.SetBoard
	cursor    int    // over every run, stage after stage
	elsewhere string // "pid 77 on desk, since 09:15" when another bam holds the set
}

type setLoadedMsg struct {
	Gen       int
	Rec       *app.SetRecord
	Elsewhere string
}

// planOf is enough of a plan for a board: the set's runs by name.
func planOf(info app.SetInfo) app.SetPlan {
	p := app.SetPlan{Name: info.Name}
	for _, st := range info.Stages {
		var runs []app.SetRunPlan
		for _, r := range st {
			names := r.Runs
			if len(names) == 0 {
				names = []string{r.Target}
			}
			for _, n := range names {
				runs = append(runs, app.SetRunPlan{Name: n, Target: r.Target})
			}
		}
		p.Stages = append(p.Stages, runs)
	}
	return p
}

func loadSetCmd(svc *app.Service, set string, gen int) tea.Cmd {
	return func() tea.Msg {
		msg := setLoadedMsg{Gen: gen}
		if svc == nil || svc.Sets == nil {
			return msg
		}
		if rec, ok, err := svc.LatestSetRun(set); err == nil && ok {
			msg.Rec = &rec
		}
		if pid, host, since, held := svc.Sets.Holder(set); held {
			msg.Elsewhere = fmt.Sprintf("pid %d on %s, since %s", pid, host, since.Local().Format("15:04"))
		}
		return msg
	}
}

// openSet opens the set screen on the selected set.
func (m Model) openSet() (tea.Model, tea.Cmd) {
	it, ok := m.sets.selected()
	if !ok {
		return m, nil
	}
	m.screen = screenSet
	m.home.tickGen++
	m.setView = setScreen{info: it.Info, rec: it.Last, board: m.boardFor(it.Info, it.Last)}
	m.setGen++
	return m, loadSetCmd(m.svc, it.Info.Name, m.setGen)
}

// boardFor is the live board when this window runs the set, else a board
// seeded from rec.
func (m Model) boardFor(info app.SetInfo, rec *app.SetRecord) *app.SetBoard {
	if m.train.running() && m.train.set == info.Name {
		return m.train.board
	}
	b := app.NewSetBoard(planOf(info))
	if rec != nil {
		b.Seed(*rec)
	}
	return b
}

func (m Model) handleSetLoaded(msg setLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.setGen {
		return m, nil
	}
	m.setView.rec, m.setView.elsewhere = msg.Rec, msg.Elsewhere
	m.setView.board = m.boardFor(m.setView.info, msg.Rec)
	return m, nil
}

// setRows is every run of the board, stage after stage.
func (m Model) setRows() []app.SetBoardRow {
	var out []app.SetBoardRow
	for _, st := range m.setView.board.Stages() {
		out = append(out, st...)
	}
	return out
}

// setActions says which of R, U and F apply to the set now.
func (m Model) setActions() (run, resume, rerun bool) {
	if m.train.running() || m.setView.elsewhere != "" {
		return false, false, false
	}
	run = true
	if r := m.setView.rec; r != nil {
		resume = r.Status == app.SetInterrupted || r.Status == app.SetTimedOut || r.Status == app.SetRunning
		rerun = r.Status == app.SetFailed
	}
	return run, resume, rerun
}

// refuseSetAction says why R, U or F cannot act, or "" when they can.
func (m Model) refuseSetAction() string {
	switch {
	case m.setView.elsewhere != "":
		return "running in another bam (" + m.setView.elsewhere + ")"
	case m.train.running():
		return m.train.set + " is running in this window"
	}
	return ""
}

func (m Model) handleSetKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.setRows()
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Back):
		return m.back()
	case key.Matches(msg, keys.Down):
		m.setView.cursor = min(m.setView.cursor+1, max(len(rows)-1, 0))
	case key.Matches(msg, keys.Up):
		m.setView.cursor = max(m.setView.cursor-1, 0)
	case key.Matches(msg, keys.Enter):
		return m.openSetBuild()
	case key.Matches(msg, keys.Open), key.Matches(msg, keys.Copy):
		if m.setView.cursor < len(rows) && rows[m.setView.cursor].Build.Key != "" && m.svc != nil {
			url := m.svc.P.URL(rows[m.setView.cursor].Build.Key)
			if key.Matches(msg, keys.Open) {
				return m.openURL(url)
			}
			return m.copyURL(url)
		}
	case key.Matches(msg, keys.Refresh):
		m.setGen++
		return m, loadSetCmd(m.svc, m.setView.info.Name, m.setGen)
	case key.Matches(msg, keys.Run), key.Matches(msg, keys.Resume), key.Matches(msg, keys.RerunFailed):
		if why := m.refuseSetAction(); why != "" {
			m.status = why
			return m, nil
		}
		run, resume, rerun := m.setActions()
		switch {
		case key.Matches(msg, keys.Run) && run:
			return m.openSetVars(setActionRun)
		case key.Matches(msg, keys.Resume) && resume:
			return m.openSetVars(setActionResume)
		case key.Matches(msg, keys.RerunFailed) && rerun:
			return m.openSetVars(setActionRerun)
		}
	case key.Matches(msg, keys.Help):
		return m.openHelp()
	case key.Matches(msg, keys.ExpandErr):
		if m.err != nil {
			m.overlay = overlayError
		}
	}
	return m, nil
}

// openSetBuild opens the run under the cursor in the panels; esc there
// returns here.
func (m Model) openSetBuild() (tea.Model, tea.Cmd) {
	rows := m.setRows()
	if m.setView.cursor >= len(rows) || rows[m.setView.cursor].Build.Key == "" || m.svc == nil {
		return m, nil
	}
	b := rows[m.setView.cursor].Build
	m.fromSet = true
	m.screen = screenColumns
	m.focus = focusMain
	m.detail = &b
	m.progress = provider.Progress{}
	m.expanded = defaultExpanded(b)
	m.treeCursor = 0
	return m.startWatch(b.Key)
}

func (m Model) setScreenView() string {
	head := []string{m.setView.info.Name}
	switch {
	case m.train.running() && m.train.set == m.setView.info.Name:
		head = append(head, m.train.board.ID(), "running")
	case m.setView.rec != nil:
		head = append(head, m.setView.rec.ID, strings.ReplaceAll(string(m.setView.rec.Status), "_", " "))
	default:
		head = append(head, "never run")
	}
	lines := []string{" " + strings.Join(head, " · ")}
	if m.setView.elsewhere != "" {
		lines = append(lines, " running in another bam ("+m.setView.elsewhere+")")
	}
	stages := m.setView.board.Stages()
	idx := 0
	for i, st := range stages {
		lines = append(lines, fmt.Sprintf(" stage %d/%d", i+1, len(stages)))
		cols := []tableCol{{title: "RUN"}, {title: "BUILD", width: 16}, {title: "STATE", width: 18}, {title: "TIME", width: 8, right: true}}
		cols = fillWidth(cols, m.width-1, "RUN")
		var cells [][]string
		for _, r := range st {
			cells = append(cells, m.setRowCells(r))
		}
		for j, l := range tableLines(cols, cells, m.setView.cursor-idx)[1:] {
			_ = j
			lines = append(lines, " "+l)
		}
		idx += len(st)
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

// setRowCells is one run row: glyph and name, build key, state (with its
// rerun count), and its duration or progress.
func (m Model) setRowCells(r app.SetBoardRow) []string {
	state, key := "pending", "–"
	if r.Build.Key != "" || r.Err != nil {
		state = stateCell(r.Build.State)
	}
	if r.Build.Key != "" {
		key = r.Build.Key
	}
	if r.Rerun > 0 {
		state += fmt.Sprintf(" (rerun %d)", r.Rerun)
	}
	when := ""
	switch {
	case r.Build.State == provider.StateRunning && r.Progress.Valid:
		when = fmt.Sprintf("%d%%", int(r.Progress.Percent*100))
	case r.Build.Duration > 0:
		when = durationShort(r.Build.Duration)
	}
	return []string{r.Name, key, state, when}
}
```

  (`tableLines` returns the header line first; the set screen draws each stage's rows without that header, hence `[1:]`. Use the package's existing `durationShort`/`ageShort` helper for durations — whichever the detail panel uses — and its existing URL open/copy helpers in place of `openURL`/`copyURL` if they are named differently.)

`Update` gains:

```go
	case setLoadedMsg:
		return m.handleSetLoaded(msg)
```

`panels.go` `keyLine`, before the Home case:

```go
	if m.screen == screenSet {
		parts := []string{"enter build", "o open", "y copy"}
		run, resume, rerun := m.setActions()
		if run {
			parts = append(parts, "R run")
		}
		if resume {
			parts = append(parts, "U resume")
		}
		if rerun {
			parts = append(parts, "F rerun failed")
		}
		return strings.Join(append(parts, "r refresh", "esc back"), "  ")
	}
```

`openSetVars` and the `setAction*` constants are Task 5's; until then add to `setscreen.go`:

```go
type setAction int

const (
	setActionRun setAction = iota
	setActionResume
	setActionRerun
)

func (m Model) openSetVars(a setAction) (tea.Model, tea.Cmd) { return m, nil }
```

and wire Home's `enter` on `homeSets` to `m.openSet()`.

- [ ] **Step 4: Run** `go test ./internal/view/tui/ -run 'SetScreen|EveryBinding|NoKeyIsBoundTwice' -update`, review the three new goldens (failed: header `release · release-20261006T084800Z · failed`, the failed run `✗ failed (rerun 1)`; elsewhere: the `running in another bam` line; new: `never run`, every run pending), then `go test ./internal/view/tui/` — PASS, no other golden changed except `help-80x24` if its visible rows changed (review it).

- [ ] **Step 5: Commit** `git add internal/view/tui && git commit -m "feat: show a set and its runs on a set screen"`

---

### Task 5: Running trains from the UI — vars prompt, dry run, live board

**Files:**
- Create: `internal/view/tui/setvars.go`, `internal/view/tui/setvars_test.go`
- Modify: `internal/view/tui/setscreen.go` (drop the Task 4 stubs), `internal/view/tui/sets.go` (drop the `trainState` stub), `internal/view/tui/model.go` (`train trainState`, `screenSetVars` routing, `setEventMsg`/`setClosedMsg`), `internal/view/tui/keys.go` (`RemoveRow` `x`, help rows), `internal/view/tui/panels.go` (`keyLine` for `screenSetVars`)

**Interfaces:**
- Consumes: `Service.PlanSet`, `RunSet`, `ResumeSet`, `RerunSet`; `app.SetBoard`.
- Produces: `type trainState struct { set string; cancel context.CancelFunc; ch <-chan app.SetEvent; board *app.SetBoard; gen int }` with `running() bool`; `type setVarsState struct { action setAction; rows []setVarRow; cursor int; editing bool; editName bool; err error }`; `type setVarRow struct { Name, Value string; Required bool }`; `setEventMsg{Gen int; Event app.SetEvent}`, `setClosedMsg{Gen int}`; `keys.RemoveRow`.

- [ ] **Step 1: Failing tests** — create `internal/view/tui/setvars_test.go`:

```go
package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// instantClock fires every After at once, so a train runs to its end.
type instantClock struct{}

func (instantClock) Now() time.Time                         { return goldenNow }
func (instantClock) After(time.Duration) <-chan time.Time { c := make(chan time.Time, 1); c <- goldenNow; return c }

// trainModel is the set screen on a one-stage set "release" whose run
// triggers PROJ-BUILD-50 and succeeds, with a real set store.
func trainModel(t *testing.T) Model {
	m := goldenModel(80, 24)
	svc := testService()
	svc.Clock = instantClock{}
	svc.Sets = &app.SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
	svc.Cfg.Machine.Sets = map[string]config.Set{"release": {Stages: []config.Stage{{{Target: "build"}}}}}
	svc.Origin = labOrigin
	f := svc.P.(*fake.Provider)
	f.TriggerResults = map[string]provider.Build{"PROJ-BUILD": {Key: "PROJ-BUILD-50", PlanKey: "PROJ-BUILD", State: provider.StateQueued}}
	f.Sequences = map[string][]provider.Build{"PROJ-BUILD-50": {{Key: "PROJ-BUILD-50", State: provider.StateRunning}, {Key: "PROJ-BUILD-50", State: provider.StateSuccess}}}
	m.svc = svc
	m.screen = screenHome
	m.home.view = homeSets
	m.focus = focusSets
	infos, err := app.DescribeSets(svc.Cfg, func(string) string { return "" })
	require.NoError(t, err)
	m.sets.setItems([]setItem{{Info: infos[0]}})
	next, _ := m.openSet()
	return next.(Model)
}

// pump runs cmd and feeds every message it yields back into m until the
// train's channel closes.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 200; i++ {
		msg := cmd()
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				if c != nil {
					if mm, ok := c().(setEventMsg); ok {
						m, cmd = send(m, mm)
					}
				}
			}
			continue
		}
		switch msg.(type) {
		case setEventMsg, setClosedMsg:
			m, cmd = send(m, msg)
		default:
			return m
		}
	}
	return m
}

func TestSetRunFromTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSetVars, m.screen)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Equal(t, screenSet, m.screen)
	require.True(t, m.train.running())
	m = pump(t, m, cmd)
	require.False(t, m.train.running(), "the train ended")
	require.Contains(t, m.status, "release succeeded")
	require.Equal(t, provider.StateSuccess, m.setRows()[0].Build.State)
}

func TestSetVarsAddEditRemove(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, mkKey("a"))
	require.Len(t, m.setVars.rows, 1)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("env")})
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("qa")})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, setVarRow{Name: "env", Value: "qa"}, m.setVars.rows[0])
	require.Equal(t, []string{"env=qa"}, m.setVars.flags())
	m, _ = send(m, mkKey("x"))
	require.Empty(t, m.setVars.rows)
}

func TestSetVarsRequiredRowsBlockRun(t *testing.T) {
	m := trainModel(t)
	m.setView.rec = &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetFailed, VarNames: []string{"env"}}
	m, _ = send(m, mkKey("F"))
	require.Equal(t, screenSetVars, m.screen)
	require.True(t, m.setVars.rows[0].Required)
	m, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.Nil(t, cmd)
	require.Equal(t, screenSetVars, m.screen)
	require.Contains(t, errorWhat(m.setVars.err), "env needs a value")
}

func TestSetVarsDryRunShowsEveryRun(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, mkKey("d"))
	require.Equal(t, overlayDryRun, m.overlay)
	require.Contains(t, m.View(), "PROJ-BUILD")
}

func TestSetTrainSurvivesLeavingTheScreen(t *testing.T) {
	m := trainModel(t)
	m, _ = send(m, mkKey("R"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.True(t, m.train.running())
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.True(t, m.train.running(), "leaving the screen does not stop the train")
	require.Contains(t, m.View(), "running")
}

func TestOnlyOneTrainPerWindow(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "other", ch: make(chan app.SetEvent), cancel: func() {}, board: app.NewSetBoard(app.SetPlan{Name: "other"})}
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenSet, m.screen)
	require.Contains(t, m.status, "other is running in this window")
	_ = context.Background
}
```

- [ ] **Step 2: Run** `go test ./internal/view/tui/ -run 'SetRun|SetVars|SetTrain|OnlyOneTrain' 2>&1 | tail -3` — expected: compile errors.

- [ ] **Step 3: Implement**

Remove the Task 3 `trainState` stub and the Task 4 `setAction`/`openSetVars` stubs. `keys.go`: `RemoveRow: b("x", "x", "remove the variable row")`, in `allBindings`, help row `{"RUN", "x", "remove a variable row (set vars prompt)"}`; extend the `a` help row's text to `all logs, not only failed; in the set vars prompt, add a variable`.

`model.go`: `train trainState` and `setVars setVarsState` on `Model`; `dispatchKey` routes `screenSetVars` to `m.handleSetVarsKey(msg)`; `View` routes `screenSetVars` to `m.overlayView(m.setVarsView())`; `Update` gains:

```go
	case setEventMsg:
		if msg.Gen != m.train.gen {
			return m, nil
		}
		return m.handleSetEvent(msg.Event)
	case setClosedMsg:
		if msg.Gen != m.train.gen {
			return m, nil
		}
		m.train.end()
		return m, nil
```

`setvars.go`:

```go
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

type setAction int

const (
	setActionRun setAction = iota
	setActionResume
	setActionRerun
)

func (a setAction) verb() string {
	return [...]string{"Run", "Resume", "Rerun the failed runs of"}[a]
}

// trainState is the one set this window runs: its context, its events and
// the board they move.
type trainState struct {
	set    string
	cancel context.CancelFunc
	ch     <-chan app.SetEvent
	board  *app.SetBoard
	gen    int
}

func (t trainState) running() bool { return t.ch != nil }

// end forgets the train once its channel closed; the board stays readable
// on the set screen through setView.
func (t *trainState) end() {
	if t.cancel != nil {
		t.cancel()
	}
	t.cancel, t.ch = nil, nil
}

type setVarRow struct {
	Name, Value string
	Required    bool // the run was started with it; resume and rerun need it again
}

type setVarsState struct {
	action   setAction
	rows     []setVarRow
	cursor   int
	editing  bool
	editName bool // a new row: its name first, then its value
	input    textinput.Model
	err      error
}

// flags is the rows as --var flags, in order, skipping rows with no name.
func (s setVarsState) flags() []string {
	var out []string
	for _, r := range s.rows {
		if r.Name != "" {
			out = append(out, r.Name+"="+r.Value)
		}
	}
	return out
}

// missing names a required row with no value, or "" when none.
func (s setVarsState) missing() string {
	for _, r := range s.rows {
		if r.Required && r.Value == "" {
			return r.Name
		}
	}
	return ""
}

type setEventMsg struct {
	Gen   int
	Event app.SetEvent
}

type setClosedMsg struct{ Gen int }

func setEventCmd(ch <-chan app.SetEvent, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return setClosedMsg{Gen: gen}
		}
		return setEventMsg{Gen: gen, Event: e}
	}
}

// openSetVars opens the prompt for a. Resume and rerun ask again for every
// name the record was started with; with none, they act at once.
func (m Model) openSetVars(a setAction) (tea.Model, tea.Cmd) {
	m.setVars = setVarsState{action: a, input: textinput.New()}
	if a != setActionRun && m.setView.rec != nil {
		for _, n := range m.setView.rec.VarNames {
			m.setVars.rows = append(m.setVars.rows, setVarRow{Name: n, Required: true})
		}
		if len(m.setVars.rows) == 0 {
			return m.startTrain()
		}
	}
	m.screen = screenSetVars
	return m, nil
}

func (m Model) handleSetVarsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	v := &m.setVars
	if v.editing {
		switch msg.Type {
		case tea.KeyEsc:
			v.editing = false
			return m, nil
		case tea.KeyEnter:
			r := &v.rows[v.cursor]
			if v.editName {
				r.Name, v.editName = strings.TrimSpace(v.input.Value()), false
				v.input.SetValue(r.Value)
				return m, nil
			}
			r.Value, v.editing = v.input.Value(), false
			return m, nil
		}
		var cmd tea.Cmd
		v.input, cmd = v.input.Update(msg)
		return m, cmd
	}
	switch {
	case key.Matches(msg, keys.Back):
		m.screen = screenSet
		m.setVars = setVarsState{}
		return m, nil
	case key.Matches(msg, keys.Quit):
		return m.quit()
	case key.Matches(msg, keys.Down):
		v.cursor = min(v.cursor+1, max(len(v.rows)-1, 0))
	case key.Matches(msg, keys.Up):
		v.cursor = max(v.cursor-1, 0)
	case key.Matches(msg, keys.AllLogs): // a: add a row
		v.rows = append(v.rows, setVarRow{})
		v.cursor = len(v.rows) - 1
	case key.Matches(msg, keys.RemoveRow):
		if v.cursor < len(v.rows) && !v.rows[v.cursor].Required {
			v.rows = append(v.rows[:v.cursor], v.rows[v.cursor+1:]...)
			v.cursor = max(min(v.cursor, len(v.rows)-1), 0)
		}
	case key.Matches(msg, keys.Enter):
		if v.cursor < len(v.rows) {
			v.editing, v.editName = true, v.rows[v.cursor].Name == ""
			v.input.SetValue("")
			if !v.editName {
				v.input.SetValue(v.rows[v.cursor].Value)
			}
			v.input.Focus()
			return m, textinput.Blink
		}
	case key.Matches(msg, keys.DryRun):
		return m.setDryRun()
	case key.Matches(msg, keys.Trigger):
		if n := v.missing(); n != "" {
			v.err = errs.Usagef("%s needs a value", n).WithWhy("the run was started with it, and bam never stores values")
			return m, nil
		}
		return m.startTrain()
	case key.Matches(msg, keys.ExpandErr):
		if v.err != nil {
			m.err, m.overlay = v.err, overlayError
		}
	}
	return m, nil
}

// setDryRun plans the set with the rows and shows every run, masked.
func (m Model) setDryRun() (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	p, err := m.svc.PlanSet(m.baseCtx(), m.setView.info.Name, app.SetOptions{Vars: m.setVars.flags(), Server: m.server})
	if err != nil {
		m.setVars.err = err
		return m, nil
	}
	m.setVars.err = nil
	m.setDry = setDryRunText(p)
	m.overlay = overlayDryRun
	return m, nil
}

// setDryRunText is the dry run as lines: every run of every stage with its
// plan, branch and changed variables, secrets masked.
func setDryRunText(p app.SetPlan) []string {
	var out []string
	for i, st := range p.Stages {
		out = append(out, fmt.Sprintf("stage %d/%d", i+1, len(p.Stages)))
		for _, r := range st {
			branch := r.Ref.Branch
			if branch == "" {
				branch = "default"
			}
			out = append(out, fmt.Sprintf("  %s  %s  branch %s", app.PlanRunName(r), r.Ref.PlanKey, branch))
			changed := r.Vars.Changed()
			for _, n := range sortedKeys(changed) {
				v, _ := r.Vars.Get(n)
				out = append(out, fmt.Sprintf("      %s = %s", n, v.Display()))
			}
		}
	}
	return out
}

// startTrain starts the action with the rows as --var flags. An error before
// the first event stays in the prompt; nothing triggered.
func (m Model) startTrain() (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	if why := m.refuseSetAction(); why != "" {
		m.status = why
		m.screen = screenSet
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	o := app.SetOptions{Vars: m.setVars.flags(), Server: m.server}
	var (
		ch  <-chan app.SetEvent
		err error
	)
	switch m.setVars.action {
	case setActionRun:
		var p app.SetPlan
		if p, err = m.svc.PlanSet(ctx, m.setView.info.Name, o); err == nil {
			_, ch, err = m.svc.RunSet(ctx, p, app.SetLockOptions{})
		}
	case setActionResume:
		ch, err = m.svc.ResumeSet(ctx, m.setView.rec.ID, o, app.SetLockOptions{})
	case setActionRerun:
		ch, err = m.svc.RerunSet(ctx, m.setView.rec.ID, o, app.SetLockOptions{})
	}
	if err != nil {
		cancel()
		m.setVars.err = err
		if m.screen != screenSetVars {
			m.err = err
		}
		return m, nil
	}
	board := app.NewSetBoard(planOf(m.setView.info))
	if m.setVars.action != setActionRun && m.setView.rec != nil {
		board.Seed(*m.setView.rec)
	}
	m.train = trainState{set: m.setView.info.Name, cancel: cancel, ch: ch, board: board, gen: m.train.gen + 1}
	m.setView.board = board
	m.setVars = setVarsState{}
	m.screen = screenSet
	m.status = strings.ToLower(m.train.set) + " started"
	return m, setEventCmd(ch, m.train.gen)
}

// handleSetEvent moves the train's board on and reads the next event. The
// end of the set goes to the status bar.
func (m Model) handleSetEvent(e app.SetEvent) (tea.Model, tea.Cmd) {
	m.train.board.Apply(e, m.now())
	switch e.Type {
	case app.SetDone:
		if e.State == provider.StateSuccess {
			m.status = m.train.set + " succeeded"
		} else {
			m.status = fmt.Sprintf("%s failed: stage %d", m.train.set, e.Stage+1)
		}
	case app.SetError:
		m.err = e.Err
	}
	return m, setEventCmd(m.train.ch, m.train.gen)
}

func (m Model) setVarsView() string {
	v := m.setVars
	title := " " + v.action.verb() + " " + m.setView.info.Name
	if v.action != setActionRun && m.setView.rec != nil {
		title += " " + m.setView.rec.ID
	}
	lines := []string{title}
	for i, r := range v.rows {
		cur := "  "
		if i == v.cursor {
			cur = "▸ "
		}
		name, val := r.Name, r.Value
		if v.editing && i == v.cursor {
			if v.editName {
				name = v.input.View()
			} else {
				val = v.input.View()
			}
		}
		if name == "" {
			name = dimStyle.Render("name")
		}
		note := ""
		if r.Required {
			note = dimStyle.Render("required: the run was started with it")
		}
		lines = append(lines, fmt.Sprintf(" %s%-16s %-20s %s", cur, name, val, note))
	}
	if len(v.rows) == 0 {
		lines = append(lines, dimStyle.Render("   no variables; a adds one"))
	}
	if v.err != nil {
		lines = append(lines, "", errorStyle.Render(" "+errorWhat(v.err)))
	}
	body := m.height - 1
	for len(lines) < body {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(append(lines[:body], m.statusBar(m.width)), "\n")
}
```

  `Model` also gains `setDry []string`; the existing `overlayDryRun` body (`dryRunBody`) shows `m.setDry` when `m.screen == screenSetVars` (one `if` at its top returning `strings.Join(m.setDry, "\n")`). `sortedKeys` — use the package's existing sorted-key helper, or add one (`func sortedKeys[V any](m map[string]V) []string`) in `setvars.go` if none exists.

`panels.go` `keyLine`:

```go
	if m.screen == screenSetVars {
		return "enter edit  a add  x remove  d dry-run  ^R " + strings.ToLower(m.setVars.action.verb()) + "  esc cancel"
	}
```

`setscreen.go`: `setScreenView` already reads `m.train`; `refuseSetAction` and `setActions` already exist.

- [ ] **Step 4: Run** `go test -race ./internal/view/tui/` — PASS; goldens unchanged except reviewed help rows.

- [ ] **Step 5: Commit** `git add internal/view/tui && git commit -m "feat: run, resume and rerun sets from the terminal UI"`

---

### Task 6: Lifecycle, sets run elsewhere, docs and the gate

**Files:**
- Modify: `internal/view/tui/model.go` (`quit`, `switchServer` ask while a train runs; elsewhere poll tick), `internal/view/tui/setscreen.go` (poll), `README.md`, `docs/superpowers/specs/2026-09-29-bam-run-sets-core-design.md` §12, spec status
- Test: `internal/view/tui/setvars_test.go`, `internal/view/tui/setscreen_test.go`

- [ ] **Step 1: Failing tests** — append to `setvars_test.go`:

```go
func TestQuitWhileATrainRunsAsks(t *testing.T) {
	m := trainModel(t)
	block := make(chan app.SetEvent) // a train that never ends on its own
	stopped := false
	m.train = trainState{set: "release", ch: block, board: app.NewSetBoard(app.SetPlan{Name: "release"}),
		cancel: func() { stopped = true }, gen: 1}
	m, cmd := send(m, mkKey("q"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Contains(t, m.confirm.Prompt, "release is running in this window")
	require.Nil(t, cmd, "nothing quits before the answer")
	m, cmd = send(m, mkKey("y"))
	require.True(t, stopped, "yes ends the train's watching")
	require.NotNil(t, cmd)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped, "no build is stopped")
}

func TestSwitchServerWhileATrainRunsAsks(t *testing.T) {
	m := trainModel(t)
	m.train = trainState{set: "release", ch: make(chan app.SetEvent), board: app.NewSetBoard(app.SetPlan{}), cancel: func() {}, gen: 1}
	m, _ = send(m, mkKey("esc"))
	next, _ := m.switchServer("other")
	require.Equal(t, overlayConfirm, next.(Model).overlay)
}
```

Append to `setscreen_test.go`:

```go
func TestSetScreenPollsWhileHeldElsewhere(t *testing.T) {
	m := setScreenModel(t, &app.SetRecord{ID: "release-1", Set: "release", Status: app.SetRunning}, "pid 77 on desk, since 09:15")
	cmd := m.setPollCmd()
	require.NotNil(t, cmd, "a set held elsewhere is re-read")
	m.setView.elsewhere = ""
	require.Nil(t, m.setPollCmd(), "no polling when nothing is held elsewhere")
}
```

- [ ] **Step 2: Run** `go test ./internal/view/tui/ -run 'QuitWhile|SwitchServerWhile|Polls' 2>&1 | tail -3` — expected: failures/compile error.

- [ ] **Step 3: Implement**

`model.go` `quit`:

```go
func (m Model) quit() (tea.Model, tea.Cmd) {
	if m.train.running() {
		set := m.train.set
		return m.ask(set+" is running in this window. Quit and stop watching it? Its builds keep running; continue with U or bam set resume.",
			func(m Model) (tea.Model, tea.Cmd) {
				m.train.end()
				m.stopWatch()
				m.stopFollow()
				return m, tea.Quit
			})
	}
	m.stopWatch()
	m.stopFollow()
	return m, tea.Quit
}
```

`switchServer` begins with:

```go
	if m.train.running() {
		set := m.train.set
		return m.ask(set+" is running in this window. Switch server and stop watching it? Its builds keep running.",
			func(m Model) (tea.Model, tea.Cmd) {
				m.train.end()
				return m.switchServer(alias)
			})
	}
```

`setscreen.go`:

```go
// setPollEvery is how often the set screen re-reads a set held by another bam.
const setPollEvery = 5 * time.Second

type setPollMsg struct{ Gen int }

// setPollCmd re-reads the set in setPollEvery while another bam holds it,
// or nil when nothing needs polling.
func (m Model) setPollCmd() tea.Cmd {
	if m.setView.elsewhere == "" || m.screen != screenSet {
		return nil
	}
	gen := m.setGen
	return tea.Tick(setPollEvery, func(time.Time) tea.Msg { return setPollMsg{Gen: gen} })
}
```

`handleSetLoaded` returns `m, m.setPollCmd()`; `Update` gains:

```go
	case setPollMsg:
		if msg.Gen != m.setGen || m.screen != screenSet {
			return m, nil
		}
		m.setGen++
		return m, loadSetCmd(m.svc, m.setView.info.Name, m.setGen)
```

(Add `"time"` to `setscreen.go`'s imports.)

`README.md`: in the terminal UI section, one paragraph: `` `:sets` lists the run sets with their last run. `enter` opens a set: its stages and runs, live while it runs. `R` runs it (a prompt takes optional variables, `d` previews the dry run, `ctrl-R` starts it), `U` resumes an interrupted run, `F` reruns the failed runs. The train keeps running while you browse; `q` asks first. A set run by another bam is shown and re-read every few seconds. `` In the key table add rows for `U`, `F`, `x`, and `:sets` in the command row.

Run sets design §12: replace `- The terminal UI run-sets panel (it will consume §4.3 events).` with `` - The terminal UI run-sets panel: done, see [`2026-10-06-bam-tui-sets-design.md`](2026-10-06-bam-tui-sets-design.md). `` Spec status → `Implemented`.

- [ ] **Step 4: Gate** — `go test -race ./... && make lint && make check-fixtures && make docs` — clean.

- [ ] **Step 5: Commit** `git add -A internal README.md docs && git commit -m "feat: ask before quitting a running train and follow sets run elsewhere"`
