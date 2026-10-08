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
	require.NotContains(t, v, "<esc> back / close", "esc only clears a filter here")
}

func TestProjectsFooterDoesNotAdvertiseRun(t *testing.T) {
	m := projectsModel(t)
	bar := m.statusBar(120)
	require.NotContains(t, bar, "R run")
	require.Contains(t, bar, "enter plans")
}

func TestProjectCountExcludesMissing(t *testing.T) {
	m := projectsModel(t, "GONE")
	require.Equal(t, 2, m.projectCount())
	title, _ := m.homeTitle()
	require.Equal(t, "projects[2]", title)
}

func TestMissingProjectsSortLast(t *testing.T) {
	m := projectsModel(t, "GONE")
	require.Equal(t, []string{"", "OPS", "PROJ", "GONE"}, projectKeys(m))
	m, _ = send(m, mkKey("s")) // name
	require.Equal(t, []string{"", "PROJ", "OPS", "GONE"}, projectKeys(m))
	m, _ = runCmdLine(m, "sort -key")
	require.Equal(t, []string{"", "PROJ", "OPS", "GONE"}, projectKeys(m))
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
