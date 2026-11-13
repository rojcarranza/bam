package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// Intent (BAM-42 P5, the captain's answer "every pipeline and every stage"):
// a pin shows the pipelines its build started, the tree of the build running
// now else of the newest one, and a refresh reads only what runs.

// stages gives b stages named names; the one at index run is in state st and
// holds one job, the earlier ones are done and the later ones not started.
func stages(b provider.Build, run int, st provider.State, names ...string) provider.Build {
	for i, n := range names {
		s := provider.StateNotBuilt
		switch {
		case i < run:
			s = provider.StateSuccess
		case i == run:
			s = st
		}
		b.Stages = append(b.Stages, provider.Stage{Name: n, State: s, Jobs: []provider.Job{{Key: fmt.Sprintf("%s-J%d-%d", b.PlanKey, i, b.Number), Name: n, State: s}}})
	}
	return b
}

// pinTreeFake is a pinned plan PROJ-TOP whose build 9 runs and started a
// child on a plan branch (PROJ-KID0-4) and one by its reason (PROJ-DOCS-3),
// in a project with 20 other plans. Build 8 of PROJ-TOP is the newest
// finished one.
func pinTreeFake() *fake.Provider {
	p := &fake.Provider{BaseURL: "https://bamboo.example.com", Plans: map[string][]provider.Plan{}, History: map[string][]provider.Build{}, Logs: map[string][]string{}}
	plans := []provider.Plan{{Key: "PROJ-TOP", Name: "Top", ProjectKey: "PROJ"}, {Key: "PROJ-KID0", Name: "Kid", ProjectKey: "PROJ"}, {Key: "PROJ-DOCS", Name: "Docs", ProjectKey: "PROJ"}}
	for i := range 20 {
		k := fmt.Sprintf("PROJ-N%d", i)
		plans = append(plans, provider.Plan{Key: k, Name: k, ProjectKey: "PROJ"})
		p.History[k] = []provider.Build{tb(k+"-1", "Scheduled", provider.StateSuccess, -500)}
	}
	p.Plans["PROJ"] = plans
	top := stages(tb("PROJ-TOP-9", "Manual run by jdoe", provider.StateRunning, 0), 1, provider.StateRunning, "Build", "Run integration builds", "Deploy", "Smoke", "Notify")
	p.History["PROJ-TOP"] = []provider.Build{top, tb("PROJ-TOP-8", "Manual run by jdoe", provider.StateSuccess, -100)}
	kid := stages(tb("PROJ-KID0-4", "Manual run by jdoe", provider.StateRunning, 2), 0, provider.StateRunning, "Provision")
	kid.Branch, kid.PlanName = "feature-x", "Kid"
	p.History["PROJ-KID0"] = []provider.Build{kid}
	p.History["PROJ-DOCS"] = []provider.Build{stages(tb("PROJ-DOCS-3", childOf("PROJ-TOP-9"), provider.StateRunning, 3), 0, provider.StateRunning, "Publish")}
	// the job of the "Run integration builds" stage links the branch build
	p.Logs["PROJ-TOP-J1-9"] = []string{"started https://bamboo.example.com/browse/PROJ-KID0-4"}
	return p
}

func pinTreeService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Projects = []string{"PROJ"}
	svc.Trees = &TreeStore{Dir: t.TempDir()}
	return svc
}

func boardWithTrees(svc *Service) context.Context { return WithPinTrees(context.Background()) }

func TestPinShowsTheTreeOfItsRunningBuildNested(t *testing.T) {
	svc := pinTreeService(t, pinTreeFake())
	b, err := svc.Board(boardWithTrees(svc), pins("g", "PROJ-TOP"))
	require.NoError(t, err)
	pin := b.Groups[0].Pins[0]
	require.True(t, pin.HasChildren())
	require.Equal(t, "PROJ-TOP-9(PROJ-DOCS-3,PROJ-KID0-4)", shape(pin.Tree.Root), "the running build 9, not the finished 8; the branch build is found through the job log")
	var kid *TreeNode
	pin.Tree.Root.Walk(func(n *TreeNode, _ int) {
		if n.Build.Key == "PROJ-KID0-4" {
			kid = n
		}
	})
	require.Equal(t, "feature-x", kid.Build.Branch)
}

func TestPinWithoutChildrenKeepsItsSingleRow(t *testing.T) {
	p := pinTreeFake()
	p.History["PROJ-TOP"] = p.History["PROJ-TOP"][1:] // only the finished build 8
	svc := pinTreeService(t, p)
	b, err := svc.Board(boardWithTrees(svc), pins("g", "PROJ-TOP"))
	require.NoError(t, err)
	require.False(t, b.Groups[0].Pins[0].HasChildren())
}

