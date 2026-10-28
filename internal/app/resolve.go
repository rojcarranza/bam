package app

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

var (
	planKeyRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*$`)
	buildKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+$`)
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
	projectRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)
)

// IsPlanKey reports whether s looks like PROJ-PLAN (or a branch plan key).
func IsPlanKey(s string) bool { return planKeyRe.MatchString(s) }

// IsProjectKey reports whether s looks like PROJ.
func IsProjectKey(s string) bool { return projectRe.MatchString(s) }

// IsBuildKey reports whether s looks like PROJ-PLAN-123.
func IsBuildKey(s string) bool { return buildKeyRe.MatchString(s) }

// PlanRef is a resolved plan, and the branch plan to act on.
type PlanRef struct {
	PlanKey      string // branch plan key when a branch is chosen, else MasterKey
	MasterKey    string
	Branch       string // branch short name; empty for the default branch
	Revision     string // commit to build, set by the caller for one run; ResolvePlan never sets it
	RevisionFrom string // build key Revision was taken from; empty when it was typed
	Target       *config.ResolvedTarget
}

// RevisionShort is the revision as text output prints it: as typed, or, when
// it was taken from a build, its first seven characters, as build show does.
func (r PlanRef) RevisionShort() string {
	if r.RevisionFrom == "" {
		return r.Revision
	}
	return provider.Revision{Revision: r.Revision}.Short()
}

// RevisionLabel is RevisionShort with the build it came from, like a
// variable's "from #N" source.
func (r PlanRef) RevisionLabel() string {
	if r.RevisionFrom == "" {
		return r.Revision
	}
	return r.RevisionShort() + " (from " + BuildLabel(r.RevisionFrom) + ")"
}

// BuildLabel is a build key's short form, "#41" for PROJ-BUILD-41.
func BuildLabel(key string) string {
	return "#" + key[strings.LastIndex(key, "-")+1:]
}

// ResolvePlan turns a target name or plan key into a PlanRef. branch
// overrides the target's branch.
func (s *Service) ResolvePlan(ctx context.Context, arg, branch string) (PlanRef, error) {
	var ref PlanRef
	switch {
	case config.ValidTargetName(arg):
		t, ok, err := s.Cfg.Target(arg)
		if err != nil {
			return PlanRef{}, err
		}
		if !ok {
			return PlanRef{}, UnknownTarget(s.Cfg, arg)
		}
		ref = PlanRef{PlanKey: t.Plan, MasterKey: t.Plan, Target: &t}
		if branch == "" {
			branch = t.Branch
		}
	case IsPlanKey(arg):
		ref = PlanRef{PlanKey: arg, MasterKey: arg}
	case IsBuildKey(arg):
		return PlanRef{}, errs.Usagef("%q is a build key, not a plan key", arg).
			WithTry("its plan, " + arg[:strings.LastIndex(arg, "-")])
	case projectRe.MatchString(arg):
		return PlanRef{}, errs.Usagef("%q is a project key, not a plan key", arg).
			WithTry("bam plan list " + arg + " to see its plans")
	default:
		return PlanRef{}, errs.Usagef("%q is neither a preset nor a plan key", arg).
			WithTry("bam preset list, or a plan key such as PROJ-PLAN")
	}
	if branch != "" {
		key, err := s.ResolveBranch(ctx, ref.MasterKey, branch)
		if err != nil {
			return PlanRef{}, err
		}
		ref.PlanKey, ref.Branch = key, branch
	}
	return ref, nil
}

// UnknownTarget is the usage error for a preset name that does not apply
// here; it names the repositories that define it, if any.
func UnknownTarget(cfg *config.Config, name string) error {
	all, _ := cfg.Targets()
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	keys, isKey := "plan keys are uppercase, like PROJ-PLAN", false
	if up := strings.ToUpper(name); up != name && (IsPlanKey(up) || IsBuildKey(up)) {
		keys, isKey = "keys are uppercase: did you mean "+up+"?", true
	}
	e := errs.Usagef("unknown preset %q", name).WithTry("bam preset list; " + keys)
	other := cfg.OtherRepoTargets()
	var roots []string
	for _, root := range slices.Sorted(maps.Keys(other)) {
		if slices.Contains(other[root], name) {
			roots = append(roots, root)
		}
	}
	if len(roots) > 0 {
		return e.WithWhy("defined only for " + strings.Join(roots, ", ") + " (repos entry in " + cfg.MachinePath + ")").
			WithTry("run bam from that repository, or bam preset migrate to use it anywhere")
	}
	if len(names) == 0 && len(other) > 0 {
		return e.WithWhy("no presets apply here; other repositories define some").
			WithTry("bam preset list from that repository, or bam preset migrate to use them anywhere")
	}
	if len(names) == 0 {
		if isKey {
			// with no presets, a lowercase key is a key typed in lowercase
			return e.WithWhy("no presets are configured").
				WithTry(keys + " or bam preset add " + name + " --plan KEY to create a preset")
		}
		return e.WithWhy("no presets are configured").
			WithTry("bam preset add " + name + " --plan KEY to create it; " + keys)
	}
	if c := Closest(name, names, 1); len(c) > 0 {
		e = e.WithTry("did you mean " + c[0] + "? bam preset list shows them all")
	}
	return e.WithWhy("presets: " + strings.Join(names, ", "))
}

