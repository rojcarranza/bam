package cli

import (
	"context"
	"fmt"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func newBuildTreeCmd(r *runtime) *cobra.Command {
	var last, watch, failed bool
	var branch string
	var depth int
	cmd := &cobra.Command{
		Use:   "tree [<build>|<plan>|<target>]",
		Short: "Show a build and every build it triggered, as a tree",
		Long: `Show a build and every build it triggered, as a tree.

bam finds a child build by its reason, "Child of <parent>", in the last 25
builds of each plan of the configured projects and of the build's project.
A children: map in the configuration narrows a plan's children and shows a
configured child that never ran. Interrupting --watch never stops a build.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("depth") && depth < 1 {
				return errs.Usagef("--depth takes 1 or more, not %d", depth)
			}
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			o := app.TreeOptions{Depth: depth}
			if watch {
				return r.treeLoop(ctx, svc, key, o, failed)
			}
			t, err := svc.BuildTree(ctx, key, o)
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
	f.StringVar(&branch, "branch", "", "plan branch when a plan or target is given")
	f.BoolVar(&watch, "watch", false, "follow the tree until every build has finished")
	f.BoolVar(&failed, "failed", false, "show only the branches that lead to a failure")
	f.IntVar(&depth, "depth", 0, "levels of builds to show below the root (default: all)")
	return cmd
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

// treeLoop renders a watched tree. Interrupts stop watching only.
func (r *runtime) treeLoop(ctx context.Context, svc *app.Service, key string, o app.TreeOptions, failed bool) error {
	var rend view.TreeRenderer
	if r.flags.json {
		rend = view.NewTreeNDJSON(view.Out{W: r.env.Stdout}, failed)
	} else {
		out, err := r.out()
		if err != nil {
			return err
		}
		if out.TTY {
			rend = view.NewTreeLive(out, failed)
		} else {
			rend = view.NewTreeLines(out, failed)
		}
	}
	for e := range svc.WatchTree(ctx, key, o) {
		rend.Event(e)
		switch e.Type {
		case app.TreeDone:
			view.PrintWarnings(r.env.Stderr, e.Tree.Warnings)
			return r.treeResult(e.Tree, key)
		case app.TreeError:
			return e.Err
		}
	}
	fmt.Fprintf(r.env.Stderr, "still running: bam build tree %s --watch\n", key)
	return ctx.Err()
}
