package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// firstTree opens the screen with t and delivers events until the first
// tree; the progress before it is delivered too.
func firstTree(t *testing.T, m Model) Model {
	t.Helper()
	m, cmd := send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	return untilTree(t, m, cmd)
}

// untilTree delivers the watch's events from cmd until the first tree.
func untilTree(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for !m.buildTree.have {
		require.NotNil(t, cmd)
		m, cmd = send(m, cmd())
	}
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
	require.Equal(t, screenPlan, m.screen)
	require.Equal(t, "PROJ-PROV-7", m.detail.Key)
	m, _ = send(m, mkKey("esc")) // Stages back to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen, "esc from a tree's build returns to the tree")
	require.NotNil(t, m.treeCh, "and watches it again")
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen, "esc from the tree returns where it was opened")
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

// restartableChild makes the root succeed, so PROJ-PROV-7 is the one
// candidate, and makes PROJ-PROV-7 restart and then succeed.
func restartableChild(f *fake.Provider) {
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	child := f.History["PROJ-PROV"][0]
	queued, done := child, child
	queued.State, done.State = provider.StateQueued, provider.StateSuccess
	done.Stages = nil
	f.Sequences = map[string][]provider.Build{"PROJ-PROV-7": {child, child, queued, done}}
}

func TestTreeScreenRerunAsksThenRestartsAndWatchesAgain(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayConfirm, m.overlay)
	require.Contains(t, m.confirm.Prompt, "Rerun the failed jobs of 1 build? PROJ-PROV-7")
	m, cmd := send(m, mkKey("y"))
	require.True(t, m.buildTree.restarting["PROJ-PROV-7"])
	require.NotNil(t, cmd)
	m, cmd = send(m, cmd())
	require.Empty(t, m.buildTree.restarting)
	require.Contains(t, m.status, "restarted")
	require.NotNil(t, cmd, "a fresh watch starts")
	require.NotNil(t, m.treeCh)
}

func TestTreeScreenRerunNothingToRerun(t *testing.T) {
	m, f := treeTUIModel(t)
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	f.History["PROJ-PROV"][0].State = provider.StateSuccess
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay)
	require.Equal(t, "nothing to rerun", m.status)
}

func TestTreeScreenRerunRefusesARunningTree(t *testing.T) {
	m, f := treeTUIModel(t)
	f.History["PROJ-BUILD"][0].State = provider.StateSuccess
	f.Plans["PROJ"] = append(f.Plans["PROJ"], provider.Plan{Key: "PROJ-DOCS", ProjectKey: "PROJ"})
	f.History["PROJ-DOCS"] = []provider.Build{{Key: "PROJ-DOCS-3", PlanKey: "PROJ-DOCS", Number: 3,
		State: provider.StateRunning, Reason: "Child of PROJ-BUILD-44"}}
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay)
	require.Contains(t, m.status, "still running")
}

func TestTreeScreenRerunIgnoresASecondF(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, _ = send(m, mkKey("y"))
	m, cmd := send(m, mkKey("F"))
	require.Equal(t, overlayNone, m.overlay, "no second question while restarting")
	require.Nil(t, cmd)
}

func TestTreeScreenLateRerunResultIsDropped(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("y"))
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenPlan, m.screen)
	m, _ = send(m, cmd())
	require.Equal(t, screenPlan, m.screen, "a result after closing does not reopen the screen")
	require.Nil(t, m.treeCh)
}

// A refresh during a rerun must not lose its result: F would stay dead and
// a failed restart would never be reported.
// The rerun runs before the refresh so the refreshed watch cannot read
// PROJ-PROV-7 first: the fake answers by call count, and an earlier read
// would hand Restart the queued build. The result still lands after the
// refresh has moved the watch generation, which is what is under test.
func TestTreeScreenRerunSurvivesARefresh(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("y"))
	res := cmd()
	m, _ = send(m, mkKey("r"))
	m, _ = send(m, res)
	require.Empty(t, m.buildTree.restarting)
	require.Contains(t, m.status, "restarted 1 build")
}

