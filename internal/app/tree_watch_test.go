package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
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

func collectTree(ch <-chan TreeEvent) []TreeEvent {
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

func TestWatchTreeGivesUpAfterThreeFailedRefreshes(t *testing.T) {
	svc := treeService(t, treeFake())
	evs := collectTree(svc.WatchTree(context.Background(), "PROJ-API-999", TreeOptions{}))
	require.Len(t, evs, 1)
	require.Equal(t, TreeError, evs[0].Type)
	require.Len(t, svc.Clock.(*fakeClock).Waits(), 2, "three attempts, two waits")
}

func TestWatchTreeStopsWhenCancelled(t *testing.T) {
	svc := treeService(t, treeFake())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Empty(t, collectTree(svc.WatchTree(ctx, "PROJ-API-142", TreeOptions{})))
}
