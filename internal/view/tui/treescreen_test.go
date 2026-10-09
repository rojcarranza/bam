package tui

import (
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// treeTUIModel is the panels on PROJ-BUILD-44 (failed) with a failed child
// PROJ-PROV-7, the Builds panel focused.
func treeTUIModel(t *testing.T) (Model, *fake.Provider) {
	t.Helper()
	m := goldenModel(80, 24)
	m.svc = testService()
	m.svc.Clock = instantClock{}
	f := m.svc.P.(*fake.Provider)
	f.History["PROJ-PROV"] = []provider.Build{{Key: "PROJ-PROV-7", PlanKey: "PROJ-PROV", Number: 7, State: provider.StateFailed,
		Reason: "Child of PROJ-BUILD-44", Duration: 40 * time.Second, URL: labOrigin + "/browse/PROJ-PROV-7",
		Stages: []provider.Stage{{Name: "Apply", State: provider.StateFailed, Duration: 40 * time.Second,
			Jobs: []provider.Job{{Key: "PROJ-PROV-TF-7", Name: "Terraform", State: provider.StateFailed}}}}}}
	m.focus = focusBuilds
	return m, f
}

// firstTree opens the screen with t and delivers the first tree.
func firstTree(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	require.NotNil(t, cmd)
	m, _ = send(m, cmd())
	require.True(t, m.buildTree.have)
	return m
}

func TestTOpensTheTreeOfTheSelectedBuild(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	require.Equal(t, "PROJ-BUILD-44", m.buildTree.root)
	require.Equal(t, "PROJ-BUILD-44(PROJ-PROV-7)", tuiShape(m.buildTree.tree.Root))
}

func tuiShape(n *app.TreeNode) string {
	s := n.Build.Key
	if len(n.Children) == 0 {
		return s
	}
	s += "("
	for i, c := range n.Children {
		if i > 0 {
			s += ","
		}
		s += tuiShape(c)
	}
	return s + ")"
}

func TestTreeScreenGolden(t *testing.T) {
	m, _ := treeTUIModel(t)
	requireGolden(t, "tree-80x24", firstTree(t, m).View())
}

func TestTreeScreenCursorMovesOverBuildsOnly(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	n, ok := m.treeSelected()
	require.True(t, ok)
	require.Equal(t, "PROJ-BUILD-44", n.Build.Key)
	m, _ = send(m, mkKey("j"))
	n, _ = m.treeSelected()
	require.Equal(t, "PROJ-PROV-7", n.Build.Key, "the stage rows are skipped")
	m, _ = send(m, mkKey("j"))
	n, _ = m.treeSelected()
	require.Equal(t, "PROJ-PROV-7", n.Build.Key, "the last build stays selected")
}

func TestTreeScreenEnterAndBack(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenColumns, m.screen)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen, "esc from a tree's build returns to the tree")
	require.NotNil(t, m.treeCh, "and watches it again")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenColumns, m.screen, "esc from the tree returns where it was opened")
	require.Nil(t, m.treeCh, "closing the screen stops watching")
}

func TestTreeCommandOpensAnyBuild(t *testing.T) {
	m, _ := treeTUIModel(t)
	next, cmd := m.runCommandLine("tree PROJ-BUILD-44")
	m = next.(Model)
	require.Equal(t, screenTree, m.screen)
	require.NotNil(t, cmd)
	next, _ = m.runCommandLine("tree")
	require.Equal(t, screenTree, next.(Model).screen, ":tree alone uses the selected build")
}

func TestTreeScreenURLAndKeyLine(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("j"))
	require.Equal(t, labOrigin+"/browse/PROJ-PROV-7", m.selectionURL())
	require.Contains(t, m.statusBar(80), "enter build")
	require.Contains(t, m.statusBar(80), "esc back")
}

func TestTreeScreenIgnoresAStaleEvent(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	before := m.buildTree.tree
	m, _ = send(m, treeEventMsg{Gen: m.treeGen - 1, Event: app.TreeEvent{Type: app.TreeUpdate, Tree: app.Tree{Root: &app.TreeNode{}}}})
	require.Equal(t, before, m.buildTree.tree)
}
