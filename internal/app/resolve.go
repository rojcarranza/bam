package app

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
)

var (
	planKeyRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*$`)
	buildKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*-[0-9]+$`)
	digitsRe   = regexp.MustCompile(`^[0-9]+$`)
	projectRe  = regexp.MustCompile(`^[A-Z][A-Z0-9]*$`)
	// jobResultRe is a job's result key, as on Bamboo's job page: PROJ-PLAN-JOB1-12.
	jobResultRe = regexp.MustCompile(`^([A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*)-([A-Z][A-Z0-9]*)-([0-9]+)$`)
)

// IsPlanKey reports whether s looks like PROJ-PLAN (or a branch plan key).
func IsPlanKey(s string) bool { return planKeyRe.MatchString(s) }

// IsProjectKey reports whether s looks like PROJ.
func IsProjectKey(s string) bool { return projectRe.MatchString(s) }

// IsBuildKey reports whether s looks like PROJ-PLAN-123.
func IsBuildKey(s string) bool { return buildKeyRe.MatchString(s) }

// SplitJobResult splits a job result key such as PROJ-PLAN-JOB1-12 into its
// build key (PROJ-PLAN-12) and job short key (JOB1).
func SplitJobResult(s string) (build, job string, ok bool) {
	m := jobResultRe.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return m[1] + "-" + m[3], m[2], true
}

// PlanRef is a resolved plan, and the branch plan to act on.
type PlanRef struct {
	PlanKey      string // branch plan key when a branch is chosen, else MasterKey
	MasterKey    string
	Branch       string // branch short name; empty for the default branch
	Revision     string // commit to build, set by the caller for one run; ResolvePlan never sets it
	RevisionFrom string // build key Revision was taken from; empty when it was typed
	// Repositories is the branch each repository of the plan branch must
	// build: the preset's, with --repository laid over by bam run.
	Repositories map[string]string
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
	arg = PresetName(s.Cfg, arg)
	switch {
	case config.ValidTargetName(arg):
		t, ok, err := s.Cfg.Target(arg)
		if err != nil {
			return PlanRef{}, err
		}
		if !ok {
			return PlanRef{}, UnknownTarget(s.Cfg, arg)
		}
		ref = PlanRef{PlanKey: t.Plan, MasterKey: t.Plan, Target: &t, Repositories: maps.Clone(t.Repositories)}
		if branch == "" {
			branch = t.Branch
		}
	case IsPlanKey(arg):
		ref = PlanRef{PlanKey: arg, MasterKey: arg}
	case IsBuildKey(arg):
		return PlanRef{}, errs.Usagef("%q is a build key, not a plan key", arg).
			WithTry("its plan, " + arg[:strings.LastIndex(arg, "-")])
	case projectRe.MatchString(arg):
		try := "bam plan list " + arg + " to see its plans"
		if p := lowerPreset(s.Cfg, arg); p != "" {
			try += ", or the preset " + p
		}
		return PlanRef{}, errs.Usagef("%q is a project key, not a plan key", arg).WithTry(try)
	default:
		try := "bam preset list, or a plan key such as PROJ-PLAN"
		if k := tidyBuildKey(arg); IsPlanKey(k) {
			try = "did you mean " + k + "?"
		} else if IsBuildKey(k) {
			try = "its plan, " + k[:strings.LastIndex(k, "-")]
		}
		return PlanRef{}, errs.Usagef("%q is neither a preset nor a plan key", arg).WithTry(try)
	}
	if branch != "" {
		var err error
		if branch, err = s.ExpandBranch(branch); err != nil {
			return PlanRef{}, err
		}
		key, err := s.ResolveBranch(ctx, ref.MasterKey, branch)
		if err != nil {
			return PlanRef{}, err
		}
		ref.PlanKey, ref.Branch = key, branch
	}
	return ref, nil
}

// PresetName is arg in lowercase when that names a preset, so "Deploy-QA"
// names deploy-qa; a plan, build or project key and anything else is
// returned unchanged.
func PresetName(cfg *config.Config, arg string) string {
	if IsPlanKey(arg) || IsBuildKey(arg) || projectRe.MatchString(arg) {
		return arg
	}
	if p := lowerPreset(cfg, arg); p != "" {
		return p
	}
	return arg
}

