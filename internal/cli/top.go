package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/rojcarranza/bam/internal/app"
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
	var once, all, tree bool
	cmd := &cobra.Command{
		Use:     "top",
		GroupID: groupBrowse,
		Short:   "Print what needs you: agents, what they build and your pinned pipelines",
		Long: "Print one board of the server: its version, what needs you, the agent counts and\n" +
			"every agent that is offline or disabled, and your pinned pipelines by group\n" +
			"with their last 10 results.\n\n" +
			"Needs you lists an enabled agent that is offline, a build of a pinned pipeline\n" +
			"that waited more than 5 minutes for an agent, a pinned pipeline whose last\n" +
			"build failed, and a pinned build running half again past its usual time.\n\n" +
			"With agents: set (bam agent mine add NAME-START) the board shows only your\n" +
			"agents, each building, idle, disabled or down, and Needs you raises your agents\n" +
			"that are down or disabled. On the live board each building agent has a row with\n" +
			"the job it runs, its plan and branch, its pin group and how long it runs, and\n" +
			"the idle agents of a pool are named on one line. --all shows every agent of the\n" +
			"server.\n\n" +
			"With --tree, each pin that started other pipelines prints them as bam build\n" +
			"tree does, under its group's table; with --json each pin gains tree. The live board\n" +
			"always draws them (space folds a tree).\n\n" +
			"Pin pipelines with bam pin add, or list groups under monitor: in the machine file\n" +
			"or .bam.yaml. When Bamboo refuses the agent list (it needs an administrator), the\n" +
			"board says so in one line and prints the rest.\n" +
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
				return r.openUI(cmd.Context(), true, all)
			}
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if all {
				ctx = app.WholeServer(ctx)
			}
			if tree {
				ctx = app.WithPinTrees(ctx)
			}
			b, err := svc.Board(ctx, groups)
			if err != nil {
				return err
			}
			return r.show(view.BoardJSON(b, svc.P.URL), func(o view.Out) error { return view.Board(o, b, svc.P.URL) })
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "show every agent of the server, not only your part")
	cmd.Flags().BoolVar(&tree, "tree", false, "print the tree of child pipelines under each pin (with --once or --json)")
	cmd.Flags().BoolVar(&once, "once", false, "print the board once instead of opening the live board")
	return cmd
}

func newPinAddCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "add GROUP PIN...",
		Short: "Pin plans, plans with a branch, presets or plan names to a group of bam top",
		Long: "Pin pipelines to a named group of bam top. Each PIN is a plan key (PROJ-PLAN), a\n" +
			"plan with a branch (PROJ-PLAN@release/1.2), a preset name, or the name of a plan.\n" +
			"A name is looked up on the selected server: a plan with exactly that name wins,\n" +
			"else the plans whose name holds all the words. bam stores the plan's key. A name\n" +
			"that fits no plan or several plans pins nothing, and none of the other pins are\n" +
			"written either. The pins go into the monitor: section of the machine file, which\n" +
			"keeps its comments.",
		Example: "  bam pin add integration PROJ-BUILD\n  bam pin add release PROJ-DEPLOY@release/1.2\n" +
			"  bam pin add integration \"Integrate For Next Release\" \"Run Feature Tests\"",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: r.completePinArgs,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			done, err := r.pinAdd(cmd.Context(), args[0], args[1:])
			if err != nil {
				return err
			}
			for _, p := range done {
				if p.Name != "" {
					fmt.Fprintf(r.env.Stdout, "Pinned %s (%s) in %s\n", p.Key, p.Name, args[0])
				} else {
					fmt.Fprintf(r.env.Stdout, "Pinned %s in %s\n", p.Key, args[0])
				}
			}
			return nil
		}),
	}
}

// pinResult is one pin pinAdd wrote: the key or preset it stored and, for a pin
// given as a plan name, that plan's name.
type pinResult struct{ Key, Name string }

