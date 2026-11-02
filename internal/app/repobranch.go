package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// Repository branches for a run (issue #130). Bamboo has no REST call that
// changes a plan's repositories, so bam runs every plan of the run on a plan
// branch with one shared name: on a plan whose default repository the user
// named, the plan branch builds the chosen branch of it; Bamboo starts a
// child plan's branch of the same name when the parent's branch finishes.
// See docs/superpowers/specs/2026-11-02-bam-run-repo-branch-design.md.

// ParseRepoBranchFlag reads one --repo-branch value: REPO=BRANCH or
// PLAN:REPO=BRANCH.
func ParseRepoBranchFlag(s string) (config.RepoBranch, error) {
	key, branch, ok := strings.Cut(s, "=")
	r, keyOK := config.ParseRepoKey(key)
	if !ok || !keyOK || strings.TrimSpace(branch) == "" {
		return config.RepoBranch{}, errs.Usagef("--repo-branch %q is not REPO=BRANCH or PLAN:REPO=BRANCH", s).
			WithTry("--repo-branch repo-a=feature/PROJ-101, or PROJ-CHILD:repo-a=feature/PROJ-101 for one plan")
	}
	r.Branch = branch
	return r, nil
}

// RepoBranchOverrides merges --repo-branch values with a preset's
// repo_branches. Flags come first, in the order given; then the preset's
// entries, by key, that no flag replaced. A key given twice on the command
// line is a usage error.
func RepoBranchOverrides(flags []string, t *config.ResolvedTarget) ([]config.RepoBranch, error) {
	var out []config.RepoBranch
	seen := map[string]bool{}
	for _, f := range flags {
		r, err := ParseRepoBranchFlag(f)
		if err != nil {
			return nil, err
		}
		if seen[r.Key()] {
			return nil, errs.Usagef("--repo-branch %s is given twice", r.Key())
		}
		seen[r.Key()] = true
		out = append(out, r)
	}
	if t != nil {
		for _, r := range t.RepoBranchList() {
			if !seen[r.Key()] {
				out = append(out, r)
			}
		}
	}
	return out, nil
}

// RepoBranchName is the plan branch name a run uses on every plan: the
// first override's branch with each "/" replaced by "-", so the name needs
// no encoded slash in Bamboo's REST paths.
func RepoBranchName(rbs []config.RepoBranch) string {
	if len(rbs) == 0 {
		return ""
	}
	return strings.ReplaceAll(rbs[0].Branch, "/", "-")
}

// BranchPlan is what a run with repository branches does to one plan.
type BranchPlan struct {
	PlanKey    string // master plan key
	Parent     string // the plan whose branch build triggers this one; "" for the root
	Name       string // plan branch name, the same on every plan of the run
	BranchKey  string // plan branch key; "" until bam creates it
	URL        string // the plan branch's page; "" until bam creates it
	Repository string // the default repository whose branch changes; "" when the plan keeps its own
	VCSBranch  string // the branch of Repository it builds; "" when the plan keeps its own
	Existing   bool   // the plan branch was there before the run; bam reuses it as it is
	Created    bool   // bam created it in this run
}

// RepoPlan is every plan branch a run with repository branches uses.
type RepoPlan struct {
	Name     string
	Plans    []BranchPlan // the root first, then by depth and plan key
	Warnings []string
}

// ChildBranchKeys are the plan branch keys of every plan but the root that
// has one, for the tree to search for its builds.
func (p RepoPlan) ChildBranchKeys() []string {
	var out []string
	for _, bp := range p.Plans {
		if bp.Parent != "" && bp.BranchKey != "" {
			out = append(out, bp.BranchKey)
		}
	}
	return out
}

// runPlan is one plan found for a run: where it hangs and its newest build.
type runPlan struct {
	key    string
	parent string
	build  provider.Build // zero when the plan has no build
	read   bool           // build was looked up
}

// defaultRepo is the repository whose commit is the build's top-level
// revision: Bamboo reports the default repository's commit there.
func defaultRepo(b provider.Build) string {
	if b.Revision == "" {
		return ""
	}
	for _, r := range b.Revisions {
		if r.Revision == b.Revision {
			return r.Repository
		}
	}
	return ""
}

func repoNames(b provider.Build) []string {
	var out []string
	for _, r := range b.Revisions {
		if !slices.Contains(out, r.Repository) {
			out = append(out, r.Repository)
		}
	}
	return out
}

