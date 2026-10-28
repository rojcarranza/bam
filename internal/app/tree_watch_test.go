package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/require"
)

// hookClock is the fake clock, calling hook with the number of each wait
// before it fires.
type hookClock struct {
	*fakeClock
	mu   sync.Mutex
	n    int
	hook func(n int)
}

func (c *hookClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.n++
	n := c.n
	c.mu.Unlock()
	if c.hook != nil {
		c.hook(n)
	}
	return c.fakeClock.After(d)
}

// collectTree is a watch's events, its first refresh's progress left out.
func collectTree(ch <-chan TreeEvent) []TreeEvent {
	var out []TreeEvent
	for _, e := range collectTreeAll(ch) {
		if e.Type != TreeLoading {
			out = append(out, e)
		}
	}
	return out
}

func collectTreeAll(ch <-chan TreeEvent) []TreeEvent {
	var out []TreeEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestWatchTreeFollowsAChildThatAppearsLater(t *testing.T) {
	p := treeFake()
	p.History = map[string][]provider.Build{"PROJ-API": {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)}}
	p.Sequences = map[string][]provider.Build{"PROJ-API-142": {
		tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0),
		tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0),
	}}
	svc := treeService(t, p)
	clock := &hookClock{fakeClock: newClock(), hook: func(n int) {
		if n == 1 { // the parent ends and Bamboo queues its child
			p.History["OPS-DEPLOY"] = []provider.Build{tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5)}
		}
	}}
	svc.Clock = clock
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Len(t, evs, 3)
	require.Equal(t, TreeUpdate, evs[0].Type)
	require.Equal(t, provider.StateRunning, evs[0].Tree.State)
	require.Equal(t, TreeUpdate, evs[1].Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88)", shape(evs[1].Tree.Root))
	require.Equal(t, TreeDone, evs[2].Type, "done after one more refresh found nothing new")
	require.Equal(t, provider.StateSuccess, evs[2].Tree.State)
	require.Equal(t, []time.Duration{10 * time.Second, 10 * time.Second}, clock.Waits())
}

func TestWatchTreeSendsOnlyChanges(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-DOCS-30": {
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateSuccess, 5),
	}}
	svc := treeService(t, p)
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	var types []TreeEventType
	for _, e := range evs {
		types = append(types, e.Type)
	}
	require.Equal(t, []TreeEventType{TreeUpdate, TreeUpdate, TreeDone}, types, "the unchanged second poll sends nothing")
	require.Equal(t, provider.StateFailed, evs[2].Tree.State, "OPS-SMOKE-51 failed")
}

func TestWatchTreeEndsAtOnceWhenTheFirstRefreshFails(t *testing.T) {
	svc := treeService(t, treeFake())
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-999", TreeOptions{}))
	require.Len(t, evs, 1)
	require.Equal(t, TreeError, evs[0].Type)
	require.ErrorIs(t, evs[0].Err, errs.ErrNotFound)
	require.Empty(t, svc.Clock.(*fakeClock).Waits(), "a mistyped build is not retried")
}

func TestWatchTreeGivesUpAfterThreeFailedRefreshes(t *testing.T) {
	p := treeFake()
	// The root still runs, so every refresh reads it; from the second on, Bamboo fails.
	p.Sequences = map[string][]provider.Build{"PROJ-API-142": {tb("PROJ-API-142", "Manual run by jdoe", provider.StateRunning, 0)}}
	fp := &flakyProvider{Provider: p, failGet: func(key string, call int) error {
		if key == "PROJ-API-142" && call > 1 {
			return errs.Bamboof("Bamboo answered 503")
		}
		return nil
	}}
	svc := treeService(t, p)
	svc.P = fp
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Equal(t, TreeUpdate, evs[0].Type)
	require.Equal(t, TreeError, evs[len(evs)-1].Type)
	require.Len(t, svc.Clock.(*fakeClock).Waits(), 3, "one good refresh, then three failed ones")
}

func TestWatchTreeEndsAtOnceOnAConfigError(t *testing.T) {
	svc := treeService(t, treeFake())
	svc.Cfg.Project.Children = map[string][]string{"proj-api": {"OPS-DEPLOY"}}
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Len(t, evs, 1)
	require.Equal(t, errs.KindConfig, errs.KindOf(evs[0].Err))
	require.Empty(t, svc.Clock.(*fakeClock).Waits())
}

