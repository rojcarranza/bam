package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// GenerateOptions are the inputs of bam preset add and bam init --plan.
type GenerateOptions struct {
	Name    string
	PlanKey string
	From    string // build for "last used" values; default last
	Branch  string
}

// GenerateTarget reads the plan's declared variables (or, when unsupported,
// a build's variables) and returns a commented target draft (spec §3.9).
func (s *Service) GenerateTarget(ctx context.Context, o GenerateOptions) (config.TargetDraft, error) {
	d, _, err := s.generate(ctx, o)
	return d, err
}

// generate is GenerateTarget that also reports whether the names came from
// the plan's declared list rather than from a build.
func (s *Service) generate(ctx context.Context, o GenerateOptions) (config.TargetDraft, bool, error) {
	if !config.ValidTargetName(o.Name) {
		return config.TargetDraft{}, false, errs.Usagef("invalid preset name %q", o.Name).
			WithWhy("preset names are lowercase letters, digits, - and _, starting with a letter")
	}
	if !IsPlanKey(o.PlanKey) {
		return config.TargetDraft{}, false, errs.Usagef("%q is not a plan key", o.PlanKey).WithTry("bam plan list")
	}
	if _, err := s.P.GetPlan(ctx, o.PlanKey); err != nil {
		return config.TargetDraft{}, false, err
	}
	d := config.TargetDraft{Name: o.Name, Plan: o.PlanKey, Branch: o.Branch}
	ref := PlanRef{PlanKey: o.PlanKey, MasterKey: o.PlanKey}
	if o.Branch != "" {
		key, err := s.ResolveBranch(ctx, o.PlanKey, o.Branch)
		if err != nil {
			return d, false, err
		}
		ref.PlanKey, ref.Branch = key, o.Branch
	}

	from := o.From
	if from == "" {
		from = "last"
	}
	var used map[string]string
	usedKey := ""
	if key, err := s.ResolveFrom(ctx, ref, from); err == nil {
		if bv, err := s.P.BuildVariables(ctx, key); err == nil {
			used, usedKey = bv, key
		} else if o.From != "" && !errors.Is(err, errs.ErrUnsupported) {
			return d, false, err
		}
	} else if o.From != "" {
		return d, false, err
	}

	declared, err := s.P.ListVariables(ctx, o.PlanKey)
	switch {
	case err == nil:
		for _, v := range declared {
			dv := draftVar(v.Name, v.Value, v.Masked, used, usedKey, true)
			d.Vars = append(d.Vars, dv)
			if v.Value == "" && !isSecretDraft(dv) {
				d.Required = append(d.Required, v.Name)
			}
		}
	case errors.Is(err, errs.ErrUnsupported):
		if used == nil {
			d.Note = "plan variables are not readable on this server; add them under defaults"
			return d, false, nil
		}
		d.Note = "plan variables are not readable on this server; names come from " + usedKey
		for _, name := range sortedNames(used) {
			d.Vars = append(d.Vars, draftVar(name, used[name], false, used, usedKey, false))
		}
		return d, false, nil
	default:
		return d, false, err
	}
	return d, true, nil
}

func isSecretDraft(v config.DraftVar) bool {
	_, ok := config.EnvRef(v.Value)
	return ok
}

func draftVar(name, value string, masked bool, used map[string]string, usedKey string, declared bool) config.DraftVar {
	if masked || value == MaskedDisplay || used[name] == MaskedDisplay || config.IsMaskedName(name) {
		env := config.EnvNameFor(name)
		return config.DraftVar{Name: name, Value: "${" + env + "}", Comment: "masked by Bamboo; set env var " + env}
	}
	if !declared {
		return config.DraftVar{Name: name, Value: value, Comment: "from " + usedKey}
	}
	if value != "" {
		return config.DraftVar{Name: name, Value: value, Comment: "plan default"}
	}
	comment := "no plan default"
	if last := used[name]; last != "" {
		comment += fmt.Sprintf("; last used %q in %s", last, usedKey)
	}
	return config.DraftVar{Name: name, Value: "", Comment: comment}
}
