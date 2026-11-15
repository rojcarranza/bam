package cli

import (
	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addQueue) }

func addQueue(root *cobra.Command, r *runtime) {
	root.AddCommand(newQueueCmd(r))
	subgroup(root, "agent", "Build agents: list, show, mine, disable, enable", groupBrowse).AddCommand(newAgentListCmd(r), newAgentMineCmd(r), newAgentSwitchCmd(r, false), newAgentSwitchCmd(r, true), newAgentShowCmd(r))
}

func newQueueCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "queue",
		GroupID: groupBrowse,
		Short:   "List the builds waiting for an agent, first in line first",
		Long: "List the builds waiting for an agent, first in line first. A queued build\n" +
			"that does not start is waiting for an agent: bam agent list shows which are\n" +
			"online, enabled and busy.\n\n" +
			"The WHY column says why a build waits: no agent can run it, agents can run it\n" +
			"but all are busy, offline or disabled, agents are free and it is next, or\n" +
			"agents are free and other builds come before it. It names the agents when\n" +
			"three or fewer can run the job. Bamboo shows which agents can run a job, and\n" +
			"whether they are free, only to an administrator. A part Bamboo refuses is left\n" +
			"out, never an error.\n\n" +
			"bam reads which jobs each agent can run once an hour and keeps them in its\n" +
			"cache, so a change to an agent's capabilities shows within the hour, and a\n" +
			"build queued since then that no agent lists may have no WHY until then.",
		Example: "  bam queue\n  bam queue --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			q, err := svc.QueueRows(cmd.Context())
			if err != nil {
				return err
			}
			return r.show(view.QueueJSON(q, svc.P.URL), func(o view.Out) error { return view.QueueList(o, q, svc.P.URL) })
		}),
	}
}

func newAgentListCmd(r *runtime) *cobra.Command {
	var mine bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the build agents: online, enabled and busy",
		Example: "  bam agent list\n  bam agent list --mine\n  bam agent list --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			as, err := svc.Agents(cmd.Context())
			if err != nil {
				return err
			}
			if mine {
				starts := svc.AgentStarts()
				if len(starts) == 0 {
					return errs.Usagef("no agents are listed as yours").WithTry(noAgentsHint)
				}
				as = app.OnlyAgents(as, starts)
			}
			return r.show(view.AgentsJSON(as), func(o view.Out) error { return view.AgentList(o, as) })
		}),
	}
	cmd.Flags().BoolVar(&mine, "mine", false, "list only the agents whose names start with one you listed (bam agent mine)")
	return cmd
}

func newAgentShowCmd(r *runtime) *cobra.Command {
	var builds int
	cmd := &cobra.Command{
		Use:   "show NAME",
		Short: "Show an agent's track record: what it builds, its latest jobs, how often they fail",
		Long: "Show one agent's state, the jobs it runs now, its latest finished jobs and the share\n" +
			"of its jobs that failed beside the other agents'. Bamboo's job records do not say\n" +
			"which agent ran a job, so bam reads the first lines of each job's log in the newest\n" +
			"builds of every plan and remembers the answer for finished jobs. It needs Bamboo's\n" +
			"agent list, which only administrators can read.",
		Example: "  bam agent show linux-3\n  bam agent show linux-3 --builds 100\n  bam agent show linux-3 --json",
		Args:    cobra.ExactArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			rec, err := svc.AgentRecord(cmd.Context(), args[0], builds)
			if err != nil {
				return err
			}
			return r.show(view.AgentTrackJSON(rec), func(o view.Out) error { return view.AgentTrack(o, rec) })
		}),
	}
	cmd.Flags().IntVar(&builds, "builds", app.DefaultTrackBuilds, "how many of the newest builds to look through")
	return cmd
}
