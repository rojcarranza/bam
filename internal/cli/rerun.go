package cli

import (
	"fmt"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/spf13/cobra"
)

func newBuildRerunCmd(r *runtime) *cobra.Command {
	var last, watch bool
	var branch string
	cmd := &cobra.Command{
		Use:   "rerun [<build>|<plan>|<target>]",
		Short: "Rerun a failed build's failed jobs",
		Long: `Rerun a failed or stopped build's failed jobs, as Bamboo's "Rerun failed jobs"
does: same build key, same variables and commit. A build that is running or
succeeded is refused.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			b, err := svc.Build(ctx, key)
			if err != nil {
				return err
			}
			// Only a failed or stopped build has failed jobs to run again.
			switch b.State {
			case provider.StateFailed, provider.StateStopped:
			case provider.StateSuccess:
				return errs.Usagef("%s succeeded; nothing to rerun", key)
			default:
				return errs.Usagef("%s is %s; nothing to rerun", key, strings.ReplaceAll(string(b.State), "_", " "))
			}
			_, restarted, err := svc.Restart(ctx, key)
			if err != nil {
				return err
			}
			if !restarted {
				return errs.Usagef("%s is running or has succeeded; nothing to rerun", key)
			}
			if !watch {
				fmt.Fprintf(r.env.Stdout, "restarted %s\n  watch: bam watch %s\n", key, key)
				return nil
			}
			return r.watchLoop(ctx, svc, key, 0, "")
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or target is given")
	f.BoolVar(&watch, "watch", false, "follow the build until it finishes")
	return cmd
}
