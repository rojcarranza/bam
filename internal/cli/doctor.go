package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
		Use:     "doctor",
		Short:   "Check config, credentials and what this Bamboo server supports",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			var checks []check
			var first error
			add := func(name, status, detail string, err error) {
				checks = append(checks, check{Name: name, Status: status, Detail: detail})
				if err != nil && first == nil {
					first = err
				}
			}
			fail := func(name string, err error) {
				detail := err.Error()
				var e *errs.Error
				if errors.As(err, &e) && e.Try != "" {
					detail += " (try: " + e.Try + ")"
				}
				add(name, "error", detail, err)
			}

			func() {
				cfg, err := r.config()
				if err != nil {
					fail("config", err)
					return
				}
				var files []string
				if cfg.ProjectPath != "" {
					files = append(files, cfg.ProjectPath)
				}
				for _, f := range []string{cfg.MachinePath, cfg.PresetsPath} {
					if _, err := os.Stat(f); f != "" && err == nil {
						files = append(files, f)
					}
				}
				detail := strings.Join(files, ", ")
				if detail == "" {
					detail = "no config file; settings come from the environment"
				}
				add("config", "ok", detail, nil)

				server, err := cfg.SelectServer(r.flags.server, "")
				if err != nil {
					fail("server", err)
					return
				}

				origin, err := credential.Origin(server.URL)
				if err != nil {
					fail("server", err)
					return
				}

				add("server", "ok", fmt.Sprintf("%s (%s)", server.Alias, server.URL), nil)
				source := "BAM_TOKEN"
				if server.Token == "" {
					_, src, err := r.store().Lookup(server.Alias, origin, server.AuthEnv)
					if err != nil {
						fail("token", err)
						return
					}
					source = string(src)
					if src == credential.SourceEnv {
						source = "$" + server.AuthEnv
					}
				}
				add("token", "ok", source, nil)

				_, backend, err := r.connectServer(server)
				if err != nil {
					fail("auth", err)
					return
				}
				user, err := backend.CurrentUser(cmd.Context())
				if err != nil {
					fail("auth", err)
					return
				}
				add("auth", "ok", fmt.Sprintf("%s (%s)", user.Name, user.FullName), nil)

				key := planKey
				if key == "" {
					projects := cfg.ProjectKeys(server)
					if len(projects) > 0 {
						plans, err := backend.ListPlans(cmd.Context(), projects[0])
						if err != nil {
							// A listing that failed is a failed diagnosis:
							// reporting it as "no plan to probe" would exit 0
							// during an outage.
							fail("capabilities", err)
							return
						}
						if len(plans) > 0 {
							key = plans[0].Key
						}
					}
				}
				if key == "" {
					add("capabilities", "skipped", "no plan to probe; pass --plan KEY or set projects in .bam.yaml", nil)
					return
				}
				for _, p := range backend.Probe(cmd.Context(), key) {
					detail := p.Detail
					if p.Status == bamboo.ProbeUnsupported {
						detail = "not supported: " + detail
					}
					if p.Status == bamboo.ProbeError {
						fail(p.Name, p.Err)
						continue
					}
					add(p.Name, string(p.Status), detail, nil)
				}
			}()

			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, map[string][]check{"checks": checks}); err != nil {
					return err
				}
			} else if err := printChecks(r, checks); err != nil {
				return err
			}
			if first != nil {
				return silentError{first}
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&planKey, "plan", "", "plan to probe (default: first plan of the first configured project)")
	_ = cmd.RegisterFlagCompletionFunc("plan", r.completePlans)
	return cmd
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
