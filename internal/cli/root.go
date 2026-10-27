package cli

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
)

// version is set at build time with -ldflags "-X github.com/r0jjames/bam-cli/internal/cli.version=v0.1.0".
// go install cannot pass -ldflags; resolveVersion covers that build.
var version = "dev"

// resolveVersion is the version set at build time or, when there is none, the
// module version Go records in a go install build (v0.2.1, or a pseudo-version
// for @main). A plain go build records "(devel)", which says nothing, so that
// stays "dev".
func resolveVersion(set string, read func() (*debug.BuildInfo, bool)) string {
	if set != "dev" {
		return set
	}
	if bi, ok := read(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return set
}

const (
	groupSetup    = "setup"
	groupBrowse   = "browse"
	groupRun      = "run"
	groupShortcut = "shortcuts"
)

// Execute runs bam with args and returns the process exit code.
func Execute(ctx context.Context, args []string, env Env) int {
	r := &runtime{env: env}
	root := newRoot(r)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return errs.Usagef("%s", err.Error()).WithTry(c.CommandPath() + " --help")
	})
	cmd, err := root.ExecuteContextC(ctx)
	var res *resultError
	var silent silentError
	var typed *errs.Error
	if err != nil && !errors.As(err, &res) && !errors.As(err, &silent) && !errors.Is(err, context.Canceled) {
		// Untyped errors here are cobra's argument checks ("accepts 1 arg(s)").
		if !errors.As(err, &typed) && !errors.Is(err, context.DeadlineExceeded) {
			err = errs.Usagef("%s", err.Error()).WithTry(cmd.CommandPath() + " --help")
		}
		view.PrintError(env.Stderr, err, r.flags.debug)
	}
	return exitCode(err)
}

func newRoot(r *runtime) *cobra.Command {
	root := &cobra.Command{
		Use:           "bam",
		Short:         "A terminal remote control for Atlassian Bamboo",
		Long:          "bam triggers, watches and diagnoses Bamboo builds from the terminal.\nRun bam alone on a terminal for the interactive UI.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if r.tuiAllowed() {
				return r.wrap(func(*cobra.Command, []string) error {
					return r.openUI(cmd.Context())
				})(cmd, args)
			}
			return cmd.Help()
		},
		// Check --color up front: commands that print no styled output never
		// reach r.out(), so a typo there was silently ignored.
		PersistentPreRunE: func(*cobra.Command, []string) error {
			_, err := style.ColorEnabled(r.flags.color, "", false)
			return err
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&r.flags.server, "server", "", "server alias to use")
	pf.BoolVar(&r.flags.json, "json", false, "print JSON")
	pf.StringVar(&r.flags.color, "color", "", "color output: auto, always or never")
	pf.BoolVar(&r.flags.debug, "debug", false, "log HTTP requests to stderr")
	pf.BoolVar(&r.flags.noTUI, "no-tui", false, "never open the terminal UI")
	addProjectFlag(root, r)

	root.AddGroup(
		&cobra.Group{ID: groupSetup, Title: "Setup:"},
		&cobra.Group{ID: groupBrowse, Title: "Browse:"},
		&cobra.Group{ID: groupRun, Title: "Run:"},
		&cobra.Group{ID: groupShortcut, Title: "Shortcuts:"},
	)
	root.AddCommand(newVersionCmd(r), newUICmd(r))
	for _, add := range commandSets {
		add(root, r)
	}
	// Cobra adds these lazily in ExecuteC, too late for the walk below.
	root.InitDefaultCompletionCmd()
	root.InitDefaultHelpCmd()
	for _, c := range root.Commands() {
		requireSubcommand(c)
	}
	return root
}

// requireSubcommand gives every group command ("bam server", "bam plan") a
// RunE: bare it prints help, with an unknown subcommand it fails as a usage
// error. Cobra's default prints help and exits 0, which hides a typo like
// "bam server remove work".
func requireSubcommand(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		requireSubcommand(c)
	}
	if !cmd.HasSubCommands() || cmd.Run != nil || cmd.RunE != nil {
		return
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return unknownCommand(cmd, args)
	}
}

// unknownCommand is the usage error for "bam <group> <typo>", with the
// closest subcommand as a "did you mean" hint.
func unknownCommand(cmd *cobra.Command, args []string) error {
	path := cmd.CommandPath()
	try := path + " --help"
	var names []string
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() {
			names = append(names, c.Name())
		}
	}
	if s := app.Closest(args[0], names, 1); len(s) > 0 {
		try = fmt.Sprintf("did you mean %s %s? %s shows them all", path, s[0], try)
	}
	return errs.Usagef("unknown command %q for %q", args[0], path).WithTry(try)
}

// commandSets register command groups; later tasks append to it from init().
var commandSets []func(root *cobra.Command, r *runtime)

// DocsRoot returns the command tree for generating the command reference.
func DocsRoot() *cobra.Command {
	root := newRoot(&runtime{env: SystemEnv()})
	root.DisableAutoGenTag = true
	root.InitDefaultCompletionCmd()
	return root
}

func newVersionCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the bam version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(r.env.Stdout, "bam %s\n", resolveVersion(version, debug.ReadBuildInfo))
			return err
		},
	}
}
