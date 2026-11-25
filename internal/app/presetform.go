package app

import (
	"context"
	"regexp"
	"slices"

	"github.com/rojcarranza/bam/internal/config"
)

// FormRow is one variable of the preset form of bam preset add.
type FormRow struct {
	Name   string
	Secret bool
	// Value is the literal value, or for a secret the ${ENV} reference the
	// preset writes. A secret row never holds a typed value.
	Value string
	// Plan is the plan's default; empty when the plan has none.
	Plan string
	// Choices are the values the arrow keys step through, without duplicates.
	// A secret row has none.
	Choices []string
	// CanStore marks a secret the plan stores: Commented then says the row
	// keeps the stored value, written as a commented line.
	CanStore  bool
	Commented bool

	// The fields below are used by bam preset edit only.
	// InPreset marks a variable the saved preset sets; the others are plan
	// variables it leaves out, written only once their value changes.
	InPreset bool
	// Orig is the value the form opened with; a row is written only when it differs.
	Orig string
	// Removed drops the variable from the preset, so the plan's own value applies.
	Removed bool
	// Options are the values the preset allows; none means any value.
	Options     []string
	OrigOptions []string
	// Literal marks a secret whose saved value is a hand-written literal: the
	// form never shows it, and typing an environment variable name replaces it.
	Literal bool
}

// FormField is one setting of a preset other than its variables, shown by
// bam preset edit as a text row: name, branch, timeout, required, or a
// repository's branch under the key "repositories.NAME".
type FormField struct {
	Key, Label string
	Value      string
	Orig       string
	// Removed drops a repository from the preset. Only repository settings
	// can be removed; the others are cleared by emptying their text.
	Removed bool
}

// RepoBranch is one repository of a plan branch and the branch it builds.
type RepoBranch struct{ Name, Branch string }

// PresetForm is what bam preset add shows before it writes the file: the
// draft's variables as rows, in the draft's order.
type PresetForm struct {
	Name, Plan, Branch, Path string
	Rows                     []FormRow
	// Editing marks the form of bam preset edit, whose Fields follow the rows.
	Editing bool
	Fields  []FormField
	// TakenNames are the names no preset can be renamed to, and RenameBlocked
	// is why this one cannot be renamed at all, or empty. Validate uses them
	// so a refusal shows while the form is open.
	TakenNames []string
	// TakenElsewhere maps a name only another repository's own preset has to
	// that repository, for a shared preset whose rename reaches its sets.
	TakenElsewhere map[string]string
	RenameBlocked  string
	// Held names what other files hold, as config.HeldBelow keys them, so
	// Validate refuses a removal the edited file cannot make and the form says
	// whose value then applies.
	Held Held
	// ReadRepos reads the repositories Bamboo lists for a branch of the plan,
	// so the form can offer them when the branch changes. Nil when Bamboo
	// cannot be read.
	ReadRepos func(ctx context.Context, branch string) ([]RepoBranch, error)
}

// Held is config.Held, named here because the terminal view may not import config.
type Held = config.Held

// NewPresetForm builds the form of draft d. planDefaults maps each declared
// variable to the plan's own value.
func NewPresetForm(d config.TargetDraft, planDefaults map[string]string) PresetForm {
	f := PresetForm{Name: d.Name, Plan: d.Plan, Branch: d.Branch}
	for _, v := range d.Vars {
		plan := planDefaults[v.Name]
		row := FormRow{Name: v.Name, Value: v.Value}
		if isSecretDraft(v) {
			row.Secret = true
			row.CanStore, row.Commented = v.Commented, v.Commented
		} else {
			row.Plan = plan
			row.Choices = appendChoice(nil, v.Value, plan)
		}
		f.Rows = append(f.Rows, row)
	}
	return f
}

// AppendChoices appends the values not already in choices.
func AppendChoices(choices []string, values ...string) []string {
	return appendChoice(choices, values...)
}

func appendChoice(choices []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(choices, v) {
			choices = append(choices, v)
		}
	}
	return choices
}

// AddRecent adds the values recent builds ran with after each row's choices.
func (f *PresetForm) AddRecent(recent []RecentVar) {
	for _, rv := range recent {
		for i := range f.Rows {
			if r := &f.Rows[i]; r.Name == rv.Name && !r.Secret {
				r.Choices = appendChoice(r.Choices, rv.Values...)
			}
		}
	}
}

// Differs reports whether the row would write something other than the plan
// does: another value, or a secret that no longer keeps the stored value.
func (r FormRow) Differs() bool {
	if r.Secret {
		return r.CanStore && !r.Commented
	}
	return r.Value != r.Plan
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether name can stand in ${NAME}.
func ValidEnvName(name string) bool { return envNameRe.MatchString(name) }

// Apply returns d with the form's values. Every variable stays in the draft.
func (f PresetForm) Apply(d config.TargetDraft) config.TargetDraft {
	d.Vars = slices.Clone(d.Vars)
	for i := range d.Vars {
		if i < len(f.Rows) && f.Rows[i].Name == d.Vars[i].Name {
			d.Vars[i].Value = f.Rows[i].Value
			d.Vars[i].Commented = f.Rows[i].Secret && f.Rows[i].Commented
		}
	}
	return d
}

// PlanDefaults reads the plan's own value of each declared variable, for the
// form. A server that cannot list them gives none.
func (s *Service) PlanDefaults(ctx context.Context, d config.TargetDraft) map[string]string {
	out := map[string]string{}
	vars, err := s.P.ListVariables(ctx, d.Plan)
	if err != nil {
		return out
	}
	if len(vars) == 0 && d.BuildPlan != "" && d.BuildPlan != d.Plan {
		vars, _ = s.P.ListVariables(ctx, d.BuildPlan)
	}
	for _, v := range vars {
		out[v.Name] = v.Value
	}
	return out
}
