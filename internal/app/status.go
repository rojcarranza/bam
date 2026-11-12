package app

import (
	"context"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// StatusWindow is how far back bam status looks in the journal.
const StatusWindow = 24 * time.Hour

// StatusEntry is one journal run with what the server says about it now.
type StatusEntry struct {
	Run      JournalRun
	Build    provider.Build
	Progress provider.Progress
	Err      error // the build could not be read; Build is then empty
	Here     bool  // the build of the branch checked out in this repository
}

// RecentRuns returns the journal runs started within StatusWindow of now,
// newest first.
func RecentRuns(runs []JournalRun, now time.Time) []JournalRun {
	var out []JournalRun
	for _, r := range runs {
		if now.Sub(r.StartedAt) <= StatusWindow {
			out = append(out, r)
		}
	}
	return out
}

// Status reads the live state of runs, all started on this service's server.
// A run whose build cannot be read keeps its error and does not stop the
// others.
func (s *Service) Status(ctx context.Context, runs []JournalRun) []StatusEntry {
	out := make([]StatusEntry, len(runs))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, r := range runs {
		out[i].Run = r
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := s.Build(ctx, r.BuildKey)
			if err != nil {
				out[i].Err = err
				return
			}
			out[i].Build = b
			if !b.State.Finished() {
				out[i].Progress, _ = s.Progress(ctx, b)
			}
		}()
	}
	wg.Wait()
	return out
}

// MarkHere marks the build of the branch checked out in repoRoot: the newest
// run started from that repository on a plan branch named like it, or, when
// there is none, the newest on the default branch. Others stay unmarked.
func MarkHere(es []StatusEntry, repoRoot, branch string) {
	if repoRoot == "" || branch == "" {
		return
	}
	best := func(match func(JournalRun) bool) int {
		pick := -1
		for i, e := range es {
			if e.Run.RepoRoot == repoRoot && match(e.Run) && (pick < 0 || e.Run.StartedAt.After(es[pick].Run.StartedAt)) {
				pick = i
			}
		}
		return pick
	}
	pick := best(func(r JournalRun) bool { return r.Branch == branch })
	if pick < 0 {
		pick = best(func(r JournalRun) bool { return r.Branch == "" })
	}
	if pick >= 0 {
		es[pick].Here = true
	}
}
