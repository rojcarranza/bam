# bam: a k9s-style look for the terminal UI, and a projects view

**Status:** Implemented | **Date:** 2026-10-08
**Product requirements:** [`docs/prd.md`](../../prd.md) "Next (v0.2): terminal UI"
**Builds on:** [`2026-09-23-bam-v0.3-tui-home-design.md`](2026-09-23-bam-v0.3-tui-home-design.md) (Home, header, `:` commands), [`2026-10-06-bam-tui-sets-design.md`](2026-10-06-bam-tui-sets-design.md) (Home views)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

The terminal UI works but is hard to read at a glance. The selected row is marked only by a small blue `▸` in the gutter, which is easy to lose in a long table. The header is one dense line (`bam · work · 9.6.9 · jdoe · project all · 50 plans · sort key↑`) that does not say which keys do what. Narrowing to one project is possible (`P`, `:project`) but has no view of its own, and in practice showed at most 25 plans per project (a separate bug, §6).

The owner compares the UI with k9s, where the selected row is a solid colour bar, the header names the context and the keys, and namespaces are a list you drill into. This design brings those three ideas to Home. It adds no new use cases: `app` is unchanged.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Scope of the k9s look | Selected-row highlight and a k9s-style header | The owner's choice; a full skin (project hotkeys, crumb bar) is not needed |
| Highlight rendering | Re-render the selected row as plain text inside one bar style | Wrapping a row whose state cell is already coloured breaks the bar at each ANSI reset |
| State colour on the selected row | Dropped inside the bar; the glyph (`✓ ✗ ●`) stays | Red on cyan is hard to read; a state is never signalled by colour alone |
| `▸` gutter mark | Kept | Selection stays visible with `NO_COLOR` and in the goldens, which render without colour |
| Picking one project | A projects view on Home, Enter drills into its plans | The owner's choice; mirrors k9s namespaces |
| Per-project plan and failing counts | Not shown | Needs every plan's last build in every project: N requests to draw a list |
| Rejected | Project hotkeys `<0> <1> <2>` in the header | Not chosen; `P` and the projects view cover it |
| Rejected | Remembering the last project between runs | Not chosen; can come later as config. A configured `default_project` and `--project` flag (MVP spec §3.5) cover it without bam writing state |

## 3. Selected-row highlight

### 3.1 Look

The row under the cursor is drawn across the full table width with background colour 6 (cyan) and foreground colour 0 (black), bold. In a panel that does not have focus, the cursor row gets background colour 8 (grey) instead, so the user still sees where they were. Unchanged: the `▸` mark in the gutter (bold blue when focused, dim when not).

```
  PROJECT  KEY           NAME                  STATE        #  AGE  BY
  PROJ     PROJ-DEPLOY   Deploy to staging     ✗ failed    13  433d jdoe
▸ PROJ     PROJ-BUILD    Build and test        ✓ success  270  2d   jdoe   ← cyan bar
  PROJ     PROJ-NIGHTLY  Nightly acceptance    ✗ failed   161  12h  sched
```

### 3.2 Rendering

- `theme.go` gains `selectedRowStyle(focused bool) lipgloss.Style`, the single definition of the bar.
- `tableLines` (`home.go`) builds the cursor row from plain cells: the state cell is `style.Glyph(s) + " " + style.Label(s)` without `stateStyle`, then the joined row is padded to the table width and rendered with `selectedRowStyle`. Other rows are unchanged.
- `selectedRow` strips every ANSI sequence from the joined row (`x/ansi.Strip`) before rendering the bar, so the cell builders do not change and no ANSI sequence sits inside the bar except its own.
- The panels' cursor rows (`panels.go`) use the same style, so the Plans, Builds and Presets panels match Home.
- Lists in overlays (picker, help) and the form are out of scope; they keep the `▸`.

### 3.3 Tests

- A unit test asserts that the cursor row of `tableLines` contains exactly one style start and one reset when colour is forced on in the test. If forcing it needs `termenv` as a direct import, that is not a new module (lipgloss already pulls it in), but the commit says so.
- Goldens are regenerated with `-update`; the diff is reviewed. It should only show rows padded to the table width, since the goldens render without colour.

## 4. k9s-style header

### 4.1 Look

At 20 rows or more, the one-line header becomes a three-line block plus a titled border line above the table:

```
 Server:  work [bamboo.example.com]   <enter> drill in          <P> filter by project     <y> copy the URL
 Version: 9.6.9                       <R> open the run form     </> filter or search      <:> command bar
 User:    jdoe                        <o> open in the browser   <s> next sort column      <?> help
──────────────────────────── plans(PROJ)[132] ────────────────────────────────────
  PROJECT  KEY        NAME                              STATE      #  AGE  BY
```

