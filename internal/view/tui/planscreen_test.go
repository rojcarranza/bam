package tui

import (
	"testing"

	"github.com/r0jjames/bam-cli/internal/app"
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
