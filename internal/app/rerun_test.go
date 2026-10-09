package app

import (
	"context"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

func keysOf(ns []*TreeNode) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Build.Key)
	}
	return out
}

func TestOlderRunsOfAChildAreSuperseded(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSuccess
	p.History["OPS-SMOKE"] = []provider.Build{
		tb("OPS-SMOKE-52", childOf("OPS-DEPLOY-88"), provider.StateSuccess, 20),
		tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9),
	}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	smokes := tr.Root.Children[0].Children
	require.Equal(t, []string{"OPS-SMOKE-52", "OPS-SMOKE-51"}, keysOf(smokes))
	require.False(t, smokes[0].Superseded)
	require.True(t, smokes[1].Superseded)
	require.Equal(t, provider.StateSuccess, tr.State, "the earlier failure no longer fails the tree")
}

func TestSupersededSubtreeDoesNotCount(t *testing.T) {
	old := &TreeNode{PlanKey: "OPS-SMOKE", Build: provider.Build{Key: "OPS-SMOKE-51", State: provider.StateFailed}, Superseded: true,
		Children: []*TreeNode{{PlanKey: "OPS-AUDIT", Build: provider.Build{Key: "OPS-AUDIT-3", State: provider.StateRunning}}}}
	root := &TreeNode{PlanKey: "PROJ-API", Build: provider.Build{Key: "PROJ-API-142", State: provider.StateSuccess}, Children: []*TreeNode{old}}
	require.Equal(t, provider.StateSuccess, TreeState(root))
	require.Empty(t, RerunCandidates(Tree{Root: root}))
}

func TestRerunCandidatesAreTheTopmostFailures(t *testing.T) {
	st := func(key string, s provider.State, kids ...*TreeNode) *TreeNode {
		return &TreeNode{PlanKey: key, Build: provider.Build{Key: key + "-1", State: s}, Children: kids}
	}
	root := st("PROJ-API", provider.StateSuccess,
		st("OPS-DEPLOY", provider.StateFailed, st("OPS-SMOKE", provider.StateFailed)),
		st("PROJ-DOCS", provider.StateStopped),
		st("PROJ-LINT", provider.StateSkipped, st("PROJ-FMT", provider.StateFailed)),
		st("PROJ-SLOW", provider.StateRunning, st("PROJ-SLOWER", provider.StateFailed)),
		&TreeNode{PlanKey: "OPS-AUDIT", NotRun: true},
	)
	loop := st("PROJ-LOOP", provider.StateFailed)
	loop.Loop = true
	root.Children = append(root.Children, loop)
	require.Equal(t, []string{"OPS-DEPLOY-1", "PROJ-DOCS-1", "PROJ-FMT-1"}, keysOf(RerunCandidates(Tree{Root: root})),
		"below a failure, below a running build, not-run and loop nodes are left out; a skipped parent is fine")

	failedRoot := st("PROJ-API", provider.StateFailed, st("OPS-DEPLOY", provider.StateFailed))
	require.Equal(t, []string{"PROJ-API-1"}, keysOf(RerunCandidates(Tree{Root: failedRoot})), "the root can be a candidate")
}

func TestRerunTreeRestartsInOrderAndGoesOnAfterAnError(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{}
	restartable(p, "OPS-DEPLOY-88", provider.StateSuccess)
	restartable(p, "PROJ-DOCS-30", provider.StateSuccess)
	p.RestartErrs = map[string]error{"OPS-DEPLOY-88": errs.Bamboof("Bamboo answered 409")}
	p.Sequences["OPS-SMOKE-51"] = []provider.Build{{Key: "OPS-SMOKE-51", State: provider.StateRunning}}
	svc := treeService(t, p)
	cands := []*TreeNode{
		{Build: provider.Build{Key: "OPS-DEPLOY-88"}},
		{Build: provider.Build{Key: "OPS-SMOKE-51"}},
		{Build: provider.Build{Key: "PROJ-DOCS-30"}},
	}
	res := svc.RerunTree(context.Background(), cands)
	require.Len(t, res, 3)
	require.Equal(t, "OPS-DEPLOY-88", res[0].Key)
	require.Error(t, res[0].Err)
	require.False(t, res[1].Restarted, "running: nothing to restart")
	require.NoError(t, res[1].Err)
	require.True(t, res[2].Restarted)
	require.Equal(t, []string{"OPS-DEPLOY-88", "PROJ-DOCS-30"}, p.Restarted, "the refused one was still asked")
}
