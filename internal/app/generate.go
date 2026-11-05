package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// GenerateOptions are the inputs of bam preset add and bam init --plan.
type GenerateOptions struct {
	Name    string
	PlanKey string // optional when From is a full build key
	From    string // build whose values fill the preset; default last, used only in comments
	Branch  string // defaults to the branch of a From build key
	// Server is the alias the preset's server line names.
	Server string
}

// GenerateTarget reads the plan's declared variables (or, when unsupported,
// a build's variables) and returns a commented target draft (spec §3.9).
func (s *Service) GenerateTarget(ctx context.Context, o GenerateOptions) (config.TargetDraft, error) {
	d, _, err := s.generate(ctx, o)
	return d, err
}

// CheckGenerate reports a bad preset name or plan key without asking Bamboo,
// so callers can fail before picking a server.
func CheckGenerate(o GenerateOptions) error {
	if !config.ValidTargetName(o.Name) {
		e := errs.Usagef("invalid preset name %q", o.Name).
			WithWhy("preset names are lowercase letters, digits, - and _, starting with a letter")
		if s := config.TargetNameFor(o.Name); s != "" {
			e = e.WithTry("did you mean " + s + "?")
		}
		return e
	}
	if o.PlanKey == "" && IsBuildKey(o.From) {
		return nil // the build names the plan
	}
	return CheckPlanKey(o.PlanKey)
}

// CheckPlanKey returns a usage error, with a hint, when a --plan flag value
// is not a plan key.
func CheckPlanKey(key string) error {
	if IsPlanKey(key) {
		return nil
	}
	try := "bam plan list"
	if up := strings.ToUpper(key); IsPlanKey(up) {
		try = "keys are uppercase: did you mean " + up + "?"
	} else if IsBuildKey(key) {
		try = "its plan, " + key[:strings.LastIndex(key, "-")]
	} else if projectRe.MatchString(key) {
		try = "bam plan list " + key + " to see its plans"
	}
	return errs.Usagef("%q is not a plan key", key).WithTry(try)
}

// generate is GenerateTarget that also reports whether the names came from
// the plan's declared list rather than from a build.
func (s *Service) generate(ctx context.Context, o GenerateOptions) (config.TargetDraft, bool, error) {
	if err := CheckGenerate(o); err != nil {
		return config.TargetDraft{}, false, err
	}
	o, err := s.planFromBuild(ctx, o)
	if err != nil {
		return config.TargetDraft{}, false, err
	}
	plan, err := s.P.GetPlan(ctx, o.PlanKey)
	if err != nil {
		return config.TargetDraft{}, false, err
	}
	// A plan branch key as --plan becomes its main plan plus the branch
	// name, so the preset carries the branch like --branch would.
	if plan.MasterKey != "" && plan.MasterKey != o.PlanKey && o.Branch == "" {
		if bs, berr := s.P.ListBranches(ctx, plan.MasterKey); berr == nil {
			for _, b := range bs {
				name := b.ShortName
				if name == "" {
					name = b.Name
				}
				if b.Key == o.PlanKey && name != "" {
					o.PlanKey, o.Branch = plan.MasterKey, name
					break
				}
			}
		}
	}
	d := config.TargetDraft{Name: o.Name, Plan: o.PlanKey, Branch: o.Branch, Server: o.Server}
	ref := PlanRef{PlanKey: o.PlanKey, MasterKey: o.PlanKey}
	if o.Branch != "" {
		key, err := s.ResolveBranch(ctx, o.PlanKey, o.Branch)
		if err != nil {
			return d, false, err
		}
		ref.PlanKey, ref.Branch = key, o.Branch
	}
	d.BuildPlan = ref.PlanKey
	used, usedKey, err := s.usedVars(ctx, ref, o.From)
	if err != nil {
		return d, false, err
	}
	d.Repos = s.draftRepos(ctx, ref, o.From)

	declared, err := s.P.ListVariables(ctx, o.PlanKey)
	switch {
	case err == nil:
		// A plan branch lists only the variables it overrides, so a branch
		// key given as --plan reads its main plan's variables instead.
		if len(declared) == 0 && plan.MasterKey != "" && plan.MasterKey != o.PlanKey {
			master, merr := s.P.ListVariables(ctx, plan.MasterKey)
			if merr == nil && len(master) > 0 {
				d.Note = fmt.Sprintf("%s is a plan branch with no variables of its own; used the variables of its main plan %s", o.PlanKey, plan.MasterKey)
				declared = master
			}
		}
		declaredDrafts(&d, declared, used, usedKey, o)
		if len(declared) == 0 {
			d.Note = fmt.Sprintf("%s lists no variables of its own; global and deployment variables are not read, so add any the run needs under defaults", o.PlanKey)
		}
		return d, true, nil
	case errors.Is(err, errs.ErrUnsupported):
		if used == nil {
			d.Note = "plan variables are not readable on this server; add them under defaults"
			return d, false, nil
		}
		d.Note = "plan variables are not readable on this server; names come from " + usedKey
		for _, name := range slices.Sorted(maps.Keys(used)) {
			d.Vars = append(d.Vars, draftVar(name, used[name], false, used))
		}
		return d, false, nil
	default:
		return d, false, err
	}
}

