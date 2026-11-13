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
	mu        sync.Mutex
	scan      *treeScan
	done      map[string]provider.Build
	scannedAt time.Time
	rootEnded bool // the root had ended when the plans were last listed
}

// pinTreeCache holds one memo per root build key.
type pinTreeCache struct {
	mu sync.Mutex
	m  map[string]*pinTreeMemo
}

func (c *pinTreeCache) memo(key string) *pinTreeMemo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*pinTreeMemo{}
	}
	if c.m[key] == nil {
		c.m[key] = &pinTreeMemo{done: map[string]provider.Build{}}
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
	case p.Current != nil:
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
		if t, ok := s.CachedTree(root.Key); ok {
			return &t
		}
	}
	m := s.pinTrees.memo(root.Key)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := s.Clock.Now()
	o := TreeOptions{}
	prev := m.scan
	if prev != nil {
		ended := TreeFinished(root.State) && !m.rootEnded
		stale := now.Sub(m.scannedAt) >= pinRescanEvery
		o.NoScan = !ended && !stale
	}
	t, scan, err := s.buildTree(ctx, root.Key, o, m.done, prev)
	if err != nil {
		return nil
	}
	if !o.NoScan {
		m.scannedAt, m.rootEnded = now, TreeFinished(root.State)
	}
	m.scan = scan
	t.Root.Walk(func(n *TreeNode, _ int) {
		if !n.NotRun && !n.Loop && TreeFinished(n.Build.State) {
			m.done[n.Build.Key] = n.Build
		}
	})
	if TreeFinal(t) {
		s.KeepTree(root.Key, t)
		s.pinTrees.forget(root.Key)
	}
	return &t
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
