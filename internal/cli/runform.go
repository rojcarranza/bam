package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/rojcarranza/bam/internal/view/tui"
	"github.com/spf13/cobra"
)

// checkForm validates the flags of bam run --plan on their own, before any
// config or network. The form and the lists draw on the terminal, so a pipe
// or --no-tui has no way to answer them.
func (f *runFlags) checkForm(r *runtime) error {
	if f.edit {
		return errs.Usagef("--plan and --edit both ask for the variables").WithTry("use one of them")
	}
	if !r.tuiAllowed() {
		return errs.Usagef("--plan needs a terminal").
			WithWhy("the form needs a terminal, and this is a pipe or --no-tui").
			WithTry("pass the values with --var")
	}
	return nil
}

// askTarget finds the plan of bam run --plan when none is named: the plan of
// the --from build, or one picked from the list the way bam preset add picks
// it, with its branch. It returns the service of the server that holds it.
func (f *runFlags) askTarget(r *runtime, cmd *cobra.Command) (*app.Service, string, error) {
	ctx := cmd.Context()
	if key, ok := pageKey(f.from); ok && app.IsBuildKey(key) {
		svc, err := r.connectPage(f.from, key)
		return svc, key[:strings.LastIndex(key, "-")], err
	}
	if app.IsBuildKey(f.from) {
		plan := f.from[:strings.LastIndex(f.from, "-")]
		svc, _, err := r.connectFor(ctx, plan)
		return svc, plan, err
	}
	svc, backend, err := r.connect(ctx, "")
	if err != nil {
		return nil, "", err
	}
	plan, err := r.askPlan(ctx, backend, "run which plan?", "bam run PROJ-PLAN --plan")
	if err != nil {
		return nil, "", canceledRun(r, err)
	}
	if f.branch == "" {
		if f.branch, err = r.askBranch(ctx, backend, plan); err != nil {
			return nil, "", canceledRun(r, err)
		}
	}
	return svc, plan, nil
}

// canceledRun says nothing was started when a prompt ended with Esc.
func canceledRun(r *runtime, err error) error {
	if errors.Is(err, context.Canceled) {
		r.note("nothing started", "")
	}
	return err
}

// formVars opens the form on the variables the run would send and returns
// the ones the person chose, with the shareable command line. start says
// whether the build should run (ctrl-r) or only the line be shown (ctrl-s).
func (f *runFlags) formVars(ctx context.Context, r *runtime, svc *app.Service, ref app.PlanRef) (vs app.VarSet, line string, start bool, err error) {
	base, err := svc.VarBase(ctx, ref, f.from)
	if err != nil {
		return vs, "", false, err
	}
	opened, err := app.ApplyVars(ref, base, f.vars)
	if err != nil {
		return vs, "", false, err
	}
	form := app.NewRunForm(ref, opened)
	edited, err := r.env.EditPreset(ctx, r.env.Stdin, r.env.Stdout, form, func(ctx context.Context) []app.RecentVar {
		return svc.RunRecent(ctx, ref, form)
	})
	if err != nil {
		return vs, "", false, canceledRun(r, err)
	}
	line = app.ShareCommand(ref, edited)
	if edited.StartNow {
		vs, err = app.ValidateEdited(ref, base, f.vars, edited.RunEdits(), r.env.Getenv)
	} else {
		vs, err = app.ApplyEdited(ref, base, f.vars, edited.RunEdits())
	}
	return vs, line, edited.StartNow, err
}

// printLine shows the command ctrl-s made and copies it when the terminal
// allows. With --json the line is a document and the clipboard is left alone.
func (r *runtime) printLine(line string) error {
	if r.flags.json {
		return view.WriteJSON(r.env.Stdout, map[string]string{"command": line})
	}
	if _, err := r.env.Stdout.Write([]byte(line + "\n")); err != nil {
		return err
	}
	if tui.CopyToClipboard(r.env.Stdout, line) == nil {
		r.note("copied to the clipboard, if your terminal allows it; nothing started", "")
	}
	return nil
}
