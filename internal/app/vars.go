package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// MaskedDisplay replaces every secret value in output.
const MaskedDisplay = "********"

// IsSecretName reports whether a variable of this name must be masked. It is
// config.IsMaskedName re-exported, because the terminal UI must classify a
// name the plan never declared and may not import config.
func IsSecretName(name string) bool { return config.IsMaskedName(name) }

// ResolvedVar is one variable after every source has been applied.
type ResolvedVar struct {
	Name      string
	Value     string // never print directly; use Display
	Source    string // plan | target | from #N | flag | edit | env
	Secret    bool
	Declared  bool
	PlanValue string
}

// Display is the value as it may be shown.
func (v ResolvedVar) Display() string {
	if v.Secret {
		return MaskedDisplay
	}
	return v.Value
}

// VarSet is the resolved variables of one run.
type VarSet struct {
	Vars          []ResolvedVar // sorted by name
	FromBuild     string
	Warnings      []string
	DeclaredKnown bool
	// Declared is the names the plan declares. ValidateVars needs it to warn
	// about a name the plan does not know, and VarBase is what fills it in.
	Declared map[string]bool
}

// Changed returns the variables to send: undeclared ones and those whose
// value differs from the plan's.
func (v VarSet) Changed() map[string]string {
	out := map[string]string{}
	for _, r := range v.Vars {
		if !r.Declared || r.Value != r.PlanValue {
			out[r.Name] = r.Value
		}
	}
	return out
}

// Secret returns the names whose values must be redacted.
func (v VarSet) Secret() map[string]bool {
	out := map[string]bool{}
	for _, r := range v.Vars {
		if r.Secret {
			out[r.Name] = true
		}
	}
	return out
}

// Get returns one variable by name.
func (v VarSet) Get(name string) (ResolvedVar, bool) {
	for _, r := range v.Vars {
		if r.Name == name {
			return r, true
		}
	}
	return ResolvedVar{}, false
}

// VarOptions are the command-line inputs to variable resolution.
type VarOptions struct {
	From  string   // --from
	Flags []string // --var name=value, in order
}

// ResolveVars applies, lowest to highest: plan values, target defaults,
// --from, --var. Then it validates env references, required names and allowed
// values (spec §2.3).
//
// It is VarBase followed by ValidateVars. The two halves are separate because
// the terminal UI's run form fetches once and then validates on every
// keystroke; keeping the rules in one function is what stops the UI and the
// commands disagreeing about a plan's variables.
func (s *Service) ResolveVars(ctx context.Context, ref PlanRef, o VarOptions) (VarSet, error) {
	base, err := s.VarBase(ctx, ref, o.From)
	if err != nil {
		return VarSet{}, err
	}
	return ValidateVars(ref, base, o.Flags, s.Getenv)
}

// VarBase fetches what the rules are applied to: the plan's declared
// variables, the target's defaults, and, when from is set, a previous build's
// values. It applies no rules, so a required variable left empty is not an
// error here.
func (s *Service) VarBase(ctx context.Context, ref PlanRef, from string) (VarSet, error) {
	var set VarSet
	vars := map[string]*ResolvedVar{}
	declaredNames := map[string]bool{}

	declared, err := s.P.ListVariables(ctx, ref.MasterKey)
	switch {
	case err == nil:
		set.DeclaredKnown = true
	case errors.Is(err, errs.ErrUnsupported):
	default:
		return VarSet{}, err
	}
	for _, d := range declared {
		declaredNames[d.Name] = true
		vars[d.Name] = &ResolvedVar{Name: d.Name, Value: d.Value, PlanValue: d.Value, Source: "plan",
			Declared: true, Secret: d.Masked || config.IsMaskedName(d.Name)}
	}
	get := func(name string) *ResolvedVar {
		v, ok := vars[name]
		if !ok {
			v = &ResolvedVar{Name: name, Secret: config.IsMaskedName(name)}
			vars[name] = v
		}
		return v
	}
	t := ref.Target
	inTarget := func(name string) bool {
		if t == nil {
			return false
		}
		_, ok := t.Defaults[name]
		return ok
	}

	if t != nil {
		for name, val := range t.Defaults {
			v := get(name)
			v.Value, v.Source = val, "preset"
			// A ${ENV} default is secret whatever the name looks like:
			// ValidateVars will mark it when it resolves it, and the run
			// form builds its fields from this base, before that happens.
			if _, ok := config.EnvRef(val); ok {
				v.Secret = true
			}
		}
	}

	if from != "" {
		key, err := s.ResolveFrom(ctx, ref, from)
		if err != nil {
			return VarSet{}, err
		}
		bv, err := s.P.BuildVariables(ctx, key)
		if errors.Is(err, errs.ErrUnsupported) {
			return VarSet{}, errs.Bamboof("cannot reuse variables of %s", key).
				WithWhy("this Bamboo server does not return the variables a build used").
				WithTry("pass the values with --var")
		}
		if err != nil {
			return VarSet{}, err
		}
		set.FromBuild = key
		label := "from " + BuildLabel(key)
		for _, name := range sortedNames(bv) {
			val := bv[name]
			if set.DeclaredKnown && !declaredNames[name] && !inTarget(name) {
				continue
			}
			if val == MaskedDisplay {
				keep := "leaving it unset"
				if v, ok := vars[name]; ok {
					keep = "keeping the " + v.Source + " value"
				}
				set.Warnings = append(set.Warnings, fmt.Sprintf("%s is masked in %s; %s", name, key, keep))
				continue
			}
			v := get(name)
			v.Value, v.Source = val, label
		}
		if !set.DeclaredKnown {
			set.Warnings = append(set.Warnings, fmt.Sprintf("plan variables of %s cannot be listed; took every variable of %s", ref.MasterKey, key))
		}
	}

	set.Declared = declaredNames
	for _, name := range sortedNames(vars) {
		set.Vars = append(set.Vars, *vars[name])
	}
	return set, nil
}