func TestWatchTreeListsPlansOnceAndReadsASmallWindowAfter(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-DOCS-30": {
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateSuccess, 5),
	}}
	fp := &flakyProvider{Provider: p}
	svc := treeService(t, p)
	svc.P = fp
	collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Equal(t, map[string]int{"PROJ": 1, "OPS": 1}, fp.plansCalls, "the candidates are found once per watch")
	require.Equal(t, 25, fp.limits[0])
	for _, l := range fp.limits[4:] { // four candidate plans per refresh
		require.Equal(t, 5, l, "later refreshes read each plan's newest builds only")
	}
}

func TestWatchTreeKeepsChildrenOfAPlanThatCannotBeReadForARefresh(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSuccess // so the watch ends
	fp := &flakyProvider{Provider: p, failBuilds: func(plan string, call int) bool { return plan == "OPS-DEPLOY" && call > 1 }}
	svc := treeService(t, p)
	svc.P = fp
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	last := evs[len(evs)-1]
	require.Equal(t, TreeDone, last.Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88(OPS-SMOKE-51),PROJ-DOCS-30)", shape(last.Tree.Root))
}

// flakyProvider wraps the fake: it counts ListPlans per project, records
// each ListBuilds limit, and fails the calls its functions pick.
type flakyProvider struct {
	*fake.Provider
	mu         sync.Mutex
	plansCalls map[string]int
	limits     []int
	buildCalls map[string]int
	getCalls   map[string]int
	failBuilds func(plan string, call int) bool
	failGet    func(key string, call int) error
}

func (f *flakyProvider) ListPlans(ctx context.Context, project string) ([]provider.Plan, error) {
	f.mu.Lock()
	if f.plansCalls == nil {
		f.plansCalls = map[string]int{}
	}
	f.plansCalls[project]++
	f.mu.Unlock()
	return f.Provider.ListPlans(ctx, project)
}

func (f *flakyProvider) ListBuilds(ctx context.Context, plan string, o provider.ListOptions) ([]provider.Build, error) {
	f.mu.Lock()
	if f.buildCalls == nil {
		f.buildCalls = map[string]int{}
	}
	f.buildCalls[plan]++
	n := f.buildCalls[plan]
	f.limits = append(f.limits, o.Limit)
	f.mu.Unlock()
	if f.failBuilds != nil && f.failBuilds(plan, n) {
		return nil, errs.Bamboof("Bamboo answered 503")
	}
	return f.Provider.ListBuilds(ctx, plan, o)
}

func (f *flakyProvider) GetBuild(ctx context.Context, key string) (provider.Build, error) {
	f.mu.Lock()
	if f.getCalls == nil {
		f.getCalls = map[string]int{}
	}
	f.getCalls[key]++
	n := f.getCalls[key]
	f.mu.Unlock()
	if f.failGet != nil {
		if err := f.failGet(key, n); err != nil {
			return provider.Build{}, err
		}
	}
	return f.Provider.GetBuild(ctx, key)
}

func TestWatchTreeStopsWhenCancelled(t *testing.T) {
	svc := treeService(t, treeFake())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, collectTree(svc.WatchTree(ctx, "PROJ-API-142", TreeOptions{})))
}

func TestWatchTreeEndsWhenAChildWasSkipped(t *testing.T) {
	p := treeFake()
	p.History["PROJ-DOCS"][0].State = provider.StateSkipped
	evs := collectTree(treeService(t, p).WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Equal(t, TreeDone, evs[len(evs)-1].Type)
}

func TestWatchTreeKeepsARestartedChildUntilItFinishes(t *testing.T) {
	// After --rerun-failed: the restarted build runs again under a rewritten
	// reason, and the watch must follow it rather than end without it.
	p := treeFake()
	p.History = map[string][]provider.Build{
		"PROJ-API":   {tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0)},
		"OPS-DEPLOY": {rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateRunning, 5)},
	}
	p.Sequences = map[string][]provider.Build{"OPS-DEPLOY-88": {
		rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateRunning, 5),
		rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateRunning, 5),
		rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateFailed, 5),
	}}
	svc := treeService(t, p)
	svc.Clock = newClock()
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.GreaterOrEqual(t, len(evs), 2)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88)", shape(evs[0].Tree.Root))
	require.Equal(t, provider.StateRunning, evs[0].Tree.State)
	last := evs[len(evs)-1]
	require.Equal(t, TreeDone, last.Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88)", shape(last.Tree.Root))
	require.Equal(t, provider.StateFailed, last.Tree.State, "the restarted build's own result decides")
}

