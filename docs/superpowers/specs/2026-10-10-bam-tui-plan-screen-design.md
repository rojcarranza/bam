# bam: a plan screen that names the pipeline, with its builds, stages and tree

**Status:** Implemented | **Date:** 2026-10-10
**Issue:** [#54](https://github.com/r0jjames/bam-cli/issues/54)
**Product requirements:** [`docs/prd.md`](../../prd.md) "Next (v0.2): terminal UI"
**Builds on:** [`2026-10-08-bam-tui-k9s-look-design.md`](2026-10-08-bam-tui-k9s-look-design.md) (Home's header, title rule, selected-row bar), [`2026-10-08-bam-build-tree-design.md`](2026-10-08-bam-build-tree-design.md) (`app.BuildTree`, the tree screen)
**Related:** [#53](https://github.com/r0jjames/bam-cli/issues/53) live stage logs, [#55](https://github.com/r0jjames/bam-cli/issues/55) undeclared variables in the run form

All hosts, keys, user names and paths are placeholders.

## 1. Problem

`bam` opens on Home, a k9s-style table of every plan. Enter on a plan opens the three-panel screen (`screenColumns`): Plans, Builds and Presets stacked on the left, build detail on the right. That screen loses track of which pipeline the user is on:

- The left column is a quarter of the terminal, 20 to 40 columns. At 80 columns the Plans panel shows only keys (`PROJ-BUILD`). Keys differ by a letter or two and do not say what the plan is.
- Titles are cut: `2 Builds · Build and test` becomes `2 Builds · Build`.
- Home's context disappears; the status bar names only the server.
- The three left panels split the height evenly, so an empty Presets panel takes as many rows as the list in use, and the right panel is mostly empty.
- The run form's title (`Run provision-lab · PROJ-PROV12 · develop`) and the log screen's header (`PROJ-BUILD-INT-44 · logs · failed`) name keys only. The user cannot tell which pipeline they are about to run, or which one they are reading.

The owner wants k9s's model: a table of pipelines, and Enter on one opens a screen about that pipeline only, with its full name on top and, in the frame, its builds, the stages of a build, and the pipelines that build triggered.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Three-panel screen | Replaced by the plan screen; not kept as an option | The owner's choice. Home does the Plans and Presets panels' job better; two layouts double the tests |
| What the plan screen holds | Builds (left), Stages and Tree of the selected build (right) | The owner's list: builds, stages, and what a build triggered |
| Top of the screen | Line 1 the plan's full name, bold. Line 2 dim: `project › key`, branch, last build | The name is what the user reads. The key stays visible once, for copying and searching Bamboo |
| Where the key shows | Only on line 2, never in a list row | Keys are not human-readable; rows that lead with them are what made the old screen unreadable |
| Picking another plan | Esc back to Home | k9s drill-down. Home already sorts, filters and scopes by project |
| Presets | Home only (`:presets`); `R` on Home's presets view is unchanged | The Presets panel goes with the three-panel screen |
| Tree in the frame | Direct children of the selected build (`BuildTree`, depth 1); Enter drills into a child's plan screen | One level answers "what does this call"; `t` still opens every level |
| Loading on cursor moves | Stages and tree load when the Builds cursor rests for 250 ms, cached per build | Holding `j` through 40 builds would otherwise send 40 build reads and 40 tree searches |
| Panel keys | `1 2 3` focus Builds, Stages, Tree; `tab`/`shift-tab` cycle them | Same meaning as before: number keys focus a panel |
| Run form and log screen | Same name-on-top line | Running and reading are where not knowing the pipeline costs most |
| Small terminals | Under 20 rows or 60 columns: one-line name, only the focused panel, full width | Keeps list rows on a small terminal; `tab` still moves between panels |
| Rejected | Wider left column and breadcrumb on the three-panel screen | The first proposal. The owner preferred a screen about one pipeline |
| Rejected | Plan rows with the name on one line and the key below | Halves the rows that fit; the key moved to the header instead |
| Out of scope | Adding undeclared variables in the run form | Its own issue, #55 |
| Out of scope | Live stage logs and search while streaming | Its own issue, #53 |

## 3. Layout

### 3.1 Look at 80×24 (widths approximate; §3.3 fixes them)

```
 Provision the integration lab cluster with nightly data
 PROJ › PROJ-PROV                                      ✗ last build #44 3m ago
──────────────────────────────── builds[44] ─────────────────────────────────
┏━Builds━━━━━━━━━━━━━━━━━━━━┓╭─Stages · #44─────────────────────────────────╮
┃▸ ✗ #44  main  3m   jdoe   ┃│▸ ✓ Build                    42s              │
┃  ✓ #43  main  1h   sched  ┃│  ✗ Test                     2m30s            │
┃  ✓ #42  dev   1d   jdoe   ┃│    ✗ Integration            2m30s            │
┃  ✗ #41  main  2d   jdoe   ┃│  – Deploy                                    │
┃                           ┃│                                              │
┃                           ┃│3 failed tests                                │
┃                           ┃├─Tree · what #44 triggered───────────────────┤
┃                           ┃│  ✗ Deploy to staging        #12  failed      │
┃                           ┃│  ✓ Smoke tests              #88  success     │
┃                           ┃│  ✓ Publish docs             #7   success     │
┃                           ┃│                                              │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━┛╰──────────────────────────────────────────────╯
lab · 9.6.4 · jdoe            enter stages  R run  l logs  t tree  esc back
```

The selected-row bar and focus colours are the k9s look's (`selectedRowStyle`, `panelEdge`); goldens render without colour, as today.

### 3.2 Header (two lines)

- **Line 1**: one space, then the plan's name in `titleStyle` (bold). A plan with no name shows its key. A name longer than the line is cut with `…` at the end; it is never wrapped.
- **Line 2**, dim: ` PROJ › PROJ-PROV`, then ` @ develop` when the Builds list is a branch plan, then, right-aligned, the last build of the list: state glyph and label, `last build #44`, age. When the line is too long, the right-hand part is dropped first; the left part is then cut at the end.
- The project part is the plan's `ProjectKey`, whether or not Home has a project filter.
- **Title rule**: `titleRule(width, "builds[n]", right)`, the Home helper. A Builds filter adds `</query>`: `builds</fail>[3]`. `right` carries the refreshing and stale markers as on Home.

When Stages or Tree has focus, the header does not change: it always names the plan the screen is about. The build being looked at is named by the panel titles (`Stages · #44`).

### 3.3 Panels

- **Builds** (left): `width*2/5`, at least 30 columns, at most 60, never more than `width-40`. Title `Builds`. One row per build: glyph, `#n`, branch, age, who started it (`jdoe`, `sched`, `commit`). Columns are dropped from the right when the panel is narrow: first who, then branch. A running build shows its glyph as today (`●`).
- **Stages** (right, top): the stage and job tree the main panel draws today (`buildTree`, `defaultExpanded`, `detailBody`), with its build header line removed: state, number, branch and duration are in the Builds row and the header. Title `Stages · #44`; while it loads, `Stages · #44 · loading`.
- **Tree** (right, bottom): one row per direct child of the selected build: glyph, the child plan's name (its key when the name is unknown), `#n`, state label. A configured child that did not run shows `– not run` (`TreeNode.NotRun`). Superseded runs are dim, as on the tree screen. A build that triggered nothing shows one dim line: `#44 triggered nothing`. Title `Tree · what #44 triggered`. Warnings from `Tree.Warnings` are not shown here; the tree screen (`t`) shows them.
- **Heights on the right**: Stages gets its rows plus border, at most 60% of the body and at least 5 rows; Tree gets the rest, at least 3 rows (border and one line). When the tree is empty, Tree takes exactly 3 rows and Stages the rest.

### 3.4 Small terminals

Under 20 rows or 60 columns: line 1 alone (no line 2, no title rule), then only the focused panel at full width and full height. `tab`, `1 2 3` switch which panel is shown. This replaces `leftWidth`'s clamping.

## 4. Behaviour

### 4.1 Entering and leaving

| From | Action | Plan screen opens on | Focus | Esc returns to |
| --- | --- | --- | --- | --- |
| Home, plans view | `enter` on a plan | that plan, cursor on its newest build | Builds | Home, cursor on that plan |
| Home, plans view | `1` / `2` / `3` | as `enter` | Builds / Stages / Tree | Home |
| Home, presets view | `enter` on a preset | the preset's plan and branch | Builds | Home, presets view |
| Set screen | `enter` on a run | that run's plan, cursor on that build | Stages | the set screen (`fromSet`) |
| Tree screen | `enter` on a build | that build's plan, cursor on that build | Stages | the tree screen (`fromTree`) |
| Plan screen, Tree panel | `enter` on a child | the child's plan, cursor on that child build | Stages | the parent plan's screen, cursor where it was |
| Run form | `ctrl-R` | the triggered build's plan, cursor on it, watched | Stages | Home |

- When the build the screen opens on is not among the listed builds (an old build from a set or tree), it is shown pinned at the top of Builds, marked dim `(older)`, until the user moves off it.
- Drilling through the Tree panel keeps a stack of plan screens (plan key, branch, cursor, focus). Esc pops one. The stack is cleared by leaving to Home, a server switch, or `:` commands that change screen.
- Esc order on the plan screen: close an overlay; clear the focused panel's filter; Stages or Tree focus → Builds; pop the drill stack; then the "Esc returns to" column.

### 4.2 Keys

Unchanged keys keep their meaning and act on the selected build (`currentBuild`): `R` run form (prefilled from the selected build, as today), `l`/`a` logs, `t` full tree screen, `C` cancel, `o`/`y` URL, `b` branch, `r` refresh the focused panel, `/` filter the focused list, `S` server, `:` command bar, `?` help.

| Key | Plan screen meaning |
| --- | --- |
| `enter` on Builds | focus Stages |
| `enter` on Stages | expand or collapse a stage, as in the detail panel today |
| `enter` on Tree | open the child's plan screen (§4.1) |
| `1` `2` `3` | focus Builds, Stages, Tree |
| `tab` / `shift-tab` | next / previous of Builds, Stages, Tree |
| `j` `k` `g` `G` | move in the focused panel; in Stages, over stage and job rows as `treeCursor` does today |

The status bar's key line follows the focused panel:

- Builds: `enter stages  R run  l logs  t tree  / filter  esc back`
- Stages: `enter expand  l logs  R run  t tree  esc builds`
- Tree: `enter open  t tree  l logs  esc builds`

Every line fits an 80-column bar beside `lab · 9.6.4 · jdoe`; the longer hints stay in `?` help, as the set screen does.

### 4.3 Loading

- Opening the screen loads the plan's builds (`loadBuilds`, unchanged) and, at once, the stages and tree of the build the cursor lands on.
- Moving the Builds cursor sends `planRestMsg{Gen, Key}` after `planRestDelay` (a package variable, 250 ms, like `homeRefreshEvery`, so tests can deliver the message directly). Each move bumps the rest generation; only the last move's message loads.
- **Stages** use the existing build read (`reloadBuildCmd`, `detailGen`). A running build is watched (`startWatch`), as opening a build does today; moving to another build stops that watch. Only one watch runs.
- **Tree** uses `svc.BuildTree(ctx, key, app.TreeOptions{Depth: 1})` in a one-shot command with its own generation (`treePreviewGen`). It is not watched. It is read again when `r` is pressed with Tree focused, and once when the watched build finishes, since Bamboo queues children when their parent ends.
- **Cache**: finished builds' stages are kept per build key while the screen is open, and finished trees for the whole session (#82), so moving back to a build, or reopening the plan from Home, draws at once. The tree screen (`t`) draws a cached tree at once too, marked `refreshing…` until its watch sends the current tree (or `refresh failed`, keeping the cached tree, when the watch ends in an error; `r` tries again), and that tree replaces the cached one or, when it runs again after a rerun, drops it. `r` on the Tree panel, and its re-read when the open build finishes, keep the tree shown with the title marked `refreshing…`; a read that fails keeps it, marked `refresh failed`. A running build is never served from the cache. The stages cache is cleared when the screen closes; both are cleared when the server switches.
- Stale results (an older generation, another server) are dropped, as for every other stream (`stale_test.go` pattern).

## 5. Run form and log screen

Both gain the plan screen's line 1 above what they show today.

**Run form**: line 1 the plan's full name; line 2 dim: `PROJ-PROV · branch develop · preset provision-lab` (branch and preset only when set). The panel's title becomes `Variables`. The key line is unchanged.

```
 Provision the integration lab cluster with nightly data
 PROJ-PROV · branch develop · preset provision-lab
┏━Variables━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
┃▸ cluster_name          beta                      required                    ┃
```

**Log screen**: line 1 the plan's full name; the current header (`PROJ-PROV-INT-44 · logs · failed   4/4`) becomes line 2. The viewport loses one line (`logHeight` is `height-3`).

Under 20 rows both drop line 1, so small terminals look as they do today. The plan's name comes from `planNameOf`; when it is unknown (a preset whose plan was never listed), line 1 shows the key.

## 6. Code

All changes are in `internal/view/tui`; `app`, `provider` and the CLI are unchanged.

- `screenColumns` is renamed `screenPlan`. Every path that set it (`drillFromHome`, `openTriggered`, `openSetBuild`, `openTreeBuild`, `closeTree`) opens the plan screen per §4.1.
- `focus` keeps `focusPlans`, `focusPresets`, `focusSets`, `focusProjects` for Home's tables. The plan screen uses `focusBuilds`, `focusStages` (renamed from `focusMain`) and a new `focusTree`. `focus.parent()` maps Stages and Tree to Builds.
- New `planscreen.go`: `planScreen` state (plan, branch, drill stack, tree preview, cache, rest generation), `planView()`, `planHeader(width)`, the panel sizing, `treePreviewRows`, and the load commands. `msgs.go` gains `planRestMsg` and `treePreviewMsg`.
- `panels.go` keeps `panel`, `truncate`, `cursorFor`, `selectedRow` use, `statusBar` and `keyLine`. `leftWidth`, `leftColumn`, `planRows`, `presetRows`, `mainPanel`, `planSummary` and `namedTitle` are removed. `buildRows` is rewritten for §3.3.
- `detail.go`: `detailBody` loses its header line when drawn in Stages.
- `form.go` and `logs.go`: the line-1 header (§5), shared with the plan screen through one helper, `nameLine(width, planKey)`.
- `keys.go`: help rows for `1 2 3`, `tab`, Home's `enter`.
- Docs: `README.md`'s TUI section and screenshot, and `docs/prd.md`'s description of the UI ("stacked side panels for … presets") are updated in the same branch, deliberately.

## 7. Tests

- **Goldens**: the plan screen at 80×24 and 120×40 (replacing `columns-*` and `detail-80x24`), at 60×16 (small, one panel), with Tree focused, and with an empty tree; `form-80x24` and `logs-80x24` regenerated with the name line. Each regenerated golden is reviewed.
- **Model tests**:
  - Enter on Home opens the plan screen on that plan with Builds focused; Esc returns to Home with the cursor on that plan.
  - Enter on a preset opens its plan and branch; Esc returns to the presets view.
  - The full plan name is on line 1 at 80 columns for a name of 60 characters; no list row contains the plan key.
  - Moving the cursor three times then delivering one `planRestMsg` loads one build, the last; an older `planRestMsg` loads nothing.
  - A cached finished build draws without a load; a running build is read again.
  - Enter on a Tree row opens the child's plan screen; Esc returns to the parent with its cursor; a second drill and two Escs return to the first plan.
  - A late `treePreviewMsg` after a server switch or a different build is dropped.
  - `1 2 3` and `tab` move focus; `R`, `l`, `t`, `C` act on the selected build.
  - Set screen and tree screen `enter` open the plan screen; Esc returns to them.
  - An old build not in the list is pinned with `(older)`.
- Existing tests that drive `screenColumns`, `focusPlans` or `focusPresets` on the panels move to the plan screen; tests of the removed panels are deleted, and the commit says which.
- `go test -race ./...` and `make lint` pass.

## 8. Out of scope

- Keeping the three-panel screen, or a setting to choose it.
- A Presets panel on the plan screen.
- Plan-level dependency graphs read from the plan's configuration; the Tree shows what a build triggered.
- Live stage logs and search while streaming (#53); undeclared variables in the run form (#55).
- Any change to `app`, `provider`, or non-interactive CLI output.