// Edit is one variable changed in bam run --edit's editor.
type Edit struct{ Name, Value string }

// ValidateVars applies the rules to already-fetched values: the --var flags,
// ${ENV} resolution, required names and allowed values. It touches no
// network and no clock, so the terminal UI calls it on every keystroke.
func ValidateVars(ref PlanRef, base VarSet, flags []string, getenv func(string) string) (VarSet, error) {
	return ValidateEdited(ref, base, flags, nil, getenv)
}

// ApplyVars applies the --var flags to base and checks no rule. It is the set
// bam run --edit opens the editor on: a required name still empty or an
// unset ${ENV} is shown there, not refused. Only --var syntax fails.
func ApplyVars(ref PlanRef, base VarSet, flags []string) (VarSet, error) {
	return applyInputs(ref, base, flags, nil)
}

// ValidateEdited is ValidateVars with the editor's changes applied last:
// plan < target < --from < --var < edit. An edited value of exactly ${NAME}
// is read from the environment like a target default.
func ValidateEdited(ref PlanRef, base VarSet, flags []string, edits []Edit, getenv func(string) string) (VarSet, error) {
	set, err := applyInputs(ref, base, flags, edits)
	if err != nil {
		return VarSet{}, err
	}
	return checkRules(ref, set, getenv)
}

// applyInputs lays the flags, then the edits, over base and warns about
// names the plan does not declare. An edited name already warned about as a
// flag is not warned about twice.
func applyInputs(ref PlanRef, base VarSet, flags []string, edits []Edit) (VarSet, error) {
	set := VarSet{FromBuild: base.FromBuild, DeclaredKnown: base.DeclaredKnown, Declared: base.Declared}
	set.Warnings = append(set.Warnings, base.Warnings...)

	vars := map[string]*ResolvedVar{}
	for i := range base.Vars {
		v := base.Vars[i]
		vars[v.Name] = &v
	}
	get := func(name string) *ResolvedVar {
		v, ok := vars[name]
		if !ok {
			v = &ResolvedVar{Name: name, Secret: config.IsMaskedName(name)}
			vars[name] = v
		}
		return v
	}
	t := ref.Target
	inTarget := func(name string) bool {
		if t == nil {
			return false
		}
		_, ok := t.Defaults[name]
		return ok
	}
	warned := map[string]bool{}
	warnUndeclared := func(name string) {
		if !set.DeclaredKnown || base.Declared[name] || inTarget(name) {
			return
		}
		warned[name] = true
		w := fmt.Sprintf("%s is not a declared variable of %s", name, ref.MasterKey)
		if c := Closest(name, sortedNames(base.Declared), 1); len(c) > 0 {
			w += "; did you mean " + c[0] + "?"
		}
		set.Warnings = append(set.Warnings, w)
	}

	for _, f := range flags {
		name, val, ok := strings.Cut(f, "=")
		if !ok || name == "" {
			if name == "" {
				name = "NAME"
			}
			return VarSet{}, errs.Usagef("--var takes name=value, not %q", f).WithTry("--var " + name + "=VALUE")
		}
		warnUndeclared(name)
		v := get(name)
		v.Value, v.Source = val, "flag"
	}
	for _, e := range edits {
		if !warned[e.Name] {
			warnUndeclared(e.Name)
		}
		v := get(e.Name)
		v.Value, v.Source = e.Value, "edit"
	}

	for _, name := range sortedNames(vars) {
		set.Vars = append(set.Vars, *vars[name])
	}
	return set, nil
}

