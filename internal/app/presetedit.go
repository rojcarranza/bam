package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/config"
)

// NewPresetEditForm builds the form bam preset edit opens: one row per
// variable the preset sets, then one per plan variable it leaves out, then the
// preset's other settings. t is the resolved preset and planDefaults maps each
// variable the plan declares to its own value; it may be empty when Bamboo
// cannot be read.
func NewPresetEditForm(t config.ResolvedTarget, planDefaults map[string]string) PresetForm {
	f := PresetForm{Name: t.Name, Plan: t.Plan, Branch: t.Branch, Editing: true}
	row := func(name, value string, in bool) FormRow {
		r := FormRow{Name: name, Value: value, Orig: value, InPreset: in, Options: slices.Clone(t.Options[name])}
		r.OrigOptions = slices.Clone(r.Options)
		if _, isRef := config.EnvRef(value); isRef || config.IsMaskedName(name) {
			r.Secret = true
			r.Literal = !isRef && value != ""
			return r
		}
		r.Plan = planDefaults[name]
		if len(r.Options) > 0 {
			r.Choices = slices.Clone(r.Options)
			r.Choices = appendChoice(r.Choices, value)
		} else {
			r.Choices = appendChoice(nil, value, r.Plan)
		}
		return r
	}
	for _, name := range slices.Sorted(maps.Keys(t.Defaults)) {
		f.Rows = append(f.Rows, row(name, t.Defaults[name], true))
	}
	for _, name := range slices.Sorted(maps.Keys(planDefaults)) {
		if _, set := t.Defaults[name]; !set {
			f.Rows = append(f.Rows, row(name, planDefaults[name], false))
		}
	}
	field := func(key, label, value string) {
		f.Fields = append(f.Fields, FormField{Key: key, Label: label, Value: value, Orig: value})
	}
	field("name", "name", t.Name)
	field("branch", "branch", t.Branch)
	for _, n := range slices.Sorted(maps.Keys(t.Repositories)) {
		field("repositories."+n, "repository "+n, t.Repositories[n])
	}
	field("required", "required", strings.Join(t.Required, ", "))
	field("timeout", "timeout", durationText(t.Timeout))
	return f
}

