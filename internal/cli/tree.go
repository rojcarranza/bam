package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func newBuildTreeCmd(r *runtime) *cobra.Command {
	var last, watch, failed bool
	var rerun, yes, noWatch bool
	var branch string
	var depth int
	cmd := &cobra.Command{
		Use:   "tree [<build>|<plan>|<preset>]",
		Short: "Show a build and every build it triggered, as a tree",
		Long: `Show a build and every build it triggered, as a tree.

bam finds a child build by its reason, "Child of <parent>", in the last 25
builds of each plan of the configured projects and of the build's project.
A children: map in the configuration narrows a plan's children and shows a
configured child that never ran. Interrupting --watch never stops a build.
On a terminal, bam shows on stderr what it is reading while the tree loads.`,
		Example:           "  bam build tree PROJ-BUILD-482\n  bam build tree --last --watch\n  bam build tree provision --failed --depth 2\n  bam build tree PROJ-BUILD-482 --rerun-failed --yes",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if rerun && !yes && (!r.env.StdinTTY || r.flags.json) {
				return errs.Usagef("--rerun-failed needs --yes when it cannot ask").WithTry("add --yes")
			}
			if cmd.Flags().Changed("depth") && depth < 1 {
				return errs.Usagef("--depth takes 1 or more, not %d", depth).WithTry("leave out --depth to show every level")
			}
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			o := app.TreeOptions{Depth: depth}
			if rerun {
				return r.rerunTree(ctx, svc, key, failed, yes, noWatch)
			}
			if watch {
				return r.treeLoop(ctx, svc, key, o, failed, 0, "bam build tree "+key+" --watch", "")
			}
			t, err := r.loadTree(ctx, svc, key, o)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, t.Warnings)
			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, view.TreeJSON(t, failed)); err != nil {
					return err
				}
			} else {
				out, err := r.out()
				if err != nil {
					return err
				}
				if err := view.TreeText(out, t, failed); err != nil {
					return err
				}
			}
			return r.treeResult(t, key)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	f.BoolVar(&watch, "watch", false, "follow the tree until every build has finished")
	f.BoolVar(&failed, "failed", false, "show only the branches that lead to a failure")
	f.BoolVar(&rerun, "rerun-failed", false, "restart the failed builds whose parents succeeded, then follow the tree")
	f.BoolVar(&yes, "yes", false, "with --rerun-failed, do not ask first")
	f.BoolVar(&noWatch, "no-watch", false, "with --rerun-failed, restart and exit without following")
	f.IntVar(&depth, "depth", 0, "levels of builds to show below the root (default: all)")
	return cmd
}

// progressOn reports whether a slow read may draw a progress line on
// stderr: both outputs are terminals, the output is not JSON, and no
// --debug log lines share stderr with it.
func (r *runtime) progressOn() bool {
	return r.env.StdoutTTY && r.env.StderrTTY && !r.flags.json && !r.flags.debug && r.env.Getenv("TERM") != "dumb"
}

// treeProgress is the line a tree load reports on, or nil when progressOn
// is false.
func (r *runtime) treeProgress() *view.ProgressLine {
	if !r.progressOn() {
		return nil
	}
	return view.NewProgressLine(r.env.Stderr, r.env.Width())
}

// loadTree reads key's tree, showing on a terminal what it is reading, and
// clears that line before returning.
func (r *runtime) loadTree(ctx context.Context, svc *app.Service, key string, o app.TreeOptions) (app.Tree, error) {
	if pl := r.treeProgress(); pl != nil {
		o.OnProgress = func(p app.TreeProgress) { pl.Show(view.TreeProgressText(p)) }
		defer pl.Clear()
	}
	return svc.BuildTree(ctx, key, o)
}

// treeResult maps a tree to the exit: 0 when it succeeded or still runs,
// 1 when any build did not succeed or a configured child did not run.
func (r *runtime) treeResult(t app.Tree, key string) error {
	switch t.State {
	case provider.StateSuccess:
		return nil
	case provider.StateRunning:
		r.note("still running: bam build tree "+key+" --watch", "")
		return nil
	}
	return &resultError{State: provider.StateFailed}
}

