package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P5, the captain's answer "every pipeline and every stage"):
// under a pinned pipeline the live board draws the pipelines its build
// started as bam build tree does, with the stage of a running one; space
// folds it, j k move over it, enter and t open a child; a short panel folds
// finished all-success trees first, then cuts the longest.

func treeBuild(key, plan string, n int, st provider.State, runStage int, names ...string) provider.Build {
	b := provider.Build{Key: key, PlanKey: plan, Number: n, State: st, StartedAt: goldenNow.Add(-5 * time.Minute), PlanName: plan}
	for i, name := range names {
		s := provider.StateSuccess
		switch {
		case i == runStage:
			s = st
		case i > runStage:
			s = provider.StateNotBuilt
		}
		b.Stages = append(b.Stages, provider.Stage{Name: name, State: s, Duration: time.Minute})
	}
	return b
}

// parentTree is a running parent with two children, one of them on a plan
// branch and itself running a grandchild: 3 pipelines below the pin's row.
func parentTree() *app.Tree {
	root := treeBuild("PROJ-TOP-9", "PROJ-TOP", 9, provider.StateRunning, 2, "Build", "Check", "Run integration builds", "Deploy", "Notify")
	k8s := treeBuild("PROJ-K8S0-4", "PROJ-K8S0", 4, provider.StateRunning, 0, "Provision")
	k8s.Branch, k8s.PlanName = "feature-x", "Kubernetes"
	docs := treeBuild("PROJ-DOCS-3", "PROJ-DOCS", 3, provider.StateSuccess, 0, "Publish")
	docs.StartedAt = goldenNow.Add(-4 * time.Minute)
	docs.Duration = time.Minute
	sub := treeBuild("PROJ-SUB-1", "PROJ-SUB", 1, provider.StateFailed, 0, "Smoke")
	t := &app.Tree{State: provider.StateRunning, Root: &app.TreeNode{PlanKey: "PROJ-TOP", Build: root, Children: []*app.TreeNode{
		{PlanKey: "PROJ-DOCS", Build: docs},
		{PlanKey: "PROJ-K8S0", Build: k8s, FromLog: true, Stage: "Run integration builds", Children: []*app.TreeNode{{PlanKey: "PROJ-SUB", Build: sub}}},
	}}}
	return t
}

func okTree(key string, kids int) (provider.Build, *app.Tree) {
	root := treeBuild(key, "PROJ-DONE", 5, provider.StateSuccess, 0, "Run")
	root.FinishedAt = goldenNow.Add(-time.Hour)
	t := &app.Tree{State: provider.StateSuccess, Root: &app.TreeNode{PlanKey: "PROJ-DONE", Build: root}}
	for i := range kids {
		b := treeBuild("PROJ-OK"+string(rune('A'+i))+"-1", "PROJ-OK"+string(rune('A'+i)), 1, provider.StateSuccess, 0, "Run")
		b.FinishedAt = goldenNow.Add(-time.Hour)
		t.Root.Children = append(t.Root.Children, &app.TreeNode{PlanKey: b.PlanKey, Build: b})
	}
	return root, t
}

func treeBoardModel(w, h int, mod func(*app.Board)) Model {
	m := boardModel(w, h)
	data := sampleBoard(goldenNow)
	t := parentTree()
	running := t.Root.Build
	data.Groups = []app.PinGroupView{{Name: "integration", Pins: []app.PinView{
		{Pin: "PROJ-TOP", PlanKey: "PROJ-TOP", Name: "Top pipeline", Current: &running, Tree: t, Usual: 10 * time.Minute, Last: 9 * time.Minute,
			Results: []provider.Build{{Key: "PROJ-TOP-8", PlanKey: "PROJ-TOP", Number: 8, State: provider.StateSuccess}}},
		{Pin: "PROJ-BUILD", PlanKey: "PROJ-BUILD", Name: "Build and test", Results: []provider.Build{{Key: "PROJ-BUILD-44", PlanKey: "PROJ-BUILD", Number: 44, State: provider.StateSuccess}}},
	}}}
	if mod != nil {
		mod(&data)
	}
	data.Needs = app.NeedsYou(data, goldenNow)
	m.board.loadGen++
	m, _ = send(m, boardLoadedMsg{Gen: m.board.loadGen, Full: true, Board: data})
	return m
}

func plain(m Model) string { return ansi.Strip(m.View()) }

