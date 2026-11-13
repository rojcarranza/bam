package cli

import (
	"fmt"
	"slices"

	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addTop) }

func addTop(root *cobra.Command, r *runtime) {
	root.AddCommand(newTopCmd(r))
	subgroup(root, "pin", "Pins: add, rm, list", groupBrowse).AddCommand(newPinAddCmd(r), newPinRmCmd(r), newPinListCmd(r))
}

func newTopCmd(r *runtime) *cobra.Command {
	var once bool
	cmd := &cobra.Command{
		Use:     "top",
		GroupID: groupBrowse,
		Short:   "Print what needs you: agents, the queue and your pinned pipelines",
		Long: "Print one board of the server: its version, what needs you, the agent counts and\n" +
			"every agent that is offline or disabled, the queue with the time each build\n" +
			"waited, and your pinned pipelines by group with their last 10 results.\n\n" +
			"Needs you lists an enabled agent that is offline, a build that waited more than\n" +
			"5 minutes, a pinned pipeline whose last build failed, and a pinned build running\n" +
			"half again past its usual time.\n\n" +
			"Pin pipelines with bam pin add, or list groups under monitor: in the machine file\n" +
			"or .bam.yaml. When Bamboo refuses the agent list or the queue (the agent list\n" +
			"needs an administrator), the board says so in one line and prints the rest.\n" +
			"bam top only reads from Bamboo.\n\n" +
			"On a terminal bam top opens the live board, which refreshes by itself (see\n" +
			"docs/tui.md). With --once, --json or a pipe it prints the board once.",
		Example: "  bam top\n  bam top --once\n  bam top --json\n  bam pin add integration PROJ-BUILD",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			groups, err := cfg.Monitor()
			if err != nil {
				return err
			}
			if !once && !r.flags.json && r.tuiAllowed() {
				return r.openUI(cmd.Context(), true)
			}
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			b, err := svc.Board(cmd.Context(), groups)
			if err != nil {
				return err
			}
			return r.show(view.BoardJSON(b, svc.P.URL), func(o view.Out) error { return view.Board(o, b, svc.P.URL) })
		}),
	}
	cmd.Flags().BoolVar(&once, "once", false, "print the board once instead of opening the live board")
	return cmd
}

func newPinAddCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "add GROUP PIN",
		Short: "Pin a plan, a plan with a branch, or a preset to a group of bam top",
		Long: "Pin a pipeline to a named group of bam top. PIN is a plan key (PROJ-PLAN), a plan\n" +
			"with a branch (PROJ-PLAN@release/1.2) or a preset name. The pin goes into the\n" +
			"monitor: section of the machine file, which keeps its comments.",
		Example: "  bam pin add integration PROJ-BUILD\n  bam pin add release PROJ-DEPLOY@release/1.2",
		Args:    cobra.ExactArgs(2),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if err := r.pinAdd(args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "Pinned %s in %s\n", args[1], args[0])
			return nil
		}),
	}
}

// pinAdd checks and writes a pin to the machine file. The configuration is
// read afresh, so a pin added while the UI is open counts as taken.
func (r *runtime) pinAdd(group, pin string) error {
	cfg, err := r.loadConfig()
	if err != nil {
		return err
	}
	if !config.ValidTargetName(group) {
		return errs.Usagef("%q is not a valid group name", group).WithTry("lowercase letters, digits, - and _, such as integration")
	}
	if err := cfg.CheckPin(pin); err != nil {
		return err
	}
	groups, err := cfg.Monitor()
	if err != nil {
		return err
	}
	for _, g := range groups {
		if g.Name != group && slices.Contains(g.Pins, pin) {
			try := "bam pin rm " + pin + " first"
			if cfg.ProjectPins(pin) {
				try = "remove it from " + cfg.ProjectPath + " by hand first"
			}
			return errs.Usagef("%s is already pinned in group %q", pin, g.Name).WithTry(try)
		}
	}
	return config.AddPin(r.env.Paths.MachineConfig, group, pin)
}

// pinRemove removes a pin from the machine file.
func (r *runtime) pinRemove(pin string) error {
	ok, err := config.RemovePin(r.env.Paths.MachineConfig, pin)
	if err != nil {
		return err
	}
	if !ok {
		return errs.Usagef("%s is not pinned in %s", pin, r.env.Paths.MachineConfig).WithTry("bam pin list; a pin in .bam.yaml is edited there by hand")
	}
	return nil
}

func newPinRmCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "rm PIN",
		Short: "Remove a pin from the machine file",
		Long: "Remove a pin from the monitor: section of the machine file. A pin that lives in\n" +
			".bam.yaml is edited there by hand.",
		Example: "  bam pin rm PROJ-BUILD",
		Args:    cobra.ExactArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if err := r.pinRemove(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "Removed %s\n", args[0])
			return nil
		}),
	}
}

// PinDoc is one pin in bam pin list --json.
type pinListDoc struct {
	Group string   `json:"group"`
	Pins  []string `json:"pins"`
}

func newPinListCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the pinned pipelines by group",
		Example: "  bam pin list\n  bam pin list --json",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			groups, err := cfg.Monitor()
			if err != nil {
				return err
			}
			docs := make([]pinListDoc, 0, len(groups))
			for _, g := range groups {
				docs = append(docs, pinListDoc{Group: g.Name, Pins: g.Pins})
			}
			return r.show(docs, func(o view.Out) error {
				if len(groups) == 0 {
					fmt.Fprintln(o.W, "No pipelines pinned. Pin one with: bam pin add GROUP PLAN")
					return nil
				}
				for _, g := range groups {
					for _, p := range g.Pins {
						fmt.Fprintf(o.W, "%s  %s\n", g.Name, p)
					}
				}
				return nil
			})
		}),
	}
}