// planFromBuild fills the plan key and branch from a --from build key when
// either is missing, and rejects a --plan the build does not belong to.
func (s *Service) planFromBuild(ctx context.Context, o GenerateOptions) (GenerateOptions, error) {
	if !IsBuildKey(o.From) || (o.PlanKey != "" && o.Branch != "") {
		return o, nil
	}
	b, err := s.P.GetBuild(ctx, o.From)
	if err != nil {
		return o, err
	}
	plan := cmp.Or(b.MasterKey, b.PlanKey)
	switch o.PlanKey {
	case "", plan:
		o.PlanKey = plan
		if o.Branch == "" {
			o.Branch = b.Branch
		}
	case b.PlanKey: // the branch plan key itself, as before
	default:
		return o, errs.Usagef("%s is a build of %s, not of %s", o.From, plan, o.PlanKey).
			WithTry("drop --plan; --from " + o.From + " names the plan")
	}
	return o, nil
}

// usedVars returns the variables of the --from build, or of the last build
// when from is empty. Without --from, a missing build or unreadable
// variables are not an error: the preset is drafted from the plan alone.
func (s *Service) usedVars(ctx context.Context, ref PlanRef, from string) (map[string]string, string, error) {
	key, err := s.ResolveFrom(ctx, ref, cmp.Or(from, "last"))
	if err != nil {
		if from != "" {
			return nil, "", err
		}
		return nil, "", nil
	}
	bv, err := s.P.BuildVariables(ctx, key)
	switch {
	case err == nil:
		return bv, key, nil
	case from == "":
		return nil, "", nil
	case errors.Is(err, errs.ErrUnsupported):
		return nil, "", errs.Bamboof("cannot read the variables of %s", key).
			WithWhy("this Bamboo server does not return the variables a build ran with").
			WithTry("drop --from and fill the values by hand")
	default:
		return nil, "", err
	}
}

// declaredDrafts adds the plan's declared variables to d, then, with --from,
// the build's variables the plan does not declare.
func declaredDrafts(d *config.TargetDraft, declared []provider.Variable, used map[string]string, usedKey string, o GenerateOptions) {
	names := map[string]bool{}
	for _, v := range declared {
		names[v.Name] = true
		value := v.Value
		if bv, ok := used[v.Name]; ok && o.From != "" {
			value = bv
		}
		dv := draftVar(v.Name, value, v.Masked, used)
		// A secret the plan stores is written commented: the plan already
		// sends it, and an active ${ENV} line would make a run need the
		// variable. Uncommenting it overrides the stored value.
		dv.Commented = isSecretDraft(dv) && value == v.Value && value != ""
		d.Vars = append(d.Vars, dv)
	}
	if o.From == "" {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(used)) {
		if !names[name] {
			dv := draftVar(name, used[name], false, used)
			if !isSecretDraft(dv) {
				dv.Comment = "not declared by the plan"
			}
			d.Vars = append(d.Vars, dv)
		}
	}
}

