package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addStatus) }

func addStatus(root *cobra.Command, r *runtime) { root.AddCommand(newStatusCmd(r)) }

// shortTimeout bounds bam status --short, which a shell prompt runs on every
// line.
const shortTimeout = 3 * time.Second

func newStatusCmd(r *runtime) *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:     "status",
		GroupID: groupRun,
		Short:   "Show every build you started with bam in the last day, and how it is going",
		Long: "Show every build you started with bam in the last day, across repositories and\n" +
			"servers, newest first, with its live state, progress and link. Inside a\n" +
			"repository the build of the checked-out branch comes first, marked →.\n\n" +
			"bam keeps the last " + fmt.Sprint(app.JournalMax) + " runs it started: build, plan, preset, server and time,\n" +
			"never a variable value.\n\n" +
			"--short prints one line for a shell prompt: the first build, its state and\n" +
			"progress, and how many others are running. It prints nothing when there is\n" +
			"nothing to show.",
		Example: "  bam status\n  bam status --short\n  bam status --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if short {
				// A prompt must not hang on a slow server: what is not
				// answered in time shows as unknown.
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, shortTimeout)
				defer cancel()
			}
			cfg, err := r.config()
			if err != nil {
				return err
			}
			store := &app.StateStore{Path: r.env.Paths.State}
			journal, err := store.Runs()
			if err != nil {
				return err
			}
			runs := app.RecentRuns(journal, r.env.Clock.Now())
			branch, _ := r.checkedOutBranch()
			var entries []app.StatusEntry
			urls := map[string]string{} // by build key and origin
			for _, origin := range origins(runs) {
				var group []app.JournalRun
				for _, run := range runs {
					if run.Origin == origin {
						group = append(group, run)
					}
				}
				server, ok := serverForOrigin(cfg, origin)
				if !ok {
					for _, run := range group {
						entries = append(entries, app.StatusEntry{Run: run, Err: errs.Configf("%s is not a configured server", origin)})
					}
					continue
				}
				svc, backend, err := r.connectServer(server)
				if err != nil {
					for _, run := range group {
						entries = append(entries, app.StatusEntry{Run: run, Err: err})
					}
					continue
				}
				for _, e := range svc.Status(ctx, group) {
					urls[origin+" "+e.Run.BuildKey] = backend.URL(e.Run.BuildKey)
					entries = append(entries, e)
				}
			}
			app.MarkHere(entries, cfg.RepoRoot, branch)
			// Newest first, with the build of this checkout's branch before all.
			slices.SortStableFunc(entries, func(a, b app.StatusEntry) int {
				if a.Here != b.Here {
					if a.Here {
						return -1
					}
					return 1
				}
				return cmp.Compare(b.Run.StartedAt.UnixNano(), a.Run.StartedAt.UnixNano())
			})
			if short {
				if line := view.StatusShort(entries); line != "" {
					fmt.Fprintln(r.env.Stdout, line)
				}
				return nil
			}
			url := func(run app.JournalRun) string { return urls[run.Origin+" "+run.BuildKey] }
			return r.show(view.StatusJSON(entries, url), func(o view.Out) error { return view.Status(o, entries, url) })
		}),
	}
	cmd.Flags().BoolVar(&short, "short", false, "print one line for a shell prompt")
	return cmd
}

// origins lists the distinct origins of runs, in order of first appearance.
func origins(runs []app.JournalRun) []string {
	var out []string
	for _, run := range runs {
		if !slices.Contains(out, run.Origin) {
			out = append(out, run.Origin)
		}
	}
	return out
}