// treeLoop renders a watched tree. Interrupts and a limit stop watching
// only; resume is the command that picks the watch up again. revision, when
// set, is the revision the root was asked for; a not-built root then says
// Bamboo could not build it.
func (r *runtime) treeLoop(ctx context.Context, svc *app.Service, key string, o app.TreeOptions, failed bool, limit time.Duration, resume, revision string) error {
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		wctx, cancel = context.WithTimeout(ctx, limit)
	}
	defer cancel()
	var rend view.TreeRenderer
	var live *view.TreeLive
	if r.flags.json {
		rend = view.NewTreeNDJSON(view.Out{W: r.env.Stdout}, failed)
	} else {
		out, err := r.out()
		if err != nil {
			return err
		}
		if out.TTY {
			var progress io.Writer
			if r.progressOn() {
				progress = r.env.Stderr
			}
			live = view.NewTreeLive(out, progress, failed)
			rend = live
		} else {
			rend = view.NewTreeLines(out, failed)
		}
	}
	// A live tree redraws every second so running times and bars move.
	var tick <-chan time.Time
	if live != nil {
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		tick = tk.C
	}
	finished := false
	events := svc.WatchTree(wctx, key, o)
loop:
	for {
		select {
		case e, ok := <-events:
			if !ok {
				break loop
			}
			rend.Event(e)
			switch e.Type {
			case app.TreeUpdate:
				finished = e.Tree.State != provider.StateRunning
			case app.TreeDone:
				view.PrintWarnings(r.env.Stderr, e.Tree.Warnings)
				if revision != "" && !r.flags.json && e.Tree.Root.Build.State == provider.StateNotBuilt {
					if out, err := r.out(); err == nil {
						view.RevisionNotBuilt(out, revision)
					}
				}
				return r.treeResult(e.Tree, key)
			case app.TreeError:
				return e.Err
			}
		case <-tick:
			live.Tick()
		}
	}
	live.Stop()
	if ctx.Err() == nil && wctx.Err() != nil {
		why := key + "'s tree is still running"
		if finished {
			// The watch waits one more poll for builds the tree may still start.
			why = "every build in " + key + "'s tree finished, but bam had not yet confirmed that no more start"
		}
		return errs.New(errs.KindTimeout, fmt.Sprintf("timed out after %s", limit)).
			WithWhy(why).WithTry(resume)
	}
	fmt.Fprintf(r.env.Stderr, "still running: %s\n", resume)
	return ctx.Err()
}

// rerunTree restarts the topmost failed builds of key's tree, after asking,
// then follows the tree unless noWatch.
func (r *runtime) rerunTree(ctx context.Context, svc *app.Service, key string, failed, yes, noWatch bool) error {
	t, err := r.loadTree(ctx, svc, key, app.TreeOptions{})
	if err != nil {
		return err
	}
	cands := app.RerunCandidates(t)
	if len(cands) == 0 {
		r.note("nothing to rerun", "")
		return r.treeResult(t, key)
	}
	if t.State == provider.StateRunning {
		return errs.Usagef("%s's tree is still running; rerun when it has finished", key).
			WithTry("bam build tree " + key + " --watch")
	}
	if !yes {
		o, err := r.out()
		if err != nil {
			return err
		}
		o.W = r.env.Stderr
		if err := view.RerunList(o, cands); err != nil {
			return err
		}
		fmt.Fprint(r.env.Stderr, "[y/N] ")
		answer, _ := bufio.NewReader(r.env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			r.note("nothing restarted", "")
			return nil
		}
	}
	var restartErr error
	for _, res := range svc.RerunTree(ctx, cands) {
		switch {
		case res.Err != nil:
			fmt.Fprintf(r.env.Stderr, "%s was not restarted: %s\n", res.Key, whatOf(res.Err))
			if restartErr == nil {
				restartErr = res.Err
			}
		case !res.Restarted:
			fmt.Fprintf(r.env.Stderr, "%s was not restarted: it is running or has succeeded\n", res.Key)
		default:
			fmt.Fprintf(r.env.Stderr, "restarted %s\n", res.Key)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if noWatch {
		return restartErr
	}
	err = r.treeLoop(ctx, svc, key, app.TreeOptions{}, failed, 0, "bam build tree "+key+" --watch", "")
	if restartErr != nil {
		return restartErr
	}
	return err
}

// whatOf is an error's What, the one line a list of problems shows.
func whatOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.What
	}
	return err.Error()
}