func TestBoardDrawsAPinsTreeWithPipelinesAndStages(t *testing.T) {
	for _, size := range [][2]int{{120, 45}, {200, 50}} {
		v := plain(treeBoardModel(size[0], size[1], nil))
		for _, want := range []string{
			"Top pipeline", "· 3/5 Run integration b…", // the pin's row names the stage
			"└─ PROJ-DOCS-3", "└─ ✓ stage Publish",
			"PROJ-K8S0-4 (Kubernetes · feature-x)", "stage Provision", "PROJ-SUB-1", "1/1 Provision", "stage Smoke",
		} {
			require.Contains(t, v, want, "%v", size)
		}
		require.Equal(t, 1, strings.Count(v, "Build and test"), "the pin without children has its one row")
	}
}

func TestBoardGoldensWithATree(t *testing.T) {
	requireGolden(t, "board-pintree-120x45", treeBoardModel(120, 45, nil).View())
	requireGolden(t, "board-pintree-200x50", treeBoardModel(200, 50, nil).View())
}

func TestBoardPinWithoutChildrenKeepsOneRow(t *testing.T) {
	v := plain(treeBoardModel(120, 45, func(b *app.Board) { b.Groups[0].Pins[0].Tree = nil }))
	require.NotContains(t, v, "PROJ-DOCS-3")
	require.NotContains(t, v, "stage ")
	require.Contains(t, v, "Top pipeline")
}

func TestBoardSpaceFoldsAndUnfoldsATree(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 45, nil))
	m, _ = send(m, mkKey("j")) // PROJ-DOCS-3
	m, _ = send(m, mkKey("j")) // PROJ-K8S0-4
	require.Equal(t, 2, m.board.cursor[boardPins])
	m, _ = send(m, mkKey(" "))
	v := plain(m)
	require.Contains(t, v, "▸ 3 child pipelines · space opens")
	require.NotContains(t, v, "PROJ-DOCS-3")
	require.Zero(t, m.board.cursor[boardPins], "the cursor goes back to the pin whose tree was folded")
	m, _ = send(m, mkKey("j"))
	require.Equal(t, 1, m.board.cursor[boardPins], "the next stop is the next pin, since the tree is folded")
	m, _ = send(m, mkKey("k"))
	m, _ = send(m, mkKey(" "))
	require.Contains(t, plain(m), "PROJ-DOCS-3")
}

func TestBoardEnterOpensAChildBuildAndTOpensItsTree(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 45, nil))
	m, _ = send(m, mkKey("j")) // PROJ-K8S0-4, the first pipeline of the tree
	require.Equal(t, "https://bamboo.lab.example/browse/PROJ-K8S0-4", m.boardSelectionURL(), "o and y act on the child")
	opened, cmd := send(m, mkKey("enter"))
	require.NotNil(t, cmd)
	require.Equal(t, screenPlan, opened.screen)
	require.Equal(t, "PROJ-K8S0", opened.plan.key)
	require.True(t, opened.fromBoard)

	opened, _ = send(m, mkKey("t"))
	require.Equal(t, screenTree, opened.screen)
	require.Equal(t, "PROJ-K8S0-4", opened.buildTree.root)

	m, _ = send(m, mkKey("P"))
	_, isPin := m.boardPin()
	require.False(t, isPin, "a child line is not a pin: P does not unpin it")
}

func TestBoardTOnAPinOpensItsTreeScreen(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 45, nil))
	m, _ = send(m, mkKey("t"))
	require.Equal(t, screenTree, m.screen)
	require.Equal(t, "PROJ-TOP-9", m.buildTree.root)
}

func TestShortPinnedFoldsFinishedTreesThenCutsTheLongest(t *testing.T) {
	doneRoot, doneTree := okTree("PROJ-DONE-5", 4)
	mod := func(b *app.Board) {
		b.Groups[0].Pins = append(b.Groups[0].Pins, app.PinView{Pin: "PROJ-DONE", PlanKey: "PROJ-DONE", Name: "Finished one",
			Results: []provider.Build{doneRoot}, Tree: doneTree})
	}
	// Roomy: everything fits, so nothing folds.
	tall := plain(treeBoardModel(120, 60, mod))
	require.Contains(t, tall, "PROJ-OKA-1")
	require.NotContains(t, tall, "space opens")

	// Pinned of 20 rows (4 spent on frame): the finished tree folds first.
	m := treeBoardModel(120, 40, mod)
	lines, _ := m.pinLines(100, false, 16)
	text := ansi.Strip(strings.Join(lines, "\n"))
	require.Contains(t, text, "▸ 4 child pipelines · space opens", "finished and all succeeded: folded first")
	require.Contains(t, text, "PROJ-K8S0-4", "the running tree is still open")
	require.NotContains(t, text, "more · t opens")

	// Shorter still: the longest tree is cut, and says so.
	lines, _ = m.pinLines(100, false, 9)
	require.LessOrEqual(t, len(lines), 9)
	text = ansi.Strip(strings.Join(lines, "\n"))
	require.Regexp(t, `\+\d+ more · t opens the tree`, text)
	require.Contains(t, text, "▸ 4 child pipelines", "the finished tree stays folded")
}