// lowerPreset returns arg in lowercase when that names a preset, for a
// preset typed in the wrong case; otherwise "".
func lowerPreset(cfg *config.Config, arg string) string {
	low := strings.ToLower(arg)
	if low == arg || !config.ValidTargetName(low) {
		return ""
	}
	if _, ok, _ := cfg.Target(low); ok {
		return low
	}
	return ""
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
	e := errs.Usagef("unknown preset %q", name).WithTry("bam preset list")
	if isKey {
		e = e.WithTry("bam preset list; " + keys)
	}
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
		if isKey && IsBuildKey(strings.ToUpper(name)) {
			// a build key never makes sense as a new preset name
			return e.WithWhy("no presets are configured").WithTry(keys)
		}
		if isKey {
			// with no presets, a lowercase key is a key typed in lowercase
			return e.WithWhy("no presets are configured").
				WithTry(keys + " or, to make a preset by that name: bam preset add " + name + " --plan KEY")
		}
		return e.WithWhy("no presets are configured").
			WithTry("bam preset add " + name + " --plan KEY to create it; " + keys)
	}
	if c := errs.Nearest(name, names); c != "" {
		e = e.WithTry("did you mean " + c + "? bam preset list shows them all")
	}
	return e.WithWhy("presets: " + strings.Join(names, ", "))
}

// ResolveBranch returns the branch plan key of branch on plan master. branch
// is a plan branch's name or the repository branch it was made for: Bamboo
// names the plan branch of feature/x feature-x, and the server does not say
// which repository branch a plan branch builds, so feature/x (or @ on that
// checkout) finds feature-x by the server's naming rule. A plan branch named
// exactly branch comes first.
func (s *Service) ResolveBranch(ctx context.Context, master, branch string) (string, error) {
	branch, err := s.ExpandBranch(branch)
	if err != nil {
		return "", err
	}
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
	if named := s.P.PlanBranchName(branch); named != branch {
		for _, b := range branches {
			if b.ShortName == named {
				return b.Key, nil
			}
		}
	}
	e := errs.Usagef("%s has no plan branch for %q", master, branch)
	if c := errs.Nearest(branch, names); c != "" {
		e = e.WithWhy("the closest plan branch is " + c)
	}
	return "", e.WithTry(fmt.Sprintf("create it in Bamboo, nothing was created: %s (bam plan branches %s lists the existing ones)",
		s.P.CreateBranchURL(master), master))
}

// CheckedOutMarker stands for the branch checked out in the working directory.
const CheckedOutMarker = "@"

// ExpandBranch turns "@" into the checked-out branch and leaves any other
// name alone.
func (s *Service) ExpandBranch(branch string) (string, error) {
	if branch != CheckedOutMarker {
		return branch, nil
	}
	if s.Checkout == nil {
		return "", errs.Usagef("bam cannot read the checked-out branch here").WithTry("name the branch instead of @")
	}
	b, err := s.Checkout()
	if err != nil {
		return "", err
	}
	return b, nil
}