// PlanRepoBranches works out what a run with repository branches does: the
// plans of the run, which of them build another branch of their default
// repository, and which plan branches exist or must be created. It checks
// every override against the plans' repositories and creates nothing.
func (s *Service) PlanRepoBranches(ctx context.Context, ref PlanRef, rbs []config.RepoBranch) (RepoPlan, error) {
	if len(rbs) == 0 {
		return RepoPlan{}, nil
	}
	if ref.Branch != "" {
		return RepoPlan{}, errs.Usagef("--repo-branch cannot be combined with --branch").
			WithWhy("bam runs every plan of the run on a plan branch named after the repository branch").
			WithTry("drop --branch, or the preset's branch:")
	}
	plans, err := s.runPlans(ctx, ref.MasterKey, rbs)
	if err != nil {
		return RepoPlan{}, err
	}
	if err := checkRepoBranches(ref.MasterKey, plans, rbs); err != nil {
		return RepoPlan{}, err
	}
	rp := RepoPlan{Name: RepoBranchName(rbs)}
	need := map[string]*BranchPlan{ref.MasterKey: {PlanKey: ref.MasterKey}}
	for _, p := range plans {
		repo, branch := p.override(rbs)
		if branch == "" {
			continue
		}
		bp := need[p.key]
		if bp == nil {
			bp = &BranchPlan{PlanKey: p.key, Parent: p.parent}
			need[p.key] = bp
		}
		bp.Repository, bp.VCSBranch = repo, branch
		// The plans between the root and this one carry the name on, or
		// Bamboo's chain of same-named branches would stop before it.
		for up := plans[p.parent]; up != nil; up = plans[up.parent] {
			if need[up.key] == nil {
				need[up.key] = &BranchPlan{PlanKey: up.key, Parent: up.parent}
			}
		}
	}
	depth := func(key string) int {
		d := 0
		for p := plans[key]; p != nil && p.parent != ""; p = plans[p.parent] {
			d++
		}
		return d
	}
	for _, bp := range need {
		bp.Name = rp.Name
		rp.Plans = append(rp.Plans, *bp)
	}
	sort.Slice(rp.Plans, func(i, j int) bool {
		di, dj := depth(rp.Plans[i].PlanKey), depth(rp.Plans[j].PlanKey)
		if di != dj {
			return di < dj
		}
		return rp.Plans[i].PlanKey < rp.Plans[j].PlanKey
	})
	var reused []string
	for i := range rp.Plans {
		bp := &rp.Plans[i]
		branches, err := s.P.ListBranches(ctx, bp.PlanKey)
		if err != nil {
			return RepoPlan{}, err
		}
		for _, b := range branches {
			if b.ShortName != rp.Name {
				continue
			}
			if b.Disabled {
				return RepoPlan{}, errs.Usagef("plan branch %s of %s is disabled", rp.Name, bp.PlanKey).
					WithWhy("Bamboo would not build it").
					WithTry("enable it in Bamboo: " + b.URL)
			}
			bp.Existing, bp.BranchKey, bp.URL = true, b.Key, b.URL
			if bp.VCSBranch != "" {
				reused = append(reused, b.Key)
			}
		}
	}
	if len(reused) > 0 {
		rp.Warnings = append(rp.Warnings, fmt.Sprintf("plan branch %s already exists on %s; bam reuses it as it is and cannot check which branch it builds",
			rp.Name, strings.Join(reused, ", ")))
	}
	return rp, nil
}

// override is the branch rbs give p's default repository: a PLAN:REPO entry
// for p wins over a REPO entry.
func (p *runPlan) override(rbs []config.RepoBranch) (repo, branch string) {
	def := defaultRepo(p.build)
	if def == "" {
		return "", ""
	}
	for _, r := range rbs {
		if r.Plan == p.key && r.Repository == def {
			return def, r.Branch
		}
	}
	for _, r := range rbs {
		if r.Plan == "" && r.Repository == def {
			return def, r.Branch
		}
	}
	return "", ""
}

