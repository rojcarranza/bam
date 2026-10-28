package app

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// treeShareAge is how long a scan is lent to another build of its plan: a
// newer scan reads only each plan's treeRefresh newest builds, so it must
// not miss more children than that.
const treeShareAge = 2 * time.Minute

// scanCache is the newest tree scan of each root plan, so the trees of one
// plan's builds share a single candidate scan (#82).
type scanCache struct {
	mu       sync.Mutex
	m        map[string]sharedScan  // root plan key → scan
	inflight map[string]*scanFlight // root plan key → the full scan being read
}

// scanFlight is a full scan in flight; done closes when it ends.
type scanFlight struct {
	done    chan struct{}
	waiters int
}

type sharedScan struct {
	scan *treeScan
	at   time.Time
}

// awaitScan is a copy of the scan of root's plan, or nil when there is
// none, it is older than treeShareAge, or a plan's window stops after root
// started, which only a full scan reads deeper. While another shared read
// of the plan runs a full scan it waits for that scan rather than start a
// second one (#82). When it returns nil the caller runs the full scan and
// must call release once it has kept it.
func (s *Service) awaitScan(ctx context.Context, root provider.Build) (sc *treeScan, release func(), err error) {
	s.scans.mu.Lock()
	defer s.scans.mu.Unlock()
	for {
		if sc := s.lendable(root); sc != nil {
			return sc, func() {}, nil
		}
		f, ok := s.scans.inflight[root.PlanKey]
		if !ok {
			break
		}
		f.waiters++
		s.scans.mu.Unlock()
		select {
		case <-f.done:
		case <-ctx.Done():
		}
		s.scans.mu.Lock()
		f.waiters--
		if err := ctx.Err(); err != nil {
			return nil, func() {}, err
		}
	}
	if s.scans.inflight == nil {
		s.scans.inflight = map[string]*scanFlight{}
	}
	f := &scanFlight{done: make(chan struct{})}
	s.scans.inflight[root.PlanKey] = f
	return nil, func() {
		s.scans.mu.Lock()
		defer s.scans.mu.Unlock()
		delete(s.scans.inflight, root.PlanKey)
		close(f.done)
	}, nil
}

// lendable is a copy of the scan awaitScan may lend to root; s.scans.mu is
// held.
func (s *Service) lendable(root provider.Build) *treeScan {
	sh, ok := s.scans.m[root.PlanKey]
	if !ok || s.Clock.Now().Sub(sh.at) > treeShareAge {
		return nil
	}
	if !root.StartedAt.IsZero() {
		for _, w := range sh.scan.windows {
			if w.full && w.read < treeDeep && w.oldest.After(root.StartedAt) {
				return nil
			}
		}
	}
	return sh.scan.clone()
}

// keepScan lends a copy of sc to the next tree of plan's builds.
func (s *Service) keepScan(plan string, sc *treeScan) {
	s.scans.mu.Lock()
	defer s.scans.mu.Unlock()
	if s.scans.m == nil {
		s.scans.m = map[string]sharedScan{}
	}
	s.scans.m[plan] = sharedScan{scan: sc.clone(), at: s.Clock.Now()}
}

// clone is a copy of sc that a refresh can change without changing sc.
func (sc *treeScan) clone() *treeScan {
	return &treeScan{
		plans:    slices.Clone(sc.plans),
		listed:   maps.Clone(sc.listed),
		builds:   maps.Clone(sc.builds),
		windows:  maps.Clone(sc.windows),
		parents:  maps.Clone(sc.parents),
		misses:   maps.Clone(sc.misses),
		logLinks: maps.Clone(sc.logLinks),
		covered:  sc.covered,
	}
}
