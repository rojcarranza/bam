package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// treeWindow is how many recent builds of each candidate plan are searched
// for children; a plan whose window does not reach back to the root's start
// is read again, four times deeper each time, up to treeDeep builds.
// treeFetch is how many requests run at a time.
const (
	treeWindow  = 25
	treeDeep    = 1600
	treeRefresh = 5 // a watch's later refreshes: a new child is among its plan's newest builds
	treeFetch   = 4
)

var (
	childOfRe = regexp.MustCompile(`^Child of ([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)$`)
	// rebuiltRe is the reason Bamboo gives a build it restarted, replacing
	// the "Child of" reason a child build had.
	rebuiltRe = regexp.MustCompile(`^Rebuilt\s+by\b`)
)

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
	Depth      int                // levels of builds below the root; 0 means all
	OnProgress func(TreeProgress) // BuildTree reports how far it got; calls are serialized and end before it returns
	Shared     bool               // start from the scan of another recent tree of the root's plan, and lend this one on
	track      *treeTracker       // set by BuildTree and WatchTree
}

// TreeNode is one build of a tree, or a configured child that never ran.
type TreeNode struct {
	PlanKey    string
	Build      provider.Build // zero for a not-run node
	NotRun     bool           // a configured child plan with no build that names the parent
	Loop       bool           // the build already appears above; its children are not followed
	Superseded bool           // an older run of the same child plan; it does not count toward the tree
	FromLog    bool           // linked by a job log of its parent, not by its reason: a script started it
	Stage      string         // for FromLog, the parent's stage whose job log linked it
	Children   []*TreeNode    // by plan key, then build number, newest first
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

// TreeFinished reports whether a build in a tree has its final result.
// Bamboo's skipped is final too, though provider.State.Finished leaves it
// out; a skipped build is not a failure either.
func TreeFinished(s provider.State) bool { return s.Finished() || s == provider.StateSkipped }

// TreeState is running while any build runs, failed when any build did not
// succeed or a configured child did not run, success otherwise.
func TreeState(root *TreeNode) provider.State {
	running, bad := false, false
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.Superseded {
			return
		}
		switch {
		case n.NotRun:
			bad = true
		case !TreeFinished(n.Build.State):
			running = true
		case n.Build.State != provider.StateSuccess && n.Build.State != provider.StateSkipped:
			bad = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
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
	o.track = newTreeTracker(o.OnProgress)
	t, _, err := s.buildTree(ctx, key, o, nil, nil)
	return t, err
}

// buildTree is BuildTree reusing what a watch already knows: done holds
// builds that finished, so they are not read again, and prev is the last
// search, so the candidates are not listed again and each plan's newest
// builds are merged into it. It returns the search for the next refresh.
func (s *Service) buildTree(ctx context.Context, key string, o TreeOptions, done map[string]provider.Build, prev *treeScan) (Tree, *treeScan, error) {
	root, ok := done[key]
	if !ok {
		var err error
		if root, err = s.Build(ctx, key); err != nil {
			return Tree{}, nil, err
		}
	}
	configured, err := s.Cfg.Children()
	if err != nil {
		return Tree{}, nil, err
	}
	tr := o.track
	var warnings []string
	var known *TreeNode
	if prev == nil && o.Shared {
		var release func()
		if prev, release, err = s.awaitScan(ctx, root); err != nil {
			return Tree{}, nil, err
		}
		defer release()
		if t, ok := s.CachedTree(key); ok && prev == nil && len(t.Warnings) == 0 {
			known = t.Root
		}
	}
	scan := prev
	limit := treeRefresh
	if scan == nil {
		plans, projects, more, err := s.treeCandidates(ctx, tr, root.PlanKey, configured)
		if err != nil {
			return Tree{}, nil, err
		}
		warnings = more
		scan = &treeScan{plans: plans, listed: projects}
		limit = treeWindow
	}
	got, failed := s.treeRead(ctx, tr, scan.plans, limit)
	var depths map[string]int
	if prev == nil && known == nil {
		depths = s.treeDeepen(ctx, tr, got, root)
	}
	if err := ctx.Err(); err != nil {
		return Tree{}, nil, err
	}
	readWarnings := make([]string, 0, len(failed))
	for _, p := range scan.plans {
		if err, ok := failed[p]; ok {
			readWarnings = append(readWarnings, fmt.Sprintf("%s: its builds could not be read: %s", p, describeErr(err)))
		}
	}
	if len(scan.plans) > 0 && len(failed) == len(scan.plans) {
		return Tree{}, nil, errs.Bamboof("no candidate plan's builds could be read").WithWhy(readWarnings[0])
	}
	warnings = append(warnings, readWarnings...)
	if prev == nil {
		scan = scan.first(got, depths)
		if known != nil {
			scan.knew(known)
		}
	} else {
		scan = scan.merged(got)
	}
	warnings = append(warnings, s.treeParents(ctx, tr, scan)...)
	t := Tree{Root: &TreeNode{PlanKey: root.PlanKey, Build: root}}
	treeGrow(t.Root, scan.index(), configured, o.Depth, 0, map[string]bool{root.Key: true})
	warnings = append(warnings, s.treeDetails(ctx, tr, t.Root, done)...)
	warnings = append(warnings, s.treeLogLinks(ctx, tr, t.Root, scan, configured, o.Depth, done)...)
	if scan.covered != root.Key {
		warnings = append(warnings, windowWarnings(scan.windows, root)...)
	}
	if err := ctx.Err(); err != nil {
		return Tree{}, nil, err
	}
	sort.Strings(warnings)
	t.Warnings, t.State = warnings, TreeState(t.Root)
	if o.Shared {
		s.keepScan(root.PlanKey, scan)
	}
	return t, scan, nil
}

// treeCandidates is every plan of the scoped projects and of the root's
// project, and every plan the children map names, sorted. It also returns
// the projects it listed.
func (s *Service) treeCandidates(ctx context.Context, tr *treeTracker, rootPlan string, configured map[string][]string) ([]string, map[string]bool, []string, error) {
	set := map[string]bool{}
	projects := slices.Clone(s.ProjectKeys())
	if p, _, ok := strings.Cut(rootPlan, "-"); ok && !slices.Contains(projects, p) {
		projects = append(projects, p)
	}
	var warnings []string
	var firstErr error
	tr.add(treeProjects, len(projects))
	for _, p := range projects {
		plans, err := s.P.ListPlans(ctx, p)
		tr.done(treeProjects)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, ctx.Err()
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
		return nil, nil, nil, firstErr
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	listed := map[string]bool{}
	for _, p := range projects {
		listed[p] = true
	}
	return out, listed, warnings, nil
}

// planWindow is what the first search saw of one plan: how many builds it
// asked for, whether it got that many, and when its oldest returned build
// started.
type planWindow struct {
	read   int
	full   bool
	oldest time.Time
}

// oldestStart is when the earliest started of bs started; zero if none did.
func oldestStart(bs []provider.Build) time.Time {
	var t time.Time
	for _, b := range bs {
		if !b.StartedAt.IsZero() && (t.IsZero() || b.StartedAt.Before(t)) {
			t = b.StartedAt
		}
	}
	return t
}

// treeDeepen reads again, deeper, each plan whose full window started after
// the root: a busy child plan can run more than a window of builds after
// the root started, which pushes the root's own child out of the newest 25.
// It returns how many builds were asked for of each plan read deeper. A
// deeper read that fails keeps the window already read.
func (s *Service) treeDeepen(ctx context.Context, tr *treeTracker, got map[string][]provider.Build, root provider.Build) map[string]int {
	depths := map[string]int{}
	if root.StartedAt.IsZero() {
		return depths
	}
	for read := treeWindow; read < treeDeep && ctx.Err() == nil; {
		var short []string
		for plan, bs := range got {
			if depth(depths, plan) == read && len(bs) >= read && oldestStart(bs).After(root.StartedAt) {
				short = append(short, plan)
			}
		}
		if len(short) == 0 {
			break
		}
		sort.Strings(short)
		read = min(read*4, treeDeep)
		more, _ := s.treeRead(ctx, tr, short, read)
		for plan, bs := range more {
			got[plan], depths[plan] = bs, read
		}
	}
	return depths
}

// depth is how many builds of plan were asked for: treeWindow unless
// treeDeepen read it deeper.
func depth(depths map[string]int, plan string) int {
	if d, ok := depths[plan]; ok {
		return d
	}
	return treeWindow
}

// treeScan is what a tree search has read: the candidate plans, each plan's
// builds, newest first, the first search's windows, and the parents of
// restarted builds, read once each.
type treeScan struct {
	plans    []string
	listed   map[string]bool // projects whose plans are in plans
	builds   map[string][]provider.Build
	windows  map[string]planWindow
	parents  map[string]string   // build key to parent key; "" for none or given up
	misses   map[string]int      // failed parent reads in a row, by build key
	logLinks map[string][]string // build keys a finished job's log links to, by job key
	covered  string              // root whose saved tree supplied its older children, so its windows warn of nothing
}

// knew lays the builds of root's saved final tree under the scan's, so its
// children older than a window are found without reading deeper: an older
// build's deep read happens once (#82).
func (sc *treeScan) knew(root *TreeNode) {
	root.Walk(func(n *TreeNode, depth int) {
		if depth == 0 || n.NotRun || n.Loop || slices.ContainsFunc(sc.builds[n.PlanKey], func(b provider.Build) bool { return b.Key == n.Build.Key }) {
			return
		}
		sc.builds[n.PlanKey] = append(sc.builds[n.PlanKey], n.Build)
	})
	for _, bs := range sc.builds {
		sort.Slice(bs, func(i, j int) bool { return bs[i].Number > bs[j].Number })
	}
	sc.covered = root.Build.Key
}

// first is the scan of a first search: its builds and their windows.
// depths holds the plans treeDeepen read deeper than a window.
func (sc *treeScan) first(got map[string][]provider.Build, depths map[string]int) *treeScan {
	out := &treeScan{plans: sc.plans, listed: sc.listed, builds: got, windows: map[string]planWindow{}, parents: map[string]string{}, misses: map[string]int{}, logLinks: map[string][]string{}}
	for plan, bs := range got {
		read := depth(depths, plan)
		out.windows[plan] = planWindow{read: read, full: len(bs) >= read, oldest: oldestStart(bs)}
	}
	return out
}

// merged is the scan with a refresh's builds laid over it: a build read
// again replaces its older copy, a new build is added, and a plan that
// could not be read keeps what was found before.
func (sc *treeScan) merged(got map[string][]provider.Build) *treeScan {
	out := &treeScan{plans: sc.plans, listed: sc.listed, builds: map[string][]provider.Build{}, windows: sc.windows, parents: maps.Clone(sc.parents), misses: maps.Clone(sc.misses), logLinks: sc.logLinks, covered: sc.covered}
	for plan, bs := range sc.builds {
		out.builds[plan] = bs
	}
	for plan, fresh := range got {
		byKey := map[string]provider.Build{}
		for _, b := range out.builds[plan] {
			byKey[b.Key] = b
		}
		for _, b := range fresh {
			byKey[b.Key] = b
		}
		all := make([]provider.Build, 0, len(byKey))
		for _, b := range byKey {
			all = append(all, b)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Number > all[j].Number })
		out.builds[plan] = all
	}
	return out
}

// index is every child build under its parent: the one its reason names,
// or for a restarted build the one its metadata names.
func (sc *treeScan) index() map[string][]provider.Build {
	idx := map[string][]provider.Build{}
	for _, plan := range sc.plans {
		for _, b := range sc.builds[plan] {
			parent, ok := ParentOf(b.Reason)
			if !ok {
				parent = sc.parents[b.Key]
			}
			if parent != "" {
				idx[parent] = append(idx[parent], b)
			}
		}
	}
	return idx
}

// unresolved reports whether a restarted build's parent is still unknown: a
// watch must not settle then, since the build may belong to the tree.
func (sc *treeScan) unresolved() bool {
	for _, plan := range sc.plans {
		for _, b := range sc.builds[plan] {
			if _, asked := sc.parents[b.Key]; !asked && rebuiltRe.MatchString(b.Reason) {
				return true
			}
		}
	}
	return false
}

// treeParents reads, with GetBuild, the parent of each restarted build the
// scan has not asked about yet: a restart rewrites a child's reason to
// "Rebuilt by ...", and only the build's metadata still names its parent. A
// parent never changes, so each build is asked once; one that cannot be read
// is asked again on the next refresh, up to treeTries times in a row.
func (s *Service) treeParents(ctx context.Context, tr *treeTracker, sc *treeScan) []string {
	var keys []string
	for _, plan := range sc.plans {
		for _, b := range sc.builds[plan] {
			if _, asked := sc.parents[b.Key]; asked || !rebuiltRe.MatchString(b.Reason) {
				continue
			}
			keys = append(keys, b.Key)
		}
	}
	parents := make([]string, len(keys))
	readErrs := make([]error, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	tr.add(treeBuilds, len(keys))
	for i, k := range keys {
		wg.Add(1)
		go func(i int, k string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.P.GetBuild(ctx, k)
			tr.done(treeBuilds)
			parents[i], readErrs[i] = b.Parent, err
		}(i, k)
	}
	wg.Wait()
	var warnings []string
	for i, k := range keys {
		if readErrs[i] != nil {
			if ctx.Err() != nil {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s: its parent could not be read: %s", k, describeErr(readErrs[i])))
			if sc.misses[k]++; sc.misses[k] >= treeTries {
				sc.parents[k] = "" // give up, so the build cannot hold a watch open
			}
			continue
		}
		delete(sc.misses, k)
		sc.parents[k] = parents[i]
	}
	// A build given up on stays named, so the final tree says what it may miss.
	for k, n := range sc.misses {
		if n >= treeTries && !slices.Contains(keys, k) {
			warnings = append(warnings, fmt.Sprintf("%s: its parent could not be read, so it is left out", k))
		}
	}
	return warnings
}

// treeRead reads the newest limit builds of every plan, treeFetch at a time.
func (s *Service) treeRead(ctx context.Context, tr *treeTracker, plans []string, limit int) (map[string][]provider.Build, map[string]error) {
	type result struct {
		builds []provider.Build
		err    error
	}
	results := make([]result, len(plans))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	tr.add(treePlans, len(plans))
	for i, p := range plans {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			// Unfinished: a child that is queued or running is part of the tree.
			b, err := s.P.ListBuilds(ctx, p, provider.ListOptions{Limit: limit, Unfinished: true})
			tr.done(treePlans)
			results[i] = result{builds: b, err: err}
		}(i, p)
	}
	wg.Wait()
	got := map[string][]provider.Build{}
	failed := map[string]error{}
	for i, r := range results {
		if r.err != nil {
			failed[plans[i]] = r.err
			continue
		}
		got[plans[i]] = r.builds
	}
	return got, failed
}

// sortKids orders children by plan key, then build number, newest first,
// and marks every run of a child plan but the newest as superseded.
func sortKids(kids []*TreeNode) {
	sort.SliceStable(kids, func(i, j int) bool {
		if kids[i].PlanKey != kids[j].PlanKey {
			return kids[i].PlanKey < kids[j].PlanKey
		}
		return kids[i].Build.Number > kids[j].Build.Number
	})
	for i := 1; i < len(kids); i++ {
		kids[i].Superseded = !kids[i].NotRun && kids[i].PlanKey == kids[i-1].PlanKey
	}
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
	sortKids(kids)
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
func (s *Service) treeDetails(ctx context.Context, tr *treeTracker, root *TreeNode, done map[string]provider.Build) []string {
	var nodes []*TreeNode
	root.Walk(func(n *TreeNode, depth int) {
		if depth > 0 && !n.NotRun && !n.Loop {
			nodes = append(nodes, n)
		}
	})
	warnings := make([]string, len(nodes))
	reads := 0
	for _, n := range nodes {
		if _, ok := done[n.Build.Key]; !ok {
			reads++
		}
	}
	tr.add(treeBuilds, reads)
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
			tr.done(treeBuilds)
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
			out = append(out, fmt.Sprintf("%s: only its last %d builds were searched; older children may be missing", plan, w.read))
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
	TreeLoading TreeEventType = "loading" // the first refresh is still reading; Progress says how far
	TreeUpdate  TreeEventType = "update"  // the tree changed (always sent for the first refresh)
	TreeDone    TreeEventType = "done"    // every build finished and nothing new appeared; last event
	TreeError   TreeEventType = "error"   // refreshes kept failing; last event
)

// TreeEvent is one step of a watched tree.
type TreeEvent struct {
	Type     TreeEventType
	Time     time.Time
	Tree     Tree
	Progress TreeProgress // for TreeLoading
	Err      error
}

// WatchTree refreshes the tree of key every treePoll and emits it when it
// changed. It ends once every build has finished and one more refresh
// found nothing new, since Bamboo queues children when their parent ends.
// Until the first tree, it sends TreeLoading events saying how far the
// search got; o.OnProgress is not called. Ending ctx stops watching; no
// build is stopped.
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
		var scan *treeScan
		fails := 0
		for {
			ro := o
			ro.track = nil
			if scan == nil {
				// The first refresh is the slow one: say how far it got. A
				// full channel drops the snapshot rather than hold up the
				// load; a newer one follows.
				ro.track = newTreeTracker(func(p TreeProgress) {
					select {
					case ch <- TreeEvent{Type: TreeLoading, Time: s.Clock.Now(), Progress: p}:
					default:
					}
				})
			}
			t, next, err := s.buildTree(ctx, key, ro, done, scan)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fails++
				// The first refresh, or an error retrying cannot fix (a
				// mistyped build, a bad children map, a refused token),
				// ends the watch at once.
				if scan == nil || permanentTreeErr(err) || fails >= treeTries {
					send(TreeEvent{Type: TreeError, Err: err})
					return
				}
			} else {
				scan, fails = next, 0
				t.Root.Walk(func(n *TreeNode, _ int) {
					if !n.NotRun && !n.Loop && TreeFinished(n.Build.State) {
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
				if t.State == provider.StateRunning || scan.unresolved() {
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

// permanentTreeErr reports whether retrying a refresh cannot help.
func permanentTreeErr(err error) bool {
	switch errs.KindOf(err) {
	case errs.KindConfig, errs.KindAuth, errs.KindUsage:
		return true
	}
	return errors.Is(err, errs.ErrNotFound)
}
