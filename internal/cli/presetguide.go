package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view/tui"
	"github.com/spf13/cobra"
)

// interactive reports whether bam preset add may ask questions: on a
// terminal, and not with --json or --print. It is the rule of the run picker,
// so a script never waits on a key.
func (f *presetAddFlags) interactive(r *runtime) bool {
	return !r.flags.json && !f.printOnly && r.tuiAllowed()
}

// askedCanceled says nothing was written when a prompt ended with Esc.
func askedCanceled(r *runtime, err error) error {
	if errors.Is(err, context.Canceled) {
		r.note("nothing written", "")
	}
	return err
}

// guide asks for what the command line left out, in the order a person
// thinks of it: the plan, its branch, then the preset's name. It connects
// only when it must list plans, and returns that service for reuse.
func (f *presetAddFlags) guide(r *runtime, cmd *cobra.Command, name string) (*app.Service, string, error) {
	var svc *app.Service
	if f.plan == "" && !app.IsBuildKey(f.from) {
		var backend Backend
		var err error
		if svc, backend, err = r.connect(cmd.Context(), ""); err != nil {
			return nil, "", err
		}
		if f.plan, err = f.askPlan(r, cmd.Context(), backend); err != nil {
			return nil, "", askedCanceled(r, err)
		}
		if f.branch == "" && (f.from == "" || app.IsSourcePlan(f.from)) {
			if f.branch, err = f.askBranch(r, cmd.Context(), backend); err != nil {
				return nil, "", askedCanceled(r, err)
			}
		}
	}
	if name == "" {
		plan := f.plan
		if plan == "" {
			plan = f.from[:strings.LastIndex(f.from, "-")]
		}
		var err error
		name, err = r.env.Ask(cmd.Context(), r.env.Stdin, r.env.Stdout, "preset name?", defaultTargetName(plan))
		if err != nil {
			return nil, "", askedCanceled(r, err)
		}
		if name == "" {
			return nil, "", errs.Usagef("a preset needs a name").WithTry("bam preset add NAME")
		}
	}
	return svc, name, nil
}

func (f *presetAddFlags) askPlan(r *runtime, ctx context.Context, backend Backend) (string, error) {
	cfg, err := r.config()
	if err != nil {
		return "", err
	}
	server, err := cfg.SelectServer(r.flags.server, "")
	if err != nil {
		return "", err
	}
	projects := cfg.ProjectKeys(server)
	if len(projects) == 0 {
		all, perr := backend.ListProjects(ctx)
		return "", noProjectsError(server.Alias, all, perr)
	}
	var keys []string
	var items []tui.Choice
	for _, project := range projects {
		plans, err := backend.ListPlans(ctx, project)
		if err != nil {
			return "", err
		}
		for _, p := range plans {
			if p.MasterKey == "" || p.MasterKey == p.Key {
				keys = append(keys, p.Key)
				items = append(items, tui.Choice{Label: p.Key, Detail: p.Name})
			}
		}
	}
	if len(keys) == 0 {
		return "", errs.Usagef("no plans to pick from").
			WithWhy("the server lists none for " + strings.Join(projects, ", ")).
			WithTry("bam preset add NAME --plan PROJ-PLAN")
	}
	i, err := r.env.Choose(ctx, r.env.Stdin, r.env.Stdout, "add a preset for which plan?", items)
	if err != nil {
		return "", err
	}
	return keys[i], nil
}

// askBranch offers the plan's branches, with the plan itself first. It asks
// nothing when the plan has none, and returns "" for the plan's default branch.
func (f *presetAddFlags) askBranch(r *runtime, ctx context.Context, backend Backend) (string, error) {
	branches, err := backend.ListBranches(ctx, f.plan)
	if err != nil || len(branches) == 0 {
		return "", nil
	}
	names := []string{""}
	items := []tui.Choice{{Label: "(no branch)", Detail: "the plan itself"}}
	for _, b := range branches {
		name := b.ShortName
		if name == "" {
			name = b.Name
		}
		names = append(names, name)
		items = append(items, tui.Choice{Label: name})
	}
	i, err := r.env.Choose(ctx, r.env.Stdin, r.env.Stdout, "which branch of "+f.plan+"?", items)
	if err != nil {
		return "", err
	}
	return names[i], nil
}

// target is the file and key path d is written to, refusing a name another
// presets file holds. The form calls it first, so a filled form is never
// refused.
func (f *presetAddFlags) target(r *runtime, d config.TargetDraft) (string, []string, error) {
	if f.shared {
		if r.cfg.ProjectPath == "" {
			return "", nil, errs.Usagef("no .bam.yaml in this repository").
				WithTry("bam init --server ALIAS --project KEY, or drop --shared to write your presets file")
		}
		return r.cfg.ProjectPath, []string{"targets"}, nil
	}
	path, err := r.presetsTarget(r.cfg, d, f.file)
	if err != nil {
		return "", nil, err
	}
	return path, []string{"presets"}, nil
}

// fillForm opens the form of d's variables and returns d with the values the
// person chose. It writes nothing.
func (f *presetAddFlags) fillForm(r *runtime, cmd *cobra.Command, svc *app.Service, d config.TargetDraft) (config.TargetDraft, error) {
	path, keyPath, err := f.target(r, d)
	if err != nil {
		return d, err
	}
	if taken, err := config.HasTarget(path, keyPath, d.Name); err != nil {
		return d, err
	} else if taken && !f.force {
		return d, errs.Usagef("preset %q already exists in %s", d.Name, path).WithTry("pass --force to replace it")
	}
	ctx := cmd.Context()
	form := app.NewPresetForm(d, svc.PlanDefaults(ctx, d))
	form.Path = path
	edited, err := r.env.EditPreset(ctx, r.env.Stdin, r.env.Stdout, form, func(ctx context.Context) []app.RecentVar {
		return svc.RecentValues(ctx, d)
	})
	if err != nil {
		return d, askedCanceled(r, err)
	}
	return edited.Apply(d), nil
}
