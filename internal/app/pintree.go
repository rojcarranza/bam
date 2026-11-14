package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// pinRescanEvery is how often a pinned tree's plans are listed again while a
// child still runs: Bamboo starts a parent's own child plans when the parent
// ends, and a script can start more while it runs.
const pinRescanEvery = 5 * time.Minute

// pinTreeMemo is what the board keeps of one pinned build's tree between
// refreshes, so a refresh reads only what runs.
type pinTreeMemo struct {
	plan      string // the plan of the root, so a newer build of it replaces this memo
	mu        sync.Mutex
	scan      *treeScan
	done      map[string]provider.Build
	scannedAt time.Time
	last      *Tree // the last tree read, kept for a read that fails
	settled   bool  // the plans were last listed after the finished tree had settled
}

// pinTreeCache holds one memo per root build key.
type pinTreeCache struct {
	mu sync.Mutex
	m  map[string]*pinTreeMemo
}

// memo is the memo of the root build key of plan. Memos of older builds of
// the same plan are dropped: a pin shows one build at a time, and a board that
// runs for days would otherwise keep every tree it ever read.
func (c *pinTreeCache) memo(plan, key string) *pinTreeMemo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*pinTreeMemo{}
	}
	for k, m := range c.m {
		if m.plan == plan && k != key {
			delete(c.m, k)
		}
	}
	if c.m[key] == nil {
		c.m[key] = &pinTreeMemo{plan: plan, done: map[string]provider.Build{}}
	}
	return c.m[key]
}

func (c *pinTreeCache) forget(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

type pinTreeKey struct{}

// WithPinTrees marks ctx as asking for the tree under each pin. The live
// board asks; bam top --once does only with --tree, since a tree costs
// requests the plain board does not.
func WithPinTrees(ctx context.Context) context.Context {
	return context.WithValue(ctx, pinTreeKey{}, true)
}

// noPinTrees is ctx without the request for trees, for the branch rows under a
// pin, which show no tree of their own.
func noPinTrees(ctx context.Context) context.Context {
	return context.WithValue(ctx, pinTreeKey{}, false)
}

// PinRoot is the build a pin's tree hangs from: the build running now, else
// the latest finished one.
func (p PinView) PinRoot() (provider.Build, bool) {
	switch {
	case p.Current != nil && p.Current.State == provider.StateRunning:
		return *p.Current, true
	case len(p.Results) > 0:
		return p.Results[0], true
	}
	return provider.Build{}, false
}

// HasChildren reports whether the pin's tree has a pipeline below its own.
func (p PinView) HasChildren() bool {
	return p.Tree != nil && p.Tree.Root != nil && len(p.Tree.Root.Children) > 0
}

// PinTree reads the tree of root, the build a pin shows. A finished build is
// drawn from the trees bam saved on disk when it has one. Otherwise the first
// reading is as bam build tree reads it, once per build; after that a read
// asks only for what runs: each running pipeline and each running job's log
// once, and no plan is listed again until the pin's own build has ended, or
// every pinRescanEvery while a child still runs. It returns nil when the tree
// could not be read; the pin then keeps its single row.
func (s *Service) PinTree(ctx context.Context, root provider.Build) *Tree {
	if TreeFinished(root.State) {
		// A tree with a failed pipeline is read again: a rerun of that pipeline
		// keeps its build key, so a saved tree cannot show it.
		if t, ok := s.CachedTree(root.Key); ok && t.Settled() && completeTree(t) && len(failedChildren(&t)) == 0 {
			return &t // saved once the tree had settled, not just as the build ended
		}
	}
	m := s.pinTrees.memo(root.PlanKey, root.Key)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := s.Clock.Now()
	o := TreeOptions{}
	if m.scan != nil {
		// A finished build is read again until a read comes after it settled;
		// a running one only every pinRescanEvery.
		o.NoScan = (!TreeFinished(root.State) || m.settled) && now.Sub(m.scannedAt) < pinRescanEvery
		o.Rescan = !o.NoScan // the pin keeps its own times to list the plans
	}
	t, scan, err := s.buildTree(ctx, root.Key, o, m.done, m.scan)
	if err != nil {
		return m.last // the tree read before, so a failed read does not blank the pin
	}
	m.scan = scan
	if !o.NoScan {
		m.scannedAt = now
		m.settled = TreeFinished(root.State) && t.Settled()
	}
	t.Root.Walk(func(n *TreeNode, _ int) {
		if !n.NotRun && !n.Loop && TreeFinished(n.Build.State) && n.Build.State != provider.StateFailed {
			m.done[n.Build.Key] = n.Build
		}
	})
	if m.settled && t.Settled() && completeTree(t) && len(failedChildren(&t)) == 0 {
		s.KeepTree(root.Key, t)
		s.pinTrees.forget(root.Key)
	}
	m.last = &t
	return &t
}

// treeLatest is when the newest pipeline of t, or root, finished: Bamboo
// queues a pipeline's own child plans a moment after it ends, so a tree is
// complete only some time after that. Zero when none has finished.
func treeLatest(t Tree, root provider.Build) time.Time {
	latest := root.FinishedAt
	if t.Root != nil {
		t.Root.Walk(func(n *TreeNode, _ int) {
			if !n.NotRun && !n.Loop && n.Build.FinishedAt.After(latest) {
				latest = n.Build.FinishedAt
			}
		})
	}
	return latest
}

// StageProgress says which stage a build is in, "3/5 Run integration builds":
// the first stage still running, else the first that failed. It is empty for
// a build that has no stages or is neither running nor failed.
func StageProgress(b provider.Build) string {
	want := provider.StateRunning
	if b.State == provider.StateFailed {
		want = provider.StateFailed
	} else if b.State != provider.StateRunning {
		return ""
	}
	for i, st := range b.Stages {
		if st.State == want {
			return fmt.Sprintf("%d/%d %s", i+1, len(b.Stages), st.Name)
		}
	}
	return ""
}

// failedChildren are the pipelines below the root of t that failed, newest
// run of each plan only.
func failedChildren(t *Tree) []*TreeNode {
	var out []*TreeNode
	if t == nil || t.Root == nil {
		return nil
	}
	t.Root.Walk(func(n *TreeNode, depth int) {
		if depth > 0 && !n.NotRun && !n.Loop && !n.Superseded && n.Build.State == provider.StateFailed {
			out = append(out, n)
		}
	})
	return out
}

// completeTree says t was read to every depth with nothing left out: the plan
// screen saves trees read one level deep, and a read can warn that a plan could
// not be read; neither may stand in for the whole tree.
func completeTree(t Tree) bool { return t.Depth == 0 && len(t.Warnings) == 0 }
