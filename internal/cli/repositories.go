package cli

import (
	"bufio"
	"context"
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/view"
)

// switchRepositories asks the person to set the repositories' branches on the
// plan branch's Repositories tab when the last build used others, then waits
// for Enter. bam never saves the tab itself. Without a terminal or with
// --json it stops instead of asking, and the error carries the link.
func (r *runtime) switchRepositories(ctx context.Context, c app.RepoCheck) error {
	if !c.Needed() {
		return nil
	}
	if r.flags.json || !r.env.StdinTTY {
		return errs.Usagef("%s builds other branches than this run wants", c.PlanKey).
			WithWhy("bam cannot ask without a terminal, and it never changes the repositories itself").
			WithTry("switch them on " + c.URL + ", then run again")
	}
	if err := r.env.OpenBrowser(c.URL); err != nil {
		fmt.Fprintf(r.env.Stderr, "bam could not open the browser: %v\n", err)
	}
	fmt.Fprint(r.env.Stderr, "Switch the branches on the Repositories tab, save, then press Enter to trigger (Ctrl-C stops): ")
	done := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(r.env.Stdin).ReadString('\n')
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		fmt.Fprintln(r.env.Stderr)
		return ctx.Err()
	}
}

// confirmRepositories reports, once the followed build is over, which branch
// each wanted repository built. It never fails the run.
func (r *runtime) confirmRepositories(ctx context.Context, svc *app.Service, buildKey string, want map[string]string) {
	if len(want) == 0 || r.flags.json || ctx.Err() != nil {
		return
	}
	changes, ok, err := svc.ConfirmRepositories(ctx, buildKey, want)
	if err != nil {
		return
	}
	view.RepoConfirm(r.env.Stderr, want, changes, ok)
}

// checkSetRepositories checks every preset of a set before its first stage:
// each run whose preset names repositories must find them on the wanted
// branches, or the person switches them first, as bam run asks. A dry run
// only prints what differs.
func (r *runtime) checkSetRepositories(ctx context.Context, svc *app.Service, p app.SetPlan, dryRun bool) error {
	seen := map[string]bool{}
	for _, st := range p.Stages {
		for _, run := range st {
			if len(run.Ref.Repositories) == 0 || seen[run.Ref.PlanKey] {
				continue
			}
			seen[run.Ref.PlanKey] = true
			c, err := svc.CheckRepositories(ctx, run.Ref, run.Ref.Repositories)
			if err != nil {
				return err
			}
			if dryRun && r.flags.json {
				continue
			}
			if c.Needed() && !r.flags.json {
				o, err := r.out()
				if err != nil {
					return err
				}
				o.W = r.env.Stderr
				fmt.Fprintf(o.W, "%s (preset %s)\n", run.Ref.PlanKey, run.Target)
				view.RepoCheck(o, c)
			}
			if !dryRun {
				if err := r.switchRepositories(ctx, c); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