// ExpandRepositories returns want with every "@" replaced by the
// checked-out branch.
func (s *Service) ExpandRepositories(want map[string]string) (map[string]string, error) {
	out := maps.Clone(want)
	for name, b := range want {
		x, err := s.ExpandBranch(b)
		if err != nil {
			return nil, err
		}
		out[name] = x
	}
	return out, nil
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
	a.Arg = PresetName(s.Cfg, a.Arg)
	bare := s.bareNumberBuild(a.Arg)
	switch {
	case a.Last:
		rec, err := s.LastBuild()
		return rec.BuildKey, err
	case IsBuildKey(a.Arg):
		return CanonicalBuildKey(a.Arg)
	case bare != "":
		return CanonicalBuildKey(bare)
	case a.Arg == "":
		return "", errs.Usagef("which build?").WithTry("pass a build key, a plan key, a preset, or --last")
	case !config.ValidTargetName(a.Arg) && !IsPlanKey(a.Arg) && !projectRe.MatchString(a.Arg):
		try := "a build key such as PROJ-PLAN-12, a plan key such as PROJ-PLAN, or bam preset list"
		if n := strings.TrimPrefix(a.Arg, "#"); digitsRe.MatchString(n) {
			try = "a build number needs its plan, as in PROJ-PLAN-" + n
			if rec, ok, _ := s.State.Last(s.Cfg.RepoRoot); ok && IsBuildKey(rec.BuildKey) {
				try = "did you mean " + rec.BuildKey[:strings.LastIndex(rec.BuildKey, "-")+1] + n + "?"
			}
		}
		if k := tidyBuildKey(a.Arg); IsBuildKey(k) {
			try = "did you mean " + k + "?"
		}
		if build, job, ok := SplitJobResult(a.Arg); ok {
			try = "that is job " + job + " of " + build + ": pass " + build + ", and --job " + job + " for bam logs"
		}
		return "", errs.Usagef("%q is not a build key, a plan key or a preset", a.Arg).WithTry(try)
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

// LastBuild is the last build bam triggered from this repository. It is a
// usage error when there is none, and a config error when it ran on another
// server than this one.
func (s *Service) LastBuild() (LastRecord, error) {
	rec, ok, err := s.State.Last(s.Cfg.RepoRoot)
	if err != nil {
		return LastRecord{}, err
	}
	if !ok {
		return LastRecord{}, errs.Usagef("no build triggered from this repository yet").WithTry("bam run <preset>")
	}
	if rec.Origin != "" && rec.Origin != s.Origin {
		return LastRecord{}, errs.Configf("the last build %s ran on %s, not on server %q", rec.BuildKey, rec.Origin, s.Server.Alias).
			WithTry("pass --server with the alias of " + rec.Origin)
	}
	return rec, nil
}

// bareNumberBuild is the build key for a bare build number (482 or #482):
// that number of the plan of the last build triggered from this repository on
// this server. It is "" for anything else, and when there is no such build.
func (s *Service) bareNumberBuild(arg string) string {
	n := strings.TrimPrefix(arg, "#")
	if !digitsRe.MatchString(n) {
		return ""
	}
	rec, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	if !ok || !IsBuildKey(rec.BuildKey) || (rec.Origin != "" && rec.Origin != s.Origin) {
		return ""
	}
	return rec.BuildKey[:strings.LastIndex(rec.BuildKey, "-")+1] + n
}

// canonicalBuildKey drops leading zeros from a build key's number
// (PROJ-PLAN-007 is PROJ-PLAN-7); ok is false for build 0 or a number too large.
func canonicalBuildKey(key string) (string, bool) {
	i := strings.LastIndex(key, "-")
	n, err := strconv.Atoi(key[i+1:])
	return key[:i+1] + strconv.Itoa(n), err == nil && n > 0
}

// CanonicalBuildKey drops leading zeros from a build key's number
// (PROJ-PLAN-007 is PROJ-PLAN-7). It is a usage error for build 0 or a
// number too large, so callers that skip ResolveBuild (such as bam url and
// bam open, which build a URL with no network call) still reject them.
func CanonicalBuildKey(key string) (string, error) {
	k, ok := canonicalBuildKey(key)
	if !ok {
		return "", errs.Usagef("build numbers start at 1, not %s", key).
			WithTry("its latest build: pass the plan key, " + key[:strings.LastIndex(key, "-")])
	}
	return k, nil
}

// tidyBuildKey repairs a key copied with Bamboo's '#' before the number
// (PROJ-PLAN-#12 or PROJ-PLAN#12) or with a stray trailing dash.
func tidyBuildKey(s string) string {
	return strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(s, "-#", "#"), "#", "-"), "-")
}

// ResolveFrom turns --from into a build key: a number of the resolved plan
// branch, a full key, or "last" (the most recent manual build, else the most
// recent build).
func (s *Service) ResolveFrom(ctx context.Context, ref PlanRef, from string) (string, error) {
	switch {
	case from == "":
		return "", nil
	case digitsRe.MatchString(from):
		n, err := strconv.Atoi(from)
		if err != nil || n < 1 {
			return "", errs.Usagef("--from takes a build number from 1 up, not %s", from).
				WithTry("--from last reuses the latest build")
		}
		return ref.PlanKey + "-" + strconv.Itoa(n), nil // 007 names build 7
	case IsBuildKey(from):
		k, ok := canonicalBuildKey(from)
		if !ok {
			return "", errs.Usagef("--from takes a build number from 1 up, not %s", from).
				WithTry("--from last reuses the latest build")
		}
		return k, nil
	case !strings.EqualFold(from, "last"):
		e := errs.Usagef("--from takes a build number, a build key or last, not %q", from)
		if up := strings.ToUpper(from); IsBuildKey(up) {
			e = e.WithTry("keys are uppercase: did you mean " + up + "?")
		}
		if k := tidyBuildKey(from); k != from && IsBuildKey(k) {
			e = e.WithTry("did you mean " + k + "?")
		}
		if n := strings.TrimPrefix(from, "#"); digitsRe.MatchString(n) {
			e = e.WithTry("drop Bamboo's '#': --from " + n)
		}
		return "", e
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
