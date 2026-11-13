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
			"online, enabled and busy.",
		Example: "  bam queue\n  bam queue --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			q, err := svc.Queue(cmd.Context())
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
