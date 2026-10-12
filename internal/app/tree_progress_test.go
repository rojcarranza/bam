package app

import (
	"context"
	"sync"
	"testing"

	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// progressRec records every snapshot a tree load reports.
type progressRec struct {
	mu  sync.Mutex
	got []TreeProgress
}

func (r *progressRec) on(p TreeProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, p)
}

func (r *progressRec) snapshots() []TreeProgress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]TreeProgress(nil), r.got...)
}

func counts(p TreeProgress) []TreeCount { return []TreeCount{p.Projects, p.Plans, p.Builds, p.Jobs} }

// requireSaneProgress: every count has Done <= Total, nothing ever goes
// down, and the last snapshot has every started phase finished.
func requireSaneProgress(t *testing.T, got []TreeProgress) {
	t.Helper()
	require.NotEmpty(t, got)
	var prev TreeProgress
	for i, p := range got {
		for j, c := range counts(p) {
			was := counts(prev)[j]
			require.LessOrEqual(t, c.Done, c.Total, "snapshot %d phase %d", i, j)
			require.GreaterOrEqual(t, c.Total, was.Total, "snapshot %d phase %d: a total shrank", i, j)
			require.GreaterOrEqual(t, c.Done, was.Done, "snapshot %d phase %d: done went back", i, j)
		}
		prev = p
	}
	for j, c := range counts(prev) {
		require.Equal(t, c.Total, c.Done, "phase %d is finished at the end", j)
	}
}

func TestATreeTrackerIsSafeWhenNil(t *testing.T) {
	require.Nil(t, newTreeTracker(nil))
	var tr *treeTracker
	tr.add(treePlans, 3)
	tr.done(treePlans)
}

func TestBuildTreeReportsEveryRequestItMakes(t *testing.T) {
	var rec progressRec
	_, err := treeService(t, treeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	require.Equal(t, TreeProgress{
		Projects: TreeCount{2, 2}, // PROJ and OPS
		Plans:    TreeCount{4, 4}, // PROJ-API, PROJ-DOCS, OPS-DEPLOY, OPS-SMOKE
		Builds:   TreeCount{3, 3}, // OPS-DEPLOY-88, OPS-SMOKE-51, PROJ-DOCS-30
	}, got[len(got)-1])
}

func TestBuildTreeCountsALinkedProjectAndItsLogs(t *testing.T) {
	var rec progressRec
	_, err := treeService(t, logTreeFake()).BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	last := got[len(got)-1]
	require.Equal(t, TreeCount{3, 3}, last.Projects, "LAB is listed once a log links to it")
	require.Equal(t, TreeCount{8, 8}, last.Plans, "five candidates, then LAB's three plans")
	require.Positive(t, last.Jobs.Total)
	overlap := false
	for _, p := range got {
		if p.Jobs.Total > 0 && p.Plans.Done < p.Plans.Total {
			overlap = true
		}
	}
	require.True(t, overlap, "LAB's builds are read while logs are being counted")
}

func TestBuildTreeCountsAFailedReadAsDone(t *testing.T) {
	p := treeFake()
	svc := treeService(t, p)
	svc.P = &flakyProvider{Provider: p, failBuilds: func(plan string, _ int) bool { return plan == "PROJ-DOCS" }}
	var rec progressRec
	tr, err := svc.BuildTree(context.Background(), "PROJ-API-142", TreeOptions{OnProgress: rec.on})
	require.NoError(t, err)
	require.NotEmpty(t, tr.Warnings)
	got := rec.snapshots()
	requireSaneProgress(t, got)
	require.Equal(t, TreeCount{4, 4}, got[len(got)-1].Plans)
}

func TestBuildTreeDoesNotCountBuildsAWatchAlreadyHas(t *testing.T) {
	var rec progressRec
	svc := treeService(t, treeFake())
	done := map[string]provider.Build{"OPS-SMOKE-51": tb("OPS-SMOKE-51", childOf("OPS-DEPLOY-88"), provider.StateFailed, 9)}
	o := TreeOptions{track: newTreeTracker(rec.on)}
	_, _, err := svc.buildTree(context.Background(), "PROJ-API-142", o, done, nil)
	require.NoError(t, err)
	got := rec.snapshots()
	require.Equal(t, TreeCount{2, 2}, got[len(got)-1].Builds, "OPS-SMOKE-51 is reused, not read")
}
