package cli

import (
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addQueue) }

func addQueue(root *cobra.Command, r *runtime) {
	root.AddCommand(newQueueCmd(r))
	subgroup(root, "agent", "Build agents: list", groupBrowse).AddCommand(newAgentListCmd(r))
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
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the build agents: online, enabled and busy",
		Example: "  bam agent list\n  bam agent list --json",
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
			return r.show(view.AgentsJSON(as), func(o view.Out) error { return view.AgentList(o, as) })
		}),
	}
}
