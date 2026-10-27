package app

import (
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
	mu sync.Mutex
	m  map[string]sharedScan // root plan key → scan
}

type sharedScan struct {
	scan *treeScan
	at   time.Time
}

// sharedScan is a copy of the scan of root's plan, or nil when there is
// none, it is older than treeShareAge, or a plan's window stops after root
// started, which only a full scan reads deeper.
func (s *Service) sharedScan(root provider.Build) *treeScan {
	s.scans.mu.Lock()
	defer s.scans.mu.Unlock()
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