// checkRules resolves ${ENV} values of target defaults and edits, then
// enforces required names and allowed values. set is not modified.
func checkRules(ref PlanRef, set VarSet, getenv func(string) string) (VarSet, error) {
	out := set
	out.Vars = append([]ResolvedVar(nil), set.Vars...)
	vars := map[string]*ResolvedVar{}
	// Vars is sorted by name, so errors come in the same order as before.
	for i := range out.Vars {
		v := &out.Vars[i]
		vars[v.Name] = v
		if v.Source != "preset" && v.Source != "edit" {
			continue
		}
		if envName, ok := config.EnvRef(v.Value); ok {
			val := getenv(envName)
			if val == "" {
				return VarSet{}, errs.Usagef("variable %s needs environment variable %s, which is not set", v.Name, envName).
					WithTry(fmt.Sprintf("export %s=..., or pass --var %s=VALUE", envName, v.Name))
			}
			v.Value, v.Source, v.Secret = val, "env", true
		}
	}
	if t := ref.Target; t != nil {
		for _, name := range t.Required {
			if v, ok := vars[name]; !ok || v.Value == "" {
				return VarSet{}, errs.Usagef("%s is required by preset %s", name, t.Name).
					WithTry(fmt.Sprintf("--var %s=VALUE", name))
			}
		}
		for _, name := range sortedNames(t.Options) {
			v, ok := vars[name]
			if !ok || v.Value == "" {
				continue
			}
			if !slices.Contains(t.Options[name], v.Value) {
				e := errs.Usagef("%s=%q is not allowed by preset %s", name, v.Display(), t.Name).
					WithWhy("allowed: " + strings.Join(t.Options[name], ", "))
				if c := Closest(v.Value, t.Options[name], 1); len(c) > 0 {
					e = e.WithTry(fmt.Sprintf("did you mean --var %s=%s?", name, c[0]))
				}
				return VarSet{}, e
			}
		}
	}
	return out, nil
}

// VarRow is one line of bam plan vars.
type VarRow struct {
	Name     string
	Value    string // plan value
	LastUsed string // value in the reference build, if readable
	Masked   bool
}

// PlanVarsResult is what bam plan vars shows.
type PlanVarsResult struct {
	Rows      []VarRow
	FromBuild string
	Warnings  []string
}

// PlanVars lists declared variables with their values in a reference build
// (from, default "last").
func (s *Service) PlanVars(ctx context.Context, ref PlanRef, from string) (PlanVarsResult, error) {
	var res PlanVarsResult
	if from == "" {
		from = "last"
	}
	var used map[string]string
	if key, err := s.ResolveFrom(ctx, ref, from); err == nil {
		bv, err := s.P.BuildVariables(ctx, key)
		switch {
		case err == nil:
			used, res.FromBuild = bv, key
		case errors.Is(err, errs.ErrUnsupported):
			res.Warnings = append(res.Warnings, "this server does not return the variables a build used")
		default:
			return res, err
		}
	} else if errs.KindOf(err) != errs.KindUsage || !strings.EqualFold(from, "last") {
		return res, err
	}

	declared, err := s.P.ListVariables(ctx, ref.MasterKey)
	switch {
	case err == nil:
		for _, d := range declared {
			res.Rows = append(res.Rows, VarRow{Name: d.Name, Value: d.Value, LastUsed: used[d.Name],
				Masked: d.Masked || config.IsMaskedName(d.Name)})
		}
	case errors.Is(err, errs.ErrUnsupported):
		res.Warnings = append(res.Warnings, fmt.Sprintf("plan variables of %s cannot be listed; showing the variables of %s", ref.MasterKey, res.FromBuild))
		for _, name := range sortedNames(used) {
			res.Rows = append(res.Rows, VarRow{Name: name, LastUsed: used[name], Masked: config.IsMaskedName(name) || used[name] == MaskedDisplay})
		}
	default:
		return res, err
	}
	return res, nil
}

func sortedNames[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