func TestCutTreeKeepsTheLineHoldingTheCursor(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 40, nil))
	m.board.cursor[boardPins] = 3 // PROJ-DOCS-3, the last line of the tree
	lines, cur := m.pinLines(100, false, 5)
	require.Contains(t, ansi.Strip(lines[cur]), "PROJ-DOCS-3", "the tree is cut below the cursor, never above it")
	m.board.cursor[boardPins] = 0
	lines, cur = m.pinLines(100, false, 5)
	require.Contains(t, ansi.Strip(lines[cur]), "Top pipeline")
	require.Regexp(t, `\+\d+ more`, ansi.Strip(strings.Join(lines, "\n")))
}

// Intent (BAM-42 P5 with the pin's branch rows): a pin that has a tree and
// branches draws the tree, then its branches, and the cursor reaches every
// line in the order it is drawn.
func TestBoardPinWithATreeAndBranchesKeepsBothInOrder(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 45, func(b *app.Board) {
		br := app.PinView{Pin: "PROJ-TOP", PlanKey: "PROJ-TOP0", Branch: "feature-y", Name: "Top pipeline",
			Results: []provider.Build{{Key: "PROJ-TOP0-2", PlanKey: "PROJ-TOP0", Number: 2, State: provider.StateSuccess}}}
		b.Groups[0].Pins[0].Branches = []app.PinView{br}
		b.Groups[0].Pins[0].BranchesMore = 2
	}))
	out := plain(m)
	tree, branch, more := strings.Index(out, "Smoke"), strings.Index(out, "Top pipeline@"), strings.Index(out, "+2 more branches")
	require.Greater(t, tree, 0)
	require.Greater(t, branch, tree, "the branch comes after the tree")
	require.Greater(t, more, branch)
	for range 4 { // DOCS, K8S0, SUB, then the branch row
		m, _ = send(m, mkKey("j"))
	}
	pin, ok := m.boardPin()
	require.True(t, ok)
	require.Equal(t, "PROJ-TOP0", pin.PlanKey, "the fourth line below the pin is its branch")
}

// Intent (review of this change): folding a pin's tree leaves its branch rows
// drawn, so the cursor stops on each of them and the keys act on the line
// marked.
func TestFoldedPinKeepsItsBranchRowsAddressable(t *testing.T) {
	m := pinCursor(treeBoardModel(120, 45, func(b *app.Board) {
		br := app.PinView{Pin: "PROJ-TOP", PlanKey: "PROJ-TOP0", Branch: "feature-y", Name: "Top pipeline",
			Results: []provider.Build{{Key: "PROJ-TOP0-2", PlanKey: "PROJ-TOP0", Number: 2, State: provider.StateSuccess}}}
		b.Groups[0].Pins[0].Branches = []app.PinView{br}
	}))
	m, _ = send(m, mkKey("j"))
	m, _ = send(m, mkKey(" "))
	pin, ok := m.boardPin()
	require.True(t, ok)
	require.Equal(t, "PROJ-TOP", pin.PlanKey, "folding from a child line puts the cursor on the pin itself")
	m, _ = send(m, mkKey("j"))
	pin, ok = m.boardPin()
	require.True(t, ok)
	require.Equal(t, "PROJ-TOP0", pin.PlanKey, "the next line is the branch row")
	require.Contains(t, plain(m), "▸   Top pipeline@", "and it is the line marked")
	m, _ = send(m, mkKey("j"))
	pin, _ = m.boardPin()
	require.Equal(t, "PROJ-BUILD", pin.PlanKey, "then the next pin")
}

// Intent (review of this change): the stage of a tree belongs to its own
// build; a queued build after a finished one does not wear the finished
// build's stage, and a long stage name is cut so it does not widen every row.
func TestPinNowShowsAStageOnlyForItsOwnBuild(t *testing.T) {
	m := treeBoardModel(120, 45, nil)
	pin := m.board.data.Groups[0].Pins[0]
	require.Contains(t, m.pinNow(pin), "3/5 Run integration b…", "a long stage name is cut")
	queued := provider.Build{Key: "PROJ-TOP-10", PlanKey: "PROJ-TOP", Number: 10, State: provider.StateQueued}
	pin.Current = &queued
	require.NotContains(t, m.pinNow(pin), "3/5", "build 10 is not in the stage build 9 was in")
}
