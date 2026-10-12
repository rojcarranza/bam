package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
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

// treeAt is a node of a tree, its depth below the root, and the builds
// from the root to it.
type treeAt struct {
	n     *TreeNode
	level int
	path  []string
}

// treeOpen is every node at or below n that can have children of its own,
// with its depth and path, and marks every build at or below n in inTree.
func treeOpen(n *TreeNode, level int, path []string, inTree map[string]bool) []treeAt {
	if n.NotRun {
		return nil
	}
	inTree[n.Build.Key] = true
	if n.Loop || n.Superseded {
		return nil
	}
	path = append(slices.Clone(path), n.Build.Key)
	out := []treeAt{{n, level, path}}
	for _, k := range n.Children {
		out = append(out, treeOpen(k, level+1, path, inTree)...)
	}
	return out
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
// is skipped. A linked build of a project the search has not listed adds
// that project's plans to sc, so its children by reason are found, and a
// watch keeps reading them. Finished jobs' links are kept in sc, so a watch
// reads each finished log once.
func (s *Service) treeLogLinks(ctx context.Context, root *TreeNode, sc *treeScan, configured map[string][]string, depth int, done map[string]provider.Build) []string {
	inTree := map[string]bool{}
	frontier := treeOpen(root, 0, nil, inTree)
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

		var fetched []provider.Build
		for i, b := range builds {
			if readErrs[i] == nil {
				fetched = append(fetched, b)
			}
		}
		warnings = append(warnings, s.treeAddProjects(ctx, sc, fetched, root.Build)...)
		idx := sc.index()

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
			path := map[string]bool{kid.Build.Key: true}
			for _, k := range f.at.path {
				path[k] = true
			}
			treeGrow(kid, idx, configured, depth, f.at.level+1, path)
			warnings = append(warnings, s.treeDetails(ctx, kid, done)...)
			f.at.n.Children = append(f.at.n.Children, kid)
			grown[f.at.n] = true
			frontier = append(frontier, treeOpen(kid, f.at.level+1, f.at.path, inTree)...)
		}
		for n := range grown {
			sortKids(n.Children)
		}
	}
	return warnings
}

// treeAddProjects lists the projects of builds the search has not listed,
// and reads the newest builds of their plans not yet in sc, as the first
// search reads every candidate (deeper for a busy plan, back to root's
// start). It adds them to sc and returns the warnings.
func (s *Service) treeAddProjects(ctx context.Context, sc *treeScan, builds []provider.Build, root provider.Build) []string {
	var warnings []string
	var plans []string
	for _, b := range builds {
		project, _, ok := strings.Cut(b.PlanKey, "-")
		if !ok || sc.listed[project] {
			continue
		}
		sc.listed[project] = true
		ps, err := s.P.ListPlans(ctx, project)
		if err != nil {
			if ctx.Err() == nil {
				warnings = append(warnings, fmt.Sprintf("project %s: its plans could not be listed: %s", project, describeErr(err)))
			}
			continue
		}
		for _, p := range ps {
			if !slices.Contains(sc.plans, p.Key) && !slices.Contains(plans, p.Key) {
				plans = append(plans, p.Key)
			}
		}
	}
	if len(plans) == 0 {
		return warnings
	}
	got, failed := s.treeRead(ctx, plans, treeWindow)
	depths := s.treeDeepen(ctx, got, root)
	for _, p := range plans {
		if err, ok := failed[p]; ok {
			warnings = append(warnings, fmt.Sprintf("%s: its builds could not be read: %s", p, describeErr(err)))
			continue
		}
		bs := got[p]
		read := depth(depths, p)
		sc.builds[p] = bs
		sc.windows[p] = planWindow{read: read, full: len(bs) >= read, oldest: oldestStart(bs)}
		sc.plans = append(sc.plans, p)
	}
	sort.Strings(sc.plans)
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
