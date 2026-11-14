package app

import (
	"context"
	"testing"

	"github.com/rojcarranza/bam/internal/provider"
	"github.com/stretchr/testify/require"
)

// runsThenEnds is a build's GetBuild answers: running for runs reads, then
// successful.
func runsThenEnds(key, reason string, minutes, runs int) []provider.Build {
	var out []provider.Build
	for i := 0; i < runs; i++ {
		out = append(out, tb(key, reason, provider.StateRunning, minutes))
	}
	end := tb(key, reason, provider.StateSuccess, minutes)
	end.FinishedAt = newClock().Now() // it ends as the test starts: its tree settles treeQuiet later
	return append(out, end)
}

// refreshCounts watches key's tree and returns, for each refresh, how many
// plans' builds it listed and how many builds it read. before runs ahead
// of refresh n (from 1), as the watch waits for it.
func refreshCounts(t *testing.T, svc *Service, fp *flakyProvider, key string, before func(n int)) (lists, gets []int, evs []TreeEvent) {
	t.Helper()
	var lastLists, lastGets int
	snap := func() {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		g := 0
		for _, n := range fp.getCalls {
			g += n
		}
		lists, gets = append(lists, len(fp.limits)-lastLists), append(gets, g-lastGets)
		lastLists, lastGets = len(fp.limits), g
	}
	svc.Clock = &hookClock{fakeClock: newClock(), hook: func(n int) {
		snap()
		if before != nil {
			before(n)
		}
	}}
	evs = collectTree(svc.WatchTree(context.Background(), key, TreeOptions{}))
	snap()
	return lists, gets, evs
}

// TestAWatchedTreeListsPlansOnlyWhenABuildEnds: while nothing in the tree
// ends, a refresh reads the builds still running and lists no plan's
// builds; when a build ends, Bamboo queues its children, so that refresh
// and the next list every candidate plan's newest builds (#199). The
// root's child OPS-DEPLOY-88 is queued a refresh after the root ended.
func TestAWatchedTreeListsPlansOnlyWhenABuildEnds(t *testing.T) {
	p := treeFake()
	p.History = map[string][]provider.Build{
		"PROJ-API":  {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)},
		"PROJ-DOCS": {tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5)},
		"OPS-SMOKE": {tb("OPS-SMOKE-50", "Manual run by jdoe", provider.StateSuccess, -30)},
	}
	p.Sequences = map[string][]provider.Build{
		"PROJ-API-142": runsThenEnds("PROJ-API-142", "Manual run by jdoe", 0, 3),    // ends on refresh 3
		"PROJ-DOCS-30": runsThenEnds("PROJ-DOCS-30", childOf("PROJ-API-142"), 5, 5), // ends on refresh 5
	}
	fp := &flakyProvider{Provider: p}
	svc := treeService(t, p)
	svc.P = fp
	plans := len(p.Plans["PROJ"]) + len(p.Plans["OPS"])
	lists, gets, evs := refreshCounts(t, svc, fp, "PROJ-API-142", func(n int) {
		if n == 4 {
			p.History["OPS-DEPLOY"] = []provider.Build{tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 6)}
		}
	})

	require.Equal(t, []int{plans, 0, 0, plans, plans, plans, plans}, lists,
		"listed first, then only on the refresh a build ended and the one after")
	require.Equal(t, []int{2, 2}, gets[1:3], "a quiet refresh reads the two running builds")
	last := evs[len(evs)-1]
	require.Equal(t, TreeDone, last.Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88,PROJ-DOCS-30)", shape(last.Tree.Root), "a child queued after its parent ended is found")
}

// TestAWatchedTreeListsAgainAfterQuietMinutes: a tree whose builds run for
// long without one ending still lists the candidates every treeRelist, so
// a child Bamboo was slow to queue is found.
func TestAWatchedTreeListsAgainAfterQuietMinutes(t *testing.T) {
	p := treeFake()
	quiet := int(treeRelist / treePoll)
	p.History = map[string][]provider.Build{"PROJ-API": {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)}}
	p.Sequences = map[string][]provider.Build{"PROJ-API-142": runsThenEnds("PROJ-API-142", "Manual run by jdoe", 0, quiet+3)}
	fp := &flakyProvider{Provider: p}
	svc := treeService(t, p)
	svc.P = fp
	lists, _, evs := refreshCounts(t, svc, fp, "PROJ-API-142", func(n int) {
		if n == quiet-3 { // queued late, without its parent ending
			p.History["PROJ-DOCS"] = []provider.Build{tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateSuccess, 5)}
		}
	})
	var listed []int
	for n, l := range lists {
		if l > 0 {
			listed = append(listed, n)
		}
	}
	require.Equal(t, []int{0, quiet, quiet + 1, quiet + 3}, listed,
		"first, after treeRelist and once more as it found a finished build, then when the root ended: its end is far past treeQuiet, so the tree settles there")
	require.Equal(t, TreeUpdate, evs[1].Type)
	require.Equal(t, "PROJ-API-142(PROJ-DOCS-30)", shape(evs[1].Tree.Root))
	require.Equal(t, provider.StateRunning, evs[1].Tree.Root.Build.State, "found while the root still ran")
}

// TestAQuietRefreshKeepsTheLastListingsReadWarnings: a plan whose builds
// cannot be read warns on the first reading, and a refresh that lists no
// plan, as nothing ended, still carries that warning.
func TestAQuietRefreshKeepsTheLastListingsReadWarnings(t *testing.T) {
	p := treeFake()
	p.History = map[string][]provider.Build{
		"PROJ-API":  {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)},
		"PROJ-DOCS": {tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5)},
	}
	fp := &flakyProvider{Provider: p, failBuilds: func(plan string, _ int) bool { return plan == "OPS-DEPLOY" }}
	svc := treeService(t, p)
	svc.P = fp
	first, scan, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, nil)
	require.NoError(t, err)
	listed := len(fp.limits)
	quiet, _, err := svc.buildTree(context.Background(), "PROJ-API-142", TreeOptions{}, nil, scan)
	require.NoError(t, err)

	require.Equal(t, listed, len(fp.limits), "the refresh lists no plan")
	require.Contains(t, first.Warnings, "OPS-DEPLOY: its builds could not be read: Bamboo answered 503")
	require.Contains(t, quiet.Warnings, "OPS-DEPLOY: its builds could not be read: Bamboo answered 503")
}
