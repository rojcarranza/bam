package app

import (
	"context"
	"fmt"
	"testing"

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
