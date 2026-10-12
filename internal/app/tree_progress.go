package app

import "sync"

// TreeCount is one phase of a tree load: requests finished and requests
// known so far. Total only grows.
type TreeCount struct{ Done, Total int }

// TreeProgress is a snapshot of a tree load.
type TreeProgress struct {
	Projects TreeCount // projects whose plans were listed
	Plans    TreeCount // plan build reads, the deeper reads included
	Builds   TreeCount // build reads: details, restarted builds' parents, builds a log links to
	Jobs     TreeCount // job logs read for the builds a script started
}

// treePhase names a TreeProgress count.
type treePhase int

const (
	treeProjects treePhase = iota
	treePlans
	treeBuilds
	treeJobs
)

// treeTracker counts a tree load's requests and reports every change, one
// call at a time, in order. A nil tracker counts nothing.
type treeTracker struct {
	mu     sync.Mutex
	p      TreeProgress
	report func(TreeProgress)
}

func newTreeTracker(report func(TreeProgress)) *treeTracker {
	if report == nil {
		return nil
	}
	return &treeTracker{report: report}
}

func (t *treeTracker) count(ph treePhase) *TreeCount {
	switch ph {
	case treeProjects:
		return &t.p.Projects
	case treePlans:
		return &t.p.Plans
	case treeBuilds:
		return &t.p.Builds
	}
	return &t.p.Jobs
}

// add raises ph's total by n, before its requests start.
func (t *treeTracker) add(ph treePhase, n int) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count(ph).Total += n
	t.report(t.p)
}

// done counts one request of ph as ended, whether it succeeded or not, so
// the count never stalls short of its total.
func (t *treeTracker) done(ph treePhase) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count(ph).Done++
	t.report(t.p)
}
