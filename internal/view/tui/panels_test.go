package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

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
	m := planGolden(80, 24) // focus on Builds
	builds := strings.Split(m.planBuildRows(40, 5), "\n")
	require.Contains(t, builds[0], barStart(true), "the focused panel's cursor row is the bright bar")
	require.NotContains(t, builds[1], barStart(true))
	stages := strings.Split(m.stagesBody(46, 10), "\n")
	require.Contains(t, stages[2], barStart(false), "an unfocused panel shows where its cursor is")
	require.Equal(t, 1, strings.Count(strings.Join(stages, "\n"), barStart(false)), "one bar")

	m.focus = focusStages
	stages = strings.Split(m.stagesBody(46, 10), "\n")
	require.Contains(t, stages[2], barStart(true), "the focused Stages cursor row is the bright bar")
	builds = strings.Split(m.planBuildRows(40, 5), "\n")
	require.Contains(t, builds[0], barStart(false))
}

func TestPlanBuildRowsTruncateToTheBar(t *testing.T) {
	m := goldenModel(80, 24)
	for _, l := range strings.Split(m.planBuildRows(8, 5), "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 8, "a build row never outgrows its panel")
	}
}
