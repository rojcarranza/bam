package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
)

func init() {
	commandSets = append(commandSets, func(root *cobra.Command, r *runtime) { root.AddCommand(newDoctorCmd(r)) })
}

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func newDoctorCmd(r *runtime) *cobra.Command {
	var planKey string
	cmd := &cobra.Command{
		Use:        "doctor",
		Short:      "Check config, credentials and what this Bamboo server supports",
		GroupID:    groupSetup,
		SuggestFor: []string{"check", "health", "ping"},
		Example: "  bam doctor\n" +
			"  bam doctor --server lab\n" +
			"  bam doctor --plan PROJ-BUILD",
		Args: cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			if planKey != "" {
				if err := app.CheckPlanKey(planKey); err != nil {
					return err
				}
			}
			d := &doctor{r: r, ctx: cmd.Context()}
			d.diagnose(planKey)
			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, map[string][]check{"checks": d.checks}); err != nil {
					return err
				}
			} else if err := printChecks(r, d.checks); err != nil {
				return err
			}
			if d.first != nil {
				return silentError{d.first}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&planKey, "plan", "", "plan to probe (default: first plan of the first configured project)")
	_ = cmd.RegisterFlagCompletionFunc("plan", r.completePlans)
	return cmd
}

// doctor collects the checks of one bam doctor run, in order, and the first
// error among them, which decides the exit code.
type doctor struct {
	r      *runtime
	ctx    context.Context
	checks []check
	first  error
}

func (d *doctor) add(name, status, detail string, err error) {
	d.checks = append(d.checks, check{Name: name, Status: status, Detail: detail})
	if err != nil && d.first == nil {
		d.first = err
	}
}

func (d *doctor) fail(name string, err error) {
	detail := err.Error()
	var e *errs.Error
	if errors.As(err, &e) && e.Try != "" {
		detail += " (try: " + e.Try + ")"
	}
	d.add(name, "error", detail, err)
}

// diagnose runs the checks in order; each one needs the ones before it, so
// the first failure ends the run.
func (d *doctor) diagnose(planKey string) {
	cfg, err := d.r.config()
	if err != nil {
		d.fail("config", err)
		return
	}
	d.add("config", "ok", configFiles(cfg), nil)

	server, err := cfg.SelectServer(d.r.flags.server, "")
	if err != nil {
		d.fail("server", err)
		return
	}
	origin, err := credential.Origin(server.URL)
	if err != nil {
		d.fail("server", err)
		return
	}
	d.add("server", "ok", fmt.Sprintf("%s (%s)", server.Alias, server.URL), nil)

	source, err := d.tokenSource(server, origin)
	if err != nil {
		d.fail("token", err)
		return
	}
	d.add("token", "ok", source, nil)

	_, backend, err := d.r.connectServer(server)
	if err != nil {
		d.fail("auth", err)
		return
	}
	user, err := backend.CurrentUser(d.ctx)
	if err != nil {
		d.fail("auth", err)
		return
	}
	d.add("auth", "ok", fmt.Sprintf("%s (%s)", user.Name, user.FullName), nil)

	if planKey == "" {
		if planKey, err = firstPlan(d.ctx, backend, cfg.ProjectKeys(server)); err != nil {
			// A listing that failed is a failed diagnosis: reporting it as
			// "no plan to probe" would exit 0 during an outage.
			d.fail("capabilities", err)
			return
		}
	}
	if planKey == "" {
		d.add("capabilities", "skipped", "no plan to probe; pass --plan KEY or set projects in .bam.yaml", nil)
		return
	}
	for _, p := range backend.Probe(d.ctx, planKey) {
		switch p.Status {
		case bamboo.ProbeError:
			d.fail(p.Name, p.Err)
		case bamboo.ProbeUnsupported:
			d.add(p.Name, string(p.Status), "not supported: "+p.Detail, nil)
		default:
			d.add(p.Name, string(p.Status), p.Detail, nil)
		}
	}
}

// configFiles lists the config files in effect, or says there are none.
func configFiles(cfg *config.Config) string {
	var files []string
	if cfg.ProjectPath != "" {
		files = append(files, cfg.ProjectPath)
	}
	for _, f := range []string{cfg.MachinePath, cfg.PresetsPath} {
		if _, err := os.Stat(f); f != "" && err == nil {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return "no config file; settings come from the environment"
	}
	return strings.Join(files, ", ")
}

// tokenSource names where the server's token comes from.
func (d *doctor) tokenSource(server config.ResolvedServer, origin string) (string, error) {
	if server.Token != "" {
		return "BAM_TOKEN", nil
	}
	_, src, err := d.r.lookupToken(server, origin)
	if err != nil {
		return "", err
	}
	if src == credential.SourceEnv {
		return "$" + server.AuthEnv, nil
	}
	return string(src), nil
}

// firstPlan is the first plan of the first project, or "" when there is none.
func firstPlan(ctx context.Context, backend Backend, projects []string) (string, error) {
	if len(projects) == 0 {
		return "", nil
	}
	plans, err := backend.ListPlans(ctx, projects[0])
	if err != nil || len(plans) == 0 {
		return "", err
	}
	return plans[0].Key, nil
}

func printChecks(r *runtime, checks []check) error {
	o, err := r.out()
	if err != nil {
		return err
	}
	glyph := map[string]string{
		"ok":          style.Colored(o.Style, provider.StateSuccess, "✓"),
		"error":       style.Colored(o.Style, provider.StateFailed, "✗"),
		"unsupported": style.Colored(o.Style, provider.StateQueued, "!"),
		"skipped":     style.Dim(o.Style, "–"),
	}
	// No flex column: doctor details (paths, reasons) are never cut.
	t := view.Table{Headers: []string{"", ""}}
	for _, c := range checks {
		t.Rows = append(t.Rows, []string{glyph[c.Status] + " " + c.Name, c.Detail})
	}
	return view.RenderRows(o, t)
}