func TestWatchTreeDoesNotSettleWhileARestartedChildsParentIsUnknown(t *testing.T) {
	// Two failed parent lookups in a row left the same tree twice, and the
	// watch ended without the restarted child.
	p := treeFake()
	p.History = map[string][]provider.Build{
		"PROJ-API":   {tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0)},
		"OPS-DEPLOY": {rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateSuccess, 5)},
	}
	boom := errs.Bamboof("boom")
	p.GetErrs = map[string][]error{"OPS-DEPLOY-88": {boom, boom}}
	svc := treeService(t, p)
	svc.Clock = newClock()
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	last := evs[len(evs)-1]
	require.Equal(t, TreeDone, last.Type)
	require.Equal(t, "PROJ-API-142(OPS-DEPLOY-88)", shape(last.Tree.Root))
}

func TestWatchTreeGivesUpOnAParentThatCannotBeRead(t *testing.T) {
	p := treeFake()
	p.History = map[string][]provider.Build{
		"PROJ-API":   {tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0)},
		"OPS-DEPLOY": {rebuilt("OPS-DEPLOY-88", "PROJ-API-142", provider.StateSuccess, 5)},
	}
	boom := errs.Bamboof("boom")
	p.GetErrs = map[string][]error{"OPS-DEPLOY-88": {boom, boom, boom, boom, boom, boom}}
	svc := treeService(t, p)
	svc.Clock = newClock()
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	last := evs[len(evs)-1]
	require.Equal(t, TreeDone, last.Type, "a parent that never answers does not hold the watch open")
	require.Equal(t, treeTries, p.GetCalls("OPS-DEPLOY-88"))
	require.Contains(t, strings.Join(last.Tree.Warnings, "\n"), "OPS-DEPLOY-88: its parent could not be read")
}

func TestWatchTreeOfLongFinishedBuildsEndsAtOnce(t *testing.T) {
	p := treeFake()
	ended := func(b provider.Build, ago time.Duration) provider.Build {
		b.FinishedAt = newClock().Now().Add(-ago)
		return b
	}
	p.History = map[string][]provider.Build{
		"PROJ-API":   {ended(tb("PROJ-API-142", "Manual run by jdoe", provider.StateSuccess, 0), 2*time.Hour)},
		"OPS-DEPLOY": {ended(tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5), time.Hour)},
	}
	svc := treeService(t, p)
	clock := newClock()
	svc.Clock = clock
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Len(t, evs, 2)
	require.Equal(t, TreeDone, evs[1].Type)
	require.Empty(t, clock.Waits(), "no settle poll for builds that ended an hour ago")

	// A build that ended within the minute still gets its settle poll.
	p.History["OPS-DEPLOY"] = []provider.Build{ended(tb("OPS-DEPLOY-88", childOf("PROJ-API-142"), provider.StateSuccess, 5), 30*time.Second)}
	svc.Clock = newClock()
	evs = collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.Equal(t, TreeDone, evs[len(evs)-1].Type)
	require.Equal(t, []time.Duration{treePoll}, svc.Clock.(*fakeClock).Waits())
}

func TestWatchTreeEstimatesRunningBuilds(t *testing.T) {
	p := treeFake()
	p.Sequences = map[string][]provider.Build{"PROJ-DOCS-30": {
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateRunning, 5),
		tb("PROJ-DOCS-30", childOf("PROJ-API-142"), provider.StateSuccess, 5),
	}}
	est := provider.Progress{Valid: true, Average: 10 * time.Minute, Elapsed: 4 * time.Minute, Remaining: 6 * time.Minute, Percent: 0.4}
	p.Progressions = map[string][]provider.Progress{"PROJ-DOCS-30": {est}}
	svc := treeService(t, p)
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-142", TreeOptions{}))
	require.GreaterOrEqual(t, len(evs), 2)
	got := map[string]TreeNode{}
	evs[0].Tree.Root.Walk(func(n *TreeNode, _ int) { got[n.Build.Key] = *n })
	require.Equal(t, est, got["PROJ-DOCS-30"].Progress, "the running child carries its estimate")
	require.Equal(t, evs[0].Time, got["PROJ-DOCS-30"].ProgressAt)
	require.False(t, got["PROJ-API-142"].Progress.Valid, "a finished build has none")
	require.Zero(t, p.ProgressCalls("PROJ-API-142"), "nor is it asked for one")

	last := evs[len(evs)-1]
	last.Tree.Root.Walk(func(n *TreeNode, _ int) {
		require.False(t, n.Progress.Valid, n.Build.Key+" has finished")
	})
}
