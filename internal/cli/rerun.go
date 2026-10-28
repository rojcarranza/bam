package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func newBuildRerunCmd(r *runtime) *cobra.Command {
	var last, watch, dryRun bool
	var branch string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "rerun [<build>|<plan>|<preset>]",
		Short: "Rerun a failed build's failed jobs",
		Long: `Rerun a failed or stopped build's failed jobs, as Bamboo's "Rerun failed jobs"
does: same build key, same variables and commit. A build that is running or
succeeded is refused.`,
		Example:           "  bam build rerun PROJ-BUILD-482\n  bam build rerun --last --watch\n  bam build rerun provision --branch feature-x --dry-run",
		Args:              cobra.MaximumNArgs(1),
		SuggestFor:        []string{"retry", "restart"},
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
				return errs.Usagef("%s succeeded; nothing to rerun", key).
					WithTry("to run it again with its variables: bam run " + b.PlanKey + " --from " + key)
			default:
				e := errs.Usagef("%s is %s; nothing to rerun", key, strings.ReplaceAll(string(b.State), "_", " "))
				if !b.State.Finished() {
					e = e.WithTry("bam watch " + key)
				}
				return e
			}
			if dryRun && svc.RefusesRestart(ctx) {
				return errs.Bamboof("this Bamboo cannot restart a build over REST").
					WithTry("bam open " + key + ", then rerun it from Bamboo's web UI")
			}
			if dryRun {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, map[string]any{"key": key, "url": svc.P.URL(key), "state": b.State, "restarted": false})
				}
				fmt.Fprintf(r.env.Stdout, "would rerun %s (%s)\n", key, b.State)
				r.note("dry run: nothing restarted", "")
				return nil
			}
			_, restarted, err := svc.Restart(ctx, key)
			if err != nil {
				return err
			}
			if !restarted {
				return errs.Usagef("%s is running or has succeeded; nothing to rerun", key)
			}
			if !watch {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, map[string]any{"key": key, "url": svc.P.URL(key), "restarted": true})
				}
				fmt.Fprintf(r.env.Stdout, "restarted %s\n  watch: bam watch %s\n", key, key)
				return nil
			}
			return r.followBuild(cmd, args, svc, key, timeout)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	f.BoolVar(&watch, "watch", false, "follow the build and the builds it triggers until they finish")
	f.BoolVar(&dryRun, "dry-run", false, "show which build would rerun, restart nothing")
	f.DurationVar(&timeout, "timeout", 0, "with --watch, stop watching after this long (the build keeps running)")
	return cmd
}
