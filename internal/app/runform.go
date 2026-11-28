package app

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/rojcarranza/bam/internal/config"
)

// NewRunForm builds the form of bam run --plan from the variables the run
// would send. A secret opens as the plan's stored value, so the shared line
// leaves it out until the person asks for it to come from their shell.
func NewRunForm(ref PlanRef, vs VarSet) PresetForm {
	f := PresetForm{Plan: ref.MasterKey, Branch: ref.Branch, RunMode: true}
	for _, v := range vs.Vars {
		row := FormRow{Name: v.Name, Orig: v.Value, Undeclared: vs.DeclaredKnown && !v.Declared}
		if v.Secret {
			row.Secret = true
			if _, ok := config.EnvRef(v.Value); ok {
				row.Value = v.Value
			} else if v.Source == "plan" {
				row.Value = "${" + config.EnvNameFor(v.Name) + "}"
				row.CanStore, row.Commented, row.OrigCommented = true, true, true
			} else {
				// A literal typed with --var is never shown or shared; the run still
				// sends it, and the line asks the shell for the variable instead.
				row.Value = "${" + config.EnvNameFor(v.Name) + "}"
				row.Literal = true
			}
			row.Orig = row.Value
			f.Rows = append(f.Rows, row)
			continue
		}
		row.Value, row.Plan = v.Value, v.PlanValue
		if strings.HasPrefix(v.Source, "from ") {
			row.From, row.FromValue = vs.FromBuild, v.Value
		}
		row.Choices = appendChoice(nil, v.Value, v.PlanValue)
		f.Rows = append(f.Rows, row)
	}
	return f
}

// RunEdits are the variables the person changed in the run form, to lay over
// the values the run opened with.
func (f PresetForm) RunEdits() []Edit {
	var out []Edit
	for _, r := range f.Rows {
		switch {
		case r.Secret && r.Commented:
		case r.Secret && (r.OrigCommented || r.Value != r.Orig):
			out = append(out, Edit{Name: r.Name, Value: r.Value})
		case !r.Secret && r.Value != r.Orig:
			out = append(out, Edit{Name: r.Name, Value: r.Value})
		}
	}
	return out
}

// RunRecent reads the values recent builds of the run's plan ran with, for
// the form's arrow keys.
func (s *Service) RunRecent(ctx context.Context, ref PlanRef, f PresetForm) []RecentVar {
	d := config.TargetDraft{BuildPlan: ref.PlanKey}
	for _, r := range f.Rows {
		if !r.Secret {
			d.Vars = append(d.Vars, config.DraftVar{Name: r.Name, Value: r.Value})
		}
	}
	return s.RecentValues(ctx, d)
}

// ShareCommand is the bam run line that starts the build the form describes
// on another person's computer: it names the plan, never a preset or a
// server alias, and carries only what changes the build. A secret is written
// as the shell reference "${NAME:?}", so the runner's shell fills it in and
// stops when it is unset instead of sending an empty value.
func ShareCommand(ref PlanRef, f PresetForm) string {
	parts := []string{"bam", "run", config.ShellWord(ref.MasterKey)}
	if ref.Branch != "" {
		parts = append(parts, "--branch", config.ShellWord(ref.Branch))
	}
	if ref.Revision != "" {
		parts = append(parts, "--revision", config.ShellWord(ref.Revision))
	}
	for _, name := range slices.Sorted(maps.Keys(ref.Repositories)) {
		parts = append(parts, "--repository", config.ShellWord(name+"="+ref.Repositories[name]))
	}
	for _, r := range f.Rows {
		switch {
		case r.Secret && r.Commented:
		case r.Secret || isEnvRef(r.Value) && r.Value != r.Orig:
			// A typed (edited) ${NAME} is read from the environment on ctrl-r, so the line
			// asks the runner's shell for it the same way instead of sending the text.
			env, ok := config.EnvRef(r.Value)
			if !ok {
				continue
			}
			parts = append(parts, "--var", config.ShellWord(r.Name)+`="${`+env+`:?}"`)
		case r.Value != r.Plan || r.Undeclared:
			parts = append(parts, "--var", config.ShellWord(r.Name+"="+r.Value))
		}
	}
	return strings.Join(parts, " ")
}

// MainPlan turns a plan branch key into its main plan and the branch's name,
// the way bam preset add does, so a build of a branch opens the form on that
// branch. Any other key comes back unchanged.
func (s *Service) MainPlan(ctx context.Context, key string) (plan, branch string) {
	p, err := s.P.GetPlan(ctx, key)
	if err != nil || p.MasterKey == "" || p.MasterKey == key {
		return key, ""
	}
	bs, err := s.P.ListBranches(ctx, p.MasterKey)
	if err != nil {
		return key, ""
	}
	for _, b := range bs {
		name := b.ShortName
		if name == "" {
			name = b.Name
		}
		if b.Key == key && name != "" {
			return p.MasterKey, name
		}
	}
	return key, ""
}

func isEnvRef(v string) bool { _, ok := config.EnvRef(v); return ok }