func isSecretDraft(v config.DraftVar) bool {
	_, ok := config.EnvRef(v.Value)
	return ok
}

func draftVar(name, value string, masked bool, used map[string]string) config.DraftVar {
	if masked || value == MaskedDisplay || used[name] == MaskedDisplay || config.IsMaskedName(name) {
		env := config.EnvNameFor(name)
		return config.DraftVar{Name: name, Value: "${" + env + "}"}
	}
	return config.DraftVar{Name: name, Value: value}
}

// draftRepos lists the repositories of ref with the branch each builds now,
// read from the --from build or the newest finished job of ref. With no
// finished job it lists the names of the newest build's revisions and no
// branch. A failed read lists nothing: the preset is drafted without them.
func (s *Service) draftRepos(ctx context.Context, ref PlanRef, from string) []config.DraftRepo {
	var built map[string]string
	if from != "" {
		if key, err := s.ResolveFrom(ctx, ref, from); err == nil {
			if b, err := s.P.GetBuild(ctx, key); err == nil {
				built = firstJobBranches(b)
			}
		}
	} else {
		built, _, _ = s.BuiltRepositories(ctx, ref.PlanKey)
	}
	var out []config.DraftRepo
	if built != nil {
		for _, name := range slices.Sorted(maps.Keys(built)) {
			out = append(out, config.DraftRepo{Name: name, Branch: built[name]})
		}
		return out
	}
	bs, err := s.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: 1})
	if err != nil || len(bs) == 0 {
		return nil
	}
	b, err := s.P.GetBuild(ctx, bs[0].Key)
	if err != nil {
		return nil
	}
	for _, name := range repoNames(b) {
		out = append(out, config.DraftRepo{Name: name})
	}
	return out
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

// RecentLimit is how many of the newest builds RecentValues reads, and
// RecentShown how many values it keeps per variable.
const (
	RecentLimit = 10
	RecentShown = 3
)

// RecentVar is a variable that ran with more than one value lately.
type RecentVar struct {
	Name   string
	Values []string // newest first, at most RecentShown
}

// RecentValues returns, for each variable of d that the last RecentLimit
// builds of its plan ran with more than one value, up to RecentShown of those
// values, newest first. Secrets are never listed. A build whose variables
// cannot be read is skipped; a server that cannot read them gives nothing.
func (s *Service) RecentValues(ctx context.Context, d config.TargetDraft) []RecentVar {
	if d.BuildPlan == "" || len(d.Vars) == 0 {
		return nil
	}
	bs, err := s.P.ListBuilds(ctx, d.BuildPlan, provider.ListOptions{Limit: RecentLimit})
	if err != nil {
		return nil
	}
	if len(bs) > RecentLimit {
		bs = bs[:RecentLimit]
	}
	seen := map[string][]string{}
	for _, b := range bs {
		bv, err := s.P.BuildVariables(ctx, b.Key)
		if errors.Is(err, errs.ErrUnsupported) {
			return nil
		}
		if err != nil {
			continue
		}
		for name, v := range bv {
			if v != MaskedDisplay && !slices.Contains(seen[name], v) {
				seen[name] = append(seen[name], v)
			}
		}
	}
	var out []RecentVar
	for _, v := range d.Vars {
		vals := seen[v.Name]
		if isSecretDraft(v) || len(vals) < 2 {
			continue
		}
		out = append(out, RecentVar{Name: v.Name, Values: vals[:min(len(vals), RecentShown)]})
	}
	return out
}
