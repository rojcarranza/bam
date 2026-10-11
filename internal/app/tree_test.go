package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

var treeT0 = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

// tb is a build of key PLAN-N with a reason, started minutes after treeT0.
func tb(key, reason string, st provider.State, minutes int) provider.Build {
	i := strings.LastIndex(key, "-")
	n, _ := strconv.Atoi(key[i+1:])
	return provider.Build{Key: key, PlanKey: key[:i], Number: n, State: st, Reason: reason,
		StartedAt: treeT0.Add(time.Duration(minutes) * time.Minute)}
}

func childOf(parent string) string { return "Child of " + parent }

// treeFake: PROJ-API-142 triggered OPS-DEPLOY-88 and PROJ-DOCS-30;
// OPS-DEPLOY-88 triggered OPS-SMOKE-51. Other builds are noise.
func treeFake() *fake.Provider {
	return &fake.Provider{
		BaseURL: "https://bamboo.example.com",
		Plans: map[string][]provider.Plan{
			"PROJ": {{Key: "PROJ-API"}, {Key: "PROJ-DOCS"}},
			"OPS":  {{Key: "OPS-DEPLOY"}, {Key: "OPS-SMOKE"}},
		},
		History: map[string][]provider.Build{
			"PROJ-API":   {tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0), tb("PROJ-API-141", "Manual run by jdoe", provider.StateSuccess, -60)},
			"PROJ-DOCS":  {tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5)},
			"OPS-DEPLOY": {tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5), tb("OPS-DEPLOY-87", childOf("PROJ-API-141"), provider.StateSuccess, -55)},
			"OPS-SMOKE":  {tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)},
		},
	}
}

func treeService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Projects = []string{"PROJ", "OPS"}
	return svc
}

// shape is a tree as KEY(child,child), not-run nodes as PLAN!, loops as KEY^.
func shape(n *TreeNode) string {
	label := n.Build.Key
	switch {
	case n.NotRun:
		return n.PlanKey + "!"
	case n.Loop:
		return label + "^"
	}
	if len(n.Children) == 0 {
		return label
	}
	parts := make([]string, len(n.Children))
	for i, c := range n.Children {
		parts[i] = shape(c)
	}
	return label + "(" + strings.Join(parts, ",") + ")"
}

func TestBuildTreeFromBuildReasons(t *testing.T) {
	svc := treeService(t, treeFake())
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Equal(t, provider.StateRunning, tr.State, "PROJ-DOCS-30 still runs")
	require.Empty(t, tr.Warnings)
}

func TestParentOf(t *testing.T) {
	p, ok := ParentOf("Child of PROJ-API-142")
	require.True(t, ok)
	require.Equal(t, "PROJ-API-142", p)
	for _, r := range []string{"Child of PROJ-API", "Manual run by jdoe", "Child of PROJ-API-142 and more", "child of PROJ-API-142"} {
		_, ok := ParentOf(r)
		require.False(t, ok, r)
	}
}

func TestBuildTreeDoesNotConfuseBuildNumbers(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"] = append(p.History["PROJ-DOCS"], tb("PROJ-DOCS-29", childOf("PROJ-API-1420"), provider.StateSuccess, 1))
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.NotContains(t, shape(tr.Root), "PROJ-DOCS-29")
}

func TestBuildTreeShowsEveryRunOfAChildNewestFirst(t *testing.T) {
	p := treeFake()
	p.History["OPS-SMOKE"] = []provider.Build{tb("OPS-SMOKE-52", childOf("OPS-DEPLOY-88"), provider.StateSuccess, 20), tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-52,OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
}

func TestBuildTreeConfiguredChildren(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Children = map[string][]string{"PROJ-API": {"OPS-DEPLOY", "OPS-AUDIT"}}
	svc.P.(*fake.Provider).History["OPS-AUDIT"] = []provider.Build{tb("OPS-AUDIT-3", "Manual run by jdoe", provider.StateSuccess, 1)}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-AUDIT!,OPS-DEPLOY-88(OPS-SMOKE-51))", shape(tr.Root),
		"only the listed plans; PROJ-DOCS-30 is left out; OPS-AUDIT never ran as a child")
	require.Equal(t, 1, svc.P.(*fake.Provider).ListCalls("OPS-AUDIT"), "a configured plan is a candidate outside the scope")
	require.Equal(t, provider.StateFailed, tr.State)
}

