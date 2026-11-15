package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

// agentSwitchDoc is the --json document of bam agent disable and enable.
type agentSwitchDoc struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Changed bool   `json:"changed"`
}

func newAgentSwitchCmd(r *runtime, enable bool) *cobra.Command {
	verb, past := "disable", "disabled"
	long := "Take an agent out of rotation: Bamboo stops giving it builds. A build the agent is\n" +
		"running now finishes first. It needs Bamboo's restricted administration or\n" +
		"administration permission, and a token that is not limited to read or trigger.\n" +
		"bam asks first; without a terminal, or with --json, it needs --yes. Undo it with\n" +
		"bam agent enable NAME."
	if enable {
		verb, past = "enable", "enabled"
		long = "Put a disabled agent back in rotation: Bamboo starts giving it builds again.\n" +
			"It needs Bamboo's restricted administration or administration permission, and a\n" +
			"token that is not limited to read or trigger. bam asks first; without a terminal,\n" +
			"or with --json, it needs --yes."
	}
	var yes bool
	cmd := &cobra.Command{
		Use:     verb + " NAME",
		Short:   strings.ToUpper(verb[:1]) + verb[1:] + " a build agent (administrators only)",
		Long:    long,
		Example: "  bam agent " + verb + " linux-3\n  bam agent " + verb + " linux-3 --yes",
		Args:    cobra.ExactArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if err := r.refuseWrite(verb + " an agent"); err != nil {
				return err
			}
			ctx := cmd.Context()
			svc, _, err := r.connect(ctx, "")
			if err != nil {
				return err
			}
			a, err := svc.FindAgent(ctx, args[0])
			if err != nil {
				return err
			}
			doc := func(changed bool) any {
				return agentSwitchDoc{ID: a.ID, Name: a.Name, Enabled: enable, Changed: changed}
			}
			if a.Enabled == enable {
				return r.show(doc(false), func(o view.Out) error {
					_, err := fmt.Fprintf(o.W, "%s is already %s\n", a.Name, past)
					return err
				})
			}
			if !yes {
				if !r.env.StdinTTY || r.flags.json {
					return errs.Usagef("bam agent %s needs --yes when it cannot ask", verb).WithTry("add --yes")
				}
				fmt.Fprint(r.env.Stderr, agentSwitchQuestion(a, verb))
				answer, _ := bufio.NewReader(r.env.Stdin).ReadString('\n')
				if x := strings.ToLower(strings.TrimSpace(answer)); x != "y" && x != "yes" {
					r.note("nothing changed", "")
					return nil
				}
			}
			if _, err := svc.SetAgentEnabled(ctx, a, enable); err != nil {
				return err
			}
			return r.show(doc(true), func(o view.Out) error {
				_, err := fmt.Fprintf(o.W, "%s is now %s\n", a.Name, past)
				return err
			})
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask first")
	return cmd
}

// agentSwitchQuestion is what bam asks before changing agent a.
func agentSwitchQuestion(a provider.Agent, verb string) string {
	q := fmt.Sprintf("%s %s?", strings.ToUpper(verb[:1])+verb[1:], a.Name)
	if verb == "disable" && a.Busy {
		q += " It is building now and finishes that build first."
	}
	return q + " [y/N] "
}
