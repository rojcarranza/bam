package app

import (
	"context"

	"github.com/r0jjames/bam-cli/internal/provider"
)

// RerunCandidates is the topmost failed or stopped builds of a tree: each
// such build whose ancestors all succeeded (or were skipped), in tree order.
// When one succeeds again Bamboo triggers its children itself, so nothing
// below a candidate is one. Not-run, loop and superseded nodes never are.
func RerunCandidates(t Tree) []*TreeNode {
	var out []*TreeNode
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.NotRun || n.Loop || n.Superseded {
			return
		}
		switch n.Build.State {
		case provider.StateFailed, provider.StateStopped:
			out = append(out, n)
		case provider.StateSuccess, provider.StateSkipped:
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	if t.Root != nil {
		walk(t.Root)
	}
	return out
}

// RerunResult is what happened to one candidate.
type RerunResult struct {
	Key       string
	Restarted bool // false: it was running or had succeeded, so nothing was restarted
	Err       error
}

// RerunTree restarts each candidate's failed jobs with Restart, one at a
// time, in order. A failed restart does not stop the others; ending ctx does.
func (s *Service) RerunTree(ctx context.Context, cands []*TreeNode) []RerunResult {
	out := make([]RerunResult, 0, len(cands))
	for _, n := range cands {
		if ctx.Err() != nil {
			break
		}
		_, restarted, err := s.Restart(ctx, n.Build.Key)
		out = append(out, RerunResult{Key: n.Build.Key, Restarted: restarted, Err: err})
	}
	return out
}
