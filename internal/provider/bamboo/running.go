package bamboo

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rojcarranza/bam/internal/provider"
)

// runningBranchBudget is how long RunningBuilds reads the plan list that holds
// each branch's current build before it gives that list up for this call, so
// a server with thousands of branches does not stall the board.
const runningBranchBudget = 20 * time.Second

// runningReaders is how many branch builds RunningBuilds reads at once.
const runningReaders = 6

type activeDTO struct {
	Key            string `json:"key"`
	LifeCycleState string `json:"lifeCycleState"`
}

type runningBranchDTO struct {
	Key                   string     `json:"key"`
	LatestCurrentlyActive *activeDTO `json:"latestCurrentlyActive"`
}

type runningPlanDTO struct {
	Key      string `json:"key"`
	Branches struct {
		Branch []runningBranchDTO `json:"branch"`
	} `json:"branches"`
}

// RunningBuilds lists the builds in progress: those of main plans from the
// server-wide result list, which holds one build for each plan and leaves out
// plan branches, then the running build of each plan branch from the plan
// list, one read for each. Each list is an optional feature, learned once: a
// server that lacks one answers without it, flagged in the result.
func (c *Client) RunningBuilds(ctx context.Context) (provider.RunningBuilds, error) {
	var out provider.RunningBuilds
	caps := c.fresh(ctx)

	if caps.RunningMain == "no" {
		out.MainMissing = true
	} else {
		q := url.Values{"includeAllStates": {"true"}, "lifeCycleState": {"InProgress"},
			"expand": {"results.result.stages.stage.results.result"}}
		items, err := pageAll[resultDTO](ctx, c, api+"/result", q, "results", 0)
		switch {
		case err == nil:
			if caps.RunningMain != "yes" {
				c.learn(ctx, func(cp *Capabilities) { cp.RunningMain = "yes" })
			}
			for _, r := range items {
				if b := c.mapBuild(r); b.State == provider.StateRunning { // the filter may be ignored
					out.Builds = append(out.Builds, b)
				}
			}
		case isUnsupportedStatus(err):
			c.learn(ctx, func(cp *Capabilities) { cp.RunningMain = "no" })
			out.MainMissing = true
		default:
			return provider.RunningBuilds{}, err
		}
	}

	if caps.RunningBranches == "no" {
		out.BranchesMissing = true
		return out, nil
	}
	keys, err := c.activeBranchBuilds(ctx)
	switch {
	case err == nil:
		if caps.RunningBranches != "yes" {
			c.learn(ctx, func(cp *Capabilities) { cp.RunningBranches = "yes" })
		}
	case ctx.Err() != nil:
		return provider.RunningBuilds{}, ctx.Err()
	case isUnsupportedStatus(err):
		c.learn(ctx, func(cp *Capabilities) { cp.RunningBranches = "no" })
		out.BranchesMissing = true
		return out, nil
	case errors.Is(err, context.DeadlineExceeded): // the budget: not learned, tried again next time
		out.BranchesMissing = true
		return out, nil
	default:
		return provider.RunningBuilds{}, err
	}
	got := make([]*provider.Build, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runningReaders)
	for i, k := range keys {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if b, err := c.GetBuild(ctx, k); err == nil && b.State == provider.StateRunning {
				got[i] = &b
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return provider.RunningBuilds{}, err
	}
	for _, b := range got {
		if b != nil {
			out.Builds = append(out.Builds, *b)
		}
	}
	return out, nil
}

// activeBranchBuilds lists the keys of the builds in progress on plan
// branches, within runningBranchBudget.
func (c *Client) activeBranchBuilds(ctx context.Context) ([]string, error) {
	read, cancel := context.WithTimeout(ctx, runningBranchBudget)
	defer cancel()
	q := url.Values{"expand": {"plans.plan.branches[0:" + queueListMax + "].branch.latestCurrentlyActive"}}
	plans, err := pageAll[runningPlanDTO](read, c, api+"/plan", q, "plans", 0)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, p := range plans {
		for _, b := range p.Branches.Branch {
			if a := b.LatestCurrentlyActive; a != nil && strings.EqualFold(a.LifeCycleState, "InProgress") && a.Key != "" {
				keys = append(keys, a.Key)
			}
		}
	}
	return keys, nil
}