- **Context block** (left): server alias with its host, Bamboo version, user name. Labels dim, values bold.
- **Key hints** (middle): up to three columns of `<key> description`, keys in colour 4. The hints for each Home view come from `keyMap` bindings (`WithHelp`), not from literal strings, so they cannot drift from the real keys. The projects view shows its own set (§5.1).
- **Title** in the rule above the table: `view(scope)[count]`.
  - `view` is `plans`, `presets`, `sets` or `projects`.
  - `scope` is the project key, or `all`. The one-line header keeps `all (none configured)`; the title does not repeat it. Sets and projects have no scope: `sets[4]`.
  - A filter adds `</query>`: `plans(PROJ)</fail>[3]`.
  - The sort, refreshing and stale markers move to the right end of the rule: `─ sort key↑ · refreshing… ─`.
- Below 20 rows, or below 60 columns, the header is today's single line, unchanged, so small terminals keep their table rows.

### 4.2 Code

- `homeHeader()` stays as the compact form. A new `homeHeaderBlock(width)` returns the context and hint lines; `homeTitleRule(width)` returns the titled rule. `homeView()` picks the form by size and shrinks the table's height by the header's height.
- The context block reads `m.server`, the server's origin from config, `m.info.Version`, `m.user.Name`. The origin is the configured URL's host only, never a path or query.

### 4.3 Tests

- Goldens for the plans view at 120×30 (block header) and 80×12 (compact header).
- Hints are `key.Binding` values from `keys`, so a hint that names an unbound key cannot compile; tests check the header shows the expected hints.
- The existing header tests for stale and refreshing still pass against the rule.

## 5. Projects view

### 5.1 Behaviour

A fourth Home view, `homeProjects`, after plans, presets and sets.

```
 Server:  work [bamboo.example.com]   <enter> drill in          <s> next sort column      <?> help
 Version: 9.6.9                       <o> open in the browser   <y> copy the URL
 User:    jdoe                        </> filter or search      <:> command bar
──────────────────────────── projects[14] ───────────────────────────────────────
  KEY      NAME
▸ (all)    every project                                                    ← bar
  LAB      Lab
● PROJ     Example Project
  OPS      Operations           missing
```

- **Open it** with `:projects`, alias `:proj` (an exact alias: as a prefix, `proj` would be ambiguous with `:project`). The help overlay and the `:` completion list it.
- **Rows** come from `ListProjects`, one request. When the configuration lists `projects:`, only those are shown; a configured project that is not on the server is shown dimmed with `missing`, as `plansLoadedMsg.Missing` already reports. The first row is `(all)`.
- **Title count and order.** The count in the title (`projects[14]`) excludes `(all)` and the missing projects. Missing projects sort last, in key order and in name order alike, after every project the server has.
- **`●`** marks the active project (`m.project`), or `(all)` when there is none.
- **Enter** on a project calls the existing `setProject(key)` and switches to the plans view. Enter on `(all)` calls `setProject("")`.
- **Esc** on the plans view, when it was reached from the projects view, goes back to the projects view with the cursor on the project just left. The project filter stays. In every other case Esc keeps its current meaning.
- **`/`** filters on key and name. **`s`** sorts by key or name.
- `P` and `:project KEY` stay and are unchanged.
- Switching server (`S`) reloads the list, as it does for plans.

### 5.2 State

- `homeState.view` gains `homeProjects`; `homeColNames[homeProjects]` is `{"key", "name"}`.
- `Model` gains `projects listState[projectItem]`, a `projectsGen` generation counter like the other streams, and `fromProjects bool`, set by Enter on the projects view; it survives the panels round trip and `:plans`; it is cleared by `:presets`, `:sets`, `:projects`, `setProject` (from `P` or `:project`) and a server switch.
- `loadProjectsCmd` returns `projectsLoadedMsg{Gen, Projects, Missing}`; stale generations are dropped as for plans.

### 5.3 Tests

- Model tests: `:projects` shows the view; Enter on `PROJ` sets the project and shows `plans(PROJ)`; Enter on `(all)` clears it; Esc returns to projects with the cursor on `PROJ`; Esc from a plans view not reached from projects does what it did before.
- A late `projectsLoadedMsg` from before a server switch is dropped (`stale_test.go` pattern).
- A golden for the projects view at 120×30.

## 6. Related fix: every plan of a project

Not part of this design, but the projects view depends on it. `ListPlans` (`provider/bamboo/read.go`) pages with `expand=plans[a:b].plan` and stops when it holds `plans.size` items. On the owner's server every project showed exactly 25 plans. The likely cause is a server that reports `size` as the page's count rather than the total, which ends the loop after the first page. The fix starts from a scrubbed recording of a project with more than 25 plans (`make record`), then changes the stop condition to stop on a short or empty page, test first. It is tracked as its own bug.

## 7. Out of scope

- Per-project plan and failing counts in the projects view.
- Project hotkeys in the header, a crumb bar, colour themes or skins.
- Highlight in overlays, pickers and the run form.
- Any change to `app`, `provider` or the CLI's non-interactive output.