// Opening PROJ-PROV-7 builds a Tree panel read of it; the test drops that
// command, so it must read nothing, or it could take the fake's failed
// answer before Restart does (#93).
func TestTreeScreenRerunSurvivesABuildVisit(t *testing.T) {
	m, f := treeTUIModel(t)
	restartableChild(f)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("F"))
	m, cmd := send(m, mkKey("y"))
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, screenPlan, m.screen)
	m, _ = send(m, cmd())
	require.Empty(t, m.buildTree.restarting, "the result lands while the build is open")
	require.Contains(t, m.status, "restarted 1 build")
	m, _ = send(m, mkKey("esc")) // Stages back to Builds
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, screenTree, m.screen)
}

// A server switch leaves the tree behind: esc from its build must not
// watch the old server's build on the new one, nor a tree with no server.
func TestTreeBuildAfterAServerSwitchDoesNotReturnToTheTree(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.fromTree)
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.Nil(t, m.svc)
	require.NotPanics(t, func() { m, _ = send(m, mkKey("esc")) })
	require.NotEqual(t, screenTree, m.screen)
	require.Nil(t, m.treeCh)
}

func TestSOnTheTreeScreenSwitchesServerAndLeavesTheTree(t *testing.T) {
	m, _ := treeTUIModel(t)
	m = firstTree(t, m)
	m, _ = send(m, mkKey("S"))
	require.Equal(t, overlayServers, m.overlay)
	next, _ := m.switchServer("lab")
	m = next.(Model)
	require.NotEqual(t, screenTree, m.screen, "a new server leaves the old server's tree")
	require.Nil(t, m.treeCh)
}

func TestLogsFromTheTreeReturnToTheTree(t *testing.T) {
	for _, k := range []string{"l", "a"} {
		m, _ := treeTUIModel(t)
		m = firstTree(t, m)
		m, _ = send(m, mkKey("j"))
		m, _ = send(m, mkKey(k))
		require.Equal(t, screenLogs, m.screen, k)
		m, _ = send(m, mkKey("esc"))
		require.Equal(t, screenTree, m.screen, "esc from logs opened on the tree returns to it (%s)", k)
		require.NotNil(t, m.treeCh, "and watches it again (%s)", k)
	}
}

func TestTreeScreenNestsALinkedBuildUnderItsStage(t *testing.T) {
	m, _ := treeTUIModel(t)
	in := &app.TreeNode{PlanKey: "LAB-INT", FromLog: true, Stage: "Run integration builds",
		Build: provider.Build{Key: "LAB-INT-46", PlanKey: "LAB-INT", PlanName: "Integrate.ForNextRelease", Number: 46, State: provider.StateSuccess}}
	root := &app.TreeNode{PlanKey: "PROJ-BUILD", Children: []*app.TreeNode{in},
		Build: provider.Build{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, State: provider.StateSuccess, Stages: []provider.Stage{
			{Name: "Run integration builds", State: provider.StateSuccess}, {Name: "Publish", State: provider.StateSuccess}}}}
	m.buildTree.tree = app.Tree{Root: root}
	lines := m.treeLines()
	require.Len(t, lines, 4)
	require.Contains(t, lines[1].text, "├─ ✓ stage Run integration builds")
	require.Contains(t, lines[2].text, "│  └─ LAB-INT-46 (Integrate.ForNextRelease)")
	require.Same(t, in, lines[2].node)
	require.NotContains(t, lines[2].text, "started by stage")
	require.Contains(t, lines[3].text, "└─ ✓ stage Publish")
}

func TestTreeScreenShowsProgressUntilTheFirstTree(t *testing.T) {
	m, _ := treeTUIModel(t)
	m, _ = send(m, mkKey("t"))
	loading := func(gen, done int) treeEventMsg {
		return treeEventMsg{Gen: gen, Event: app.TreeEvent{Type: app.TreeLoading,
			Progress: app.TreeProgress{Plans: app.TreeCount{Done: done, Total: 40}}}}
	}
	m, cmd := send(m, loading(m.treeGen, 12))
	require.NotNil(t, cmd, "the screen keeps reading the watch")
	require.False(t, m.buildTree.have)
	require.Contains(t, m.View(), "PROJ-BUILD-44 · reading builds 12/40 plans")

	m, _ = send(m, loading(m.treeGen-1, 39))
	require.Contains(t, m.View(), "12/40", "a stale watch's progress is dropped")
}