// runPlans finds the plans of a run from root: the tree of the root's newest
// build, the children: config, and every plan an override names. Each gets
// its newest build, for its repositories.
func (s *Service) runPlans(ctx context.Context, root string, rbs []config.RepoBranch) (map[string]*runPlan, error) {
	plans := map[string]*runPlan{root: {key: root}}
	add := func(key, parent string) *runPlan {
		if p, ok := plans[key]; ok {
			return p
		}
		p := &runPlan{key: key, parent: parent}
		plans[key] = p
		return p
	}
	last, err := s.P.ListBuilds(ctx, root, provider.ListOptions{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(last) > 0 {
		tree, err := s.BuildTree(ctx, last[0].Key, TreeOptions{})
		if err != nil {
			return nil, err
		}
		plans[root].build, plans[root].read = tree.Root.Build, true
		var walk func(n *TreeNode, parent string)
		walk = func(n *TreeNode, parent string) {
			for _, c := range n.Children {
				if c.Loop || c.Superseded {
					continue
				}
				key := c.PlanKey
				if c.Build.MasterKey != "" {
					key = c.Build.MasterKey
				}
				p := add(key, parent)
				if !c.NotRun && !p.read {
					p.build, p.read = c.Build, true
				}
				if !c.NotRun {
					walk(c, key)
				}
			}
		}
		walk(tree.Root, root)
	}
	configured, err := s.Cfg.Children()
	if err != nil {
		return nil, err
	}
	queue := slices.Sorted(maps.Keys(plans))
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, c := range configured[key] {
			if _, ok := plans[c]; !ok {
				add(c, key)
				queue = append(queue, c)
			}
		}
	}
	for _, r := range rbs {
		if r.Plan != "" {
			add(r.Plan, root)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(plans)) {
		p := plans[key]
		if p.read {
			continue
		}
		p.read = true
		bs, err := s.P.ListBuilds(ctx, key, provider.ListOptions{Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(bs) == 0 {
			continue
		}
		if p.build, err = s.P.GetBuild(ctx, bs[0].Key); err != nil {
			return nil, err
		}
	}
	return plans, nil
}

// checkRepoBranches fails with a usage error, before anything is created or
// triggered, when an override names a repository its plans do not build as
// their default repository.
func checkRepoBranches(root string, plans map[string]*runPlan, rbs []config.RepoBranch) error {
	keys := slices.Sorted(maps.Keys(plans))
	notDefault := func(p *runPlan, repo string) error {
		return errs.Usagef("%s is not the default repository of %s", repo, p.key).
			WithWhy(fmt.Sprintf("Bamboo can switch only a plan's default repository; %s's is %s", p.key, defaultRepo(p.build)))
	}
	for _, r := range rbs {
		if r.Plan == "" {
			continue
		}
		p := plans[r.Plan]
		switch def := defaultRepo(p.build); {
		case p.build.Key == "":
			return errs.Usagef("%s has no build, so bam cannot tell which repositories it builds", p.key).
				WithTry("run " + p.key + " once, then try again")
		case def == r.Repository:
		case slices.Contains(repoNames(p.build), r.Repository):
			return notDefault(p, r.Repository)
		default:
			return errs.Usagef("%s does not build %s", p.key, r.Repository).
				WithWhy(fmt.Sprintf("its newest build, %s, built %s", p.build.Key, strings.Join(repoNames(p.build), ", ")))
		}
	}
	for _, r := range rbs {
		if r.Plan != "" {
			continue
		}
		var other *runPlan
		found := false
		for _, k := range keys {
			p := plans[k]
			if defaultRepo(p.build) == r.Repository {
				found = true
				break
			}
			if other == nil && slices.Contains(repoNames(p.build), r.Repository) {
				other = p
			}
		}
		switch {
		case found:
		case other != nil:
			return notDefault(other, r.Repository)
		default:
			var built []string
			for _, k := range keys {
				repo := defaultRepo(plans[k].build)
				if repo == "" {
					repo = "no build"
				}
				built = append(built, fmt.Sprintf("%s (%s)", k, repo))
			}
			return errs.Usagef("no plan of this run builds %s", r.Repository).
				WithWhy("the plans of this run from " + root + " and their default repositories: " + strings.Join(built, ", ")).
				WithTry("name a repository as a build of the plan shows it: bam build show " + root)
		}
	}
	return nil
}

// CreateRepoBranches creates the plan branches rp lacks, the deepest plans
// first and the root last, so every child's branch is there before the root
// can trigger it. It fills each created plan's key and page. A failure stops
// at once; its Why names the branches already created.
func (s *Service) CreateRepoBranches(ctx context.Context, rp *RepoPlan) error {
	var created []string
	for i := len(rp.Plans) - 1; i >= 0; i-- {
		bp := &rp.Plans[i]
		if bp.Existing || bp.Created {
			continue
		}
		b, err := s.P.CreateBranch(ctx, bp.PlanKey, rp.Name, bp.VCSBranch)
		if err != nil {
			var e *errs.Error
			if len(created) > 0 && errors.As(err, &e) {
				done := "bam already created " + strings.Join(created, ", ")
				if e.Why == "" {
					e.Why = done
				} else {
					e.Why += "; " + done
				}
			}
			return err
		}
		bp.BranchKey, bp.URL, bp.Created = b.Key, b.URL, true
		created = append(created, b.Key)
	}
	return nil
}

// UseRepoBranches points ref at the root's plan branch, so Run triggers it.
func (r *PlanRef) UseRepoBranches(rp RepoPlan) {
	if len(rp.Plans) == 0 {
		return
	}
	r.RepoBranches = rp.Plans
	r.Branch = rp.Name
	if k := rp.Plans[0].BranchKey; k != "" {
		r.PlanKey = k
	}
}