// durationText is a timeout as people write it: 45m, not 45m0s.
func durationText(d config.Duration) string {
	if d == 0 {
		return ""
	}
	s := time.Duration(d).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// sameTimeout reports whether two timeout texts are the same duration.
func sameTimeout(a, b string) bool {
	da, ea := time.ParseDuration(strings.TrimSpace(a))
	db, eb := time.ParseDuration(strings.TrimSpace(b))
	if ea != nil || eb != nil {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return da == db
}

// Validate checks what the person typed in an edit form, so a mistake is
// shown while the form is open and not after it closed.
func (f PresetForm) Validate() error {
	if err := f.validateHeld(); err != nil {
		return err
	}
	for _, fl := range f.Fields {
		v := strings.TrimSpace(fl.Value)
		switch {
		case fl.Key == "name" && !config.ValidTargetName(v):
			return fmt.Errorf("%q is not a valid preset name: lowercase letters, digits, - and _", v)
		case fl.Key == "name" && v != strings.TrimSpace(fl.Orig) && slices.Contains(f.TakenNames, v):
			return fmt.Errorf("preset %q already exists", v)
		case fl.Key == "name" && v != strings.TrimSpace(fl.Orig) && f.TakenElsewhere[v] != "":
			return TakenElsewhereError(v, f.TakenElsewhere[v])
		case fl.Key == "name" && v != strings.TrimSpace(fl.Orig) && f.RenameBlocked != "":
			return errors.New(f.RenameBlocked)
		case fl.Key == "timeout" && v != "":
			if d, err := time.ParseDuration(v); err != nil || d < 0 {
				return fmt.Errorf("%q is not a duration like 45m or 1h30m", v)
			}
		case strings.HasPrefix(fl.Key, "repositories.") && !fl.Removed && v == "":
			return fmt.Errorf("%s needs a branch", fl.Label)
		}
	}
	branch := ""
	for _, fl := range f.Fields {
		if fl.Key == "branch" {
			branch = strings.TrimSpace(fl.Value)
		}
	}
	for _, fl := range f.Fields {
		if branch == "" && strings.HasPrefix(fl.Key, "repositories.") && !fl.Removed {
			return fmt.Errorf("repositories need a branch")
		}
	}
	for _, r := range f.Rows {
		if r.Removed || r.Secret || len(r.Options) == 0 || r.Value == "" {
			continue
		}
		if !slices.Contains(r.Options, r.Value) {
			return fmt.Errorf("%s=%q is not one of %s", r.Name, r.Value, strings.Join(r.Options, ", "))
		}
	}
	return nil
}

// validateHeld refuses what the edited file cannot remove because another
// file holds it, naming that file.
func (f PresetForm) validateHeld() error {
	no := func(what, key string) error {
		return fmt.Errorf("%s is set in %s, not in %s: change it there, or give it a value here", what, f.Held.Only[key], f.Path)
	}
	for _, r := range f.Rows {
		if _, only := f.Held.Only["variables."+r.Name]; only && r.Removed {
			return no(r.Name, "variables."+r.Name)
		}
		if _, only := f.Held.Only["options."+r.Name]; only && len(r.Options) == 0 && len(r.OrigOptions) > 0 {
			return no("the options of "+r.Name, "options."+r.Name)
		}
	}
	for _, fl := range f.Fields {
		if _, only := f.Held.Only[fl.Key]; only && fl.Removed {
			return no(fl.Label, fl.Key)
		}
		if strings.TrimSpace(fl.Value) != "" {
			continue
		}
		if _, only := f.Held.Only[fl.Key]; only && (fl.Key == "branch" || fl.Key == "timeout" || fl.Key == "required") {
			return no(fl.Label, fl.Key)
		}
	}
	return nil
}

// Edit turns the form into the changes it holds against what it opened with.
// A variable the preset left out is written only when its value changed, an
// empty one included, as --var NAME= does; x leaves it to the plan.
func (f PresetForm) Edit() config.PresetEdit {
	var e config.PresetEdit
	for _, r := range f.Rows {
		switch {
		case r.Removed:
			if r.InPreset {
				e.Unset = append(e.Unset, r.Name)
			}
		case r.Value != r.Orig:
			e.Set = append(e.Set, config.VarValue{Name: r.Name, Value: r.Value})
		}
		if !slices.Equal(r.Options, r.OrigOptions) {
			if e.Options == nil {
				e.Options = map[string][]string{}
			}
			e.Options[r.Name] = r.Options
		}
	}
	var repos []config.RepoBranch
	reposChanged := false
	for _, fl := range f.Fields {
		v := strings.TrimSpace(fl.Value)
		changed := v != strings.TrimSpace(fl.Orig)
		if fl.Key == "timeout" {
			changed = !sameTimeout(v, fl.Orig)
		}
		switch {
		case fl.Key == "name" && changed:
			e.Rename = v
		case fl.Key == "branch" && changed:
			e.Branch = &v
		case fl.Key == "timeout" && changed:
			e.Timeout = &v
		case fl.Key == "required" && changed:
			req := SplitList(v)
			e.Required = &req
		case strings.HasPrefix(fl.Key, "repositories.") && fl.Removed:
			reposChanged = reposChanged || fl.Orig != ""
		case strings.HasPrefix(fl.Key, "repositories."):
			repos = append(repos, config.RepoBranch{Name: strings.TrimPrefix(fl.Key, "repositories."), Branch: v})
			reposChanged = reposChanged || changed
		}
	}
	if reposChanged {
		e.Repositories = &repos
	}
	return e
}

// RepositoryChanges counts the repositories the form adds, removes or gives
// another branch, the way the save lists them: one change each.
func (f *PresetForm) RepositoryChanges() int {
	n := 0
	for _, fl := range f.Fields {
		if !strings.HasPrefix(fl.Key, "repositories.") {
			continue
		}
		if fl.Removed {
			if fl.Orig != "" {
				n++
			}
		} else if strings.TrimSpace(fl.Value) != strings.TrimSpace(fl.Orig) {
			n++
		}
	}
	return n
}

// SplitList splits a comma separated list, dropping blanks and repeats.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// PlanVariables reads the plan's declared variables with their own values, for
// the edit form. A server that cannot list them gives none.
func (s *Service) PlanVariables(ctx context.Context, plan string) map[string]string {
	return s.PlanDefaults(ctx, config.TargetDraft{Plan: plan})
}

// SetRepository sets the branch of repository name, adding the setting when
// the form does not have it yet, and brings back one that was removed.
func (f *PresetForm) SetRepository(name, branch string) {
	key := "repositories." + name
	for i := range f.Fields {
		if f.Fields[i].Key == key {
			f.Fields[i].Value, f.Fields[i].Removed = branch, false
			return
		}
	}
	at := len(f.Fields)
	for i, fl := range f.Fields {
		if fl.Key == "branch" || strings.HasPrefix(fl.Key, "repositories.") {
			at = i + 1
		}
	}
	fl := FormField{Key: key, Label: "repository " + name, Value: branch}
	f.Fields = slices.Insert(f.Fields, at, fl)
}

// UseRepositories makes the repositories the form holds those Bamboo lists
// for the new branch: each listed one gets its branch, one the preset had that
// Bamboo does not list is removed, and one added in this form that Bamboo does
// not list is dropped.
func (f *PresetForm) UseRepositories(repos []RepoBranch) {
	listed := map[string]bool{}
	for _, r := range repos {
		listed[r.Name] = true
		f.SetRepository(r.Name, r.Branch)
	}
	f.Fields = slices.DeleteFunc(f.Fields, func(fl FormField) bool {
		return strings.HasPrefix(fl.Key, "repositories.") && !listed[strings.TrimPrefix(fl.Key, "repositories.")] && fl.Orig == ""
	})
	for i := range f.Fields {
		fl := &f.Fields[i]
		if strings.HasPrefix(fl.Key, "repositories.") && !listed[strings.TrimPrefix(fl.Key, "repositories.")] {
			fl.Removed = true
		}
	}
}

// PlanBranchRepositories reads the repositories the newest finished build of
// plan on branch used, with the branch each built. A repository the build does
// not record a branch for is left out. An empty branch has none.
func (s *Service) PlanBranchRepositories(ctx context.Context, plan, branch string) ([]RepoBranch, error) {
	if branch == "" {
		return nil, nil
	}
	branch, err := s.ExpandBranch(branch)
	if err != nil {
		return nil, err
	}
	key, err := s.ResolveBranch(ctx, plan, branch)
	if err != nil {
		return nil, err
	}
	built, _, err := s.BuiltRepositories(ctx, key)
	if err != nil {
		return nil, err
	}
	var out []RepoBranch
	for _, name := range slices.Sorted(maps.Keys(built)) {
		if built[name] != "" {
			out = append(out, RepoBranch{Name: name, Branch: built[name]})
		}
	}
	return out, nil
}

// TakenElsewhereError says why a shared preset cannot take a name that only
// another repository's own preset has.
func TakenElsewhereError(name, repo string) error {
	return fmt.Errorf("preset %q already exists in the repository %s", name, repo)
}
