package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// projectsListed builds the tree of key and reports how many projects it
// listed: a full candidate scan lists them, a shared one does not.
func projectsListed(t *testing.T, svc *Service, key string, shared bool) (string, int) {
	t.Helper()
	var last TreeProgress
	tr, err := svc.BuildTree(context.Background(), key, TreeOptions{Shared: shared, OnProgress: func(p TreeProgress) { last = p }})
	require.NoError(t, err)
	return shape(tr.Root), last.Projects.Total
}

func TestTreesOfOnePlanShareOneScan(t *testing.T) {
	svc := treeService(t, treeFake())
	tree, listed := projectsListed(t, svc, "PROJ-API-142", true)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", tree)
	require.Equal(t, 2, listed)

	tree, listed = projectsListed(t, svc, "PROJ-API-141", true)
	require.Equal(t, "PROJ-API-141(OPS-DEPLOY-87)", tree)
	require.Zero(t, listed, "another build of the plan starts from the first scan")

	_, listed = projectsListed(t, svc, "PROJ-API-141", false)
	require.Equal(t, 2, listed, "a fresh read scans again")

	svc.Clock.(*fakeClock).After(treeShareAge + 1)
	_, listed = projectsListed(t, svc, "PROJ-API-142", true)
	require.Equal(t, 2, listed, "an old scan is not lent")
}

func TestASharedScanThatStopsAfterTheRootIsNotLent(t *testing.T) {
	p := treeFake()
	var docs []provider.Build
	for i := 0; i < treeWindow; i++ { // a full window, between PROJ-API-141 and PROJ-API-142
		docs = append(docs, tb(fmt.Sprintf("PROJ-DOCS-%d", 100-i), "Manual run by jdoe", provider.StateSuccess, -1-i))
	}
	p.History["PROJ-DOCS"] = docs
	svc := treeService(t, p)
	_, listed := projectsListed(t, svc, "PROJ-API-142", true)
	require.Equal(t, 2, listed)
	_, listed = projectsListed(t, svc, "PROJ-API-141", true)
	require.Equal(t, 2, listed, "PROJ-API-141 needs PROJ-DOCS read deeper")
}

// TestASavedTreeSparesTheDeepRead: PROJ-DOCS ran more than a window since
// PROJ-API-142 started, so its first tree reads PROJ-DOCS deeper. Once the
// tree is saved, a shared read finds the old child from it instead.
func TestASavedTreeSparesTheDeepRead(t *testing.T) {
	p := treeFake()
	var docs []provider.Build
	for i := 0; i < 60; i++ {
		docs = append(docs, tb(fmt.Sprintf("PROJ-DOCS-%d", 200-i), "Manual run by jdoe", provider.StateSuccess, 200-i))
	}
	docs = append(docs, tb("PROJ-DOCS-140", childOf("PROJ-API-142"), provider.StateSuccess, 5),
		tb("PROJ-DOCS-139", "Manual run by jdoe", provider.StateSuccess, -5))
	p.History["PROJ-DOCS"] = docs
	svc := treeService(t, p)
	svc.Trees = &TreeStore{Dir: t.TempDir()}
	read := func(shared bool) (Tree, int) {
		var last TreeProgress
		tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{Shared: shared, OnProgress: func(p TreeProgress) { last = p }})
		require.NoError(t, err)
		return tr, last.Plans.Total
	}
	const want = "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-140)"

	tr, plans := read(true)
	require.Equal(t, want, shape(tr.Root))
	require.Equal(t, 5, plans, "four windows and PROJ-DOCS read deeper")
	svc.KeepTree("PROJ-API-142", tr)

	svc.Clock.(*fakeClock).After(treeShareAge + 1) // the scan is no longer lent
	tr, plans = read(true)
	require.Equal(t, want, shape(tr.Root))
	require.Empty(t, tr.Warnings)
	require.Equal(t, 4, plans, "the saved tree supplies PROJ-DOCS-140")

	_, plans = read(false)
	require.Equal(t, 5, plans, "a fresh read reads deeper again")
}

// TestAShallowSavedTreeDoesNotStandForADeepRead: the Tree panel saves a
// one-level tree. A full read must still find OPS-SMOKE-51, which ran more
// than a window ago, rather than take the shallow tree as complete.
func TestAShallowSavedTreeDoesNotStandForADeepRead(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSuccess
	var smoke []provider.Build
	for i := 0; i < 60; i++ {
		smoke = append(smoke, tb(fmt.Sprintf("OPS-SMOKE-%d", 200-i), "Manual run by jdoe", provider.StateSuccess, 200-i))
	}
	p.History["OPS-SMOKE"] = append(smoke, p.History["OPS-SMOKE"]...)
	svc := treeService(t, p)
	svc.Trees = &TreeStore{Dir: t.TempDir()}
	ctx := context.Background()

	shallow, err := svc.BuildTree(ctx, "PROJ-API-142", TreeOptions{Depth: 1, Shared: true})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88,PROJ-DOCS-30)", shape(shallow.Root))
	svc.KeepTree("PROJ-API-142", shallow)

	svc.Clock.(*fakeClock).After(treeShareAge + 1)
	deep, err := svc.BuildTree(ctx, "PROJ-API-142", TreeOptions{Shared: true})
	require.NoError(t, err)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(deep.Root))
}

// gatedPlans holds every ListPlans until release closes.
type gatedPlans struct {
	provider.Provider
	release chan struct{}
}

func (g gatedPlans) ListPlans(ctx context.Context, project string) ([]provider.Plan, error) {
	<-g.release
	return g.Provider.ListPlans(ctx, project)
}

// waiters is how many shared reads wait for plan's full scan.
func waiters(svc *Service, plan string) int {
	svc.scans.mu.Lock()
	defer svc.scans.mu.Unlock()
	if f, ok := svc.scans.inflight[plan]; ok {
		return f.waiters
	}
	return 0
}

func TestASharedReadWaitsForTheScanInFlight(t *testing.T) {
	svc := treeService(t, treeFake())
	gate := gatedPlans{Provider: svc.P, release: make(chan struct{})}
	svc.P = gate
	type result struct {
		tree   string
		listed int
	}
	first, second := make(chan result, 1), make(chan result, 1)
	read := func(key string, out chan<- result) {
		var last TreeProgress
		tr, err := svc.BuildTree(context.Background(), key, TreeOptions{Shared: true, OnProgress: func(p TreeProgress) { last = p }})
		if err != nil {
			out <- result{tree: err.Error()}
			return
		}
		out <- result{shape(tr.Root), last.Projects.Total}
	}
	go read("PROJ-API-142", first)
	require.Eventually(t, func() bool {
		svc.scans.mu.Lock()
		defer svc.scans.mu.Unlock()
		return svc.scans.inflight["PROJ-API"] != nil
	}, 5*time.Second, time.Millisecond)
	go read("PROJ-API-141", second)
	require.Eventually(t, func() bool { return waiters(svc, "PROJ-API") == 1 }, 5*time.Second, time.Millisecond)
	close(gate.release)

	require.Equal(t, result{"PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", 2}, <-first)
	require.Equal(t, result{"PROJ-API-141(OPS-DEPLOY-87)", 0}, <-second, "the second read starts from the first one's scan")
}

func TestAWaitingReadEndsWithItsContext(t *testing.T) {
	svc := treeService(t, treeFake())
	_, release, err := svc.awaitScan(context.Background(), provider.Build{PlanKey: "PROJ-API"})
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.BuildTree(ctx, "PROJ-API-141", TreeOptions{Shared: true})
	require.ErrorIs(t, err, context.Canceled)
}
