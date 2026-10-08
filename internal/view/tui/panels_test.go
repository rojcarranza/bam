package tui

import (
	"strings"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// TestPlanRowsShowTheNameWhenItFits: keys alone differ by a letter or two,
// so a row names its plan once the panel has room for it.
func TestPlanRowsShowTheNameWhenItFits(t *testing.T) {
	m := goldenModel(160, 40) // a 40-column left panel
	require.Contains(t, m.planRows(40, 10), "PROJ-BUILD  Build and test")

	narrow := goldenModel(80, 24) // a 20-column left panel: the key alone
	rows := narrow.planRows(20, 10)
	require.Contains(t, rows, "PROJ-BUILD")
	require.NotContains(t, rows, "Build")
}

func TestPlanRowsWithoutANameShowTheKey(t *testing.T) {
	m := goldenModel(160, 40)
	m.plans.setItems([]provider.Plan{{Key: "PROJ-BARE", ProjectKey: "PROJ"}})
	// The cursor row is a bar padded to the panel width, so trim the padding.
	require.Equal(t, "▸ PROJ-BARE", strings.TrimRight(m.planRows(40, 10), " "))
}

// TestBuildsPanelNamesItsPlan: after enter on a plan nothing else on screen
// said which plan the builds belong to.
func TestBuildsPanelNamesItsPlan(t *testing.T) {
	m := goldenModel(120, 40)
	m.buildsPlan = "PROJ-BUILD"
	require.Contains(t, m.View(), "2 Builds · Build and test")

	// A branch plan is named after its master plan.
	m.buildsPlan = "PROJ-BUILD12"
	require.Contains(t, m.View(), "2 Builds · Build and test")

	// A plan outside the plans load has no name to show.
	m.buildsPlan = "LAB-OTHER"
	require.Contains(t, m.View(), "2 Builds─")
}

// TestBuildsTitleFollowsTheDrill: enter clears the Builds panel at once, so
// its title must name the plan being loaded, not the one being left, even
// when the load never answers.
func TestBuildsTitleFollowsTheDrill(t *testing.T) {
	m := goldenModel(120, 40)
	m.svc = testService()
	m.plans.cursor = 2 // PROJ-DEPLOY, after OPS-NIGHTLY and PROJ-BUILD
	p, _ := m.plans.selected()
	require.Equal(t, "PROJ-DEPLOY", p.Key)

	m, _ = send(m, mkKey("enter"))
	require.Equal(t, focusBuilds, m.focus)
	require.Contains(t, m.View(), "2 Builds · Deploy")
}

func TestMainPanelNamesTheBuildsPlan(t *testing.T) {
	m := goldenModel(120, 40)
	m.focus = focusBuilds
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Build and test")

	m.focus = focusMain
	m.detail = ptr(sampleBuild())
	m.expanded = defaultExpanded(sampleBuild())
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Build and test")
}

// TestMainPanelShowsThePlanUnderTheCursor: moving the Plans cursor loads
// nothing, so the main panel says which plan enter would open rather than
// describing a build from the plan the user is leaving.
func TestMainPanelShowsThePlanUnderTheCursor(t *testing.T) {
	m := goldenModel(120, 40)
	m.plans.setItems([]provider.Plan{{
		Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ",
		LastBuild: &provider.BuildSummary{Number: 44, State: provider.StateFailed,
			FinishedAt: goldenNow.Add(-3 * 60e9)},
	}})
	v := m.View()
	require.Contains(t, v, "PROJ-BUILD · Build and test")
	require.Contains(t, v, "last build ✗ failed #44 3m ago")
	require.Contains(t, v, "press enter to show its builds")
	require.NotContains(t, v, "press enter to open this build")

	// An open build stays on screen while the user looks for another plan.
	m.detail = ptr(sampleBuild())
	m.expanded = defaultExpanded(sampleBuild())
	require.NotContains(t, m.View(), "press enter to show its builds")
}

func TestPlanNameOf(t *testing.T) {
	m := goldenModel(80, 24)
	m.plans.setItems([]provider.Plan{
		{Key: "PROJ-BUILD", Name: "Build and test"},
		{Key: "PROJ-BUILD1", Name: "Build one"},
	})
	require.Equal(t, "Build and test", m.planNameOf("PROJ-BUILD"))
	require.Equal(t, "Build one", m.planNameOf("PROJ-BUILD1"), "an exact key beats a branch match")
	require.Equal(t, "Build and test", m.planNameOf("PROJ-BUILD23"), "PROJ-BUILD23 does not start with PROJ-BUILD1")
	require.Equal(t, "Build one", m.planNameOf("PROJ-BUILD15"), "both masters match; the longer key is the closer one")
	require.Equal(t, "", m.planNameOf("PROJ-OTHER"))
	require.Equal(t, "", m.planNameOf(""))
	require.Equal(t, "", m.planNameOf("PROJ-BUILDX"), "a branch suffix is digits only")
}

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
