package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// historySample is how many successful builds a local estimate's median is
// taken from; minHistory is how many it needs at least.
const (
	historySample = 10
	minHistory    = 3
)

// estimateCache keeps each plan branch's median for the life of the
// process, so a plan's history is read at most once.
type estimateCache struct {
	mu sync.Mutex
	m  map[string]time.Duration // plan key → median; 0 with too little history
}

// historyMedian is the median duration of planKey's last successful builds:
// the lower middle value of an even count, always a real build's duration.
func (s *Service) historyMedian(ctx context.Context, planKey string) (time.Duration, error) {
	s.estimates.mu.Lock()
	if d, ok := s.estimates.m[planKey]; ok {
		s.estimates.mu.Unlock()
		return d, nil
	}
	s.estimates.mu.Unlock()
	builds, err := s.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: historySample, State: provider.StateSuccess})
	if err != nil {
		// A plan whose history cannot be read (no permission, a server
		// error) is kept as "no estimate", or a watch would repeat the read
		// on every poll. An interrupt says nothing about the plan, so it is
		// not kept.
		if ctx.Err() == nil {
			s.keepMedian(planKey, 0)
		}
		return 0, err
	}
	var ds []time.Duration
	for _, b := range builds {
		if b.State == provider.StateSuccess && b.Duration > 0 {
			ds = append(ds, b.Duration)
		}
	}
	var med time.Duration
	if len(ds) >= minHistory {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		med = ds[(len(ds)-1)/2]
	}
	s.keepMedian(planKey, med)
	return med, nil
}

func (s *Service) keepMedian(planKey string, med time.Duration) {
	s.estimates.mu.Lock()
	defer s.estimates.mu.Unlock()
	if s.estimates.m == nil {
		s.estimates.m = map[string]time.Duration{}
	}
	s.estimates.m[planKey] = med
}

// historyProgress estimates a running build from its plan branch's history,
// for a server that reports no progress. A build that is not running, or a
// plan with too little history, gets no estimate.
func (s *Service) historyProgress(ctx context.Context, b provider.Build) provider.Progress {
	if b.State != provider.StateRunning || b.StartedAt.IsZero() {
		return provider.Progress{}
	}
	med, err := s.historyMedian(ctx, b.PlanKey)
	if err != nil || med <= 0 {
		return provider.Progress{}
	}
	elapsed := max(s.Clock.Now().Sub(b.StartedAt), 0)
	p := provider.Progress{Valid: true, Average: med, Elapsed: elapsed, Percent: 1, Source: "history", Stage: runningStage(b)}
	if elapsed < med {
		p.Remaining = med - elapsed
		p.Percent = float64(elapsed) / float64(med)
	}
	return p
}

// runningStage is the stage of b that is running now, or "".
func runningStage(b provider.Build) string {
	for _, st := range b.Stages {
		if st.State == provider.StateRunning {
			return st.Name
		}
	}
	return ""
}
