package app

import (
	"context"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/errs"
)

// SyncPlan is what bam preset sync would change in one target. It only
// describes the change; the caller applies Edits with config.SyncTarget.
type SyncPlan struct {
	Name  string
	Plan  string
	File  string            // file that receives Added
	Added []config.DraftVar // declared on the plan, missing from defaults; in plan order
	Stale []string          // in defaults, no longer declared on the plan; sorted, never nil
	Edits []SyncEdit        // one per layer with something to do, lowest precedence first
}

// SyncEdit is the edit for one layer's entry of the target.
type SyncEdit struct {
	Path    string
	KeyPath []string
	Edit    config.TargetEdit
}

// PlanSync compares target t with its plan's declared variables. New names
// are added to the layer that sets the effective plan; names the plan no
// longer declares are kept and marked in every layer that holds them.
func (s *Service) PlanSync(ctx context.Context, t config.ResolvedTarget) (SyncPlan, error) {
	p := SyncPlan{Name: t.Name, Plan: t.Plan, Stale: []string{}}
	d, declared, err := s.generate(ctx, GenerateOptions{Name: t.Name, PlanKey: t.Plan, Branch: t.Branch, Full: true})
	if err != nil {
		return p, err
	}
	if !declared {
		return p, errs.Bamboof("cannot read plan variables of %s on this server", t.Plan).
			WithWhy("sync needs the declared list to tell current names from stale ones").
			Wrap(errs.ErrUnsupported)
	}

	names := map[string]bool{}
	for _, v := range d.Vars {
		names[v.Name] = true
		if _, ok := t.Defaults[v.Name]; !ok {
			p.Added = append(p.Added, v)
		}
	}
	for _, n := range sortedNames(t.Defaults) {
		if !names[n] {
			p.Stale = append(p.Stale, n)
		}
	}

	planLayer := 0
	for i, l := range t.Layers {
		if l.Target.Plan != "" {
			planLayer = i
		}
	}
	for i, l := range t.Layers {
		e := config.TargetEdit{Plan: t.Plan}
		if i == planLayer {
			p.File = l.Path
			e.Add = p.Added
		}
		for _, n := range sortedNames(l.Target.Defaults) {
			if names[n] {
				e.Unmark = append(e.Unmark, n)
			} else {
				e.Mark = append(e.Mark, n)
			}
		}
		if len(e.Add) > 0 || len(e.Mark) > 0 || len(e.Unmark) > 0 {
			p.Edits = append(p.Edits, SyncEdit{Path: l.Path, KeyPath: l.KeyPath, Edit: e})
		}
	}
	return p, nil
}

// DisplayDraftValue returns an added value safe to print: ${ENV} references
// as written, and a literal under a masked name as MaskedDisplay.
func DisplayDraftValue(v config.DraftVar) string {
	if _, ref := config.EnvRef(v.Value); !ref && v.Value != "" && config.IsMaskedName(v.Name) {
		return MaskedDisplay
	}
	return v.Value
}
