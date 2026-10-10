package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var goldenNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

// goldenModel is the one fixture every golden renders, so a diff in one
// golden means a deliberate change to that screen and nothing else.
func goldenModel(w, h int) Model {
	m := New(Deps{Servers: []Server{{Alias: "lab", URL: labOrigin}}, Initial: "lab"})
	m.width, m.height = w, h
	m.info = provider.ServerInfo{Version: "9.6.4"}
	m.user = provider.User{Name: "jdoe"}
	m.plans.setItems([]provider.Plan{
		{Key: "PROJ-BUILD", Name: "Build and test", ProjectKey: "PROJ"},
		{Key: "PROJ-DEPLOY", Name: "Deploy", ProjectKey: "PROJ"},
		{Key: "OPS-NIGHTLY", Name: "Nightly", ProjectKey: "OPS"},
	})
	m.builds.setItems([]provider.Build{
		{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, State: provider.StateFailed, Branch: "main",
			FinishedAt: goldenNow.Add(-3 * time.Minute), Duration: 192 * time.Second},
		{Key: "PROJ-BUILD-43", PlanKey: "PROJ-BUILD", Number: 43, State: provider.StateSuccess, Branch: "main",
			FinishedAt: goldenNow.Add(-time.Hour), Duration: 52 * time.Second},
	})
	m.buildsPlan = "PROJ-BUILD"
	m.now = func() time.Time { return goldenNow }
	m.screen = screenPlan // the v0.2 tests start on the plan screen; Home has its own tests
	m.focus = focusBuilds
	return m
}

func requireGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run: go test ./internal/view/tui/ -update")
	require.Equal(t, string(want), got)
}

// TestColumnsGolden80x24 is the smallest supported size and the screen the
// spec draws.
func TestColumnsGolden80x24(t *testing.T) {
	requireGolden(t, "columns-80x24", goldenModel(80, 24).View())
}

func TestColumnsGolden120x40(t *testing.T) {
	requireGolden(t, "columns-120x40", goldenModel(120, 40).View())
}

// TestColumnsGolden60x20 proves the layout degrades rather than panics below
// the supported size.
func TestColumnsGolden60x20(t *testing.T) {
	requireGolden(t, "columns-60x20", goldenModel(60, 20).View())
}

// TestViewNeverExceedsTheTerminal: a line wider than the terminal wraps and
// destroys the layout, so no rendered line may exceed the width.
func TestViewNeverExceedsTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {60, 20}} {
		v := goldenModel(size[0], size[1]).View()
		for i, line := range strings.Split(v, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), size[0], "line %d at %dx%d", i, size[0], size[1])
		}
		require.LessOrEqual(t, len(strings.Split(v, "\n")), size[1], "too many rows at %dx%d", size[0], size[1])
	}
}

// TestFocusedPanelIsMarked so the user can see where the keys go.
func TestFocusedPanelIsMarked(t *testing.T) {
	m := goldenModel(80, 24)
	first := m.View()
	m.focus = focusStages
	require.NotEqual(t, first, m.View(), "moving focus must change the rendering")
}

// TestZeroSizeDoesNotPanic: bubbletea sends the first WindowSizeMsg after
// the first View, so View runs at 0x0 at least once.
func TestZeroSizeDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() { _ = New(Deps{}).View() })
}

// TestDetailGolden is the screen spec §3 draws.
func TestDetailGolden(t *testing.T) {
	m := goldenModel(80, 24)
	m.focus = focusStages
	m.detail = ptr(sampleBuild())
	m.expanded = defaultExpanded(sampleBuild())
	requireGolden(t, "detail-80x24", m.View())
}

func TestHelpGolden(t *testing.T) {
	// Opened through the key, so the viewport is sized as it would be.
	m, _ := send(goldenModel(80, 24), mkKey("?"))
	requireGolden(t, "help-80x24", m.View())
}

// TestDetailWithEstimateGolden pins the bar in the detail header.
func TestDetailWithEstimateGolden(t *testing.T) {
	m := goldenModel(80, 24)
	m.focus = focusStages
	m.detail = ptr(runningDetail())
	m.expanded = defaultExpanded(runningDetail())
	m.progress = runningEstimate()
	requireGolden(t, "detail-estimate-80x24", m.View())
}
