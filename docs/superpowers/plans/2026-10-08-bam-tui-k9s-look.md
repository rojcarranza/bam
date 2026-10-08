# k9s-style look and projects view Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the terminal UI a full-width selected-row bar, a k9s-style header (context, key hints, `view(scope)[count]` title), and a projects view that drills into one project's plans.

**Architecture:** Everything lives in `internal/view/tui`. The highlight is one helper in `theme.go` (`selectedRow`) used by Home's `tableLines` and the three panels. The header is new functions in `home.go` that `homeView` picks by terminal size. The projects view copies the sets view's shape: a fourth `homeView`, a `focusProjects`, its own `listState`, its own load stream reusing `loadProjectsCmd`. `app`, `provider` and the CLI do not change.

**Tech Stack:** Go 1.26, bubbletea v1.3, bubbles v0.21 (`key`), lipgloss v1.1, `github.com/charmbracelet/x/ansi` (already in `go.mod` as lipgloss's dependency; becomes a direct import), `github.com/muesli/termenv` (same; tests only), testify.

**Spec:** `docs/superpowers/specs/2026-10-08-bam-tui-k9s-look-design.md` (issues #44, #45). #43 (the plan-paging bug) is not in this plan.

## Global Constraints

- Placeholders only in code, tests, goldens and commits: `bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`.
- No new modules. `x/ansi` and `termenv` are already required by lipgloss; the commit that first imports one says so.
- `app` never imports `view`; `TestAppStaysUIReady` and `imports_test.go` must keep passing.
- A state is never signalled by colour alone: the `▸` mark stays on every cursor row; the glyph stays inside the bar.
- No rendered line may be wider than the terminal; no screen may have more lines than the terminal.
- Goldens are rewritten only with `go test ./internal/view/tui/ -update` after a deliberate renderer change, and the diff is read before committing.
- Conventional Commits, one commit per task, Roj's git identity, no `Co-Authored-By` or generated-by lines.
- Before each commit: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"; go test -race ./internal/view/tui/`.

## Review Focus

1. **Long or wide names on the cursor row** (a 60-character plan name, CJK in a name): the bar must not push the line past the terminal width. Pinned in Task 1 (`TestCursorRowNeverExceedsTheTable`) and Task 5 (`TestProjectsNeverExceedTheTerminal`).
2. **Resizing across the 20-row / 60-column threshold:** the screen must stay exactly `height` lines at 19 and at 20 rows. Pinned in Task 3 (`TestHomeFillsTheTerminalExactly`).
3. **An empty projects list or a filter that matches nothing:** Enter must do nothing, not panic or filter to `""` by accident. Pinned in Task 5 (`TestEnterOnAnEmptyProjectsTableDoesNothing`).
4. **A configured project that is missing on the server:** Enter must not narrow to a project with no plans; it says why. Pinned in Task 5 (`TestEnterOnAMissingProjectSaysSo`).
5. **Switching server while the projects view is open:** a late load from the old server must not appear, and Esc must not lead back to the old server's list. Pinned in Task 6 (`TestServerSwitchDropsTheProjectsTrail`).

---

### Task 1: Selected-row bar on Home's tables

**Files:**
- Modify: `internal/view/tui/theme.go` (add `selectedRowStyle`, `selectedRow`)
- Modify: `internal/view/tui/home.go:299-315` (`tableLines`; add `rowWidth`)
- Test: `internal/view/tui/theme_test.go`, `internal/view/tui/home_test.go`
- Goldens: `internal/view/tui/testdata/golden/home-*.txt`, `sets-80x24.txt`

**Interfaces:**
- Produces: `selectedRowStyle(focused bool) lipgloss.Style`; `selectedRow(s string, width int, focused bool) string` (strips every ANSI sequence from `s`, pads to `width` cells, renders with the bar style); `rowWidth(cols []tableCol) int`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/view/tui/theme_test.go` (add imports `strings`, `github.com/charmbracelet/lipgloss`, `github.com/charmbracelet/x/ansi`, `github.com/muesli/termenv` as needed):

```go
// forceColour turns on ANSI output for one test. Tests otherwise render
// without colour, as the goldens do.
func forceColour(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestSelectedRowIsOneSolidBar(t *testing.T) {
	forceColour(t)
	row := stateCell(provider.StateFailed) + "  PROJ-BUILD"
	got := selectedRow(row, 30, true)
	require.Equal(t, 2, strings.Count(got, "\x1b["), "one style start, one reset: no colour of the state's own inside the bar")
	require.Equal(t, 30, lipgloss.Width(got), "the bar spans the width it is given")
	require.True(t, strings.HasPrefix(ansi.Strip(got), "✗ failed  PROJ-BUILD"), "the glyph and label stay")
}

func TestSelectedRowDiffersWithoutFocus(t *testing.T) {
	forceColour(t)
	require.NotEqual(t, selectedRow("PROJ-BUILD", 12, true), selectedRow("PROJ-BUILD", 12, false))
}
```

Append to `internal/view/tui/home_test.go`:

```go
func TestCursorRowSpansTheTable(t *testing.T) {
	m := goldenHome(120, 30)
	lines := m.planTable(120, 10)
	require.Equal(t, 120, lipgloss.Width(lines[1]), "the cursor row is padded to the table's width")
	require.True(t, strings.HasPrefix(lines[1], "▸ "), "the mark stays, so selection shows without colour")
	require.Less(t, lipgloss.Width(lines[2]), 120, "other rows are not padded")
}

func TestCursorRowNeverExceedsTheTable(t *testing.T) {
	m := goldenHome(80, 24)
	long := provider.Plan{Key: "OPS-LONG", ProjectKey: "OPS", Name: strings.Repeat("名前", 40)}
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: append(samplePlans(), long)})
	m.plans.selectFirst(func(p provider.Plan) bool { return p.Key == "OPS-LONG" })
	for i, l := range strings.Split(m.View(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 80, "line %d", i)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestSelectedRow|TestCursorRow' -v`
Expected: FAIL to compile with `undefined: selectedRow`.

- [ ] **Step 3: Implement**

In `internal/view/tui/theme.go` add imports `strings` and `github.com/charmbracelet/x/ansi`, and below the `var (...)` block:

```go
// selectedRowStyle is the cursor row's bar, as k9s draws it: black on cyan
// where the keys go, light on grey in a panel without focus, so the user
// still sees where they were.
func selectedRowStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("8"))
}

// selectedRow draws s as the cursor row's bar, width cells wide. Any colour
// s carries is dropped first: a state's own colour ends in a reset, which
// would cut the bar in two.
func selectedRow(s string, width int, focused bool) string {
	plain := ansi.Strip(s)
	if pad := width - lipgloss.Width(plain); pad > 0 {
		plain += strings.Repeat(" ", pad)
	}
	return selectedRowStyle(focused).Render(plain)
}
```

In `internal/view/tui/home.go`, replace `tableLines` with:

```go
// tableLines is a header line and one line per row. cursor is the row index
// drawn as the selected bar, behind the ▸ mark; -1 marks none. Home's table
// always has the keys, so its bar is the focused one.
func tableLines(cols []tableCol, cells [][]string, cursor int) []string {
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.title
	}
	lines := []string{"  " + titleStyle.Render(joinCells(cols, head))}
	for r, row := range cells {
		if r == cursor {
			lines = append(lines, cursorStyle.Render("▸")+" "+selectedRow(joinCells(cols, row), rowWidth(cols), true))
			continue
		}
		lines = append(lines, "  "+joinCells(cols, row))
	}
	return lines
}

// rowWidth is how wide a row of cols is: the columns and the two spaces
// between each pair.
func rowWidth(cols []tableCol) int {
	w := 2 * (len(cols) - 1)
	for _, c := range cols {
		w += c.width
	}
	return w
}
```

- [ ] **Step 4: Run the tests**

Run: `go mod tidy && go test ./internal/view/tui/ -run 'TestSelectedRow|TestCursorRow' -v`
Expected: PASS. `go.mod` now lists `github.com/charmbracelet/x/ansi` and `github.com/muesli/termenv` in the direct `require` block; no new module was downloaded.

- [ ] **Step 5: Regenerate the goldens and read the diff**

Run: `go test ./internal/view/tui/ -update && git diff --stat internal/view/tui/testdata && git diff internal/view/tui/testdata | head -80`
Expected: only `home-*.txt` and `sets-80x24.txt` change, and only the `▸` row of each gains trailing spaces up to the table width. Anything else is a bug: stop and fix it.

- [ ] **Step 6: Run the package and commit**

```bash
go test -race ./internal/view/tui/
git add go.mod go.sum internal/view/tui/theme.go internal/view/tui/theme_test.go internal/view/tui/home.go internal/view/tui/home_test.go internal/view/tui/testdata/golden
git commit -m "feat(tui): draw Home's selected row as a full-width bar

x/ansi and termenv were already required through lipgloss; they are now
imported directly (ansi.Strip for the bar, termenv to force colour in tests)."
```

---

### Task 2: Selected-row bar in the panels

**Files:**
- Modify: `internal/view/tui/panels.go` (`planRows`, `buildRows`, `presetRows`)
- Test: `internal/view/tui/panels_test.go`
- Goldens: `columns-*.txt`, `detail-*.txt`, `help-80x24.txt`, `form-80x24.txt`, `logs-80x24.txt` may change only on panel cursor rows.

**Interfaces:**
- Consumes: `selectedRow(s string, width int, focused bool) string` from Task 1; `forceColour(t)` from `theme_test.go`.

- [ ] **Step 1: Write the failing test**

Append to `internal/view/tui/panels_test.go`:

```go
// barStart is the escape sequence a bar of this focus begins with.
func barStart(focused bool) string {
	return strings.SplitN(selectedRowStyle(focused).Render("x"), "x", 2)[0]
}

func TestPanelCursorRowsAreBars(t *testing.T) {
	forceColour(t)
	m := goldenModel(80, 24) // focus on Plans
	plans := strings.Split(m.planRows(20, 5), "\n")
	require.Contains(t, plans[0], barStart(true), "the focused panel's cursor row is the bright bar")
	require.NotContains(t, plans[1], barStart(true))
	builds := strings.Split(m.buildRows(40, 5), "\n")
	require.Contains(t, builds[0], barStart(false), "an unfocused panel shows where its cursor is")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/view/tui/ -run TestPanelCursorRowsAreBars -v`
Expected: FAIL: `plans[0]` does not contain the bar's escape sequence.

- [ ] **Step 3: Implement**

In `panels.go`, `planRows`: replace the line `b.WriteString(truncate(label, width-4))` with:

```go
		label = truncate(label, width-4)
		if m.plans.cursor == i {
			label = selectedRow(label, width-4, m.focus == focusPlans)
		}
		b.WriteString(label)
```

`presetRows`: replace `b.WriteString(truncate(label, width-4))` with:

```go
		label = truncate(label, width-4)
		if m.presets.cursor == i {
			label = selectedRow(label, width-4, m.focus == focusPresets)
		}
		b.WriteString(label)
```

`buildRows`: replace the loop body so the row is built before it is written:

```go
	for i := start; i < end; i++ {
		bd := rows[i]
		b.WriteString(m.cursorFor(m.focus == focusBuilds, m.builds.cursor == i))
		line := stateStyle(bd.State).Render(stateGlyph(bd.State)) +
			fmt.Sprintf(" #%d ", bd.Number) + dimStyle.Render(m.ago(bd.FinishedAt))
		if m.builds.cursor == i {
			line = selectedRow(line, width-4, m.focus == focusBuilds)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/view/tui/ -run 'TestPanelCursorRowsAreBars|TestViewNeverExceedsTheTerminal' -v`
Expected: PASS.

- [ ] **Step 5: Regenerate the goldens and read the diff**

Run: `go test ./internal/view/tui/ -update && git diff internal/view/tui/testdata | head -80`
Expected: changes only on panel rows that start with `▸`, which now run to the panel's inner edge. The panels already pad every line to the inner width, so most panel goldens may not change at all.

- [ ] **Step 6: Run the package and commit**

```bash
go test -race ./internal/view/tui/
git add internal/view/tui/panels.go internal/view/tui/panels_test.go internal/view/tui/testdata/golden
git commit -m "feat(tui): draw the panels' selected rows as bars"
```

---

### Task 3: k9s-style header and title rule

**Files:**
- Modify: `internal/view/tui/theme.go` (add `hintKeyStyle`)
- Modify: `internal/view/tui/home.go` (`homeHeader`, `homeView`; add `homeLoadMarker`, `homeTitle`, `homeHints`, `serverHost`, `homeHeaderBlock`, `titleRule`, `homeChrome`)
- Test: `internal/view/tui/home_test.go`
- Goldens: all `home-*.txt`, `sets-80x24.txt`; new `home-80x12.txt`

**Interfaces:**
- Produces: `func (m Model) homeTitle() (title, right string)` (title is `view(scope)</query>[count]`, right is `sort <label>[ · refreshing…|stale …]`); `func homeHints(v homeView) []key.Binding`; `func (m Model) homeChrome() []string`; `func titleRule(width int, title, right string) string`. Task 5 adds a `homeProjects` case to `homeTitle`, `homeHints` and `homeHeader`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/view/tui/home_test.go`:

```go
func TestHomeHeaderBlockNamesContextKeysAndTitle(t *testing.T) {
	v := goldenHome(120, 30).View()
	for _, want := range []string{
		"Server:", "lab [bamboo.lab.example]", "Version:", "9.6.4", "User:", "jdoe",
		"<enter> drill in", "<P> filter by project", "<?> help",
		"plans(all)[4]", "sort key↑",
	} {
		require.Contains(t, v, want)
	}
}

func TestHomeTitleCarriesProjectFilterAndCount(t *testing.T) {
	m := goldenHome(120, 30)
	m.project = "PROJ"
	m.applyFilter("fail")
	title, _ := m.homeTitle()
	require.Equal(t, "plans(PROJ)</fail>[1]", title)
	next, _ := m.showSets()
	title, _ = next.(Model).homeTitle()
	require.Equal(t, "sets[0]", title, "sets have no project scope")
}

func TestHomeTitleRuleShowsStale(t *testing.T) {
	m := goldenHome(120, 30)
	m.home.stale = true
	_, right := m.homeTitle()
	require.Equal(t, "sort key↑ · stale", right)
}

func TestTitleRuleFillsTheWidth(t *testing.T) {
	for _, w := range []int{20, 40, 80, 120} {
		require.Equal(t, w, lipgloss.Width(titleRule(w, "plans(all)[4]", "sort key↑ · refreshing…")), "width %d", w)
	}
	require.NotContains(t, titleRule(20, "plans(all)[4]", "sort key↑ · refreshing…"), "sort", "the right part goes first when it does not fit")
}

func TestSmallTerminalsKeepTheOneLineHeader(t *testing.T) {
	for _, size := range [][2]int{{80, 19}, {59, 30}} {
		v := goldenHome(size[0], size[1]).View()
		require.True(t, strings.HasPrefix(v, " bam · lab"), "%dx%d", size[0], size[1])
		require.NotContains(t, v, "Server:")
	}
}

func TestHomeFillsTheTerminalExactly(t *testing.T) {
	for _, size := range [][2]int{{80, 19}, {80, 20}, {120, 30}, {60, 20}} {
		lines := strings.Split(goldenHome(size[0], size[1]).View(), "\n")
		require.Len(t, lines, size[1], "%dx%d", size[0], size[1])
	}
}

func TestHomeGolden80x12(t *testing.T) { requireGolden(t, "home-80x12", goldenHome(80, 12).View()) }
```

In `TestShowPresetsSwitchesTheTable`, replace the two count assertions:

```go
	require.Contains(t, v, "presets(all)[3]")
	next, _ := m.showPlans()
	require.Contains(t, next.(Model).View(), "plans(all)[4]")
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestHomeHeaderBlock|TestHomeTitle|TestTitleRule|TestSmallTerminals|TestHomeFills|TestShowPresetsSwitchesTheTable' -v`
Expected: FAIL to compile with `undefined: m.homeTitle`.

- [ ] **Step 3: Implement**

In `theme.go`, add to the `var (...)` block:

```go
	hintKeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
```

In `home.go`, add imports `net/url` and `github.com/charmbracelet/bubbles/key` if missing. Replace the load-marker `switch` at the end of `homeHeader` with a call to a new helper, so both header forms say the same thing:

```go
	if s := m.homeLoadMarker(); s != "" {
		parts = append(parts, s)
	}
	return " " + strings.Join(parts, " · ")
}

// homeLoadMarker is "refreshing…", "stale 3m", "stale" or "": what both
// header forms say about the last plans load.
func (m Model) homeLoadMarker() string {
	switch {
	case m.plans.loading && len(m.plans.items) > 0:
		return "refreshing…"
	case m.home.stale && !m.home.loadedAt.IsZero():
		return "stale " + ageShort(m.now().Sub(m.home.loadedAt))
	case m.home.stale:
		return "stale"
	}
	return ""
}

// homeTitle is the title set into the rule above the table, k9s style:
// view(scope)</query>[count], and what goes at the rule's right end.
func (m Model) homeTitle() (title, right string) {
	scope := m.project
	if scope == "" {
		scope = "all"
	}
	name, scope, n, query, sortLabel := "plans", "("+scope+")", m.plans.len(), m.plans.query, m.home.sort.label(homePlans)
	switch m.home.view {
	case homePresets:
		name, n, query, sortLabel = "presets", m.presets.len(), m.presets.query, m.home.presetSort.label(homePresets)
	case homeSets:
		name, scope, n, query, sortLabel = "sets", "", m.sets.len(), m.sets.query, m.home.setSort.label(homeSets)
	}
	title = name + scope
	if query != "" {
		title += "</" + query + ">"
	}
	title += fmt.Sprintf("[%d]", n)
	right = "sort " + sortLabel
	if s := m.homeLoadMarker(); s != "" {
		right += " · " + s
	}
	return title, right
}

// homeHints is the key hints the header shows for view, most useful first.
// They are the bindings themselves, so a hint cannot name a key that is not
// bound.
func homeHints(v homeView) []key.Binding {
	switch v {
	case homePresets:
		return []key.Binding{keys.Enter, keys.Run, keys.Project, keys.Filter, keys.Sort, keys.Command, keys.Help}
	case homeSets:
		return []key.Binding{keys.Enter, keys.Run, keys.Filter, keys.Sort, keys.Command, keys.Help}
	}
	return []key.Binding{keys.Enter, keys.Run, keys.Open, keys.Project, keys.Filter, keys.Sort, keys.Copy, keys.Command, keys.Help}
}

// serverHost is the host of the server the UI is on, from the configuration:
// never a path or a query.
func (m Model) serverHost() string {
	for _, s := range m.deps.Servers {
		if s.Alias == m.server {
			if u, err := url.Parse(s.URL); err == nil {
				return u.Host
			}
		}
	}
	return ""
}

// hintWidth is one key-hint column: "<o> open in the browser" and a gap.
const hintWidth = 26

// homeHeaderBlock is the three lines of the k9s-style header: who and where
// on the left, key hints in as many columns as fit beside them.
func (m Model) homeHeaderBlock(width int) []string {
	server := m.server
	if h := m.serverHost(); h != "" {
		server += " [" + h + "]"
	}
	ctx := [3][2]string{{"Server:", server}, {"Version:", m.info.Version}, {"User:", m.user.Name}}
	leftW := 0
	for _, c := range ctx {
		leftW = max(leftW, 9+lipgloss.Width(c[1]))
	}
	hints := homeHints(m.home.view)
	cols := max((width-leftW-4)/hintWidth, 0)
	lines := make([]string, 3)
	for r, c := range ctx {
		var b strings.Builder
		b.WriteString(" " + dimStyle.Render(fmt.Sprintf("%-9s", c[0])) + titleStyle.Render(c[1]))
		b.WriteString(strings.Repeat(" ", leftW-9-lipgloss.Width(c[1])+3))
		for col := 0; col < cols; col++ {
			i := col*3 + r
			if i >= len(hints) {
				break
			}
			h := hints[i].Help()
			cell := hintKeyStyle.Render("<"+h.Key+">") + " " + h.Desc
			b.WriteString(cell + strings.Repeat(" ", max(hintWidth-lipgloss.Width(cell), 1)))
		}
		lines[r] = truncate(strings.TrimRight(b.String(), " "), width)
	}
	return lines
}

// titleRule is a rule width cells wide with title centred in it and right
// near its end. right is left out when it does not fit.
func titleRule(width int, title, right string) string {
	t := " " + title + " "
	tw := lipgloss.Width(t)
	if tw >= width {
		return truncate(t, width)
	}
	left := (width - tw) / 2
	rest := width - left - tw
	r, tail := "", ""
	if right != "" && lipgloss.Width(right)+3 <= rest {
		r, tail = " "+right+" ", "─"
	}
	mid := rest - lipgloss.Width(r) - lipgloss.Width(tail)
	return dimStyle.Render(strings.Repeat("─", left)) + titleStyle.Render(t) +
		dimStyle.Render(strings.Repeat("─", mid)+r+tail)
}

// homeChrome is what sits above Home's table: the k9s-style block and title
// rule when the terminal has room, else the one-line header.
func (m Model) homeChrome() []string {
	if m.width < 60 || m.height < 20 {
		return []string{truncate(m.homeHeader(), m.width)}
	}
	title, right := m.homeTitle()
	return append(m.homeHeaderBlock(m.width), titleRule(m.width, title, right))
}
```

In `homeView`, replace `body := m.height - 2` with:

```go
	head := m.homeChrome()
	body := max(m.height-len(head)-1, 0)
```

and replace `out = append(out, truncate(m.homeHeader(), m.width))` with `out = append(out, head...)`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/view/tui/ -run 'TestHome|TestTitleRule|TestSmallTerminals|TestShowPresets|TestTheHeader' -v`
Expected: PASS, except the golden tests, which fail until Step 5.

- [ ] **Step 5: Regenerate the goldens and read them**

Run: `go test ./internal/view/tui/ -update && cat internal/view/tui/testdata/golden/home-120x30.txt internal/view/tui/testdata/golden/home-80x12.txt`
Expected: `home-120x30` opens with the three header lines (`Server:  lab [bamboo.lab.example]` and three hint columns), then `──── plans(all)[4] ──── sort key↑ ─`. `home-80x12` still opens with ` bam · lab · 9.6.4 · jdoe · project all (none configured) · 4 plans · sort key↑`. Every file has exactly as many lines as its height.

- [ ] **Step 6: Run the package and commit**

```bash
go test -race ./internal/view/tui/
git add internal/view/tui/theme.go internal/view/tui/home.go internal/view/tui/home_test.go internal/view/tui/testdata/golden
git commit -m "feat(tui): k9s-style header with context, key hints and a titled rule"
```

---

### Task 4: Projects load reports missing projects and names its stream

**Files:**
- Modify: `internal/view/tui/msgs.go:56-68` (add `streamProjects`), `msgs.go:413-444` (`projectsLoadedMsg`, `loadProjectsCmd`)
- Modify: `internal/view/tui/model.go` (`projectsGen` field, `currentStream`, picker case of `projectsLoadedMsg`, the picker's `loadProjectsCmd` call near line 632)
- Test: `internal/view/tui/msgs_test.go`

**Interfaces:**
- Produces: `streamProjects stream`; `projectsLoadedMsg{Gen int; Stream stream; Projects []provider.Project; Missing []string}`; `loadProjectsCmd(ctx context.Context, svc *app.Service, s stream, gen int) tea.Cmd`; `Model.projectsGen int`.

- [ ] **Step 1: Write the failing test and update the old calls**

In `msgs_test.go`, change both existing calls `loadProjectsCmd(context.Background(), svc, 0)` to `loadProjectsCmd(context.Background(), svc, streamPicker, 0)`, then append:

```go
func TestLoadProjectsCmdReportsConfiguredProjectsMissingOnTheServer(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.Projects = append(f.Projects, provider.Project{Key: "OPS", Name: "Operations"})
	svc.Cfg.Project = &config.ProjectFile{Version: 1, Projects: []string{"OPS", "GONE", "PROJ"}}

	got := loadProjectsCmd(context.Background(), svc, streamProjects, 7)().(projectsLoadedMsg)
	require.Equal(t, streamProjects, got.Stream)
	require.Equal(t, 7, got.Gen)
	require.Equal(t, []string{"GONE"}, got.Missing)
	require.Len(t, got.Projects, 2)
}

func TestLoadProjectsCmdMissesNothingWhenNoneConfigured(t *testing.T) {
	got := loadProjectsCmd(context.Background(), testService(), streamPicker, 0)().(projectsLoadedMsg)
	require.Empty(t, got.Missing)
	require.Equal(t, streamPicker, got.Stream)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run TestLoadProjectsCmd -v`
Expected: FAIL to compile: `too many arguments in call to loadProjectsCmd`.

- [ ] **Step 3: Implement**

In `msgs.go`, append `streamProjects` as the last constant of the `stream` block. Replace `projectsLoadedMsg` and `loadProjectsCmd` with:

```go
type projectsLoadedMsg struct {
	Gen      int
	Stream   stream // streamPicker for the P picker, streamProjects for the projects view
	Projects []provider.Project
	Missing  []string // configured projects the server does not have
}

// loadProjectsCmd lists the projects: the configured projects that are
// actually on the server, in configured order, or every project on the
// server when none is configured. Missing names the configured ones the
// server does not have; the picker ignores it, the projects view shows it.
func loadProjectsCmd(ctx context.Context, svc *app.Service, s stream, gen int) tea.Cmd {
	return func() tea.Msg {
		ps, err := svc.P.ListProjects(ctx)
		if err != nil {
			return errMsg{Err: err, Where: "projects", Stream: s, Gen: gen}
		}
		keys := svc.ProjectKeys()
		if len(keys) == 0 {
			return projectsLoadedMsg{Gen: gen, Stream: s, Projects: ps}
		}
		byKey := map[string]provider.Project{}
		for _, p := range ps {
			byKey[p.Key] = p
		}
		msg := projectsLoadedMsg{Gen: gen, Stream: s}
		for _, k := range keys {
			if p, ok := byKey[k]; ok {
				msg.Projects = append(msg.Projects, p)
			} else {
				msg.Missing = append(msg.Missing, k)
			}
		}
		return msg
	}
}
```

In `model.go`: add a field line `projectsGen int` after `setsGen, setGen int`. In `currentStream`, add:

```go
	case streamProjects:
		return gen == m.projectsGen
```

In `Update`, change the picker's guard in `case projectsLoadedMsg:` to:

```go
		if msg.Stream != streamPicker || msg.Gen != m.pickerGen {
			return m, nil
		}
```

Change the picker's call near line 632 to `loadProjectsCmd(m.baseCtx(), m.svc, streamPicker, m.pickerGen)`.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/view/tui/`
Expected: PASS (the P picker tests still pass unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui/msgs.go internal/view/tui/model.go internal/view/tui/msgs_test.go
git commit -m "refactor(tui): let the projects load name its stream and report missing projects"
```

---

### Task 5: Projects view on Home

**Files:**
- Create: `internal/view/tui/projects.go`
- Create: `internal/view/tui/projects_test.go`
- Modify: `internal/view/tui/home.go` (`homeProjects`, `homeColNames`, `homeState.projectSort`, `homeFocus`, `homeView` table pick, `homeHeader`, `homeTitle`, `homeHints`, `currentHomeSort`, `setHomeSort`, `handleHomeKey`)
- Modify: `internal/view/tui/model.go` (`focusProjects`, `projects` field, `New`, `Update` for `projectsLoadedMsg` and `connectedMsg` and `errMsg`, `applyFilter`, `moveFocused`, `jumpFocused`, `selectionURL`)
- Modify: `internal/view/tui/command.go` (`alias` field, `projects` command, `findCommand`)
- Golden: new `home-projects-120x30.txt`

**Interfaces:**
- Consumes: `loadProjectsCmd(ctx, svc, streamProjects, gen)`, `projectsLoadedMsg.Missing`, `Model.projectsGen` (Task 4); `homeTitle`, `homeHints`, `tableLines` (Tasks 1, 3); existing `setProject(key string) (tea.Model, tea.Cmd)`, `goHome()`.
- Produces: `type projectItem struct{ Key, Name, URL string; Missing bool }` (Key "" is the `(all)` row); `func (m Model) showProjects() (tea.Model, tea.Cmd)`; `func (m Model) handleProjectsKey(msg tea.KeyMsg) (Model, tea.Cmd, bool)`; `func (m *Model) loadProjects() tea.Cmd`; `func (m Model) projectsLoaded(msg projectsLoadedMsg) Model`; `homeProjects homeView`; `focusProjects focus`. Task 6 changes the Enter case of `handleProjectsKey` and adds to `showProjects`.

- [ ] **Step 1: Write the failing tests**

Create `internal/view/tui/projects_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func sampleProjects() []provider.Project {
	return []provider.Project{
		{Key: "PROJ", Name: "Example Project", URL: labOrigin + "/browse/PROJ"},
		{Key: "OPS", Name: "Operations", URL: labOrigin + "/browse/OPS"},
	}
}

// projectsModel is Home on the projects view with sampleProjects loaded.
func projectsModel(t *testing.T, missing ...string) Model {
	t.Helper()
	m := goldenHome(120, 30)
	next, _ := m.showProjects()
	m = next.(Model)
	m, _ = send(m, projectsLoadedMsg{Gen: m.projectsGen, Stream: streamProjects, Projects: sampleProjects(), Missing: missing})
	return m
}

func projectKeys(m Model) []string {
	out := []string{}
	for _, p := range m.projects.rows() {
		out = append(out, p.Key)
	}
	return out
}

func selectedProject(m Model) string {
	p, _ := m.projects.selected()
	return p.Key
}

func TestProjectsCommandShowsTheProjectsTable(t *testing.T) {
	m, cmd := runCmdLine(goldenHome(120, 30), "projects")
	require.Equal(t, homeProjects, m.home.view)
	require.Equal(t, focusProjects, m.focus)
	require.True(t, m.projects.loading)
	require.NotNil(t, cmd)
	c, err := findCommand("proj")
	require.NoError(t, err, ":proj is the alias, not an ambiguous prefix")
	require.Equal(t, "projects", c.name)
	c, err = findCommand("project")
	require.NoError(t, err)
	require.Equal(t, "project", c.name, ":project keeps its meaning")
}

func TestProjectsTableListsAllFirstThenByKey(t *testing.T) {
	m := projectsModel(t)
	require.Equal(t, []string{"", "OPS", "PROJ"}, projectKeys(m))
	require.Equal(t, "", selectedProject(m), "the cursor starts on the active choice, (all)")
	title, _ := m.homeTitle()
	require.Equal(t, "projects[2]", title, "the (all) row is not counted")
	v := m.View()
	require.Contains(t, v, "(all)")
	require.Contains(t, v, "every project")
	require.Contains(t, v, "<esc> back / close")
}

func TestProjectsSortKeepsAllFirst(t *testing.T) {
	m := projectsModel(t)
	m, _ = send(m, mkKey("s")) // name
	require.Equal(t, []string{"", "PROJ", "OPS"}, projectKeys(m), "Example Project before Operations")
	m, _ = runCmdLine(m, "sort -key")
	require.Equal(t, []string{"", "PROJ", "OPS"}, projectKeys(m))
}

func TestProjectsFilterMatchesKeyAndName(t *testing.T) {
	m := projectsModel(t)
	m.applyFilter("oper")
	require.Equal(t, []string{"OPS"}, projectKeys(m))
	title, _ := m.homeTitle()
	require.Equal(t, "projects</oper>[1]", title)
}

func TestEnterOnAProjectNarrowsThePlans(t *testing.T) {
	m := projectsModel(t)
	m, _ = send(m, mkKey("j")) // OPS
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, "OPS", m.project)
	require.Equal(t, homePlans, m.home.view)
	require.Equal(t, focusPlans, m.focus)
	require.True(t, m.plans.loading)
	require.NotNil(t, cmd)
	title, _ := m.homeTitle()
	require.True(t, strings.HasPrefix(title, "plans(OPS)"))
}

func TestEnterOnAllClearsTheProject(t *testing.T) {
	m := goldenHome(120, 30)
	m.project = "OPS"
	next, _ := m.showProjects()
	m = next.(Model)
	m, _ = send(m, projectsLoadedMsg{Gen: m.projectsGen, Stream: streamProjects, Projects: sampleProjects()})
	require.Equal(t, "OPS", selectedProject(m), "the cursor starts on the active project")
	require.Contains(t, m.View(), "●")
	m, _ = send(m, mkKey("g"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, "", m.project)
	require.Equal(t, homePlans, m.home.view)
}

func TestEnterOnAMissingProjectSaysSo(t *testing.T) {
	m := projectsModel(t, "GONE")
	require.Contains(t, m.View(), "missing")
	m.projects.selectFirst(func(p projectItem) bool { return p.Key == "GONE" })
	m, cmd := send(m, mkKey("enter"))
	require.Nil(t, cmd)
	require.Equal(t, "", m.project)
	require.Equal(t, homeProjects, m.home.view)
	require.Contains(t, m.status, "GONE is not on this server")
}

func TestEnterOnAnEmptyProjectsTableDoesNothing(t *testing.T) {
	m := projectsModel(t)
	m.applyFilter("zzz")
	require.Equal(t, 0, m.projects.len())
	m, cmd := send(m, mkKey("enter"))
	require.Nil(t, cmd)
	require.Equal(t, homeProjects, m.home.view)
}

func TestProjectURLIsTheSelection(t *testing.T) {
	m := projectsModel(t)
	m, _ = send(m, mkKey("j"))
	require.Equal(t, labOrigin+"/browse/OPS", m.selectionURL())
}

func TestALateProjectsLoadIsDropped(t *testing.T) {
	m := projectsModel(t)
	m, _ = send(m, projectsLoadedMsg{Gen: m.projectsGen - 1, Stream: streamProjects})
	require.Equal(t, []string{"", "OPS", "PROJ"}, projectKeys(m))
	m, _ = send(m, projectsLoadedMsg{Gen: m.pickerGen, Stream: streamPicker})
	require.Equal(t, []string{"", "OPS", "PROJ"}, projectKeys(m), "a picker load never fills the projects view")
}

func TestProjectsNeverExceedTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {80, 24}, {60, 20}, {80, 12}} {
		m := projectsModel(t, "GONE")
		m.width, m.height = size[0], size[1]
		lines := strings.Split(m.View(), "\n")
		require.Len(t, lines, size[1])
		for i, l := range lines {
			require.LessOrEqual(t, lipgloss.Width(l), size[0], "line %d at %dx%d", i, size[0], size[1])
		}
	}
}

func TestProjectsGolden120x30(t *testing.T) {
	requireGolden(t, "home-projects-120x30", projectsModel(t, "GONE").View())
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'Project' -v`
Expected: FAIL to compile with `undefined: m.showProjects`.

- [ ] **Step 3: Create `projects.go`**

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// projectItem is one row of the projects table. The row with no key is
// (all): enter on it clears the project filter.
type projectItem struct {
	Key, Name, URL string
	Missing        bool // configured, but not on the server
}

// projectMatchText is what / matches a project on.
func projectMatchText(p projectItem) string {
	if p.Key == "" {
		return "(all) every project"
	}
	return p.Key + " " + p.Name
}

// projectLess orders the projects table by key or name under s. (all) is
// always first.
func projectLess(s homeSort) func(a, b projectItem) bool {
	return func(a, b projectItem) bool {
		if (a.Key == "") != (b.Key == "") {
			return a.Key == ""
		}
		x, y := a.Key, b.Key
		if s.col == colName {
			x, y = a.Name, b.Name
		}
		if c := strings.Compare(x, y); c != 0 {
			if s.desc {
				return c > 0
			}
			return c < 0
		}
		return a.Key < b.Key
	}
}

func (m *Model) loadProjects() tea.Cmd {
	m.projectsGen++
	m.projects.loading = true
	return loadProjectsCmd(m.baseCtx(), m.svc, streamProjects, m.projectsGen)
}

// showProjects picks Home's projects table (:projects) and reloads it: the
// list is one request.
func (m Model) showProjects() (tea.Model, tea.Cmd) {
	m.home.view = homeProjects
	var load tea.Cmd
	if m.svc != nil {
		load = m.loadProjects()
	}
	next, home := m.goHome()
	return next, tea.Batch(load, home)
}

// projectsLoaded fills the table. The cursor stays on the row it was on, or
// starts on the active project.
func (m Model) projectsLoaded(msg projectsLoadedMsg) Model {
	keep, had := m.projects.selected()
	items := []projectItem{{}}
	for _, p := range msg.Projects {
		items = append(items, projectItem{Key: p.Key, Name: p.Name, URL: p.URL})
	}
	for _, k := range msg.Missing {
		items = append(items, projectItem{Key: k, Missing: true})
	}
	m.projects.loading = false
	m.projects.setItems(items)
	want := m.project
	if had {
		want = keep.Key
	}
	m.projects.selectFirst(func(p projectItem) bool { return p.Key == want })
	return m
}

// projectCount is the projects the table shows, not counting (all).
func (m Model) projectCount() int {
	n := 0
	for _, p := range m.projects.rows() {
		if p.Key != "" {
			n++
		}
	}
	return n
}

// projectColumns fits the projects table to width; NAME takes what is left.
// The first column is the ● of the active project, the last says missing.
func projectColumns(width int, rows []projectItem) []tableCol {
	keyW := len("(all)")
	for _, p := range rows {
		keyW = max(keyW, lipgloss.Width(p.Key))
	}
	return fillWidth([]tableCol{
		{title: "", width: 1},
		{title: "KEY", width: min(keyW, 24)},
		{title: "NAME"},
		{title: "", width: len("missing")},
	}, width, "NAME")
}

func (m Model) projectCells(p projectItem) []string {
	mark := ""
	if p.Key == m.project {
		mark = "●"
	}
	switch {
	case p.Key == "":
		return []string{mark, "(all)", "every project", ""}
	case p.Missing:
		return []string{mark, dimStyle.Render(p.Key), "", dimStyle.Render("missing")}
	}
	return []string{mark, p.Key, p.Name, ""}
}

func (m Model) projectTable(width, height int) []string {
	rows := m.projects.rows()
	cols := projectColumns(width, rows)
	start, end := m.projects.window(height - 1)
	cells := make([][]string, 0, end-start)
	for _, p := range rows[start:end] {
		cells = append(cells, m.projectCells(p))
	}
	return tableLines(cols, cells, m.projects.cursor-start)
}

// handleProjectsKey takes the keys that differ on the projects table: enter
// narrows the plans to the project, r reloads the list, run and panel keys
// mean nothing here.
func (m Model) handleProjectsKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch {
	case key.Matches(msg, keys.Enter):
		p, ok := m.projects.selected()
		if !ok {
			return m, nil, true
		}
		if p.Missing {
			m.status = fmt.Sprintf("%s is not on this server", p.Key)
			return m, nil, true
		}
		next, load := m.setProject(p.Key)
		nm := next.(Model)
		nm.home.view = homePlans
		shown, home := nm.goHome()
		return shown.(Model), tea.Batch(load, home), true
	case key.Matches(msg, keys.Refresh):
		if m.svc == nil {
			return m, nil, true
		}
		cmd := m.loadProjects()
		return m, cmd, true
	case key.Matches(msg, keys.Run),
		key.Matches(msg, keys.Panel1), key.Matches(msg, keys.Panel2), key.Matches(msg, keys.Panel3),
		key.Matches(msg, keys.NextPanel), key.Matches(msg, keys.PrevPanel):
		return m, nil, true
	}
	return m, nil, false
}
```

- [ ] **Step 4: Wire it into `home.go`**

1. `homeView` constants: add `homeProjects` after `homeSets`.
2. `homeColNames`: add `homeProjects: {"key", "name"},`.
3. `homeState`: add `projectSort homeSort // the projects table's order` after `setSort`.
4. `homeFocus`: add before `return focusPlans`:
   ```go
   	if m.home.view == homeProjects {
   		return focusProjects
   	}
   ```
5. `homeView()`: after the `homeSets` table pick, add:
   ```go
   	if m.home.view == homeProjects {
   		lines = m.projectTable(m.width, body)
   	}
   ```
6. `homeHeader()`: after the `homeSets` block that sets `count, sortLabel, query`, add:
   ```go
   	if m.home.view == homeProjects {
   		count, sortLabel, query = countOf(m.projectCount(), "project"), m.home.projectSort.label(homeProjects), m.projects.query
   	}
   ```
   and change `if m.home.view == homeSets {` (the one that drops the project part) to `if m.home.view == homeSets || m.home.view == homeProjects {`.
7. `homeTitle()`: add to its `switch` (its local is `sortLabel`, not `sort`, which would shadow the package):
   ```go
   	case homeProjects:
   		name, scope, n, query, sortLabel = "projects", "", m.projectCount(), m.projects.query, m.home.projectSort.label(homeProjects)
   ```
8. `homeHints()`: add to its `switch`:
   ```go
   	case homeProjects:
   		return []key.Binding{keys.Enter, keys.Back, keys.Open, keys.Filter, keys.Sort, keys.Copy, keys.Command, keys.Help}
   ```
9. `currentHomeSort()`: add first:
   ```go
   	if m.home.view == homeProjects {
   		return m.home.projectSort
   	}
   ```
10. `setHomeSort()`: add first:
    ```go
    	if m.home.view == homeProjects {
    		if int(s.col) >= len(homeColNames[homeProjects]) {
    			s.col = 0 // key and name only
    		}
    		keep, had := m.projects.selected()
    		m.home.projectSort = s
    		m.projects.setOrder(projectLess(s))
    		if had {
    			m.projects.selectFirst(func(p projectItem) bool { return p.Key == keep.Key })
    		}
    		return
    	}
    ```
11. `handleHomeKey()`: add first:
    ```go
    	if m.home.view == homeProjects {
    		if next, cmd, handled := m.handleProjectsKey(msg); handled {
    			return next, cmd, true
    		}
    	}
    ```

- [ ] **Step 5: Wire it into `model.go` and `command.go`**

`model.go`:
1. `focus` constants: add `focusProjects // Home's projects table; not in the panels' tab order` after `focusSets`.
2. `Model`: add `projects listState[projectItem]` after `sets listState[setItem]`.
3. `New`: add `projects: newList(projectMatchText),` after the `sets:` line, and after `m.plans.setOrder(planLess(m.home.sort))` add `m.projects.setOrder(projectLess(homeSort{}))`.
4. `Update`, `case projectsLoadedMsg:` add first:
   ```go
   		if msg.Stream == streamProjects {
   			if msg.Gen != m.projectsGen {
   				return m, nil
   			}
   			return m.projectsLoaded(msg), nil
   		}
   ```
5. `Update`, `case connectedMsg:` replace `cmd := m.loadPlans(); return m, cmd` with:
   ```go
   		cmd := m.loadPlans()
   		if m.home.view == homeProjects {
   			projects := m.loadProjects()
   			return m, tea.Batch(cmd, projects)
   		}
   		return m, cmd
   ```
6. `Update`, `case errMsg:` after `m.plans.loading, m.builds.loading = false, false` add:
   ```go
   		if msg.Stream == streamProjects {
   			m.projects.loading = false
   		}
   ```
7. `applyFilter`, `moveFocused`, `jumpFocused`: add a `case focusProjects:` calling `m.projects.setQuery(q)`, `m.projects.move(delta)`, `jump(&m.projects, top)` respectively.
8. `selectionURL`: add
   ```go
   	case focusProjects:
   		if p, ok := m.projects.selected(); ok {
   			return p.URL
   		}
   ```

`command.go`:
1. `command` struct: add `alias string // another exact name, for one a prefix cannot reach` after `name`.
2. `commands()`: after the `sets` entry add (keyed fields, since the struct gained one):
   ```go
   		{name: "projects", alias: "proj", desc: "the projects table; enter narrows the plans to one", run: func(m Model, _ string) (tea.Model, tea.Cmd, error) {
   			next, cmd := m.showProjects()
   			return next, cmd, nil
   		}},
   ```
   The other entries are positional; convert them all to keyed fields (`name:`, `arg:`, `desc:`, `run:`) in the same edit so the file compiles.
3. `findCommand`: change `if c.name == name {` to `if c.name == name || (c.alias != "" && c.alias == name) {`.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/view/tui/ -run 'Project|TestHome|Command|Colon' -v`
Expected: PASS except `TestProjectsGolden120x30`, which has no golden yet.

- [ ] **Step 7: Create the golden and read it**

Run: `go test ./internal/view/tui/ -run TestProjectsGolden120x30 -update && cat internal/view/tui/testdata/golden/home-projects-120x30.txt`
Expected: header block with `<esc> back / close`, rule `projects[2]`, then rows `▸ ● (all)  every project` (padded), `OPS  Operations`, `PROJ  Example Project`, `GONE … missing`. Then run `go test ./internal/view/tui/ -update` and check `git status`: the only other golden allowed to change is `help-80x24.txt`, and only if its visible part reaches the COMMANDS section, which now lists `:projects`. Add it to the commit if so.

- [ ] **Step 8: Run the package and commit**

```bash
go test -race ./internal/view/tui/
git add internal/view/tui/projects.go internal/view/tui/projects_test.go internal/view/tui/home.go internal/view/tui/model.go internal/view/tui/command.go internal/view/tui/testdata/golden/home-projects-120x30.txt
git commit -m "feat(tui): projects view on Home, enter narrows the plans to one project"
```

---

### Task 6: Esc back to the projects view

**Files:**
- Modify: `internal/view/tui/model.go` (`fromProjects` field, `back()` Home case, `setProject`, `switchServer`)
- Modify: `internal/view/tui/projects.go` (`handleProjectsKey` Enter, `showProjects`, add `backToProjects`)
- Modify: `internal/view/tui/home.go` (`showPresets`), `internal/view/tui/sets.go` (`showSets`)
- Test: `internal/view/tui/projects_test.go`

**Interfaces:**
- Consumes: `showProjects`, `handleProjectsKey`, `projectsModel(t)` (Task 5).
- Produces: `Model.fromProjects bool`; `func (m Model) backToProjects() (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

Append to `projects_test.go`:

```go
// fromOPS is the plans view reached from the projects view on OPS.
func fromOPS(t *testing.T) Model {
	t.Helper()
	m := projectsModel(t)
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.fromProjects)
	return m
}

func TestEscReturnsFromPlansToProjects(t *testing.T) {
	m, cmd := send(fromOPS(t), mkKey("esc"))
	require.Equal(t, homeProjects, m.home.view)
	require.Equal(t, focusProjects, m.focus)
	require.Equal(t, "OPS", selectedProject(m), "the cursor is on the project just left")
	require.Equal(t, "OPS", m.project, "the filter stays")
	require.NotNil(t, cmd, "the list reloads")
	require.False(t, m.fromProjects)
}

func TestEscClearsThePlansFilterBeforeLeaving(t *testing.T) {
	m := fromOPS(t)
	m.applyFilter("x")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, homePlans, m.home.view)
	require.Equal(t, "", m.plans.query)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, homeProjects, m.home.view)
}

func TestEscFromPanelsThenHomeStillReturnsToProjects(t *testing.T) {
	m := fromOPS(t)
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: []provider.Plan{{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"}}})
	m, _ = send(m, mkKey("enter")) // panels, Builds
	m, _ = send(m, mkKey("esc"))   // Plans
	m, _ = send(m, mkKey("esc"))   // Home
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homePlans, m.home.view)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, homeProjects, m.home.view)
}

func TestEscOnPlansNotFromProjectsStaysPut(t *testing.T) {
	m, cmd := send(goldenHome(120, 30), mkKey("esc"))
	require.Equal(t, homePlans, m.home.view)
	require.Nil(t, cmd)
}

func TestPickingAProjectElsewhereEndsTheTrail(t *testing.T) {
	m := fromOPS(t)
	next, _ := m.setProject("PROJ") // what P and :project do
	m = next.(Model)
	require.False(t, m.fromProjects)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, homePlans, m.home.view)

	m = fromOPS(t)
	next, _ = m.showPresets()
	require.False(t, next.(Model).fromProjects)
	next, _ = m.showSets()
	require.False(t, next.(Model).fromProjects)
}

func TestServerSwitchDropsTheProjectsTrail(t *testing.T) {
	m := projectsModel(t)
	old := m.projectsGen
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.Empty(t, m.projects.rows(), "the old server's projects go")
	m, _ = send(m, projectsLoadedMsg{Gen: old, Stream: streamProjects, Projects: sampleProjects()})
	require.Empty(t, m.projects.rows(), "a late load from the old server is dropped")

	m = fromOPS(t)
	next, _ = m.switchServer("lab")
	require.False(t, next.(Model).fromProjects)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestEsc|TestPickingAProject|TestServerSwitchDrops' -v`
Expected: FAIL to compile with `m.fromProjects undefined`.

- [ ] **Step 3: Implement**

`model.go`:
1. `Model`: after `fromSet bool ...` add `fromProjects bool // Home's plans were reached from the projects view; esc returns there`.
2. `setProject`: make the first statement of the function `m.fromProjects = false // a project picked any other way ends the trail` (before the `if key == m.project` early return).
3. `switchServer`: after `m.project = ""` add:
   ```go
   	m.fromProjects = false
   	m.projectsGen++ // a projects load from the old server is dropped
   	m.projects.setItems(nil)
   ```
4. `back()`: replace the whole `case m.screen == screenHome:` body with:
   ```go
   	case m.screen == screenHome:
   		// Home is the top: esc clears the active table's filter, then on a
   		// plans table reached from the projects view goes back there, and
   		// otherwise does nothing.
   		switch {
   		case m.home.view == homeSets && m.sets.query != "":
   			m.sets.setQuery("")
   		case m.home.view == homePresets && m.presets.query != "":
   			m.presets.setQuery("")
   		case m.home.view == homeProjects && m.projects.query != "":
   			m.projects.setQuery("")
   		case m.home.view == homePlans && m.plans.query != "":
   			m.plans.setQuery("")
   		case m.home.view == homePlans && m.fromProjects:
   			return m.backToProjects()
   		}
   		return m, nil
   ```

`projects.go`:
1. `showProjects`: add `m.fromProjects = false` after `m.home.view = homeProjects`.
2. `handleProjectsKey`, Enter case: replace `return shown.(Model), tea.Batch(load, home), true` with:
   ```go
   		sm := shown.(Model)
   		sm.fromProjects = true // after setProject, which ends any older trail
   		return sm, tea.Batch(load, home), true
   ```
3. Add:
   ```go
   // backToProjects is esc on a plans table reached from the projects view:
   // the projects table again, the cursor on the project just left.
   func (m Model) backToProjects() (tea.Model, tea.Cmd) {
   	project := m.project
   	next, cmd := m.showProjects()
   	nm := next.(Model)
   	nm.projects.selectFirst(func(p projectItem) bool { return p.Key == project })
   	return nm, cmd
   }
   ```

`home.go`, `showPresets`: add `m.fromProjects = false` as its first statement. `sets.go`, `showSets`: same.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/view/tui/`
Expected: PASS, including the older `TestEscOnHomeClearsTheFilterAndNeverQuits` and `TestBackResolvesInSpecOrder`.

- [ ] **Step 5: Commit**

```bash
git add internal/view/tui/model.go internal/view/tui/projects.go internal/view/tui/projects_test.go internal/view/tui/home.go internal/view/tui/sets.go
git commit -m "feat(tui): esc on a project's plans returns to the projects view"
```

---

### Task 7: Help, README, changelog, spec status

**Files:**
- Modify: `internal/view/tui/keys.go` (the `HOME :` help row)
- Modify: `README.md` (terminal UI section, the `:` row near line 249)
- Modify: `CHANGELOG.md` (`## [Unreleased]`)
- Modify: `docs/superpowers/specs/2026-10-08-bam-tui-k9s-look-design.md` (Status)
- Golden: `help-80x24.txt`

- [ ] **Step 1: Update the help row and its golden**

In `keys.go`, `helpRows`, change the `HOME` `:` row's text to `"command bar: :plans :presets :sets :projects :project :server :sort :q"`.

Run: `go test ./internal/view/tui/ -update && git diff internal/view/tui/testdata`
Expected: only `help-80x24.txt` changes, and only that row.

- [ ] **Step 2: Update README.md**

Change the table row near line 249 to:

```markdown
| | `:` | command bar: :plans :presets :sets :projects :project :server :sort :q |
```

After the `:sets` paragraph near line 234 add:

```markdown
`:projects` (or `:proj`) lists the projects, `(all)` first, `●` on the one the plans are narrowed to. `enter` shows that project's plans; `esc` goes back to the list. A configured project the server does not have is marked `missing`.
```

- [ ] **Step 3: Update CHANGELOG.md**

Under `## [Unreleased]`, add an `### Added` section above `### Changed`:

```markdown
### Added

- The terminal UI's `:projects` view lists the projects; `enter` shows one project's plans and `esc` goes back.

```

and add to `### Changed`:

```markdown
- The terminal UI draws the selected row as a full-width bar and opens with a header that names the server, version and user, lists the keys, and titles the table, such as `plans(PROJ)[12]`.
```

- [ ] **Step 4: Mark the spec implemented**

In the spec, change `**Status:** Proposed` to `**Status:** Implemented`.

- [ ] **Step 5: Full verification**

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
go test -race ./...
make lint
make docs && git diff --exit-code docs/cli
make check-fixtures
```

Expected: all pass; `docs/cli` unchanged (no cobra command changed).

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui/keys.go internal/view/tui/testdata/golden/help-80x24.txt README.md CHANGELOG.md docs/superpowers/specs/2026-10-08-bam-tui-k9s-look-design.md
git commit -m "docs: describe the projects view and the new header"
```
