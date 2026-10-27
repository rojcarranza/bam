package cli

import (
	"context"
	"runtime/debug"
	"sort"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/view/tui"
	"github.com/spf13/cobra"
)

func newUICmd(r *runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ui",
		Short:   "Open the terminal UI",
		GroupID: groupBrowse,
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			if !r.env.StdoutTTY || !r.env.StdinTTY {
				return errs.Configf("bam ui needs a terminal").
					WithWhy("stdin and stdout are not both a terminal").
					WithTry("run bam ui from an interactive shell, or use bam's commands on a pipe")
			}
			return r.openUI(cmd.Context())
		}),
	}
	addProjectFlag(cmd, r)
	return cmd
}

// addProjectFlag is a local flag, not a global one: init and server add
// have a --project of their own.
func addProjectFlag(cmd *cobra.Command, r *runtime) {
	cmd.Flags().StringVar(&r.flags.project, "project", "",
		"project the UI opens on (all: every project; default: default_project)")
}

// tuiAllowed says whether bare bam may open the UI. A pipe, a dumb terminal,
// --no-tui and BAM_NO_TUI all mean no.
func (r *runtime) tuiAllowed() bool {
	if r.flags.noTUI || r.env.Getenv("BAM_NO_TUI") != "" {
		return false
	}
	if term := r.env.Getenv("TERM"); term == "" || term == "dumb" {
		return false
	}
	return r.env.StdoutTTY && r.env.StdinTTY
}

// openUI builds the UI's dependencies from the same configuration and the
// same connect function the commands use, then hands them over.
func (r *runtime) openUI(ctx context.Context) error {
	cfg, err := r.config()
	if err != nil {
		return err
	}
	servers := cfg.Servers()
	deps := tui.Deps{
		Project: r.flags.project,
		// Connect takes the alias the UI asks for rather than resolving one,
		// because inside the UI the server picker is what chooses.
		Connect: func(_ context.Context, alias string) (*app.Service, error) {
			s, ok := servers[alias]
			if !ok || s.URL == "" {
				return nil, cfg.UnknownServer(alias)
			}
			svc, _, err := r.connectServer(s)
			return svc, err
		},
		// Reread the files on every call: the UI's r on the Presets panel is
		// documented as picking up a preset added while it is open, and the
		// cached Config holds the .bam.yaml parsed at start-up. The server
		// connection is untouched by this.
		Targets: func() ([]app.TargetInfo, error) {
			fresh, err := r.loadConfig()
			if err != nil {
				return nil, err
			}
			return app.DescribeTargets(fresh, r.env.Getenv)
		},
		Sets: func() ([]app.SetInfo, error) {
			fresh, err := r.loadConfig()
			if err != nil {
				return nil, err
			}
			return app.DescribeSets(fresh, r.env.Getenv)
		},
		// SavePreset writes what preset add --from BUILD writes, reading the
		// files fresh so a preset added since start-up still counts as taken.
		SavePreset: func(ctx context.Context, svc *app.Service, name, build string) (string, error) {
			d, err := svc.GenerateTarget(ctx, app.GenerateOptions{Name: name, From: build})
			if err != nil {
				return "", err
			}
			fresh, err := r.loadConfig()
			if err != nil {
				return "", err
			}
			return r.writePreset(fresh, d, "", false)
		},
		Open:      r.env.OpenBrowser,
		Getenv:    r.env.Getenv,
		Clipboard: r.env.Stdout,
		Output:    r.env.Stdout,
		Version:   resolveVersion(version, debug.ReadBuildInfo),
	}
	for alias := range servers {
		deps.Servers = append(deps.Servers, tui.Server{Alias: alias, URL: servers[alias].URL})
	}
	sortServers(deps.Servers)

	sel, err := cfg.SelectServer(r.flags.server, "")
	switch {
	case err == nil:
		deps.Initial = sel.Alias
	case r.flags.server != "":
		// An explicit --server that cannot be resolved is an error here for
		// the same reason it is for every command: opening a different
		// server than the one asked for is worse than not opening one.
		return err
	case len(deps.Servers) > 0:
		deps.Initial = deps.Servers[0].Alias
	default:
		return err
	}
	return r.env.RunTUI(ctx, deps)
}

// sortServers keeps the picker's order stable across runs; Servers() is a map.
func sortServers(ss []tui.Server) {
	sort.Slice(ss, func(i, j int) bool { return ss[i].Alias < ss[j].Alias })
}
