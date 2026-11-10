package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
)

// editVars is the loop of bam run --edit (run-edit spec §3). It opens the
// resolved variables in the editor and reopens it, with the problems on top
// and the user's own text below, until the buffer validates or the user
// aborts. A rule the editor can satisfy (required, options, an unset ${ENV})
// is shown in the first buffer rather than refused.
func (r *runtime) editVars(ctx context.Context, ref app.PlanRef, base app.VarSet, flags []string, server string, dryRun bool) (app.VarSet, error) {
	opened, err := app.ApplyVars(ref, base, flags)
	if err != nil {
		return app.VarSet{}, err
	}
	cfg, err := r.config()
	if err != nil {
		return app.VarSet{}, err
	}
	editor := view.EditorCommand(r.env.Getenv, cfg.Machine.Editor, r.env.GOOS)

	var problems []string
	if _, err := app.ValidateEdited(ref, base, flags, nil, r.env.Getenv); err != nil {
		problems = ruleProblem(err)
	}
	buf := app.EditBuffer(ref, opened, r.env.Getenv, server, dryRun, problems)
	for {
		text, err := r.env.RunEditor(editor, buf)
		if ctx.Err() != nil {
			return app.VarSet{}, ctx.Err()
		}
		if errors.Is(err, errEditorCancelled) {
			return app.VarSet{}, r.abortRun()
		}
		if err != nil {
			return app.VarSet{}, err
		}
		edits, abort, problems := app.ParseEdits(text, ref, opened)
		if abort {
			return app.VarSet{}, r.abortRun()
		}
		vs, err := app.ValidateEdited(ref, base, flags, edits, r.env.Getenv)
		if err == nil && len(problems) == 0 {
			return vs, nil
		}
		if err != nil {
			problems = append(problems, ruleProblem(err)...)
		}
		buf = app.ReopenBuffer(text, problems)
	}
}

// abortRun reports a run the user abandoned in the editor. It exits 130,
// like an interrupt: both are a user cancel, and nothing was triggered.
func (r *runtime) abortRun() error {
	fmt.Fprintln(r.env.Stderr, "run aborted: nothing triggered")
	return silentError{context.Canceled}
}

// ruleProblem is a validation error as the buffer shows it: What, then Why
// on the next line. Try is dropped; it suggests --var, and the user is
// already in the editor.
func ruleProblem(err error) []string {
	var e *errs.Error
	if errors.As(err, &e) {
		if e.Why != "" {
			return []string{e.What + "\n" + e.Why}
		}
		return []string{e.What}
	}
	return []string{err.Error()}
}
