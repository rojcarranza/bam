package tui

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var homeNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func withBuild(p provider.Plan, n int, s provider.State, ago time.Duration, reason string) provider.Plan {
	p.LastBuild = &provider.BuildSummary{Key: p.Key + "-" + strconv.Itoa(n), Number: n, State: s,
		FinishedAt: homeNow.Add(-ago), Reason: reason}
	return p
}

func sortedKeys(ps []provider.Plan, less func(a, b provider.Plan) bool) []string {
	out := append([]provider.Plan(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	keys := []string{}
	for _, p := range out {
		keys = append(keys, p.Key)
	}
	return keys
}

func samplePlans() []provider.Plan {
	return []provider.Plan{
		withBuild(provider.Plan{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"}, 4, provider.StateFailed, 12*time.Minute, "Manual run by J Doe"),
		withBuild(provider.Plan{Key: "PROJ-PROV", Name: "Provision lab", ProjectKey: "PROJ"}, 8, provider.StateSuccess, 3*time.Hour, "Scheduled"),
		withBuild(provider.Plan{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"}, 9, provider.StateSuccess, 30*time.Second, "Changes by a1b2c3d <jdoe@example.com>"),
		{Key: "OPS-OLD", Name: "Archived", ProjectKey: "OPS"},
	}
}

func TestPlanLessColumns(t *testing.T) {
	ps := samplePlans()
	require.Equal(t, []string{"OPS-NIGHTLY", "OPS-OLD", "PROJ-BUILD", "PROJ-PROV"}, sortedKeys(ps, planLess(homeSort{})),
		"default: project, then key")
	require.Equal(t, []string{"OPS-OLD", "PROJ-BUILD", "OPS-NIGHTLY", "PROJ-PROV"}, sortedKeys(ps, planLess(homeSort{col: colName})))
	require.Equal(t, []string{"PROJ-BUILD", "OPS-NIGHTLY", "PROJ-PROV", "OPS-OLD"}, sortedKeys(ps, planLess(homeSort{col: colState})),
		"failed first; equal states break on project then key; no build last")
	require.Equal(t, []string{"OPS-NIGHTLY", "PROJ-BUILD", "PROJ-PROV", "OPS-OLD"}, sortedKeys(ps, planLess(homeSort{col: colAge})),
		"newest first; no build last")
	require.Equal(t, []string{"PROJ-PROV", "PROJ-BUILD", "OPS-OLD", "OPS-NIGHTLY"}, sortedKeys(ps, planLess(homeSort{col: colKey, desc: true})))
}

func TestHomeSortCyclesAndLabels(t *testing.T) {
	s := homeSort{}
	require.Equal(t, "key↑", s.label(homePlans))
	s = s.next()
	require.Equal(t, homeSort{col: colName}, s)
	require.Equal(t, "plan↑", s.label(homePresets))
	require.Equal(t, homeSort{}, homeSort{col: colAge, desc: true}.next(), "after age comes key again, ascending")
	require.Equal(t, "age↓", homeSort{col: colAge, desc: true}.label(homePlans))
}

func TestParseSort(t *testing.T) {
	s, err := parseSort(homePlans, "-age")
	require.NoError(t, err)
	require.Equal(t, homeSort{col: colAge, desc: true}, s)
	s, err = parseSort(homePresets, "target")
	require.NoError(t, err)
	require.Equal(t, homeSort{col: colKey}, s)
	_, err = parseSort(homePlans, "target")
	require.EqualError(t, err, `unknown sort column "target"; columns: key name state age`)
}

func TestShortBy(t *testing.T) {
	for reason, want := range map[string]string{
		"Manual run by J Doe":                   "J Doe",
		"Scheduled":                             "sched",
		"Changes by a1b2c3d <jdoe@example.com>": "commit",
		"Code has changed":                      "commit",
		"Child of PROJ-BUILD-4":                 "dep",
		"Dependant of PROJ-BUILD":               "dep",
		"Rebuilt by jdoe":                       "Rebuilt",
		"":                                      "–",
	} {
		require.Equal(t, want, shortBy(reason), reason)
	}
}

func TestAgeShort(t *testing.T) {
	require.Equal(t, "now", ageShort(30*time.Second))
	require.Equal(t, "12m", ageShort(12*time.Minute))
	require.Equal(t, "3h", ageShort(3*time.Hour))
	require.Equal(t, "2d", ageShort(49*time.Hour))
}

func titles(cols []tableCol) string {
	out := []string{}
	for _, c := range cols {
		out = append(out, c.title)
	}
	return strings.Join(out, " ")
}

func TestPlanColumnsDropBYThenPROJECT(t *testing.T) {
	ps := samplePlans()
	require.Equal(t, "PROJECT KEY NAME STATE # AGE BY", titles(planColumns(120, ps)))
	require.Equal(t, "PROJECT KEY NAME STATE # AGE", titles(planColumns(90, ps)))
	require.Equal(t, "KEY NAME STATE # AGE", titles(planColumns(70, ps)))
	for _, w := range []int{120, 90, 70} {
		total := 2
		for i, c := range planColumns(w, ps) {
			if i > 0 {
				total += 2
			}
			total += c.width
		}
		require.LessOrEqual(t, total, w, "width %d", w)
	}
}

func TestBuildCells(t *testing.T) {
	st, num, age, by := buildCells(nil, nil, homeNow)
	require.Equal(t, []string{"–", "–", "–", "–"}, []string{st, num, age, by})

	last := &provider.BuildSummary{Number: 4, State: provider.StateFailed, FinishedAt: homeNow.Add(-12 * time.Minute), Reason: "Scheduled"}
	st, num, age, by = buildCells(last, nil, homeNow)
	require.Contains(t, st, "failed")
	require.Equal(t, []string{"4", "12m", "sched"}, []string{num, age, by})

	live := &provider.Build{Number: 5, State: provider.StateRunning, Reason: "Manual run by jdoe"}
	st, num, age, by = buildCells(last, live, homeNow)
	require.Contains(t, st, "running")
	require.Equal(t, []string{"5", "now", "jdoe"}, []string{num, age, by}, "a live build wins over the last finished one")

	done := &provider.Build{Number: 5, State: provider.StateSuccess}
	st, _, _, _ = buildCells(last, done, homeNow)
	require.Contains(t, st, "failed", "a finished live build is not live")
}

func TestTableLinesPadsAlignsAndMarksTheCursor(t *testing.T) {
	cols := []tableCol{{title: "KEY", width: 6}, {title: "#", width: 3, right: true}, {title: "NAME", width: 5}}
	lines := tableLines(cols, [][]string{{"A", "4", "Longer name"}, {"B", "12", "x"}}, 1)
	require.Equal(t, []string{
		"  KEY       #  NAME",
		"  A         4  Long…",
		"▸ B        12  x    ",
	}, lines)
}

func TestPlanMatchTextIncludesProjectAndState(t *testing.T) {
	txt := planMatchText(samplePlans()[0])
	for _, want := range []string{"PROJ", "PROJ-BUILD", "Build and test", "failed"} {
		require.Contains(t, txt, want)
	}
}

func TestPlanCellsFollowsTheColumns(t *testing.T) {
	p := samplePlans()[0] // PROJ-BUILD, failed #4, 12m, Manual run by J Doe
	cols := planColumns(120, samplePlans())
	cells := planCells(cols, p, nil, homeNow)
	require.Len(t, cells, len(cols))
	got := map[string]string{}
	for i, c := range cols {
		got[c.title] = cells[i]
	}
	require.Equal(t, "PROJ", got["PROJECT"])
	require.Equal(t, "PROJ-BUILD", got["KEY"])
	require.Equal(t, "Build and test", got["NAME"])
	require.Contains(t, got["STATE"], "failed")
	require.Equal(t, "4", got["#"])
	require.Equal(t, "12m", got["AGE"])
	require.Equal(t, "J Doe", got["BY"])

	narrow := planColumns(70, samplePlans())
	require.Len(t, planCells(narrow, p, nil, homeNow), len(narrow), "cells follow the dropped columns")
}

func homeModel() Model {
	m := New(Deps{Servers: []Server{{Alias: "lab", URL: labOrigin}}, Initial: "lab"})
	m.width, m.height = 120, 30
	m.now = func() time.Time { return homeNow }
	m.svc = testService()
	m.server = "lab"
	m, _ = send(m, plansLoadedMsg{Gen: m.plansGen, Plans: samplePlans()})
	return m
}

func selectedKey(m Model) string {
	p, _ := m.plans.selected()
	return p.Key
}

func TestUIStartsOnHome(t *testing.T) {
	m := New(Deps{})
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, focusPlans, m.focus)
}

func TestHomeRowsAreSortedByProjectThenKey(t *testing.T) {
	m := homeModel()
	require.Equal(t, "OPS-NIGHTLY", selectedKey(m))
}

func TestEnterOnHomeOpensThePanelsOnThePlan(t *testing.T) {
	m := homeModel()
	m, _ = send(m, mkKey("j")) // OPS-OLD
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	require.True(t, m.builds.loading)
	require.NotNil(t, cmd)
	require.Equal(t, "OPS-OLD", selectedKey(m), "the Plans panel shows the plan that was opened")
}

func TestNumberKeysOnHomeOpenThePanelsWithThatFocus(t *testing.T) {
	for k, want := range map[string]focus{"1": focusBuilds, "2": focusStages, "3": focusTree} {
		m, _ := send(homeModel(), mkKey(k))
		require.Equal(t, screenPlan, m.screen, k)
		require.Equal(t, want, m.focus, k)
	}
}

func TestEscFromThePlansPanelReturnsHome(t *testing.T) {
	m := homeModel()
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc")) // Builds -> Plans
	m, _ = send(m, mkKey("esc")) // Plans -> Home
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, focusPlans, m.focus)
}

func TestEscOnHomeClearsTheFilterAndNeverQuits(t *testing.T) {
	m := homeModel()
	m.applyFilter("fail")
	require.Equal(t, 1, m.plans.len())
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, 4, m.plans.len())
	require.Nil(t, cmd)
	m, cmd = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Nil(t, cmd, "esc on Home with nothing to clear does nothing")
}

func TestHomeFilterMatchesStateAndProject(t *testing.T) {
	m := homeModel()
	m.applyFilter("fail")
	require.Equal(t, "PROJ-BUILD", selectedKey(m))
	m.applyFilter("ops")
	require.Equal(t, 2, m.plans.len())
}

func TestSKeyCyclesTheSortAndKeepsTheCursorOnItsPlan(t *testing.T) {
	m := homeModel()
	m, _ = send(m, mkKey("G")) // PROJ-PROV, last by key
	m, _ = send(m, mkKey("s")) // name
	require.Equal(t, homeSort{col: colName}, m.home.sort)
	require.Equal(t, "PROJ-PROV", selectedKey(m))
	m, _ = send(m, mkKey("s")) // state
	require.Equal(t, "PROJ-BUILD", m.plans.rows()[0].Key, "failed first")
	require.Equal(t, "PROJ-PROV", selectedKey(m))
}

func TestPanelOnlyKeysDoNothingOnHome(t *testing.T) {
	for _, k := range []string{"tab", "shift+tab", "l", "a", "f", "C", "b", "n", "N"} {
		m, cmd := send(homeModel(), mkKey(k))
		require.Equal(t, screenHome, m.screen, k)
		require.Equal(t, focusPlans, m.focus, k)
		require.Nil(t, cmd, k)
	}
}

func TestTheRunFormReturnsToWhereItWasOpened(t *testing.T) {
	m := homeModel()
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
}

// TestBackFromTheRunFormRestartsHomesAutoRefresh covers finding 1: a tick
// that arrives while the run form is open ends the auto-refresh loop (it
// dropped, off Home); nothing restarted it once esc closed the form back to
// Home. back() must restart it exactly as goHome does elsewhere.
func TestBackFromTheRunFormRestartsHomesAutoRefresh(t *testing.T) {
	m := homeModel()
	m, _ = send(m, mkKey("R"))
	require.Equal(t, screenForm, m.screen)
	tick := m.home.tickGen
	// The tick arrives while off Home: homeTick's own guard drops it and ends
	// the loop. The command it returns is never executed here (constraints).
	m, _ = send(m, homeTickMsg{Gen: tick})
	m, cmd := send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.NotNil(t, cmd, "back to Home must restart the loop")
	require.Greater(t, m.home.tickGen, tick)
}

// TestLiveForMatchesTheMasterAndItsBranches covers finding 3: a branch match
// only counts when the live build itself names a branch. Without one, a plan
// key that merely looks like a branch of another (PROJ-B2 of PROJ-B) must
// mark only its own row.
func TestLiveForMatchesTheMasterAndItsBranches(t *testing.T) {
	m := homeModel()
	m.home.live = map[string]provider.Build{
		"PROJ-PROV12": {Key: "PROJ-PROV12-9", PlanKey: "PROJ-PROV12", Branch: "develop", State: provider.StateRunning},
	}
	require.NotNil(t, m.liveFor("PROJ-PROV"), "a real branch build still marks its master")
	require.Nil(t, m.liveFor("PROJ-PRO"), "a key prefix that is not a branch number does not match")
	require.True(t, isBranchOf("PROJ-PROV12", "PROJ-PROV"))
	require.False(t, isBranchOf("PROJ-PROVX", "PROJ-PROV"))
	require.False(t, isBranchOf("PROJ-PROV", "PROJ-PROV"))
}

func TestLiveForExactKeyNeverNeedsABranch(t *testing.T) {
	m := homeModel()
	m.home.live = map[string]provider.Build{
		"PROJ-B2": {Key: "PROJ-B2-1", PlanKey: "PROJ-B2", State: provider.StateRunning},
	}
	require.NotNil(t, m.liveFor("PROJ-B2"), "an exact key match always counts")
	require.Nil(t, m.liveFor("PROJ-B"), "PROJ-B2 has no branch of its own; it must not mark PROJ-B")
}

func goldenHome(w, h int) Model {
	m := homeModel()
	m.width, m.height = w, h
	m.info = provider.ServerInfo{Version: "9.6.4"}
	m.user = provider.User{Name: "jdoe"}
	return m
}

func TestHomeGolden120x30(t *testing.T) { requireGolden(t, "home-120x30", goldenHome(120, 30).View()) }
func TestHomeGolden90x30(t *testing.T)  { requireGolden(t, "home-90x30", goldenHome(90, 30).View()) }
func TestHomeGolden80x24(t *testing.T)  { requireGolden(t, "home-80x24", goldenHome(80, 24).View()) }

func TestHomeNeverExceedsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{120, 30}, {90, 30}, {80, 24}, {60, 20}} {
		v := goldenHome(size[0], size[1]).View()
		lines := strings.Split(v, "\n")
		require.LessOrEqual(t, len(lines), size[1])
		for i, l := range lines {
			require.LessOrEqual(t, lipgloss.Width(l), size[0], "line %d at %dx%d", i, size[0], size[1])
		}
	}
}

func presetModel() Model {
	m := homeModel()
	m, _ = send(m, presetsLoadedMsg{Gen: m.presetsGen, Targets: []app.TargetInfo{
		{Name: "smoke", Plan: "PROJ-PROV", Branch: "develop", Server: "lab"},
		{Name: "build", Plan: "PROJ-BUILD", Server: "lab"},
		{Name: "orphan", Plan: "LAB-X", Server: "lab"},
	}})
	next, _ := m.showPresets()
	return next.(Model)
}

func TestShowPresetsSwitchesTheTable(t *testing.T) {
	m := presetModel()
	require.Equal(t, homePresets, m.home.view)
	require.Equal(t, focusPresets, m.focus)
	v := m.View()
	require.Contains(t, v, "TARGET")
	require.Contains(t, v, "presets(all)[3]")
	next, _ := m.showPlans()
	require.Contains(t, next.(Model).View(), "plans(all)[4]")
}

func TestPresetsSortByTargetAndState(t *testing.T) {
	m := presetModel()
	names := func() []string {
		out := []string{}
		for _, t := range m.presets.rows() {
			out = append(out, t.Name)
		}
		return out
	}
	require.Equal(t, []string{"build", "orphan", "smoke"}, names())
	m, _ = send(m, mkKey("s")) // plan
	m, _ = send(m, mkKey("s")) // state: failed PROJ-BUILD, success PROJ-PROV, no build LAB-X
	require.Equal(t, []string{"build", "smoke", "orphan"}, names())
}

func TestPresetRowTakesItsPlansState(t *testing.T) {
	m := presetModel()
	require.NotNil(t, m.lastBuildOf("PROJ-BUILD"))
	require.Nil(t, m.lastBuildOf("LAB-X"))
	cells := presetCells(presetColumns(120, m.presets.rows()), app.TargetInfo{Name: "x", Plan: "LAB-X"}, nil, nil, homeNow)
	require.Contains(t, cells, "–")
}

func TestEnterOnAPresetOpensThePanels(t *testing.T) {
	m := presetModel()
	m, cmd := send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, focusBuilds, m.focus)
	require.NotNil(t, cmd)
}