// pinAdd checks pins and writes them to the machine file in one write, so a
// bad pin leaves the file as it was. A plan key, a plan key with a branch
// and an existing preset are taken as they are; any other pin is looked up
// as a plan name, which asks the server. The configuration is read afresh,
// so a pin added while the UI is open counts as taken.
func (r *runtime) pinAdd(ctx context.Context, group string, pins []string) ([]pinResult, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	if !config.ValidTargetName(group) {
		return nil, errs.Usagef("%q is not a valid group name", group).WithTry("lowercase letters, digits, - and _, such as integration")
	}
	groups, err := cfg.Monitor()
	if err != nil {
		return nil, err
	}
	var svc *app.Service
	var done []pinResult
	for _, pin := range pins {
		pin = strings.TrimSpace(pin)
		p := pinResult{Key: pin}
		if err := cfg.CheckPin(pin); err != nil {
			if strings.Contains(pin, "@") {
				return nil, err
			}
			if svc == nil {
				if svc, _, err = r.connect(ctx, ""); err != nil {
					return nil, err
				}
			}
			plan, err := svc.FindPlanByName(ctx, pin)
			if err != nil {
				return nil, err
			}
			p = pinResult{Key: plan.Key, Name: plan.Name}
			if err := cfg.CheckPin(p.Key); err != nil {
				return nil, err
			}
		}
		for _, g := range groups {
			if g.Name != group && slices.Contains(g.Pins, p.Key) {
				try := "bam pin rm " + p.Key + " first"
				if cfg.ProjectPins(p.Key) {
					try = "remove it from " + cfg.ProjectPath + " by hand first"
				}
				return nil, errs.Usagef("%s is already pinned in group %q", p.Key, g.Name).WithTry(try)
			}
		}
		if !slices.ContainsFunc(done, func(d pinResult) bool { return d.Key == p.Key }) {
			done = append(done, p)
		}
	}
	keys := make([]string, len(done))
	for i, d := range done {
		keys[i] = d.Key
	}
	err = config.AddPins(r.env.Paths.MachineConfig, group, keys...)
	r.configChanged()
	if err != nil {
		return nil, err
	}
	return done, nil
}

// pinNames asks the server for the name of each pinned plan key, so the list
// can show it. It is best effort: a plan the server does not know leaves that
// name out, and any other failure, or the lookup outlasting shortTimeout,
// stops asking and the list prints the remaining pins alone.
func (r *runtime) pinNames(ctx context.Context, groups []config.PinGroup) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, shortTimeout)
	defer cancel()
	names := map[string]string{}
	var svc *app.Service
	for _, g := range groups {
		for _, pin := range g.Pins {
			plan, _ := config.SplitPin(pin)
			if !app.IsPlanKey(plan) {
				continue
			}
			if svc == nil {
				var err error
				if svc, _, err = r.connect(ctx, ""); err != nil {
					return nil
				}
			}
			n, err := svc.PlanName(ctx, plan)
			switch {
			case errors.Is(err, errs.ErrNotFound):
			case err != nil:
				return names
			case n != "":
				names[pin] = n
			}
		}
	}
	return names
}

// pinRemove removes a pin from the machine file.
func (r *runtime) pinRemove(pin string) error {
	ok, err := config.RemovePin(r.env.Paths.MachineConfig, pin)
	r.configChanged()
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
	// Names holds the plan's name of each pin that is a plan key, when the
	// server could be asked.
	Names map[string]string `json:"names,omitempty"`
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
			names := r.pinNames(cmd.Context(), groups)
			docs := make([]pinListDoc, 0, len(groups))
			for _, g := range groups {
				d := pinListDoc{Group: g.Name, Pins: g.Pins}
				for _, p := range g.Pins {
					if n := names[p]; n != "" {
						if d.Names == nil {
							d.Names = map[string]string{}
						}
						d.Names[p] = n
					}
				}
				docs = append(docs, d)
			}
			return r.show(docs, func(o view.Out) error {
				if len(groups) == 0 {
					fmt.Fprintln(o.W, "No pipelines pinned. Pin one with: bam pin add GROUP PLAN")
					return nil
				}
				t := view.Table{Headers: []string{"GROUP", "PIN", "NAME"}, Flex: []int{2}}
				for _, g := range groups {
					for _, p := range g.Pins {
						t.Rows = append(t.Rows, []string{g.Name, p, names[p]})
					}
				}
				return t.Render(o)
			})
		}),
	}
}