func TestBuildTreeConfiguredPlanThatCannotBeRead(t *testing.T) {
	p := treeFake()
	svc := treeService(t, p)
	svc.Cfg.Project.Children = map[string][]string{"PROJ-API": {"OPS-GONE"}}
	svc.P = listErrProvider{Provider: p, failing: "OPS-GONE"}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-GONE!)", shape(tr.Root))
	require.Len(t, tr.Warnings, 1)
	require.Contains(t, tr.Warnings[0], "OPS-GONE: its builds could not be read")
}

// listErrProvider fails ListBuilds for one plan.
type listErrProvider struct {
	*fake.Provider
	failing string
}

func (l listErrProvider) ListBuilds(ctx context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	if key == l.failing {
		return nil, errs.Bamboof("plan %s not found", key).Wrap(errs.ErrNotFound)
	}
	return l.Provider.ListBuilds(ctx, key, o)
}

func TestBuildTreeCutsALoop(t *testing.T) {
	p := treeFake()
	p.History["PROJ-API"][0].Reason = childOf("OPS-SMOKE-51")
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51(PROJ-API-142^)),PROJ-DOCS-30)", shape(tr.Root))
}

func TestBuildTreeDepthStopsTheWalkAndTheReads(t *testing.T) {
	svc := treeService(t, treeFake())
	counting := &getCounter{Provider: svc.P.(*fake.Provider)}
	svc.P = counting
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{Depth: 1})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88,PROJ-DOCS-30)", shape(tr.Root))
	require.Zero(t, counting.calls["OPS-SMOKE-51"], "a node past the depth is not read")
	require.Equal(t, 1, counting.calls["OPS-DEPLOY-88"])
}

// getCounter counts GetBuild calls by key; the tree reads nodes in parallel.
type getCounter struct {
	*fake.Provider
	mu    sync.Mutex
	calls map[string]int
}

func (g *getCounter) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]int{}
	}
	g.calls[key]++
	g.mu.Unlock()
	return g.Provider.GetBuild(ctx, key)
}

func TestBuildTreeReadsEachNodesDetail(t *testing.T) {
	p := treeFake()
	detail := tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)
	detail.Stages = []provider.Stage{{Name: "Smoke", State: provider.StateFailed, Jobs: []provider.Job{{Key: "OPS-SMOKE-JOB1-51", Name: "Smoke tests", State: provider.StateFailed}}}}
	detail.FailedTests = []string{"a", "b", "c"}
	p.Sequences = map[string][]provider.Build{"OPS-SMOKE-51": {detail}}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	smoke := tr.Root.Children[0].Children[0]
	require.Equal(t, "Smoke tests", smoke.Build.Stages[0].Jobs[0].Name)
	require.Len(t, smoke.Build.FailedTests, 3)
}

func TestBuildTreeWindowWarning(t *testing.T) {
	p := treeFake()
	var docs []provider.Build
	for i := 0; i < 25; i++ {
		docs = append(docs, tb(fmt.Sprintf("PROJ-DOCS-%d", 100-i), "Manual run by jdoe", provider.StateSuccess, 30+i))
	}
	p.History["PROJ-DOCS"] = docs
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"PROJ-DOCS: only its last 25 builds were searched; older children may be missing"}, tr.Warnings)

	docs[24].StartedAt = treeT0.Add(-time.Hour) // the window reaches past the root
	tr, err = treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Empty(t, tr.Warnings)
}

func TestBuildTreeAProjectThatCannotBeListed(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Projects = []string{"PROJ", "OPS", "LAB"}
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
	require.Len(t, tr.Warnings, 1)
	require.Contains(t, tr.Warnings[0], "project LAB: its plans could not be listed")
}