// ResolveBranch returns the branch plan key of branch on plan master.
func (s *Service) ResolveBranch(ctx context.Context, master, branch string) (string, error) {
	branches, err := s.P.ListBranches(ctx, master)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(branches))
	for _, b := range branches {
		if b.ShortName == branch || b.Name == branch {
			return b.Key, nil
		}
		names = append(names, b.ShortName)
	}
	e := errs.Usagef("branch %q not found on %s", branch, master).
		WithTry(fmt.Sprintf("bam plan branches %s (omit --branch for the default branch)", master))
	if close := Closest(branch, names, 3); len(close) > 0 {
		e = e.WithWhy("close matches: " + strings.Join(close, ", "))
	}
	return "", e
}

// BuildArg is how a command names a build.
type BuildArg struct {
	Arg    string // build key, plan key or target name
	Last   bool
	Branch string
}

// ResolveBuild returns a build key: the key itself, the latest build of a
// plan or target, or the last build bam triggered from this repository.
func (s *Service) ResolveBuild(ctx context.Context, a BuildArg) (string, error) {
	switch {
	case a.Last:
		rec, ok, err := s.State.Last(s.Cfg.RepoRoot)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errs.Usagef("no build triggered from this repository yet").WithTry("bam run <preset>")
		}
		if rec.Origin != "" && rec.Origin != s.Origin {
			return "", errs.Configf("the last build %s ran on %s, not on server %q", rec.BuildKey, rec.Origin, s.Server.Alias).
				WithTry("pass --server with the alias of " + rec.Origin)
		}
		return rec.BuildKey, nil
	case IsBuildKey(a.Arg):
		return a.Arg, nil
	case a.Arg == "":
		return "", errs.Usagef("which build?").WithTry("pass a build key, a plan key, a preset, or --last")
	case !config.ValidTargetName(a.Arg) && !IsPlanKey(a.Arg) && !projectRe.MatchString(a.Arg):
		return "", errs.Usagef("%q is not a build key, a plan key or a preset", a.Arg).
			WithTry("a build key such as PROJ-PLAN-12, a plan key such as PROJ-PLAN, or bam preset list")
	}
	ref, err := s.ResolvePlan(ctx, a.Arg, a.Branch)
	if err != nil {
		return "", err
	}
	builds, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: 1})
	if err != nil {
		return "", err
	}
	if len(builds) == 0 {
		return "", errs.Bamboof("no builds of %s yet", ref.PlanKey).WithTry("bam run " + a.Arg)
	}
	return builds[0].Key, nil
}

// ResolveFrom turns --from into a build key: a number of the resolved plan
// branch, a full key, or "last" (the most recent manual build, else the most
// recent build).
func (s *Service) ResolveFrom(ctx context.Context, ref PlanRef, from string) (string, error) {
	switch {
	case from == "":
		return "", nil
	case digitsRe.MatchString(from):
		return ref.PlanKey + "-" + from, nil
	case IsBuildKey(from):
		return from, nil
	case from != "last":
		return "", errs.Usagef("--from takes a build number, a build key or last, not %q", from)
	}
	builds, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: 20})
	if err != nil {
		return "", err
	}
	if len(builds) == 0 {
		return "", errs.Usagef("no builds of %s to reuse variables from", ref.PlanKey)
	}
	for _, b := range builds {
		r := strings.ToLower(b.Reason)
		if strings.Contains(r, "manual") || strings.Contains(r, "custom") {
			return b.Key, nil
		}
	}
	return builds[0].Key, nil
}

// WithBranch fills in the branch a plan's builds ran on, which a result
// list may leave out: only a build's plan master names its branch.
func WithBranch(bs []provider.Build, branch string) {
	for i := range bs {
		if bs[i].Branch == "" {
			bs[i].Branch = branch
		}
	}
}
