package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/r0jjames/bam-cli/internal/app"
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
	// The list's newest build is the one running, so the header and the
	// Builds row agree with Stages.
	m.builds.items[0].State, m.builds.items[0].FinishedAt = b.State, time.Time{}
	m.builds.setItems(m.builds.items)
	requireGolden(t, "plan-estimate-80x24", m.View())
}

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

func TestHelpGolden(t *testing.T) {
	// Opened through the key, so the viewport is sized as it would be.
	m, _ := send(goldenModel(80, 24), mkKey("?"))
	requireGolden(t, "help-80x24", m.View())
}