func TestBuildTreeWithoutAConfiguredScope(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Projects = nil
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(PROJ-DOCS-30)", shape(tr.Root), "only the root's project is searched")
	require.Zero(t, svc.P.(*fake.Provider).ListCalls("OPS-DEPLOY"))
}

func TestBuildTreeRootOnABranch(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-API12-5": {tb("PROJ-API12-5", "Manual run by jdoe", provider.StateSuccess, 0)}}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API12-5", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API12-5", shape(tr.Root))
	require.Equal(t, provider.StateSuccess, tr.State)
}

func TestBuildTreeUnknownRoot(t *testing.T) {
	_, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-999", TreeOptions{})
	require.ErrorIs(t, err, errs.ErrNotFound)
}

func TestTreeState(t *testing.T) {
	ok := &TreeNode{Build: provider.Build{State: provider.StateSuccess}}
	require.Equal(t, provider.StateSuccess, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{ok}}))
	require.Equal(t, provider.StateFailed, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{NotRun: true}}}))
	require.Equal(t, provider.StateFailed, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{Build: provider.Build{State: provider.StateStopped}}}}))
	require.Equal(t, provider.StateRunning, TreeState(&TreeNode{Build: ok.Build, Children: []*TreeNode{{NotRun: true}, {Build: provider.Build{State: provider.StateQueued}}}}))
}

func TestTreeNodeWalk(t *testing.T) {
	tr, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	var got []string
	tr.Root.Walk(func(n *TreeNode, depth int) { got = append(got, fmt.Sprintf("%d:%s", depth, n.Build.Key)) })
	require.Equal(t, []string{"0:PROJ-API-142", "1:OPS-DEPLOY-88", "2:OPS-SMOKE-51", "1:PROJ-DOCS-30"}, got)
}

func TestBuildTreeFailsWhenNoPlansBuildsCanBeRead(t *testing.T) {
	p := treeFake()
	svc := treeService(t, p)
	svc.P = &flakyProvider{Provider: p, failBuilds: func(string, int) bool { return true }}
	_, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.Error(t, err, "a root alone would read as success")
	require.Equal(t, errs.KindBamboo, errs.KindOf(err))
	require.Contains(t, err.Error(), "no candidate plan's builds could be read")
}

// Bamboo's skipped is a final result, though provider.State.Finished leaves
// it out: a skipped build must neither keep a tree running nor fail it.
func TestTreeStateSkippedIsFinishedAndNotAFailure(t *testing.T) {
	ok := provider.Build{State: provider.StateSuccess}
	tree := &TreeNode{Build: ok, Children: []*TreeNode{{Build: provider.Build{State: provider.StateSkipped}}}}
	require.Equal(t, provider.StateSuccess, TreeState(tree))
	require.True(t, TreeFinished(provider.StateSkipped))
	require.True(t, TreeFinished(provider.StateFailed))
	require.False(t, TreeFinished(provider.StateRunning))
}

// rebuilt is a child build after a restart: Bamboo rewrites its reason, and
// only GetBuild still reports the parent.
func rebuilt(key, parent string, st provider.State, minutes int) provider.Build {
	b := tb(key, "Rebuilt     by J Doe", st, minutes)
	b.Parent = parent
	return b
}

func TestBuildTreeFindsARestartedChildThroughItsParent(t *testing.T) {
	p := treeFake()
	p.History["OPS-DEPLOY"] = []provider.Build{rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateSuccess, 5)}
	tr, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(tr.Root))
}

func TestBuildTreeAsksOnlyRestartedBuildsForTheirParent(t *testing.T) {
	p := treeFake()
	p.History["OPS-DEPLOY"] = []provider.Build{
		rebuilt("OPS-DEPLOY-89", "", provider.StateSuccess, 6), // a restarted top-level build
		tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5),
	}
	_, err := treeService(t, p).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, p.GetCalls("OPS-DEPLOY-89"), "its reason names no parent, so GetBuild is asked once")
	require.Equal(t, 0, p.GetCalls("PROJ-API-141"), "a manual build is never asked")
}