func TestEscFromPanelsReturnsToThePresetsTable(t *testing.T) {
	m := presetModel()
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, mkKey("esc"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenHome, m.screen)
	require.Equal(t, homePresets, m.home.view)
	require.Equal(t, focusPresets, m.focus)
}

func TestHomeGoldenPresets120x30(t *testing.T) {
	m := presetModel()
	m.info = provider.ServerInfo{Version: "9.6.4"}
	m.user = provider.User{Name: "jdoe"}
	requireGolden(t, "home-presets-120x30", m.View())
}

func TestTheHeaderCountsOnePlanInTheSingular(t *testing.T) {
	m := homeModel()
	m.applyFilter("fail")
	require.Contains(t, m.homeHeader(), "· 1 plan ·")
	m.applyFilter("")
	require.Contains(t, m.homeHeader(), "· 4 plans ·")
}

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

func TestHomeHeaderBlockNamesBamAndBambooVersions(t *testing.T) {
	m := goldenHome(120, 30)
	m.deps.Version = "v0.3.0"
	require.Contains(t, m.View(), "bam v0.3.0 · Bamboo 9.6.4")
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
	m.home.stale, m.home.loadedAt = true, time.Time{}
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

func TestNarrowHeaderKeepsOneHintColumn(t *testing.T) {
	v := goldenHome(60, 20).View()
	require.Contains(t, v, "<enter> drill in", "between 60 and 80 columns one hint column still fits")
	for i, l := range strings.Split(v, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 60, "line %d", i)
	}
}

func TestHomeFillsTheTerminalExactly(t *testing.T) {
	for _, size := range [][2]int{{80, 19}, {80, 20}, {120, 30}, {60, 20}} {
		lines := strings.Split(goldenHome(size[0], size[1]).View(), "\n")
		require.Len(t, lines, size[1], "%dx%d", size[0], size[1])
	}
}

func TestHomeGolden80x12(t *testing.T) { requireGolden(t, "home-80x12", goldenHome(80, 12).View()) }
