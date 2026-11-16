package cli

import (
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/credential"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

const noAgentsHint = "Show only your agents: bam agent mine add NAME-START"

func newAgentMineCmd(r *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mine",
		Short: "The agents you support: add, rm, list, forget",
		Long: "bam top shows the health of the agents you support, not the whole server's. Name\n" +
			"them by the start of their names: an agent is yours when its name starts with one\n" +
			"of the starts you list (case-sensitive, no wildcards). The list is agents: in the\n" +
			"machine file, which keeps its comments; it is also read from the project file, a\n" +
			"servers entry and a repos entry, wherever projects: is.\n\n" +
			"bam remembers the names of your agents it has seen, so an agent Bamboo drops from\n" +
			"its list still shows as down, and forgets a name that has been gone for 30 days.",
	}
	cmd.AddCommand(newMineAddCmd(r), newMineRmCmd(r), newMineListCmd(r), newMineForgetCmd(r))
	return cmd
}

func newMineAddCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "add NAME-START...",
		Short:   "List name starts of the agents you support",
		Example: "  bam agent mine add team-a-agent team-b-agent",
		Args:    cobra.MinimumNArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if strings.TrimSpace(a) == "" || strings.ContainsAny(a, "*?") {
					return errs.Usagef("%q is not a name start", a).WithTry("the first characters of your agents' names, such as team-a-agent; no wildcards")
				}
			}
			for _, a := range args {
				added, err := config.AddAgentStart(r.env.Paths.MachineConfig, a)
				if err != nil {
					return err
				}
				if added {
					fmt.Fprintf(r.env.Stdout, "Listed %s\n", a)
				} else {
					fmt.Fprintf(r.env.Stdout, "%s is already listed\n", a)
				}
			}
			return r.mineNoteOverride(args)
		}),
	}
}

// mineNoteOverride says so when an agents: list in a more specific place hides
// the machine file's, which is what bam agent mine edits.
func (r *runtime) mineNoteOverride(edited []string) error {
	cfg, err := r.config()
	if err != nil {
		return err
	}
	server, err := cfg.SelectServer(r.flags.server, "")
	if err != nil {
		return nil // no server yet: nothing is hidden that matters
	}
	effective := cfg.AgentStarts(server)
	for _, e := range edited {
		for _, s := range effective {
			if s == e {
				return nil
			}
		}
	}
	fmt.Fprintf(r.env.Stdout, "Note: agents: is also set elsewhere (%s) and takes precedence over the machine file\n", strings.Join(effective, ", "))
	return nil
}

func newMineRmCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "rm NAME-START...",
		Short:   "Stop listing name starts",
		Example: "  bam agent mine rm team-b-agent",
		Args:    cobra.MinimumNArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				ok, err := config.RemoveAgentStart(r.env.Paths.MachineConfig, a)
				if err != nil {
					return err
				}
				if !ok {
					return errs.Usagef("%s is not listed in %s", a, r.env.Paths.MachineConfig).WithTry("bam agent mine list; a start in .bam.yaml is edited there by hand")
				}
				fmt.Fprintf(r.env.Stdout, "Removed %s\n", a)
			}
			return nil
		}),
	}
}

func newMineListCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the name starts of the agents you support",
		Example: "  bam agent mine list\n  bam agent mine list --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			starts, err := r.agentStarts()
			if err != nil {
				return err
			}
			if starts == nil {
				starts = []string{}
			}
			return r.show(starts, func(o view.Out) error {
				if len(starts) == 0 {
					fmt.Fprintln(o.W, "No agents listed. "+noAgentsHint)
				}
				for _, s := range starts {
					fmt.Fprintln(o.W, s)
				}
				return nil
			})
		}),
	}
}

// agentStarts is the name starts listed for the selected server.
func (r *runtime) agentStarts() ([]string, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	server, err := cfg.SelectServer(r.flags.server, "")
	if err != nil {
		return nil, err
	}
	return cfg.AgentStarts(server), nil
}

func newMineForgetCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "forget NAME...",
		Short: "Forget agents bam remembers, so they stop showing as down",
		Long: "bam remembers the names of your agents it has seen, so an agent Bamboo no longer\n" +
			"lists shows as down. Forget a name to stop that at once, for an agent you retired;\n" +
			"bam forgets a name by itself after 30 days.",
		Example: "  bam agent mine forget team-a-agent-pool-7",
		Args:    cobra.MinimumNArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			server, err := cfg.SelectServer(r.flags.server, "")
			if err != nil {
				return err
			}
			origin, err := credential.Origin(server.URL)
			if err != nil {
				return err
			}
			gone, err := (&app.StateStore{Path: r.env.Paths.State}).ForgetAgents(origin, args)
			if err != nil {
				return err
			}
			if len(gone) == 0 {
				return errs.Usagef("bam does not remember %s", strings.Join(args, ", ")).WithTry("names are the agent names as Bamboo lists them")
			}
			for _, g := range gone {
				fmt.Fprintf(r.env.Stdout, "Forgot %s\n", g)
			}
			return nil
		}),
	}
}
