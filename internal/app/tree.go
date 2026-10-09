package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// treeWindow is how many recent builds of each candidate plan are searched
// for children; treeFetch is how many requests run at a time.
const (
	treeWindow = 25
	treeFetch  = 4
)

var childOfRe = regexp.MustCompile(`^Child of ([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)$`)

// ParentOf returns the build a build reason names as its parent: Bamboo
// gives a child build the reason "Child of PROJ-API-142".
func ParentOf(reason string) (string, bool) {
	m := childOfRe.FindStringSubmatch(reason)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// TreeOptions shape a build tree.
type TreeOptions struct {
	Depth int // levels of builds below the root; 0 means all
}

// TreeNode is one build of a tree, or a configured child that never ran.
type TreeNode struct {
	PlanKey  string
	Build    provider.Build // zero for a not-run node
	NotRun   bool           // a configured child plan with no build that names the parent
	Loop     bool           // the build already appears above; its children are not followed
	Children []*TreeNode    // by plan key, then build number, newest first
}

// Walk calls f for n and every node below it, parents first; the root is at
// depth 0.
func (n *TreeNode) Walk(f func(n *TreeNode, depth int)) {
	var walk func(n *TreeNode, depth int)
	walk = func(n *TreeNode, depth int) {
		f(n, depth)
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(n, 0)
}

// Tree is a build and every build it caused.
type Tree struct {
	Root     *TreeNode
	State    provider.State // TreeState of the root
	Warnings []string       // sorted; never change the result
}

// TreeState is running while any build runs, failed when any build did not
// succeed or a configured child did not run, success otherwise.
func TreeState(root *TreeNode) provider.State {
	running, bad := false, false
	root.Walk(func(n *TreeNode, _ int) {
		switch {
		case n.NotRun:
			bad = true
		case !n.Build.State.Finished():
			running = true
		case n.Build.State != provider.StateSuccess:
			bad = true
		}
	})
	switch {
	case running:
		return provider.StateRunning
	case bad:
		return provider.StateFailed
	}
	return provider.StateSuccess
}

// BuildTree reads build key and every build it caused. It never triggers or
// stops anything.
func (s *Service) BuildTree(ctx context.Context, key string, o TreeOptions) (Tree, error) {
	return s.buildTree(ctx, key, o, nil)
}

// buildTree is BuildTree reusing done: builds already known to have
// finished are not read again.
func (s *Service) buildTree(ctx context.Context, key string, o TreeOptions, done map[string]provider.Build) (Tree, error) {
	root, ok := done[key]
	if !ok {
		var err error
		if root, err = s.Build(ctx, key); err != nil {
			return Tree{}, err
		}
	}
	configured, err := s.Cfg.Children()
	if err != nil {
		return Tree{}, err
	}
	plans, warnings, err := s.treeCandidates(ctx, root.PlanKey, configured)
	if err != nil {
		return Tree{}, err
	}
	idx, windows, more := s.treeIndex(ctx, plans)
	warnings = append(warnings, more...)
	t := Tree{Root: &TreeNode{PlanKey: root.PlanKey, Build: root}}
	treeGrow(t.Root, idx, configured, o.Depth, 0, map[string]bool{root.Key: true})
	warnings = append(warnings, s.treeDetails(ctx, t.Root, done)...)
	warnings = append(warnings, windowWarnings(windows, root)...)
	if err := ctx.Err(); err != nil {
		return Tree{}, err
	}
	sort.Strings(warnings)
	t.Warnings, t.State = warnings, TreeState(t.Root)
	return t, nil
}

// treeCandidates is every plan of the scoped projects and of the root's
// project, and every plan the children map names, sorted.
func (s *Service) treeCandidates(ctx context.Context, rootPlan string, configured map[string][]string) ([]string, []string, error) {
	set := map[string]bool{}
	projects := slices.Clone(s.ProjectKeys())
	if p, _, ok := strings.Cut(rootPlan, "-"); ok && !slices.Contains(projects, p) {
		projects = append(projects, p)
	}
	var warnings []string
	var firstErr error
	for _, p := range projects {
		plans, err := s.P.ListPlans(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			if firstErr == nil {
				firstErr = err
			}
			warnings = append(warnings, fmt.Sprintf("project %s: its plans could not be listed: %s", p, describeErr(err)))
			continue
		}
		for _, pl := range plans {
			set[pl.Key] = true
		}
	}
	for k, vs := range configured {
		set[k] = true
		for _, v := range vs {
			set[v] = true
		}
	}
	if len(set) == 0 && firstErr != nil {
		return nil, nil, firstErr
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, warnings, nil
}

// planWindow is what the search saw of one plan: whether it returned a full
// window, and when its oldest returned build started.
type planWindow struct {
	full   bool
	oldest time.Time
}

// treeIndex reads the recent builds of every plan and indexes each child
// build under the parent its reason names.
func (s *Service) treeIndex(ctx context.Context, plans []string) (map[string][]provider.Build, map[string]planWindow, []string) {
	type result struct {
		builds []provider.Build
		err    error
	}
	results := make([]result, len(plans))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	for i, p := range plans {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.ListBuilds(ctx, p, provider.ListOptions{Limit: treeWindow})
			results[i] = result{builds: b, err: err}
		}(i, p)
	}
	wg.Wait()
	idx := map[string][]provider.Build{}
	windows := map[string]planWindow{}
	var warnings []string
	for i, r := range results {
		if r.err != nil {
			if ctx.Err() == nil {
				warnings = append(warnings, fmt.Sprintf("%s: its builds could not be read: %s", plans[i], describeErr(r.err)))
			}
			continue
		}
		w := planWindow{full: len(r.builds) >= treeWindow}
		for _, b := range r.builds {
			if !b.StartedAt.IsZero() && (w.oldest.IsZero() || b.StartedAt.Before(w.oldest)) {
				w.oldest = b.StartedAt
			}
			if parent, ok := ParentOf(b.Reason); ok {
				idx[parent] = append(idx[parent], b)
			}
		}
		windows[plans[i]] = w
	}
	return idx, windows, warnings
}

// treeGrow adds the children of n from the index, then theirs. path holds
// the builds from the root to n.
func treeGrow(n *TreeNode, idx map[string][]provider.Build, configured map[string][]string, depth, level int, path map[string]bool) {
	if depth > 0 && level >= depth {
		return
	}
	listed, has := configured[n.PlanKey]
	var kids []*TreeNode
	for _, b := range idx[n.Build.Key] {
		if has && !slices.Contains(listed, b.PlanKey) {
			continue
		}
		kids = append(kids, &TreeNode{PlanKey: b.PlanKey, Build: b})
	}
	for _, p := range listed {
		if !slices.ContainsFunc(kids, func(k *TreeNode) bool { return k.PlanKey == p }) {
			kids = append(kids, &TreeNode{PlanKey: p, NotRun: true})
		}
	}
	sort.SliceStable(kids, func(i, j int) bool {
		if kids[i].PlanKey != kids[j].PlanKey {
			return kids[i].PlanKey < kids[j].PlanKey
		}
		return kids[i].Build.Number > kids[j].Build.Number
	})
	n.Children = kids
	for _, k := range kids {
		if k.NotRun {
			continue
		}
		if path[k.Build.Key] {
			k.Loop = true
			continue
		}
		path[k.Build.Key] = true
		treeGrow(k, idx, configured, depth, level+1, path)
		delete(path, k.Build.Key)
	}
}

// treeDetails reads every build node below the root with GetBuild, for its
// stages, jobs and failed tests; done builds are reused.
func (s *Service) treeDetails(ctx context.Context, root *TreeNode, done map[string]provider.Build) []string {
	var nodes []*TreeNode
	root.Walk(func(n *TreeNode, depth int) {
		if depth > 0 && !n.NotRun && !n.Loop {
			nodes = append(nodes, n)
		}
	})
	warnings := make([]string, len(nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	for i, n := range nodes {
		if b, ok := done[n.Build.Key]; ok {
			n.Build = b
			continue
		}
		wg.Add(1)
		go func(i int, n *TreeNode) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.GetBuild(ctx, n.Build.Key)
			if err != nil {
				if ctx.Err() == nil {
					warnings[i] = fmt.Sprintf("%s: its stages could not be read: %s", n.Build.Key, describeErr(err))
				}
				return
			}
			n.Build = b
		}(i, n)
	}
	wg.Wait()
	return slices.DeleteFunc(warnings, func(w string) bool { return w == "" })
}

// windowWarnings names the plans whose full window started after the root:
// older builds of theirs, unread, may be children.
func windowWarnings(windows map[string]planWindow, root provider.Build) []string {
	if root.StartedAt.IsZero() {
		return nil
	}
	var out []string
	for plan, w := range windows {
		if w.full && w.oldest.After(root.StartedAt) {
			out = append(out, fmt.Sprintf("%s: only its last %d builds were searched; older children may be missing", plan, treeWindow))
		}
	}
	return out
}

// treePoll is the time between two refreshes of a watched tree; treeTries
// is how many failed refreshes in a row end the watch.
const (
	treePoll  = 10 * time.Second
	treeTries = 3
)

// TreeEventType names a step of a watched tree.
type TreeEventType string

const (
	TreeUpdate TreeEventType = "update" // the tree changed (always sent for the first refresh)
	TreeDone   TreeEventType = "done"   // every build finished and nothing new appeared; last event
	TreeError  TreeEventType = "error"  // refreshes kept failing; last event
)

// TreeEvent is one step of a watched tree.
type TreeEvent struct {
	Type TreeEventType
	Time time.Time
	Tree Tree
	Err  error
}

// WatchTree refreshes the tree of key every treePoll and emits it when it
// changed. It ends once every build has finished and one more refresh
// found nothing new, since Bamboo queues children when their parent ends.
// Ending ctx stops watching; no build is stopped.
func (s *Service) WatchTree(ctx context.Context, key string, o TreeOptions) <-chan TreeEvent {
	ch := make(chan TreeEvent, 4)
	go func() {
		defer close(ch)
		send := func(e TreeEvent) bool {
			e.Time = s.Clock.Now()
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}
		done := map[string]provider.Build{}
		var last, settled string
		fails := 0
		for {
			t, err := s.buildTree(ctx, key, o, done)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fails++
				if fails >= treeTries {
					send(TreeEvent{Type: TreeError, Err: err})
					return
				}
			} else {
				fails = 0
				t.Root.Walk(func(n *TreeNode, _ int) {
					if !n.NotRun && !n.Loop && n.Build.State.Finished() {
						done[n.Build.Key] = n.Build
					}
				})
				sig := treeSignature(t)
				if sig != last {
					if !send(TreeEvent{Type: TreeUpdate, Tree: t}) {
						return
					}
					last = sig
				}
				if t.State == provider.StateRunning {
					settled = ""
				} else if settled == sig {
					send(TreeEvent{Type: TreeDone, Tree: t})
					return
				} else {
					settled = sig
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.Clock.After(treePoll):
			}
		}
	}()
	return ch
}

// treeSignature is what a watcher compares between refreshes: every node,
// its state and its stages' states.
func treeSignature(t Tree) string {
	var b strings.Builder
	t.Root.Walk(func(n *TreeNode, depth int) {
		fmt.Fprintf(&b, "%d %s %s %s %t %t", depth, n.PlanKey, n.Build.Key, n.Build.State, n.NotRun, n.Loop)
		for _, st := range n.Build.Stages {
			fmt.Fprintf(&b, " %s=%s", st.Name, st.State)
		}
		b.WriteByte('\n')
	})
	return b.String()
}
