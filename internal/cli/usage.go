package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/usage"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

func init() {
	commandSets = append(commandSets, func(root *cobra.Command, r *runtime) { root.AddCommand(newUsageCmd(r)) })
}

func (r *runtime) usageStore() *usage.Store {
	return &usage.Store{Path: r.env.Paths.Usage}
}

// recordUsage adds one to the tally for the command that just ran. It passes
// the command's path ("bam build show") and never its arguments. A failure to
// write is ignored: the tally must not change what a command does.
func (r *runtime) recordUsage(cmd *cobra.Command) {
	if cmd == nil || r.env.Paths.Usage == "" {
		return
	}
	// An unknown subcommand leaves its word in the parent's arguments.
	if cmd.HasSubCommands() && cmd.Flags().NArg() > 0 {
		return
	}
	name := cmd.CommandPath()
	for _, skip := range []string{"bam __complete", "bam __completeNoDesc", "bam completion", "bam help", "bam usage"} {
		if name == skip || strings.HasPrefix(name, skip+" ") {
			return
		}
	}
	_ = r.usageStore().Record(name, r.env.Clock.Now())
}

func newUsageCmd(r *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "usage [off|on]",
		Short:   "Show how often you ran each command, or turn counting off or on",
		GroupID: groupSetup,
		Long: "bam keeps a private tally on this computer of which commands you run and how often,\n" +
			"by day. It holds command names and counts only, never arguments or the names of\n" +
			"plans, servers, jobs, agents or branches, and bam sends it nowhere.\n\n" +
			"bam usage        shows the tally\n" +
			"bam usage off    stops counting (the counts so far stay)\n" +
			"bam usage on     counts again",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"off", "on"},
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			store := r.usageStore()
			if len(args) == 1 {
				switch args[0] {
				case "off", "on":
					off := args[0] == "off"
					if err := store.SetOff(off); err != nil {
						return err
					}
					if off {
						_, err := fmt.Fprintf(r.env.Stdout, "bam no longer counts the commands you run. The counts so far stay in %s.\n", store.Path)
						return err
					}
					_, err := fmt.Fprintln(r.env.Stdout, "bam counts the commands you run again.")
					return err
				default:
					return errs.Usagef("usage takes off or on, not %q", args[0]).WithTry("bam usage --help")
				}
			}
			t, err := store.Load()
			if err != nil {
				return err
			}
			days, totals := t.DayTotals(), t.Totals()
			if r.flags.json {
				cmds := map[string]int{}
				all := 0
				for _, c := range totals {
					cmds[c.Command] = c.Count
					all += c.Count
				}
				doc := map[string]any{"counting": !t.Off, "total": all, "commands": cmds, "days": orEmpty(t.Days)}
				if len(days) > 0 {
					doc["first_day"] = days[0].Day
				}
				return view.WriteJSON(r.env.Stdout, doc)
			}
			return writeUsage(r, t, days, totals)
		}),
	}
	return cmd
}

func orEmpty(m map[string]map[string]int) map[string]map[string]int {
	if m == nil {
		return map[string]map[string]int{}
	}
	return m
}

func writeUsage(r *runtime, t usage.Tally, days []usage.DayTotal, totals []usage.Total) error {
	w := r.env.Stdout
	state := "on"
	if t.Off {
		state = "off"
	}
	if len(days) == 0 {
		_, err := fmt.Fprintf(w, "No commands counted yet (counting is %s).\nThe tally is %s, on this computer only.\n", state, filepath.Clean(r.env.Paths.Usage))
		return err
	}
	all := 0
	for _, d := range days {
		all += d.Count
	}
	fmt.Fprintf(w, "%d commands on %d days since %s (counting is %s)\n\n", all, len(days), days[0].Day, state)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMMAND\tCOUNT")
	for _, c := range totals {
		fmt.Fprintf(tw, "%s\t%d\n", c.Command, c.Count)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DAY\tCOMMANDS")
	for _, d := range days {
		fmt.Fprintf(tw, "%s\t%d\n", d.Day, d.Count)
	}
	return tw.Flush()
}