func TestBoardReadsNoTreeUnlessAsked(t *testing.T) {
	p := pinTreeFake()
	svc := pinTreeService(t, p)
	b, err := svc.Board(context.Background(), pins("g", "PROJ-TOP"))
	require.NoError(t, err)
	require.Nil(t, b.Groups[0].Pins[0].Tree)
	require.Zero(t, p.ListCalls("PROJ-N0"), "bam top --once without --tree lists no plan")
}

func TestStageProgress(t *testing.T) {
	run := stages(tb("PROJ-TOP-9", "", provider.StateRunning, 0), 2, provider.StateRunning, "A", "B", "Run integration builds", "D", "E")
	require.Equal(t, "3/5 Run integration builds", StageProgress(run))
	failed := stages(tb("PROJ-TOP-9", "", provider.StateFailed, 0), 3, provider.StateFailed, "A", "B", "C", "Smoke")
	require.Equal(t, "4/4 Smoke", StageProgress(failed))
	require.Empty(t, StageProgress(stages(tb("PROJ-TOP-9", "", provider.StateSuccess, 0), 0, provider.StateSuccess, "A")))
	require.Empty(t, StageProgress(tb("PROJ-TOP-9", "", provider.StateRunning, 0)), "no stages read")
}

func TestAFailedChildPipelineIsOneNeedsYouLine(t *testing.T) {
	p := pinTreeFake()
	docs := tb("PROJ-DOCS-3", childOf("PROJ-TOP-9"), provider.StateFailed, 3)
	p.History["PROJ-DOCS"] = []provider.Build{docs}
	svc := pinTreeService(t, p)
	b, err := svc.Board(boardWithTrees(svc), pins("g", "PROJ-TOP"))
	require.NoError(t, err)
	var got []Need
	for _, n := range b.Needs {
		if n.Kind == "pin_child_failed" {
			got = append(got, n)
		}
	}
	require.Equal(t, []Need{{Kind: "pin_child_failed", Text: "Top: the child pipeline PROJ-DOCS-3 failed", Build: "PROJ-DOCS-3"}}, got)
}

// A full refresh of a running tree asks once for each running pipeline and
// once for each running job's log, and lists no plan; the pin's own build
// ending lists them again, and so does five minutes with a child running.
func TestARunningPinTreeRefreshReadsOnlyWhatRuns(t *testing.T) {
	p := pinTreeFake()
	svc := pinTreeService(t, p)
	ctx := context.Background()
	root := p.History["PROJ-TOP"][0]
	require.NotNil(t, svc.PinTree(ctx, root))

	counts := func() (gets, logs, lists int) {
		for _, k := range []string{"PROJ-TOP-9", "PROJ-DOCS-3", "PROJ-KID0-4"} {
			gets += p.GetCalls(k)
		}
		for _, k := range []string{"PROJ-TOP-J1-9", "PROJ-DOCS-J0-3", "PROJ-KID0-J0-4"} {
			logs += p.LogCalls(k)
		}
		for _, pl := range p.Plans["PROJ"] {
			lists += p.ListCalls(pl.Key)
		}
		return
	}
	g0, l0, p0 := counts()
	tr := svc.PinTree(ctx, root)
	require.NotNil(t, tr)
	g1, l1, p1 := counts()
	require.Equal(t, 3, g1-g0, "one read per running pipeline")
	require.Equal(t, 3, l1-l0, "one read per running job")
	require.Zero(t, p1-p0, "no plan is listed")

	svc.Clock.(*fakeClock).After(pinRescanEvery)
	svc.PinTree(ctx, root)
	_, _, p2 := counts()
	require.Equal(t, 23, p2-p1, "every 5 minutes the plans are read again")

	ended := root
	ended.State = provider.StateSuccess
	svc.PinTree(ctx, ended)
	_, _, p3 := counts()
	require.Equal(t, 23, p3-p2, "when the pin's own build ends the plans are read again")
}

func TestAFinishedPinTreeIsDrawnFromTheSavedOne(t *testing.T) {
	p := pinTreeFake()
	for _, k := range []string{"PROJ-TOP", "PROJ-DOCS", "PROJ-KID0"} {
		for i := range p.History[k] {
			p.History[k][i].State = provider.StateSuccess
			p.History[k][i].FinishedAt = treeT0.Add(-time.Hour)
		}
	}
	p.History["PROJ-TOP"][0].Stages = nil
	svc := pinTreeService(t, p)
	ctx := context.Background()
	first := svc.PinTree(ctx, p.History["PROJ-TOP"][0])
	require.NotNil(t, first)
	require.True(t, TreeFinal(*first))
	lists := 0
	for _, pl := range p.Plans["PROJ"] {
		lists += p.ListCalls(pl.Key)
	}
	second := svc.PinTree(ctx, p.History["PROJ-TOP"][0])
	again := 0
	for _, pl := range p.Plans["PROJ"] {
		again += p.ListCalls(pl.Key)
	}
	require.Equal(t, lists, again, "no request for a tree bam saved")
	require.Equal(t, shape(first.Root), shape(second.Root))
}
