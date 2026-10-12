package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// buildLinkRe is a link to a build's page, .../browse/PROJ-PLAN-42. A job's
// page (PROJ-PLAN-JOB1-42) and a plan's page (PROJ-PLAN) do not match.
var buildLinkRe = regexp.MustCompile(`/browse/([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+)(?:[^A-Za-z0-9-]|$)`)

// BuildLinks returns the build keys lines link to, in order, once each. A
// script that starts plans through the REST API, rather than through child
// dependencies, prints such a link for each build it started.
func BuildLinks(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		for _, m := range buildLinkRe.FindAllStringSubmatch(l, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

// treeAt is a node of a tree and its depth below the root.
type treeAt struct {
	n     *TreeNode
	level int
}

// logLink is a build a job's log links to, and the stage of that job.
type logLink struct {
	key, stage string
}

// treeLogLinks adds to the tree the builds that its builds' job logs link
// to: builds a script started, which name no parent in their reason. A
// linked build becomes a child of the build whose log named it, gets its
// own children by reason, and has its logs read in turn. A link to a build
// already in the tree, to a build Bamboo does not know, or to a build that
// started before the build whose log names it (one the log only mentions)
// is skipped. Finished jobs' links are kept in sc, so a watch reads each
// finished log once.
func (s *Service) treeLogLinks(ctx context.Context, root *TreeNode, sc *treeScan, idx map[string][]provider.Build, configured map[string][]string, depth int, done map[string]provider.Build) []string {
	inTree := map[string]bool{}
	var frontier []treeAt
	root.Walk(func(n *TreeNode, level int) {
		if n.NotRun {
			return
		}
		inTree[n.Build.Key] = true
		if !n.Loop && !n.Superseded {
			frontier = append(frontier, treeAt{n, level})
		}
	})
	var warnings []string
	for len(frontier) > 0 && ctx.Err() == nil {
		var open []treeAt
		for _, at := range frontier {
			if depth == 0 || at.level < depth {
				open = append(open, at)
			}
		}
		links, w := s.treeReadLinks(ctx, open, sc)
		warnings = append(warnings, w...)

		type found struct {
			at   treeAt
			link logLink
		}
		var todo []found
		for i, at := range open {
			for _, l := range links[i] {
				if !inTree[l.key] {
					inTree[l.key] = true
					todo = append(todo, found{at, l})
				}
			}
		}
		builds := make([]provider.Build, len(todo))
		readErrs := make([]error, len(todo))
		var wg sync.WaitGroup
		sem := make(chan struct{}, treeFetch)
		for i, f := range todo {
			if b, ok := done[f.link.key]; ok {
				builds[i] = b
				continue
			}
			wg.Add(1)
			go func(i int, key string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				builds[i], readErrs[i] = s.P.GetBuild(ctx, key)
			}(i, f.link.key)
		}
		wg.Wait()

		frontier = nil
		grown := map[*TreeNode]bool{}
		for i, f := range todo {
			b := builds[i]
			if err := readErrs[i]; err != nil {
				if ctx.Err() == nil && !errors.Is(err, errs.ErrNotFound) {
					warnings = append(warnings, fmt.Sprintf("%s: linked from %s's log, but it could not be read: %s", f.link.key, f.at.n.Build.Key, describeErr(err)))
				}
				continue
			}
			if parent := f.at.n.Build.StartedAt; !parent.IsZero() && !b.StartedAt.IsZero() && b.StartedAt.Before(parent) {
				continue
			}
			kid := &TreeNode{PlanKey: b.PlanKey, Build: b, FromLog: true, Stage: f.link.stage}
			treeGrow(kid, idx, configured, depth, f.at.level+1, map[string]bool{kid.Build.Key: true})
			warnings = append(warnings, s.treeDetails(ctx, kid, done)...)
			f.at.n.Children = append(f.at.n.Children, kid)
			grown[f.at.n] = true
			kid.Walk(func(n *TreeNode, level int) {
				if n.NotRun {
					return
				}
				inTree[n.Build.Key] = true
				if !n.Loop && !n.Superseded {
					frontier = append(frontier, treeAt{n, f.at.level + 1 + level})
				}
			})
		}
		for n := range grown {
			sortKids(n.Children)
		}
	}
	return warnings
}

// treeReadLinks reads the logs of the started jobs of each node, treeFetch
// at a time, and returns the builds each node's logs link to. A job with no
// log yet adds nothing; one whose log cannot be read adds a warning.
func (s *Service) treeReadLinks(ctx context.Context, nodes []treeAt, sc *treeScan) ([][]logLink, []string) {
	type jobAt struct {
		node  int
		stage string
		job   provider.Job
	}
	var jobs []jobAt
	for i, at := range nodes {
		for _, st := range at.n.Build.Stages {
			for _, j := range st.Jobs {
				if !notStarted(j.State) {
					jobs = append(jobs, jobAt{i, st.Name, j})
				}
			}
		}
	}
	keys := make([][]string, len(jobs))
	readErrs := make([]error, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, treeFetch)
	for i, j := range jobs {
		if k, ok := sc.logLinks[j.job.Key]; ok {
			keys[i] = k
			continue
		}
		wg.Add(1)
		go func(i int, j jobAt) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			chunk, err := s.P.FetchLog(ctx, j.job.Key, provider.LogOptions{})
			if err != nil {
				if !errors.Is(err, errs.ErrNotFound) {
					readErrs[i] = err
				}
				return
			}
			keys[i] = BuildLinks(chunk.Lines)
		}(i, j)
	}
	wg.Wait()

	out := make([][]logLink, len(nodes))
	var warnings []string
	for i, j := range jobs {
		if err := readErrs[i]; err != nil {
			if ctx.Err() == nil {
				warnings = append(warnings, fmt.Sprintf("%s: its log could not be read for the builds it started: %s", j.job.Key, describeErr(err)))
			}
			continue
		}
		if _, cached := sc.logLinks[j.job.Key]; !cached && j.job.State.Finished() {
			sc.logLinks[j.job.Key] = keys[i]
		}
		own := nodes[j.node].n.Build.Key
		for _, k := range keys[i] {
			if k != own {
				out[j.node] = append(out[j.node], logLink{k, j.stage})
			}
		}
	}
	return out, warnings
}
