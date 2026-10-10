# Plan Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the terminal UI's three-panel screen with a plan screen that shows the pipeline's full name on top and, in the frame, its builds, the stages of the selected build, and the builds that build triggered.

**Architecture:** Everything stays in `internal/view/tui`. The old `screenColumns` becomes `screenPlan`; the open build (`m.detail`) becomes the Stages panel; a new `planScreen` struct holds the plan the screen is about, a drill stack, a per-build cache, and a one-level tree preview loaded with `app.BuildTree(…, Depth: 1)`. Cursor moves in Builds load the new build after a 250 ms rest (`planRestMsg`, a `tea.Tick` with a generation), mirroring `homeRefreshEvery`.

**Tech Stack:** Go, bubbletea, lipgloss, bubbles/key, testify. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-10-bam-tui-plan-screen-design.md` (issue #54). Read it before starting; section numbers below (§3.2 etc.) refer to it.

## Global Constraints

- Public repository: placeholders only (`bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`). No real hostnames, project keys, plan keys or user names anywhere, including test data and commit messages.
- No new dependencies (spec §12 list only). `slices` and other standard library packages are fine.
- `app`, `provider` and `cli` do not change. `TestAppStaysUIReady` must still pass.
- Commits: Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`), the repository owner's git identity only (`Roj Carranza <rojjamescarranza@gmail.com>`). **No `Co-Authored-By`, no `Claude-Session`, no "Generated with Claude" line.** Check `git config user.name` / `user.email` before the first commit.
- In a non-interactive shell, prefix Go commands with `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.
- Goldens are rewritten only with `go test ./internal/view/tui/ -update`, and every rewritten golden is read before committing.
- `go test -race ./...` and `make lint` pass at the end of every task.
- Every asynchronous result carries a generation and is dropped when stale (the `stale_test.go` pattern). No test sleeps or touches the network: delays are package variables tests set to 0, as `homeRefreshEvery` is.

## Review Focus

1. **A plan with no builds.** Builds is empty: Stages and Tree say `no builds yet`, nothing panics, and `R` still opens the run form for the plan (fallback to `m.buildsPlan`). Test: Task 2, `TestPlanWithNoBuildsStillRuns`.
2. **Resizing across the small-terminal threshold.** At 59×19, 40×10 and back to 80×24 the plan screen never draws a line wider or a screen taller than the terminal. Test: Task 5, `TestPlanViewNeverExceedsTheTerminal`.
3. **A builds refresh while a build is open.** A refresh (watch end, `r`) that adds a newer build must keep the cursor on the open build, not on whatever row now has its index. Test: Task 3, `TestBuildsRefreshKeepsTheCursorOnTheOpenBuild`.
4. **The tree preview fails.** A Bamboo error from `BuildTree` shows in the Tree panel only; the status bar and the Stages panel are untouched. Test: Task 4, `TestTreePreviewErrorStaysInItsPanel`.
5. **A very long plan name.** A 120-character name is cut with `…` on line 1 at 80 columns and no line exceeds 80 cells. Test: Task 5, `TestLongPlanNameIsCutWithAnEllipsis`.

---

## File map

| File | Change |
| --- | --- |
| `internal/view/tui/model.go` | focus enum, tab order, `back()`, key dispatch on the plan screen, `drill()`, `refresh()`, `selectionURL()`, `openForm()`, `askCancel()`, `switchServer()`, `setProject()`, `openTriggered()`, `View()` |
| `internal/view/tui/planscreen.go` | **new**: `planScreen` state, `openPlan`, `closePlan`, `showBuild`, rest loading, tree preview, drill stack, header and panel rendering |
| `internal/view/tui/planscreen_test.go` | **new**: model and rendering tests for the plan screen |
| `internal/view/tui/msgs.go` | `planRestMsg`, `treePreviewMsg`, `treePreviewCmd` |
| `internal/view/tui/home.go` | `drillFromHome`, Home's `1 2 3`, `goHome` closes the plan screen |
| `internal/view/tui/setscreen.go`, `treescreen.go` | open the plan screen instead of the panels |
| `internal/view/tui/panels.go` | old panel layout removed; `keyLine` per focus |
| `internal/view/tui/detail.go` | Stages body without the build header line |
| `internal/view/tui/form.go`, `logs.go` | name line on top |
| `internal/view/tui/keys.go` | help rows |
| `internal/view/tui/testdata/golden/*` | `columns-*`, `detail-*` replaced by `plan-*`; `form`, `logs` regenerated |
| `README.md`, `docs/prd.md`, `CHANGELOG.md` | describe the plan screen |

---

### Task 1: Plan screen focus, tab order and Esc order

**Files:**
- Modify: `internal/view/tui/model.go` (lines 30–80, `dispatchKey`, `back`)
- Modify: `internal/view/tui/home.go` (`handleHomeKey` panel keys)
- Modify: every `*.go` in `internal/view/tui` (rename)
- Test: `internal/view/tui/model_test.go`, `home_test.go`, `setscreen_test.go`, `treescreen_test.go`

**Interfaces:**
- Produces: `screenPlan` (renamed from `screenColumns`), `focusStages` (renamed from `focusMain`), new `focusTree`; `tabOrder = []focus{focusBuilds, focusStages, focusTree}`; `func (f focus) next() focus`, `prev()` (`parent()` is deleted: `back()` no longer uses it, and `make lint` flags it as unused); `func (m Model) planView() string` (renamed from `columnsView`, body unchanged for now).

- [ ] **Step 1: Mechanical renames**

```bash
cd internal/view/tui
sed -i '' -e 's/screenColumns/screenPlan/g' -e 's/focusMain/focusStages/g' -e 's/columnsView/planView/g' *.go
cd ../../..
```
(On Linux use `sed -i` without `''`.) Run `go build ./... && go test ./internal/view/tui/` and expect it to pass: this step changes names only.

- [ ] **Step 2: Write the failing tests**

Replace `TestTabCyclesPanels`, `TestNumberKeysFocusPanelsDirectly` and `TestBackResolvesInSpecOrder` in `model_test.go`, and delete `TestPresetsBacksOutToPlans`:

```go
// TestTabCyclesPanels pins the plan screen's tab order (plan screen spec §4.2).
func TestTabCyclesPanels(t *testing.T) {
	m := testModel()
	require.Equal(t, focusBuilds, m.focus)
	for _, want := range []focus{focusStages, focusTree, focusBuilds} {
		m, _ = send(m, mkKey("tab"))
		require.Equal(t, want, m.focus)
	}
	for _, want := range []focus{focusTree, focusStages, focusBuilds} {
		m, _ = send(m, mkKey("shift+tab"))
		require.Equal(t, want, m.focus)
	}
}

// TestNumberKeysFocusPanelsDirectly pins 1, 2 and 3 on the plan screen.
func TestNumberKeysFocusPanelsDirectly(t *testing.T) {
	m := testModel()
	for _, tc := range []struct {
		key  string
		want focus
	}{{"2", focusStages}, {"3", focusTree}, {"1", focusBuilds}} {
		m, _ = send(m, mkKey(tc.key))
		require.Equal(t, tc.want, m.focus, "key %q", tc.key)
	}
}

// TestBackResolvesInSpecOrder pins plan screen spec §4.1: overlay, log
// screen, Stages or Tree to Builds, then Home.
func TestBackResolvesInSpecOrder(t *testing.T) {
	m := testModel()
	m.overlay, m.screen, m.focus = overlayHelp, screenLogs, focusStages

	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, screenLogs, m.screen, "the overlay closes before the log screen does")
	require.Nil(t, cmd)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusStages, m.focus, "the log screen closes before focus moves")

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus, "Stages backs out to Builds")

	m.focus = focusTree
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus, "Tree backs out to Builds")

	m.builds.setQuery("fail")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen, "esc clears the Builds filter before leaving")
	require.Empty(t, m.builds.query)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen, "esc on Builds returns to Home")
}
```

Change `testModel()` in `model_test.go` and `goldenModel()` in `golden_test.go` so they start on Builds:

```go
	m.screen = screenPlan // the v0.2 tests start on the plan screen; Home has its own tests
	m.focus = focusBuilds
```

In `home_test.go`, replace `TestNumberKeysOnHomeOpenThePanelsWithThatFocus`'s expectations with `{"1", focusBuilds}, {"2", focusStages}, {"3", focusTree}` and delete `TestEscOnPresetsPanelGoesThroughPlansFirst` (the Presets panel no longer exists on this screen).

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestTabCyclesPanels|TestNumberKeysFocusPanelsDirectly|TestBackResolvesInSpecOrder|TestNumberKeysOnHome' -v`
Expected: FAIL (`focusTree` undefined).

- [ ] **Step 4: Implement**

In `model.go` replace the focus block, `tabOrder`, `next`, `prev` with the code below, and delete `parent()`:

```go
// focus is which panel or table takes keys. Plans, Presets, Sets and
// Projects are Home's tables; Builds, Stages and Tree are the plan screen's
// panels.
type focus int

const (
	focusPlans focus = iota
	focusBuilds
	focusPresets
	focusStages // the plan screen's stage and job tree of the open build
	focusSets   // Home's sets table; not in the plan screen's tab order
	focusProjects
	focusTree // the plan screen's list of what the open build triggered
)
```

```go
var tabOrder = []focus{focusBuilds, focusStages, focusTree}

// next and prev look the focus up rather than indexing by its value: Home's
// tables are not in the tab order, and from one of them tab lands on Builds.
func (f focus) next() focus {
	return tabOrder[(slices.Index(tabOrder, f)+1)%len(tabOrder)]
}

func (f focus) prev() focus {
	i := max(slices.Index(tabOrder, f), 0)
	return tabOrder[(i+len(tabOrder)-1)%len(tabOrder)]
}
```

Add `"slices"` to `model.go`'s imports. In `dispatchKey` change the panel keys:

```go
	case key.Matches(msg, keys.Panel1):
		m.focus = focusBuilds
	case key.Matches(msg, keys.Panel2):
		m.focus = focusStages
	case key.Matches(msg, keys.Panel3):
		m.focus = focusTree
```

In `back()`, replace the two `screenPlan && fromSet` / `screenPlan && fromTree` cases and the final `case m.focus != focusPlans:` with one case placed where the `fromSet` case was:

```go
	case m.screen == screenPlan && m.focus == focusBuilds && m.builds.query != "":
		m.builds.setQuery("") // esc clears the focused panel's filter first (§4.1)
		return m, nil
	case m.screen == screenPlan && m.focus != focusBuilds:
		m.focus = focusBuilds
		return m, nil
	case m.screen == screenPlan && m.fromSet:
		m.fromSet = false
		m.stopWatch()
		m.screen = screenSet
		return m, nil
	case m.screen == screenPlan && m.fromTree:
		m.fromTree = false
		m.stopWatch()
		m.screen = screenTree
		return m.watchTree() // watching paused while the build was open
```

(the `screenTree` and `screenSet` cases stay; the function still ends with `return m.goHome()`).

In `home.go` `handleHomeKey`, map Home's number keys:

```go
	case key.Matches(msg, keys.Panel1):
		next, cmd := m.drillFromHome(focusBuilds)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Panel2):
		next, cmd := m.drillFromHome(focusStages)
		return next.(Model), cmd, true
	case key.Matches(msg, keys.Panel3):
		next, cmd := m.drillFromHome(focusTree)
		return next.(Model), cmd, true
```

- [ ] **Step 5: Run the package and fix the coupled tests**

Run: `go test ./internal/view/tui/`
Expected failures and what to do with each:

| Test | Action |
| --- | --- |
| `TestMovementAppliesToTheFocusedPanel` | rewrite: focus Builds, `j` moves `builds.cursor` only; focus Stages, `j` moves `treeCursor` only (load `sampleBuild()` into `m.detail` with `defaultExpanded`) |
| `TestGAndShiftGJumpToTheEnds` | rewrite on `m.builds` with three builds and `m.focus = focusBuilds` |
| `TestSetScreenBuildReturnsToTheSet` | after Enter, focus is Stages: press esc twice (Stages → Builds → set) |
| `TestTreeScreenEnterAndBack` | same: two escs back to the tree screen |
| `TestTreeScreenRerunSurvivesABuildVisit`, `TestTreeScreenLateRerunResultIsDropped` | same: two escs where they had one |
| `TestEnterOnAPresetSelectsItsPlan`, `TestEnterOnAPresetWhosePlanIsNotListedStillLoadsIt`, `TestRefreshOnPresetsRereadsTheProjectFile`, `TestOpeningAPresetStopsTheWatchToo` | leave failing only if they fail; they are rewritten in Task 2 against Home's presets view. If they still pass, leave them |

Any other failure that drives the Plans or Presets panel on the plan screen: delete the test if the behaviour no longer exists, otherwise set `m.focus = focusBuilds` and keep its assertion. List deleted tests in the commit message body.

Run: `go test -race ./internal/view/tui/ && make lint`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui
git commit -m "refactor(tui): the plan screen's focus is Builds, Stages and Tree" -m "Renames screenColumns to screenPlan and focusMain to focusStages, adds focusTree, and looks the tab order up instead of indexing it. Deleted tests: <list>."
```

---

### Task 2: Open and close the plan screen from every entry point

**Files:**
- Create: `internal/view/tui/planscreen.go`
- Create: `internal/view/tui/planscreen_test.go`
- Modify: `internal/view/tui/model.go` (`buildsLoadedMsg` case, `drill`, `openForm`, `openTriggered`, `switchServer`, `setProject`, Branch key)
- Modify: `internal/view/tui/home.go` (`drillFromHome`, `goHome`)
- Modify: `internal/view/tui/setscreen.go` (`openSetBuild`), `treescreen.go` (`openTreeBuild`, `closeTree`)

**Interfaces:**
- Consumes: `screenPlan`, `focusBuilds/Stages/Tree` (Task 1).
- Produces:
  - `type planScreen struct { key string; want *provider.Build; older string; restGen int; stack []planFrame; cache map[string]provider.Build; trees map[string]app.Tree; tree treePreview; treeCursor int; stagesLoading bool }`
  - `type planFrame struct { key, buildsPlan string; build provider.Build; focus focus }`
  - `type treePreview struct { key string; tree app.Tree; have, loading bool; err error }`
  - `Model.plan planScreen`, `Model.treePreviewGen int`
  - `func (m Model) openPlan(key, buildsPlan string, want *provider.Build, f focus) (Model, tea.Cmd)`
  - `func (m *Model) closePlan()`
  - `func (m Model) showBuild(b provider.Build) (Model, tea.Cmd)` — in this task it only sets `m.detail` and watches or reloads; Task 3 adds the cache and Task 4 the tree preview.
  - `func (m Model) planBuildsLoaded() (Model, tea.Cmd)`
  - `func (m Model) masterOf(planKey string) string`

- [ ] **Step 1: Write the failing tests** in a new `planscreen_test.go`:

```go
package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// planHome is Home's plans table on the lab server, connected.
func planHome(t *testing.T) Model {
	t.Helper()
	m := goldenModel(80, 24)
	m.svc = testService()
	m.screen, m.focus = screenHome, focusPlans
	m.home.view = homePlans
	m.builds.setItems(nil)
	m.buildsPlan = ""
	return m
}

func TestEnterOnHomeOpensThePlanScreen(t *testing.T) {
	m := planHome(t)
	require.True(t, m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "PROJ-BUILD" }))

	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	require.Equal(t, "PROJ-BUILD", m.plan.key)
	require.Equal(t, "PROJ-BUILD", m.buildsPlan)
	require.NotNil(t, cmd)

	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	require.NotNil(t, m.detail, "the newest build opens as soon as the builds load")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, "PROJ-BUILD", selectedKey(m), "Home's cursor stays on the plan")
	require.Empty(t, m.plan.key, "leaving closes the plan screen")
	require.Nil(t, m.detail)
}

func TestEnterOnAPresetOpensItsPlanAndBranch(t *testing.T) {
	m := presetModel()
	m.svc = testService()
	require.True(t, m.presets.selectFirst(func(ti app.TargetInfo) bool { return ti.Name == "smoke" }))

	// drillFromHome directly: through handleKey the command may be wrapped
	// in a batch with Home's idle tick.
	next, cmd := m.drillFromHome(focusBuilds)
	m = next.(Model)
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	msg := cmd()
	loaded, ok := msg.(buildsLoadedMsg)
	require.True(t, ok, "got %T", msg)
	require.Equal(t, "PROJ-PROV12", loaded.PlanKey, "a preset's branch resolves to its branch plan")

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homePresets, m.home.view)
}

func TestPlanWithNoBuildsStillRuns(t *testing.T) {
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: m.buildsPlan})
	require.Nil(t, m.detail)
	require.NotPanics(t, func() { _ = m.View() })

	m, cmd := send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen, "R opens the form for the plan itself")
	require.NotNil(t, cmd)
}

func TestSetScreenRunOpensThePlanScreenOnThatBuild(t *testing.T) {
	m := planHome(t)
	b := sampleBuild()
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &b, focusStages)
	require.Equal(t, focusStages, m.focus)
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key, "the wanted build shows before the list loads")

	older := b
	older.Key, older.Number = "PROJ-BUILD-3", 3
	m, _ = m.openPlan("PROJ-BUILD", "PROJ-BUILD", &older, focusStages)
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: []provider.Build{sampleBuild()}})
	rows := m.builds.rows()
	require.Equal(t, "PROJ-BUILD-3", rows[0].Key, "a build not among the newest is pinned on top")
	require.Equal(t, "PROJ-BUILD-3", m.plan.older)
	require.Equal(t, 0, m.builds.cursor)
}

func TestServerSwitchLeavesThePlanScreen(t *testing.T) {
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.Equal(t, screenHome, m.screen)
	require.Empty(t, m.plan.key)
}
```

Add `"github.com/r0jjames/bam-cli/internal/app"` to the imports for `app.TargetInfo`. `time` is used by later tasks; remove it now if `go vet` complains.

In `model_test.go`, delete `TestEnterOnAPresetSelectsItsPlan`, `TestEnterOnAPresetWhosePlanIsNotListedStillLoadsIt`, `TestRefreshOnPresetsRereadsTheProjectFile` and `TestPresetRowShowsNamePlanAndBranch` if still present (covered by `TestEnterOnAPresetOpensItsPlanAndBranch` and Home's preset tests). In `home_test.go`, rename `TestEnterOnHomeOpensThePanelsOnThePlan` and `TestEnterOnAPresetOpensThePanels` only if they now duplicate the new tests; otherwise update them to expect `screenPlan` and `focusBuilds`. In `stale_test.go`, port `TestOpeningAPresetStopsTheWatchToo` to open the preset from Home's presets view (`presetModel()`, Enter) while a watch runs, and assert `m.watchCancel == nil` afterwards.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestEnterOnHomeOpensThePlanScreen|TestEnterOnAPresetOpensItsPlanAndBranch|TestPlanWithNoBuildsStillRuns|TestSetScreenRunOpensThePlanScreenOnThatBuild|TestServerSwitchLeavesThePlanScreen' -v`
Expected: FAIL (`m.plan` undefined).

- [ ] **Step 3: Create `planscreen.go`**

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// planScreen is the screen about one plan: its builds, the stages of the
// selected build, and what that build triggered (plan screen spec §3).
type planScreen struct {
	key   string          // the plan the screen is about; the header names it
	want  *provider.Build // the build to put the cursor on when the builds load
	older string          // a build pinned on top because it is not among the newest

	restGen int         // bumped by every Builds cursor move
	stack   []planFrame // plan screens Enter on a Tree row came from

	cache map[string]provider.Build // finished builds with their stages, by key
	trees map[string]app.Tree       // finished builds' trees, by key

	tree       treePreview
	treeCursor int

	stagesLoading bool // the open build's stages are being read
}

// planFrame is one plan screen on the drill stack: enough to draw it again
// with the cursor where it was.
type planFrame struct {
	key, buildsPlan string
	build           provider.Build
	focus           focus
}

// treePreview is the Tree panel: the direct children of one build.
type treePreview struct {
	key     string
	tree    app.Tree
	have    bool
	loading bool
	err     error
}

// openPlan shows the plan screen on key, listing buildsPlan's builds (the
// master or a branch plan). want, when set, is shown at once and the cursor
// lands on it once the list arrives. The drill stack and cache survive, so
// Enter on a Tree row and esc back keep them.
func (m Model) openPlan(key, buildsPlan string, want *provider.Build, f focus) (Model, tea.Cmd) {
	m.screen = screenPlan
	m.home.tickGen++ // Home's auto-refresh stops while it is not visible
	m.leaveBuild()
	m.plan.key, m.plan.want, m.plan.older = key, want, ""
	m.plan.restGen++
	m.plan.tree, m.plan.treeCursor = treePreview{}, 0
	m.treePreviewGen++
	m.focus = f
	m.builds.setQuery("")
	cmds := []tea.Cmd{m.loadBuilds(buildsPlan, true)}
	if want != nil {
		var show tea.Cmd
		m, show = m.showBuild(*want)
		cmds = append(cmds, show)
	}
	return m, tea.Batch(cmds...)
}

// closePlan forgets the plan screen: its build, its watch, its stack and
// its cache. Every way off the plan screen to Home, and a server switch,
// calls it.
func (m *Model) closePlan() {
	m.leaveBuild()
	m.plan = planScreen{}
	m.treePreviewGen++
}

// showBuild makes b the open build: Stages shows it, a running build is
// watched, a finished one is read once for its stages.
func (m Model) showBuild(b provider.Build) (Model, tea.Cmd) {
	m.stopWatch()
	m.detailGen++
	m.cancelGen++
	m.progress = provider.Progress{}
	m.treeCursor = 0
	m.dropOlder(b.Key)
	bb := b
	m.detail = &bb
	m.expanded = defaultExpanded(b)
	m.plan.stagesLoading = true
	if b.State.Finished() {
		return m, reloadBuildCmd(m.baseCtx(), m.svc, b.Key, m.detailGen)
	}
	next, cmd := m.startWatch(b.Key)
	return next.(Model), cmd
}

// dropOlder removes the pinned older build once the cursor opens another.
func (m *Model) dropOlder(key string) {
	if m.plan.older == "" || m.plan.older == key {
		return
	}
	older := m.plan.older
	m.plan.older = ""
	items := make([]provider.Build, 0, len(m.builds.items))
	for _, b := range m.builds.items {
		if b.Key != older {
			items = append(items, b)
		}
	}
	m.builds.setItems(items)
	m.builds.selectFirst(func(b provider.Build) bool { return b.Key == key })
}

// planBuildsLoaded places the cursor once the Builds list arrives: on the
// wanted build (pinned on top when it is older than the list), on the open
// build after a refresh, or on the newest build, which then opens.
func (m Model) planBuildsLoaded() (Model, tea.Cmd) {
	if w := m.plan.want; w != nil {
		m.plan.want = nil
		if !m.builds.selectFirst(func(b provider.Build) bool { return b.Key == w.Key }) && w.Number != 0 {
			m.builds.setItems(append([]provider.Build{*w}, m.builds.items...))
			m.builds.top()
			m.plan.older = w.Key
		}
		return m, nil
	}
	if m.detail != nil {
		m.builds.selectFirst(func(b provider.Build) bool { return b.Key == m.detail.Key })
		return m, nil
	}
	b, ok := m.builds.selected()
	if !ok || m.svc == nil {
		return m, nil
	}
	return m.showBuild(b)
}

// masterOf is the plan a branch plan belongs to, when the plans list has
// it; otherwise planKey itself.
func (m Model) masterOf(planKey string) string {
	best, n := planKey, 0
	for _, p := range m.plans.items {
		if p.Key == planKey {
			return planKey
		}
		if len(p.Key) > n && isBranchOf(planKey, p.Key) {
			best, n = p.Key, len(p.Key)
		}
	}
	return best
}
```

Add `plan planScreen` and `treePreviewGen int` to `Model` (next to `buildTree` and `treeGen`).

- [ ] **Step 4: Wire the entry and exit points**

`model.go`, `buildsLoadedMsg` case — after `m.builds.setItems(msg.Builds)`:

```go
		if m.screen == screenPlan {
			return m.planBuildsLoaded()
		}
		return m, nil
```

`model.go`, `drill()` — delete the `focusPlans` and `focusPresets` cases, and replace the `focusBuilds` case:

```go
	case focusBuilds:
		b, ok := m.builds.selected()
		if !ok || m.svc == nil {
			return m, nil
		}
		m.focus = focusStages
		if m.detail != nil && m.detail.Key == b.Key {
			return m, nil
		}
		return m.showBuild(b)
```

`home.go`, `drillFromHome`:

```go
// drillFromHome opens the plan screen on the row's plan, or on a preset's
// plan and branch, with focus on want.
func (m Model) drillFromHome(want focus) (tea.Model, tea.Cmd) {
	if m.svc == nil {
		return m, nil
	}
	if m.home.view == homePresets {
		t, ok := m.presets.selected()
		if !ok || t.Plan == "" {
			return m, nil
		}
		m, _ = m.openPlan(t.Plan, t.Plan, nil, want)
		// A preset may name a branch, and its builds live under the branch
		// plan. resolveTargetBuildsCmd resolves it exactly as bam run does,
		// under the builds generation openPlan just bumped.
		return m, resolveTargetBuildsCmd(m.baseCtx(), m.svc, t, m.buildsGen)
	}
	p, ok := m.plans.selected()
	if !ok {
		return m, nil
	}
	return m.openPlan(p.Key, p.Key, nil, want)
}
```

(`openPlan`'s own `loadBuilds` command is discarded in the preset branch on purpose: `m.buildsGen` is the same, and only the resolved load is sent.)

`home.go`, `goHome` — first line:

```go
	if m.screen == screenPlan {
		m.closePlan()
	}
```

`setscreen.go`, `openSetBuild`:

```go
	b := rows[m.setView.cursor].Build
	m.fromSet = true
	return m.openPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
```

`treescreen.go`, `openTreeBuild` — replace from `m.fromTree = true` to the end:

```go
	m.fromTree = true
	next, cmd := m.openPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
	if !logs {
		return next, cmd
	}
	next.logsFromTree = true
	after, logsCmd := next.openLogs("", all)
	return after, tea.Batch(cmd, logsCmd)
```

`treescreen.go`, `closeTree`: the existing `if m.screen == screenTree { m.screen = screenPlan }` (renamed in Task 1) stays.

`model.go`, `openTriggered` — replace the body above `next, watch := m.startWatch(b.Key)` and the rest with:

```go
	m.revisionRun.Key, m.revisionRun.Rev = b.Key, m.form.planRef().RevisionShort()
	m.form = formState{}
	m, cmd := m.openPlan(m.masterOf(b.PlanKey), b.PlanKey, &b, focusStages)
	// showBuild started the watch; note the build only after, since
	// stopping the previous watch clears the marker.
	m.noteLive(b)
	m.status = "queued " + b.Key
	return m, cmd
```

`model.go`, `openForm` default case — a plan with no builds still runs:

```go
	default:
		if b, ok := m.currentBuild(); ok {
			planKey = b.PlanKey
		} else {
			planKey = m.buildsPlan
		}
```

`model.go`, Branch key — the plan screen's plan, not Home's cursor:

```go
	case key.Matches(msg, keys.Branch):
		if m.screen != screenPlan || m.plan.key == "" || m.svc == nil {
			return m, nil
		}
		m.overlay = overlayBranches
		m.pickerGen++
		m.picker.setQuery("")
		m.picker.setItems(nil)
		return m, loadBranchesCmd(m.baseCtx(), m.svc, m.plan.key, m.pickerGen)
```

`model.go`, `switchServer` — after the `if m.screen == screenTree { … }` block:

```go
	if m.screen == screenPlan {
		// The plan and its builds are the old server's.
		m.closePlan()
		m.screen = screenHome
		m.focus = m.homeFocus()
	}
```

`model.go`, `setProject` — replace `if m.screen != screenHome { m.focus = focusPlans }` with:

```go
	if m.screen == screenPlan {
		m.closePlan()
		m.screen = screenHome
		m.focus = m.homeFocus()
	}
```

`model.go`, `askCancel` — `C` acts on the open build from any plan screen panel:

```go
	if m.screen != screenPlan {
		return m, nil
	}
```

(replacing `if m.focus != focusBuilds && m.focus != focusStages {`). Update `TestCIsIgnoredOutsideBuildsAndMain` in `form_test.go` to assert `C` is ignored on Home and accepted from Stages and Tree; rename it `TestCIsIgnoredOffThePlanScreen`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/view/tui/ -run 'TestEnterOnHomeOpensThePlanScreen|TestEnterOnAPresetOpensItsPlanAndBranch|TestPlanWithNoBuildsStillRuns|TestSetScreenRunOpensThePlanScreenOnThatBuild|TestServerSwitchLeavesThePlanScreen' -v`
Expected: PASS

Run: `go test -race ./internal/view/tui/ && make lint`
Expected: PASS. Fix coupled tests with the rule from Task 1 Step 5; `TestASuccessfulRunOpensAndWatchesTheNewBuild` now expects `focusStages`.

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): open a plan screen from Home, presets, sets, trees and a run"
```

---

### Task 3: Load the selected build when the Builds cursor rests, with a cache

**Files:**
- Modify: `internal/view/tui/planscreen.go` (`showBuild`, new `restCmd`, `planRest`)
- Modify: `internal/view/tui/msgs.go` (`planRestMsg`)
- Modify: `internal/view/tui/model.go` (`dispatchKey` movement, `Update` cases `planRestMsg`, `buildLoadedMsg`, `handleWatchEvent`)
- Test: `internal/view/tui/planscreen_test.go`

**Interfaces:**
- Consumes: `planScreen`, `showBuild`, `openPlan` (Task 2).
- Produces: `var planRestDelay = 250 * time.Millisecond`; `type planRestMsg struct { Gen int; Key string }`; `func (m Model) restCmd() tea.Cmd`; `func (m Model) cacheBuild(b provider.Build) Model`.

- [ ] **Step 1: Write the failing tests** (append to `planscreen_test.go`):

```go
// instantRest makes the Builds rest fire at once.
func instantRest(t *testing.T) {
	t.Helper()
	old := planRestDelay
	planRestDelay = 0
	t.Cleanup(func() { planRestDelay = old })
}

func threeBuilds() []provider.Build {
	out := make([]provider.Build, 0, 3)
	for i, n := range []int{44, 43, 42} {
		b := sampleBuild()
		b.Key, b.Number = "PROJ-BUILD-"+strconv.Itoa(n), n
		b.Stages = nil // a listing carries no stages
		if i == 0 {
			b.State = provider.StateFailed
		} else {
			b.State = provider.StateSuccess
		}
		out = append(out, b)
	}
	return out
}

// planOnBuilds is the plan screen on PROJ-BUILD with three builds, the
// newest open.
func planOnBuilds(t *testing.T) Model {
	t.Helper()
	m := planHome(t)
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: threeBuilds()})
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	return m
}

func TestOnlyTheRestingBuildLoads(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	var rests []tea.Msg
	for range 2 {
		var cmd tea.Cmd
		m, cmd = send(m, mkKey("j"))
		require.NotNil(t, cmd, "a cursor move schedules a rest")
		rests = append(rests, cmd())
	}
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key, "moving alone loads nothing")

	m, cmd := send(m, rests[0])
	require.Nil(t, cmd, "an older rest is dropped")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)

	m, cmd = send(m, rests[1])
	require.NotNil(t, cmd)
	require.Equal(t, "PROJ-BUILD-42", m.detail.Key)
}

func TestAFinishedBuildIsServedFromTheCache(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	full := sampleBuild()
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: full})
	require.Contains(t, m.plan.cache, "PROJ-BUILD-44")

	m, cmd := send(m, mkKey("j"))
	m, _ = send(m, cmd())
	m, cmd = send(m, mkKey("k"))
	m, cmd = send(m, cmd())
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Len(t, m.detail.Stages, 3, "the cached build has its stages")
	require.False(t, m.plan.stagesLoading)
	require.Nil(t, cmd, "a cached finished build is not read again")
}

func TestARunningBuildIsNeverCached(t *testing.T) {
	m := planOnBuilds(t)
	running := sampleBuild()
	running.State = provider.StateRunning
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: running})
	require.NotContains(t, m.plan.cache, running.Key)
}

func TestBuildsRefreshKeepsTheCursorOnTheOpenBuild(t *testing.T) {
	m := planOnBuilds(t)
	m.builds.cursor = 1
	m, _ = m.showBuild(m.builds.rows()[1]) // PROJ-BUILD-43 open
	newer := sampleBuild()
	newer.Key, newer.Number = "PROJ-BUILD-45", 45
	refreshed := append([]provider.Build{newer}, threeBuilds()...)

	m.buildsGen++
	m, _ = send(m, buildsLoadedMsg{Gen: m.buildsGen, PlanKey: "PROJ-BUILD", Builds: refreshed})
	b, _ := m.builds.selected()
	require.Equal(t, "PROJ-BUILD-43", b.Key)
	require.Equal(t, "PROJ-BUILD-43", m.detail.Key)
}
```

Add `"strconv"` and `tea "github.com/charmbracelet/bubbletea"` to the test imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestOnlyTheRestingBuildLoads|TestAFinishedBuildIsServedFromTheCache|TestARunningBuildIsNeverCached|TestBuildsRefreshKeepsTheCursorOnTheOpenBuild' -v`
Expected: FAIL (`planRestDelay` undefined).

- [ ] **Step 3: Implement**

`msgs.go`:

```go
// planRestMsg fires once the Builds cursor has stayed on Key for
// planRestDelay. Gen is the plan screen's restGen when it was scheduled.
type planRestMsg struct {
	Gen int
	Key string
}
```

`planscreen.go` (add `"time"` import):

```go
// planRestDelay is how long the Builds cursor rests before its build loads.
// A variable only so tests can make it fire at once.
var planRestDelay = 250 * time.Millisecond

// restCmd schedules loading the build under the Builds cursor, unless it is
// the one already open. Every call abandons the previous schedule.
func (m *Model) restCmd() tea.Cmd {
	if m.screen != screenPlan || m.focus != focusBuilds {
		return nil
	}
	b, ok := m.builds.selected()
	if !ok || (m.detail != nil && m.detail.Key == b.Key) {
		return nil
	}
	m.plan.restGen++
	gen, key := m.plan.restGen, b.Key
	return tea.Tick(planRestDelay, func(time.Time) tea.Msg { return planRestMsg{Gen: gen, Key: key} })
}

// planRest opens the build the cursor rested on, if it is still there.
func (m Model) planRest(msg planRestMsg) (Model, tea.Cmd) {
	if msg.Gen != m.plan.restGen || m.screen != screenPlan {
		return m, nil
	}
	b, ok := m.builds.selected()
	if !ok || b.Key != msg.Key {
		return m, nil
	}
	return m.showBuild(b)
}

// cacheBuild keeps a finished build with its stages, so moving back to it
// draws at once. A running build changes under the cache, so it never
// enters it.
func (m Model) cacheBuild(b provider.Build) Model {
	if m.screen != screenPlan || !b.State.Finished() || len(b.Stages) == 0 {
		return m
	}
	if m.plan.cache == nil {
		m.plan.cache = map[string]provider.Build{}
	}
	m.plan.cache[b.Key] = b
	return m
}
```

In `showBuild`, serve the cache before reading:

```go
	if c, ok := m.plan.cache[b.Key]; ok {
		m.detail = &c
		m.expanded = defaultExpanded(c)
		m.plan.stagesLoading = false
		return m, nil
	}
```

placed after `m.dropOlder(b.Key)` and before `bb := b`.

`model.go`, `dispatchKey` movement — schedule a rest after moving:

```go
	case key.Matches(msg, keys.Down):
		m.moveFocused(1)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Up):
		m.moveFocused(-1)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Top):
		m.jumpFocused(true)
		rest := m.restCmd()
		return m, rest
	case key.Matches(msg, keys.Bottom):
		m.jumpFocused(false)
		rest := m.restCmd()
		return m, rest
```

`restCmd` has a pointer receiver and bumps `restGen`; it is called in its own statement because Go does not order the evaluation of `m` and a call that mutates `m` within one `return` (the reason `goHome` does the same with `restartHomeTick`).

`Update`, new case:

```go
	case planRestMsg:
		return m.planRest(msg)
```

`Update`, `buildLoadedMsg` case — after `m.clampTree()`:

```go
		m.plan.stagesLoading = false
		m = m.cacheBuild(b)
```

`handleWatchEvent` — after `m.clampTree()` inside `if e.Build.Key != ""`:

```go
		m.plan.stagesLoading = false
```

and in `case app.EventDone:` before `m.stopWatch()`:

```go
		m = m.cacheBuild(e.Build)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/view/tui/ -run 'TestOnlyTheRestingBuildLoads|TestAFinishedBuildIsServedFromTheCache|TestARunningBuildIsNeverCached|TestBuildsRefreshKeepsTheCursorOnTheOpenBuild' -v`
Expected: PASS

Run: `go test -race ./internal/view/tui/ && make lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): load the build the Builds cursor rests on, and cache finished ones"
```

---

### Task 4: The Tree panel: what the open build triggered, and drilling into it

**Files:**
- Modify: `internal/view/tui/msgs.go` (`treePreviewMsg`, `treePreviewCmd`)
- Modify: `internal/view/tui/planscreen.go` (`showBuild` loads the preview, `treePreviewRows`, `drillTree`, `popPlan`)
- Modify: `internal/view/tui/model.go` (`Update` case, `drill`, `back`, `refresh`, `moveFocused`, `jumpFocused`, `selectionURL`, `handleWatchEvent`)
- Test: `internal/view/tui/planscreen_test.go`

**Interfaces:**
- Consumes: `planScreen.tree`, `treePreviewGen`, `showBuild`, `openPlan` (Tasks 2–3).
- Produces: `type treePreviewMsg struct { Gen int; Key string; Tree app.Tree; Err error }`; `func treePreviewCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd`; `func (m Model) treePreviewRows() []*app.TreeNode`; `func (m Model) loadTreePreview(key string, fresh bool) (Model, tea.Cmd)`; `func (m Model) drillTree() (Model, tea.Cmd)`; `func (m Model) popPlan() (Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests** (append to `planscreen_test.go`; `treeTUIModel`'s fake has PROJ-PROV-7 as a child of PROJ-BUILD-44):

```go
// planWithTree is the plan screen on PROJ-BUILD with build 44 open and its
// tree preview delivered.
func planWithTree(t *testing.T) Model {
	t.Helper()
	m, _ := treeTUIModel(t)
	m.screen, m.focus = screenHome, focusPlans
	require.True(t, m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "PROJ-BUILD" }))
	m, _ = send(m, mkKey("enter"))
	b := sampleBuild()
	m, cmd := m.showBuild(b)
	for _, msg := range runBatch(cmd) {
		if tp, ok := msg.(treePreviewMsg); ok {
			m, _ = send(m, tp)
		}
	}
	require.True(t, m.plan.tree.have)
	return m
}

func TestTreePanelListsDirectChildren(t *testing.T) {
	m := planWithTree(t)
	rows := m.treePreviewRows()
	require.Len(t, rows, 1)
	require.Equal(t, "PROJ-PROV-7", rows[0].Build.Key)
}

func TestEnterOnATreeRowOpensTheChildPlanAndEscComesBack(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-PROV", m.plan.key)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	require.Equal(t, focusStages, m.focus)
	require.Len(t, m.plan.stack, 1)

	m, _ = send(m, mkKey("esc"))
	require.Equal(t, focusBuilds, m.focus)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "PROJ-BUILD", m.plan.key, "esc on Builds pops the drill stack")
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Equal(t, focusTree, m.focus, "focus returns to where the drill started")
	require.Empty(t, m.plan.stack)

	m.focus = focusBuilds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
}

func TestALateTreePreviewIsDropped(t *testing.T) {
	m := planWithTree(t)
	stale := treePreviewMsg{Gen: m.treePreviewGen - 1, Key: "PROJ-BUILD-44"}
	before := m.plan.tree
	m, _ = send(m, stale)
	require.Equal(t, before, m.plan.tree)

	gen := m.treePreviewGen
	next, _ := m.switchServer("lab")
	m = next.(Model)
	m, _ = send(m, treePreviewMsg{Gen: gen, Key: "PROJ-BUILD-44"})
	require.False(t, m.plan.tree.have, "a preview from before a server switch is dropped")
}

func TestTreePreviewErrorStaysInItsPanel(t *testing.T) {
	m := planWithTree(t)
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: m.detail.Key, Err: errBoom})
	require.Equal(t, errBoom, m.plan.tree.err)
	require.Nil(t, m.err, "the status bar is not the tree's")
	require.NotNil(t, m.detail)
}

func TestRefreshOnTreeReadsItAgain(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	gen := m.treePreviewGen
	m, cmd := send(m, mkKey("r"))
	require.NotNil(t, cmd)
	require.Greater(t, m.treePreviewGen, gen)
	require.True(t, m.plan.tree.loading)
}

func TestTreeRowURLIsTheChildBuilds(t *testing.T) {
	m := planWithTree(t)
	m.focus = focusTree
	require.Equal(t, labOrigin+"/browse/PROJ-PROV-7", m.selectionURL())
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestTreePanel|TestEnterOnATreeRow|TestALateTreePreview|TestTreePreviewError|TestRefreshOnTree|TestTreeRowURL' -v`
Expected: FAIL (`treePreviewMsg` undefined).

- [ ] **Step 3: Implement**

`msgs.go`:

```go
// treePreviewMsg is the Tree panel's one-level tree of build Key.
type treePreviewMsg struct {
	Gen  int
	Key  string
	Tree app.Tree
	Err  error
}

// treePreviewCmd reads what build key triggered, one level down. It is read
// once, not watched: the plan screen reads it again on r and when the open
// build finishes.
func treePreviewCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		t, err := svc.BuildTree(ctx, key, app.TreeOptions{Depth: 1})
		return treePreviewMsg{Gen: gen, Key: key, Tree: t, Err: err}
	}
}
```

`planscreen.go`:

```go
// loadTreePreview reads key's tree, or takes it from the cache unless fresh.
func (m Model) loadTreePreview(key string, fresh bool) (Model, tea.Cmd) {
	m.treePreviewGen++
	m.plan.treeCursor = 0
	if t, ok := m.plan.trees[key]; ok && !fresh {
		m.plan.tree = treePreview{key: key, tree: t, have: true}
		return m, nil
	}
	m.plan.tree = treePreview{key: key, loading: true}
	if m.svc == nil {
		return m, nil
	}
	return m, treePreviewCmd(m.baseCtx(), m.svc, key, m.treePreviewGen)
}

// treePreviewLoaded keeps the tree for the build it was read for. A finished
// tree is cached: Bamboo queues children when the parent ends, so a tree is
// final once nothing in it runs.
func (m Model) treePreviewLoaded(msg treePreviewMsg) (Model, tea.Cmd) {
	if msg.Gen != m.treePreviewGen || m.screen != screenPlan || msg.Key != m.plan.tree.key {
		return m, nil
	}
	m.plan.tree = treePreview{key: msg.Key, tree: msg.Tree, have: true, err: msg.Err}
	if msg.Err == nil && msg.Tree.Root != nil && msg.Tree.State != provider.StateRunning {
		if m.plan.trees == nil {
			m.plan.trees = map[string]app.Tree{}
		}
		m.plan.trees[msg.Key] = msg.Tree
	}
	return m, nil
}

// treePreviewRows is the Tree panel's rows: the open build's direct children.
func (m Model) treePreviewRows() []*app.TreeNode {
	if !m.plan.tree.have || m.plan.tree.tree.Root == nil {
		return nil
	}
	return m.plan.tree.tree.Root.Children
}

// drillTree opens the plan screen of the child under the Tree cursor, and
// remembers this one so esc comes back to it.
func (m Model) drillTree() (Model, tea.Cmd) {
	rows := m.treePreviewRows()
	if m.plan.treeCursor >= len(rows) || m.svc == nil {
		return m, nil
	}
	n := rows[m.plan.treeCursor]
	if n.Loop {
		return m, nil
	}
	frame := planFrame{key: m.plan.key, buildsPlan: m.buildsPlan, focus: focusTree}
	if m.detail != nil {
		frame.build = *m.detail
	}
	stack := append(m.plan.stack, frame)
	var want *provider.Build
	if !n.NotRun {
		b := n.Build
		want = &b
	}
	m, cmd := m.openPlan(m.masterOf(n.PlanKey), n.PlanKey, want, focusStages)
	m.plan.stack = stack
	return m, cmd
}

// popPlan goes back to the plan screen the last Tree drill came from.
func (m Model) popPlan() (Model, tea.Cmd) {
	last := len(m.plan.stack) - 1
	f := m.plan.stack[last]
	stack := m.plan.stack[:last]
	var want *provider.Build
	if f.build.Key != "" {
		b := f.build
		want = &b
	}
	m, cmd := m.openPlan(f.key, f.buildsPlan, want, f.focus)
	m.plan.stack = stack
	return m, cmd
}
```

In `showBuild`, load the preview for every build it opens. Change both return paths so the tree loads too:

```go
	if c, ok := m.plan.cache[b.Key]; ok {
		m.detail = &c
		m.expanded = defaultExpanded(c)
		m.plan.stagesLoading = false
		return m.loadTreePreview(b.Key, false)
	}
	bb := b
	m.detail = &bb
	m.expanded = defaultExpanded(b)
	m.plan.stagesLoading = true
	var read tea.Cmd
	if b.State.Finished() {
		read = reloadBuildCmd(m.baseCtx(), m.svc, b.Key, m.detailGen)
	} else {
		var next tea.Model
		next, read = m.startWatch(b.Key)
		m = next.(Model)
	}
	m, tree := m.loadTreePreview(b.Key, false)
	return m, tea.Batch(read, tree)
```

`TestAFinishedBuildIsServedFromTheCache` (Task 3) asserts `cmd == nil` for a cached build. With the preview loading too, that holds only when the tree is cached as well. Replace it with:

```go
func TestAFinishedBuildIsServedFromTheCache(t *testing.T) {
	instantRest(t)
	m := planOnBuilds(t)
	full := sampleBuild()
	m, _ = send(m, buildLoadedMsg{Gen: m.detailGen, Build: full})
	done := app.Tree{Root: &app.TreeNode{PlanKey: "PROJ-BUILD", Build: full}, State: provider.StateFailed}
	m, _ = send(m, treePreviewMsg{Gen: m.treePreviewGen, Key: full.Key, Tree: done})
	require.Contains(t, m.plan.cache, "PROJ-BUILD-44")
	require.Contains(t, m.plan.trees, "PROJ-BUILD-44")

	m, cmd := send(m, mkKey("j"))
	m, _ = send(m, cmd())
	m, cmd = send(m, mkKey("k"))
	m, cmd = send(m, cmd())
	require.Equal(t, "PROJ-BUILD-44", m.detail.Key)
	require.Len(t, m.detail.Stages, 3, "the cached build has its stages")
	require.False(t, m.plan.stagesLoading)
	require.True(t, m.plan.tree.have, "the cached tree shows at once")
	require.Nil(t, cmd, "a cached finished build and tree are not read again")
}
```

`model.go`:

- `Update`: `case treePreviewMsg: return m.treePreviewLoaded(msg)`
- `drill()`: add

```go
	case focusTree:
		return m.drillTree()
```

- `back()`: insert after the `screenPlan && m.focus != focusBuilds` case:

```go
	case m.screen == screenPlan && len(m.plan.stack) > 0:
		return m.popPlan()
```

- `refresh()`: add

```go
	case focusTree:
		if m.detail == nil {
			return m, nil
		}
		return m.loadTreePreview(m.detail.Key, true)
```

- `moveFocused`: `case focusTree: m.plan.treeCursor = min(max(m.plan.treeCursor+delta, 0), max(len(m.treePreviewRows())-1, 0))`
- `jumpFocused`: `case focusTree: m.plan.treeCursor = 0; if !top { m.plan.treeCursor = max(len(m.treePreviewRows())-1, 0) }`
- `selectionURL()`: add

```go
	case focusTree:
		if rows := m.treePreviewRows(); m.plan.treeCursor < len(rows) {
			if u := rows[m.plan.treeCursor].Build.URL; u != "" {
				return u
			}
		}
```

- `handleWatchEvent`, `case app.EventDone:` — read the tree again once, since children are queued when the parent ends:

```go
		if m.screen == screenPlan && m.detail != nil && m.plan.tree.key == m.detail.Key {
			var tree tea.Cmd
			m, tree = m.loadTreePreview(m.detail.Key, true)
			if m.svc != nil && m.buildsPlan != "" {
				load := m.loadBuilds(m.buildsPlan, false)
				return m, tea.Batch(tree, load)
			}
			return m, tree
		}
```

placed after `m.stopWatch()` and before the existing builds reload.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/view/tui/ -run 'TestTreePanel|TestEnterOnATreeRow|TestALateTreePreview|TestTreePreviewError|TestRefreshOnTree|TestTreeRowURL|TestAFinishedBuildIsServedFromTheCache' -v`
Expected: PASS

Run: `go test -race ./internal/view/tui/ && make lint`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): the Tree panel lists what a build triggered and drills into it"
```

---

### Task 5: Draw the plan screen

**Files:**
- Modify: `internal/view/tui/planscreen.go` (rendering)
- Modify: `internal/view/tui/panels.go` (remove the old layout)
- Modify: `internal/view/tui/detail.go` (`detailBody` without its header line)
- Modify: `internal/view/tui/model.go` (`planView`)
- Modify: `internal/view/tui/golden_test.go`, `panels_test.go`
- Goldens: delete `columns-60x20.txt`, `columns-80x24.txt`, `columns-120x40.txt`, `detail-80x24.txt`, `detail-estimate-80x24.txt`; add `plan-80x24.txt`, `plan-120x40.txt`, `plan-60x16.txt`, `plan-tree-80x24.txt`, `plan-notree-80x24.txt`, `plan-estimate-80x24.txt`

**Interfaces:**
- Consumes: everything in `planScreen` (Tasks 2–4).
- Produces: `func (m Model) planHeader(width int) []string`; `func (m Model) planNameOf(planKey string) string` (exists, unchanged); `func planLeftWidth(total int) int`; `func (m Model) planBuildRows(width, height int) string`; `func (m Model) stagesBody(width, height int) string` (renamed from `detailBody`); `func (m Model) treeBody(width, height int) string`; `func (m Model) nameLine(width int, planKey string) string`.

- [ ] **Step 1: Write the failing tests**

In `golden_test.go`, replace the three `TestColumnsGolden*`, `TestDetailGolden` and `TestDetailWithEstimateGolden` with:

```go
// planGolden is goldenModel on the plan screen with build 44 open, its
// stages read and its tree delivered: PROJ-PROV-7 and a not-run child.
func planGolden(w, h int) Model {
	m := goldenModel(w, h)
	m.plan.key = "PROJ-BUILD"
	b := sampleBuild()
	b.FinishedAt = goldenNow.Add(-3 * time.Minute)
	m.detail = &b
	m.expanded = defaultExpanded(b)
	m.plan.tree = treePreview{key: b.Key, have: true, tree: app.Tree{Root: &app.TreeNode{
		PlanKey: "PROJ-BUILD", Build: b,
		Children: []*app.TreeNode{
			{PlanKey: "PROJ-PROV", Build: provider.Build{Key: "PROJ-PROV-7", PlanKey: "PROJ-PROV", Number: 7, State: provider.StateFailed}},
			{PlanKey: "OPS-NIGHTLY", NotRun: true},
		},
	}}}
	return m
}

func TestPlanGolden80x24(t *testing.T)  { requireGolden(t, "plan-80x24", planGolden(80, 24).View()) }
func TestPlanGolden120x40(t *testing.T) { requireGolden(t, "plan-120x40", planGolden(120, 40).View()) }
func TestPlanGolden60x16(t *testing.T)  { requireGolden(t, "plan-60x16", planGolden(60, 16).View()) }

func TestPlanGoldenTreeFocused(t *testing.T) {
	m := planGolden(80, 24)
	m.focus = focusTree
	requireGolden(t, "plan-tree-80x24", m.View())
}

func TestPlanGoldenEmptyTree(t *testing.T) {
	m := planGolden(80, 24)
	m.plan.tree.tree.Root.Children = nil
	requireGolden(t, "plan-notree-80x24", m.View())
}

func TestPlanGoldenWithEstimate(t *testing.T) {
	m := planGolden(80, 24)
	b := runningDetail()
	m.detail = &b
	m.progress = runningEstimate()
	requireGolden(t, "plan-estimate-80x24", m.View())
}
```

Change `TestViewNeverExceedsTheTerminal` to range over the plan screen too, and add it under the Review Focus name:

```go
// TestPlanViewNeverExceedsTheTerminal resizes across the small-terminal
// threshold and back (Review Focus 2).
func TestPlanViewNeverExceedsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {59, 19}, {40, 10}, {60, 20}, {20, 6}, {80, 24}} {
		m := planGolden(size[0], size[1])
		lines := strings.Split(m.View(), "\n")
		require.LessOrEqual(t, len(lines), size[1], "%v", size)
		for _, l := range lines {
			require.LessOrEqual(t, lipgloss.Width(l), size[0], "%v: %q", size, l)
		}
	}
}
```

Add to `planscreen_test.go`:

```go
func TestPlanHeaderNamesThePlanAndNeverItsKeyInARow(t *testing.T) {
	m := planGolden(80, 24)
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Build and test", lines[0])
	require.Contains(t, lines[1], "PROJ › PROJ-BUILD")
	require.Contains(t, lines[1], "last build #44")
	for _, l := range lines[3:] {
		require.NotContains(t, l, "PROJ-BUILD-", "rows show numbers, not build keys")
	}
}

func TestLongPlanNameIsCutWithAnEllipsis(t *testing.T) {
	m := planGolden(80, 24)
	long := strings.Repeat("Provision the integration lab cluster ", 4)[:120]
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", Name: long, ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.True(t, strings.HasSuffix(lines[0], "…"), lines[0])
	for _, l := range lines {
		require.LessOrEqual(t, lipgloss.Width(l), 80)
	}
}

func TestStagesTitleSaysLoading(t *testing.T) {
	m := planGolden(80, 24)
	m.plan.stagesLoading = true
	require.Contains(t, m.View(), "Stages · #44 · loading")
}
```

(add `"strings"`, `"github.com/charmbracelet/lipgloss"` to the test imports; `golden_test.go` needs `"github.com/r0jjames/bam-cli/internal/app"`.)

In `panels_test.go`, delete `TestPlanRowsShowTheNameWhenItFits`, `TestPlanRowsWithoutANameShowTheKey` and `TestMainPanelNamesTheBuildsPlan` (their panels are gone; the header test replaces them). Port `TestPanelCursorRowsAreBars` to `planBuildRows` and Stages: the cursor row of each is one `selectedRowStyle` bar.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestPlanGolden|TestPlanViewNeverExceedsTheTerminal|TestPlanHeader|TestLongPlanName|TestStagesTitle' -v`
Expected: FAIL (golden files missing; header text differs).

- [ ] **Step 3: Implement the rendering** in `planscreen.go` (add imports `fmt`, `strings`, `lipgloss`):

```go
// planCompact is the small-terminal form: name line only, one panel.
func (m Model) planCompact() bool { return m.width < 60 || m.height < 20 }

// planLeftWidth is the Builds column: two fifths of the terminal, at least
// 30 and at most 60 columns, leaving the right side at least 40.
func planLeftWidth(total int) int {
	w := min(max(total*2/5, 30), 60)
	return max(min(w, total-40), 0)
}

// nameLine is the plan's full name, bold, or its key when the name is not
// known; the line the plan screen, the run form and the log screen share.
func (m Model) nameLine(width int, planKey string) string {
	name := m.planNameOf(planKey)
	if name == "" {
		name = planKey
	}
	return " " + titleStyle.Render(cut(name, width-1))
}

// projectOf is the plan's project key from the plans list, or the key's
// first part.
func (m Model) projectOf(planKey string) string {
	for _, p := range m.plans.items {
		if p.Key == planKey && p.ProjectKey != "" {
			return p.ProjectKey
		}
	}
	project, _, _ := strings.Cut(planKey, "-")
	return project
}

// planHeader is §3.2: the name, then project › key, branch and last build.
func (m Model) planHeader(width int) []string {
	lines := []string{m.nameLine(width, m.plan.key)}
	if m.planCompact() {
		return lines
	}
	left := " " + m.projectOf(m.plan.key) + " › " + m.plan.key
	if m.buildsPlan != "" && m.buildsPlan != m.plan.key {
		branch := m.buildsPlan
		if len(m.builds.items) > 0 && m.builds.items[0].Branch != "" {
			branch = m.builds.items[0].Branch
		}
		left += " @ " + branch
	}
	right := ""
	if len(m.builds.items) > 0 {
		lb := m.builds.items[0]
		if lb.Key == m.plan.older && len(m.builds.items) > 1 {
			lb = m.builds.items[1]
		}
		right = stateCell(lb.State) + dimStyle.Render(fmt.Sprintf(" last build #%d %s", lb.Number, m.ago(lb.FinishedAt)))
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if right == "" || gap < 2 {
		lines = append(lines, dimStyle.Render(cut(left, width)))
	} else {
		lines = append(lines, dimStyle.Render(left)+strings.Repeat(" ", gap)+right)
	}
	title := "builds"
	if q := m.builds.query; q != "" {
		title += "</" + q + ">"
	}
	title += fmt.Sprintf("[%d]", m.builds.len())
	marker := ""
	if m.builds.loading {
		marker = "refreshing…"
	}
	return append(lines, titleRule(width, title, marker))
}

// planBuildRows is the Builds panel: glyph, number, branch, age, who. Who
// goes first, then branch, when the panel is narrow.
func (m Model) planBuildRows(width, height int) string {
	rows := m.builds.rows()
	if len(rows) == 0 {
		if m.builds.loading {
			return dimStyle.Render("loading…")
		}
		return dimStyle.Render("no builds yet")
	}
	start, end := m.builds.window(height)
	inner := width - 4
	var b strings.Builder
	for i := start; i < end; i++ {
		bd := rows[i]
		focused := m.focus == focusBuilds
		b.WriteString(m.cursorFor(focused, m.builds.cursor == i))
		head := stateStyle(bd.State).Render(stateGlyph(bd.State)) + fmt.Sprintf(" #%-4d", bd.Number)
		branch, age, by := branchOrDefault(bd), ageShort(m.now().Sub(bd.FinishedAt)), shortBy(bd.Reason)
		if bd.FinishedAt.IsZero() {
			age = "–"
		}
		if bd.Key == m.plan.older {
			by = "(older)"
		}
		// Who goes first, then branch, when the panel is narrow (§3.3).
		line := head + "  " + branch + "  " + age + "  " + by
		if lipgloss.Width(line) > inner {
			line = head + "  " + branch + "  " + age
		}
		if lipgloss.Width(line) > inner {
			line = head + "  " + age
		}
		line = truncate(line, inner)
		if bd.Key == m.plan.older {
			line = dimStyle.Render(line)
		}
		if m.builds.cursor == i {
			line = selectedRow(line, inner, focused)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// treeBody is the Tree panel: one row per child of the open build.
func (m Model) treeBody(width, height int) string {
	t := m.plan.tree
	switch {
	case m.detail == nil:
		return dimStyle.Render("no builds yet")
	case t.loading:
		return dimStyle.Render("reading the tree…")
	case t.err != nil:
		return errorStyle.Render("could not read the tree: " + errorWhat(t.err))
	}
	rows := m.treePreviewRows()
	if len(rows) == 0 {
		return dimStyle.Render(fmt.Sprintf("#%d triggered nothing", m.detail.Number))
	}
	start, end := treeWindow(m.plan.treeCursor, len(rows), height)
	inner := width - 4
	focused := m.focus == focusTree
	var b strings.Builder
	for i := start; i < end; i++ {
		n := rows[i]
		name := m.planNameOf(n.PlanKey)
		if name == "" {
			name = n.PlanKey
		}
		var line string
		switch {
		case n.NotRun:
			line = dimStyle.Render("– " + name + "  not run")
		case n.Loop:
			line = dimStyle.Render("↺ " + name + "  already shown")
		default:
			tail := fmt.Sprintf("  #%d  %s", n.Build.Number, stateCell(n.Build.State))
			line = stateStyle(n.Build.State).Render(stateGlyph(n.Build.State)) + " " +
				cut(name, inner-2-lipgloss.Width(tail)) + tail
			if n.Superseded {
				line = dimStyle.Render(line)
			}
		}
		line = truncate(line, inner)
		b.WriteString(m.cursorFor(focused, m.plan.treeCursor == i))
		if m.plan.treeCursor == i {
			line = selectedRow(line, inner, focused)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// stagesTitle names the build Stages shows.
func (m Model) stagesTitle() string {
	if m.detail == nil {
		return "Stages"
	}
	t := fmt.Sprintf("Stages · #%d", m.detail.Number)
	if m.plan.stagesLoading {
		t += " · loading"
	}
	return t
}

func (m Model) treeTitle() string {
	if m.detail == nil {
		return "Tree"
	}
	return fmt.Sprintf("Tree · what #%d triggered", m.detail.Number)
}

func (m Model) stagesPanel(width, height int) string {
	body := dimStyle.Render("no builds yet")
	if m.detail != nil {
		body = m.stagesBody(width-2, height-2)
	}
	return panel(m.stagesTitle(), m.focus == focusStages, width, height, body)
}

// rightHeights splits the right column (§3.3): Stages gets its rows up to
// 60%, at least 5; Tree the rest, at least 3; an empty tree takes 3.
func (m Model) rightHeights(body int) (stages, tree int) {
	if m.plan.tree.have && m.plan.tree.err == nil && len(m.treePreviewRows()) == 0 {
		return body - 3, 3
	}
	want := 2 + 2 // border and the reason line with its blank
	if m.detail != nil {
		want += len(buildTree(*m.detail, m.expanded)) + failedTestLines(*m.detail)
	}
	stages = min(max(want, 5), body*3/5)
	tree = body - stages
	if tree < 3 {
		tree, stages = 3, body-3
	}
	return stages, tree
}

// planView is the whole plan screen: header, panels, status bar.
func (m Model) planView() string {
	head := m.planHeader(m.width)
	body := max(m.height-len(head)-1, 0)
	var frame string
	if m.planCompact() {
		switch m.focus {
		case focusStages:
			frame = m.stagesPanel(m.width, body)
		case focusTree:
			frame = panel(m.treeTitle(), true, m.width, body, m.treeBody(m.width, body-2))
		default:
			frame = panel("Builds", true, m.width, body, m.planBuildRows(m.width, body-2))
		}
	} else {
		lw := planLeftWidth(m.width)
		rw := m.width - lw
		sh, th := m.rightHeights(body)
		frame = lipgloss.JoinHorizontal(lipgloss.Top,
			panel("Builds", m.focus == focusBuilds, lw, body, m.planBuildRows(lw, body-2)),
			lipgloss.JoinVertical(lipgloss.Left,
				m.stagesPanel(rw, sh),
				panel(m.treeTitle(), m.focus == focusTree, rw, th, m.treeBody(rw, th-2))))
	}
	out := append(append([]string{}, head...), frame, m.statusBar(m.width))
	return m.overlayView(strings.Join(out, "\n"))
}
```

In `model.go`, delete the old `planView` (formerly `columnsView`) body that used `leftWidth`/`leftColumn`/`mainPanel`.

In `detail.go`, rename `detailBody` to `stagesBody` and replace its first lines (the `header` and the `lines := []string{header, reason, ""}`) with a reason line that keeps the progress bar:

```go
func (m Model) stagesBody(width, height int) string {
	b := *m.detail
	first := dimStyle.Render(strings.TrimSpace(b.Reason + " " + revisionShort(b)))
	// The estimate bar stays: it is the one thing about a running build the
	// Builds row and the header do not say.
	if bar := view.BarWithin(style.Mode{}, m.progress, width-lipgloss.Width(first)-3); bar != "" {
		first += "   " + bar
	}
	lines := []string{first, ""}
```

and change `m.cursorFor(m.focus == focusStages, …)` (already renamed) — unchanged otherwise.

In `panels.go`, delete `leftWidth`, `planRows`, `buildRows`, `presetRows`, `leftColumn`, `mainPanel`, `namedTitle`, `planSummary`. Keep `panel`, `truncate`, `cursorFor`, `ago`, `branchOrDefault`, `keyLine`, `statusBar`. Run `go vet ./internal/view/tui/` and delete any test that only exercised a removed function (`leftWidth` tests, if any); list them in the commit message.

- [ ] **Step 4: Generate and review the goldens**

```bash
git rm internal/view/tui/testdata/golden/columns-60x20.txt internal/view/tui/testdata/golden/columns-80x24.txt internal/view/tui/testdata/golden/columns-120x40.txt internal/view/tui/testdata/golden/detail-80x24.txt internal/view/tui/testdata/golden/detail-estimate-80x24.txt
go test ./internal/view/tui/ -run 'TestPlanGolden' -update
cat internal/view/tui/testdata/golden/plan-80x24.txt internal/view/tui/testdata/golden/plan-60x16.txt internal/view/tui/testdata/golden/plan-notree-80x24.txt
```

Check by eye against spec §3.1: line 1 ` Build and test`; line 2 `PROJ › PROJ-BUILD` with `✗ failed last build #44 3m ago` on the right; a `builds[2]` rule; Builds 32 columns wide at 80; Stages top right; Tree under it with `PROJ-PROV` (key, since its name is not in the plans list) and `– Nightly  not run`; nothing wider than 80. At 60×16, one name line and one full-width Builds panel. In the empty-tree golden, Tree is 3 rows with `#44 triggered nothing`.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/view/tui/ && make lint`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): draw the plan screen with the plan's name on top" -m "Removes the three-panel layout and its goldens. Deleted tests: <list>."
```

---

### Task 6: Key line and help for the plan screen

**Files:**
- Modify: `internal/view/tui/panels.go` (`keyLine`)
- Modify: `internal/view/tui/keys.go` (`helpRows`)
- Test: `internal/view/tui/planscreen_test.go`, `keys_test.go` if it pins help text, `help-80x24.txt` golden

**Interfaces:**
- Consumes: `screenPlan`, `focusBuilds/Stages/Tree`.
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

```go
func TestKeyLineFollowsThePlanScreenFocus(t *testing.T) {
	m := planGolden(80, 24)
	for _, tc := range []struct {
		f    focus
		want string
	}{
		{focusBuilds, "enter stages  R run  l logs  t tree  / filter  esc back"},
		{focusStages, "enter expand  l logs  R run  t tree  esc builds"},
		{focusTree, "enter open  t tree  l logs  esc builds"},
	} {
		m.focus = tc.f
		require.Equal(t, tc.want, m.keyLine())
		bar := m.statusBar(80)
		require.Contains(t, bar, tc.want, "the line fits an 80-column bar beside lab · 9.6.4 · jdoe")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/view/tui/ -run TestKeyLineFollowsThePlanScreenFocus -v`
Expected: FAIL (the key line is `?help  : cmd  tab focus  l logs  o open  q quit`).

- [ ] **Step 3: Implement**

In `keyLine()`, replace the final `return "?help  : cmd  tab focus  l logs  o open  q quit"` with:

```go
	switch m.focus {
	case focusStages:
		return "enter expand  l logs  R run  t tree  esc builds"
	case focusTree:
		return "enter open  t tree  l logs  esc builds"
	}
	return "enter stages  R run  l logs  t tree  / filter  esc back"
```

In `keys.go` `helpRows()`, change these rows:

```go
		{"MOVE", "tab", "next panel: Builds, Stages, Tree"},
		{"MOVE", "shift-tab", "previous panel"},
		{"MOVE", "1 2 3", "focus Builds, Stages, Tree (on Home: open the plan screen there)"},
		{"MOVE", "enter", "drill in: a build's stages, expand a stage, open a triggered plan"},
		{"MOVE", "esc", "back one level, close an overlay; from Stages or Tree to Builds; from a triggered plan to its parent; from Builds to Home; from a project's plans to :projects"},
		{"HOME", "enter", "open the plan screen on the row's plan"},
```

- [ ] **Step 4: Run the tests and regenerate the help golden**

Run: `go test ./internal/view/tui/ -run TestKeyLineFollowsThePlanScreenFocus -v` → PASS.
Run: `go test ./internal/view/tui/ -run TestHelpGolden -update && git diff internal/view/tui/testdata/golden/help-80x24.txt` and read the diff: only the rows above change.
Run: `go test -race ./internal/view/tui/ && make lint` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): the key line and help follow the plan screen's panels"
```

---

### Task 7: The plan's name on the run form and the log screen

**Files:**
- Modify: `internal/view/tui/form.go` (`formView`)
- Modify: `internal/view/tui/logs.go` (`logsView`), `model.go` (`logHeight`)
- Test: `internal/view/tui/planscreen_test.go`; goldens `form-80x24.txt`, `logs-80x24.txt`

**Interfaces:**
- Consumes: `func (m Model) nameLine(width int, planKey string) string` (Task 5), `m.masterOf` (Task 2).

- [ ] **Step 1: Write the failing tests**

```go
func TestRunFormNamesThePlan(t *testing.T) {
	m := formModel()
	m.plans.setItems([]provider.Plan{{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Provision lab", lines[0])
	require.Contains(t, lines[1], "PROJ-PROV12")
	require.Contains(t, lines[1], "branch develop")
	require.Contains(t, lines[2], "Variables")
}

func TestLogScreenNamesThePlan(t *testing.T) {
	m := logModel()
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"}})
	lines := strings.Split(m.View(), "\n")
	require.Equal(t, " Build and test", lines[0])
	require.Contains(t, lines[1], "logs")
	require.Len(t, lines, m.height)
}

func TestSmallScreensKeepTheirOldHeaders(t *testing.T) {
	m := logModel()
	m.height = 12
	m.logs.resize(m.width, m.logHeight())
	require.NotContains(t, strings.Split(m.View(), "\n")[0], "Build and test")
}
```

Check `formModel()` (form_test.go:109) and `logModel()` (logs_test.go:21): if their form ref is not `PROJ-PROV12` on `develop` or their log build is not on `PROJ-BUILD`, use the keys they do set in the assertions.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestRunFormNamesThePlan|TestLogScreenNamesThePlan|TestSmallScreensKeepTheirOldHeaders' -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

`form.go`, `formView`:

```go
// formView is the whole terminal: the plan's name, what will run, and the
// variables in the standard panel frame.
func (m Model) formView() string {
	ref := m.form.ref
	head := []string{}
	if m.height >= 20 {
		what := ref.PlanKey
		if ref.Branch != "" {
			what += " · branch " + ref.Branch
		}
		if m.form.target != "" {
			what += " · preset " + m.form.target
		}
		head = append(head, m.nameLine(m.width, m.masterOf(ref.PlanKey)), dimStyle.Render(" "+cut(what, m.width-1)))
	}
	title := "Variables"
	if len(head) == 0 {
		// A small terminal keeps the old title, so the plan is still named.
		title = "Run " + ref.PlanKey
		if m.form.target != "" {
			title = "Run " + m.form.target + " · " + ref.PlanKey
		}
		if ref.Branch != "" {
			title += " · " + ref.Branch
		}
	}
	body := m.height - 1 - len(head)
	parts := append(head, panel(title, true, m.width, body, m.formBody(m.width-2, body-2)), m.statusBar(m.width))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
```

`model.go`, `logHeight`:

```go
// logHeight is the viewport's height: the terminal minus the name line (on
// a terminal of 20 rows or more), the header and the key line.
func (m Model) logHeight() int {
	h := m.height - 2
	if m.height >= 20 {
		h--
	}
	return max(h, 1)
}
```

`logs.go`, `logsView` — before the final `return`:

```go
	out := []string{head, m.logs.vp.View(), foot}
	if m.height >= 20 {
		planKey := ""
		if b, ok := m.logBuild(); ok {
			planKey = m.masterOf(b.PlanKey)
		}
		out = append([]string{m.nameLine(m.width, planKey)}, out...)
	}
	return strings.Join(out, "\n")
```

- [ ] **Step 4: Run, regenerate, review**

Run: `go test ./internal/view/tui/ -run 'TestRunFormNamesThePlan|TestLogScreenNamesThePlan|TestSmallScreensKeepTheirOldHeaders' -v` → PASS.
Run: `go test ./internal/view/tui/ -run 'TestFormGolden|TestLogsGolden' -update` (use the real test names from `grep -n "form-80x24\|logs-80x24" internal/view/tui/*_test.go`), then `git diff internal/view/tui/testdata/golden/` and read it: one name line on top, `Variables` as the form's title, the log viewport one line shorter.
Run: `go test -race ./internal/view/tui/ && make lint` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui
git commit -m "feat(tui): name the plan on the run form and the log screen"
```

---

### Task 8: Docs, changelog and final checks

**Files:**
- Modify: `README.md` (TUI section, lines ~60–140)
- Modify: `docs/prd.md` (lines 14 and 77)
- Modify: `CHANGELOG.md` (`## [Unreleased]`)
- Modify: `docs/superpowers/specs/2026-10-10-bam-tui-plan-screen-design.md` (`**Status:** Draft` → `Implemented`)

- [ ] **Step 1: README**

Replace the paragraph `It opens on the plans table; \`enter\` opens the panels for a plan:` and the panels drawing under it with the plan screen drawing from `testdata/golden/plan-80x24.txt` (copy it verbatim into a fenced block), introduced by: `It opens on the plans table; \`enter\` opens a plan screen: the plan's full name on top, its builds on the left, and on the right the stages of the selected build and the plans that build triggered. \`enter\` on a triggered plan opens its own plan screen; \`esc\` comes back.`

Delete the sentence starting `Plans show their names next to their keys when the panel has room.` Update the key table rows: `tab` → `next panel: Builds, Stages, Tree`; `1 2 3` → `focus Builds, Stages, Tree (on Home: open the plan screen there)`; Home `enter` → `open the plan screen on the row's plan`.

- [ ] **Step 2: PRD**

`docs/prd.md` line 14: replace `with lazygit-style detail panels` with `with a plan screen for each pipeline`. Line 77: replace `behind it, a lazygit-style UI: stacked side panels for servers, projects and plans, builds, and presets; a main panel for build detail, stages, jobs and logs.` with `behind it, a plan screen per plan: its full name on top, its builds, the stages and jobs of the selected build, and the plans that build triggered; logs and the run form open from there.`

- [ ] **Step 3: CHANGELOG**

Under `## [Unreleased]`, in its `### Changed` group (create the group if it is missing), add one line:

```markdown
- The terminal UI's three-panel screen is replaced by a plan screen: the plan's full name on top, its builds, the selected build's stages, and the plans that build triggered. The run form and log screen name the plan too.
```

- [ ] **Step 4: Spec status**

Set `**Status:** Implemented` in the spec.

- [ ] **Step 5: Full verification**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
go test -race ./...
make lint
make check-fixtures
make docs && git diff --exit-code docs/cli
make build && ./bin/bam --version
```

Expected: all pass, `docs/cli` unchanged (no command changed). Then run `bam` against a personal Bamboo or the e2e target if one is configured and walk: Home → enter → j/k → tab → enter on a Tree row → esc esc → R → esc → l → esc → esc. If no server is available, say so in the PR instead of claiming it was checked.

- [ ] **Step 6: Commit**

```bash
git add README.md docs/prd.md CHANGELOG.md docs/superpowers/specs/2026-10-10-bam-tui-plan-screen-design.md
git commit -m "docs: describe the plan screen" -m "Closes #54."
```
