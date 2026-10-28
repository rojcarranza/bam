package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
		e := errs.Usagef("%s", flagValueMessage(err))
		if name, ok := strings.CutPrefix(err.Error(), "unknown flag: --"); ok {
			names := flagNames(c)
			if near := errs.Closest(name, names, 1); len(near) > 0 {
				return e.WithTry("did you mean --" + near[0] + "?")
			}
			for _, syn := range flagSynonyms[name] {
				// Only counts are synonyms: --last on build commands is a bool.
				if slices.Contains(names, syn) && c.Flag(syn).Value.Type() == "int" {
					return e.WithTry("did you mean --" + syn + "?")
				}
			}
			// --plan KEY where the command takes the plan as an argument.
			if token, ok := argFlags[name]; ok {
				if usage := strings.TrimPrefix(c.Use, c.Name()); strings.Contains(usage, token) {
					return e.WithTry("the " + name + " is an argument: " + c.CommandPath() + usage)
				}
			}
			if sib := siblingWithFlag(c, name); sib != "" {
				return e.WithTry("--" + name + " is a flag of " + sib)
			}
		}
		return e.WithTry(c.CommandPath() + " --help")
	})
	cmd, err := root.ExecuteContextC(ctx)
	var res *resultError
	var silent silentError
	var typed *errs.Error
	if err != nil && !errors.As(err, &res) && !errors.As(err, &silent) && !errors.Is(err, context.Canceled) {
		// Untyped errors here are cobra's argument checks ("accepts 1 arg(s)").
		if !errors.As(err, &typed) && !errors.Is(err, context.DeadlineExceeded) {
			err = argCountError(cmd, err)
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
		Version:       resolveVersion(version, debug.ReadBuildInfo),
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
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := style.ColorEnabled(r.flags.color, "", false); err != nil {
				return err
			}
			// watch, run and build rerun share --timeout; cobra accepts a negative one silently.
			if d, err := cmd.Flags().GetDuration("timeout"); err == nil && d < 0 {
				return errs.Usagef("--timeout takes 0 or more, not %s", cmd.Flags().Lookup("timeout").Value).
					WithTry("leave out --timeout to watch until the build finishes")
			}
			return nil
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&r.flags.server, "server", "", "server alias to use")
	// The flag goes anywhere on the line, so positional args must not hide the aliases.
	_ = root.RegisterFlagCompletionFunc("server", func(c *cobra.Command, _ []string, s string) ([]string, cobra.ShellCompDirective) {
		return r.completeServers(false)(c, nil, s)
	})
	pf.BoolVar(&r.flags.json, "json", false, "print JSON")
	pf.StringVar(&r.flags.color, "color", "", "color output: auto, always or never")
	_ = root.RegisterFlagCompletionFunc("color", cobra.FixedCompletions([]string{"auto", "always", "never"}, cobra.ShellCompDirectiveNoFileComp))
	pf.BoolVar(&r.flags.debug, "debug", false, "log HTTP requests to stderr")
	pf.BoolVar(&r.flags.noTUI, "no-tui", false, "never open the terminal UI")
	addProjectFlag(root, r)
	// Same output as bam version, including its --json form; a version string never holds a quote.
	root.SetVersionTemplate(`{{if eq (.Flags.Lookup "json").Value.String "true"}}{
  "version": "{{.Version}}"
}
{{else}}bam {{.Version}}
{{end}}`)

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
	root.InitDefaultVersionFlag()
	strictHelp(root)
	for _, c := range root.Commands() {
		requireSubcommand(c)
	}
	completePlanFlags(root, r)
	return root
}

// completePlanFlags registers plan branch completion on every --branch flag,
// recent build completion on every --from flag and job completion on every
// --job flag. A command without its own
// argument completion offers nothing: no bam argument is a file name.
func completePlanFlags(cmd *cobra.Command, r *runtime) {
	for _, c := range cmd.Commands() {
		completePlanFlags(c, r)
	}
	if cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	if cmd.LocalFlags().Lookup("branch") != nil {
		_ = cmd.RegisterFlagCompletionFunc("branch", r.completeBranch)
	}
	if cmd.LocalFlags().Lookup("from") != nil {
		_ = cmd.RegisterFlagCompletionFunc("from", r.completeFrom)
	}
	if cmd.LocalFlags().Lookup("job") != nil {
		_ = cmd.RegisterFlagCompletionFunc("job", r.completeJob)
	}
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
	// Cobra's completion command sets NoArgs, which would turn "completion zhs" into "unexpected argument".
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return unknownCommand(cmd, args)
	}
}

// strictHelp makes "bam help <typo>" a usage error. Cobra's help command
// prints root help and exits 0, because the root's Args accepts any word.
func strictHelp(root *cobra.Command) {
	help, _, err := root.Find([]string{"help"})
	if err != nil {
		return
	}
	help.Run = nil
	help.RunE = func(_ *cobra.Command, args []string) error {
		cmd, rest, err := root.Find(args)
		if err != nil {
			return err
		}
		// A leaf command's leftover words are its own arguments, as in "help build show X".
		if len(rest) > 0 && cmd.HasSubCommands() {
			return unknownCommand(cmd, rest)
		}
		cmd.InitDefaultHelpFlag()
		return cmd.Help()
	}
}

// showSuggest points the words other CLIs use for "show" at bam's show commands.
var showSuggest = []string{"view", "info", "describe", "get"}

// unknownCommand is the usage error for "bam <group> <typo>", with the
// closest subcommand as a "did you mean" hint.
func unknownCommand(cmd *cobra.Command, args []string) error {
	path := cmd.CommandPath()
	try := path + " --help"
	var names, suggest []string
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		names = append(names, c.Name())
		if slices.Contains(c.SuggestFor, strings.ToLower(args[0])) {
			suggest = []string{withFlag(c.Name(), c, strings.ToLower(args[0]))}
		}
	}
	if len(suggest) == 0 {
		suggest = nestedCommands(cmd, strings.ToLower(args[0]))
	}
	if len(suggest) == 0 {
		suggest = errs.Closest(args[0], names, 1)
	}
	for i, s := range suggest {
		suggest[i] = path + " " + s
	}
	// "bam build stats" meant "bam plan stats": look in the sibling groups.
	if root := cmd.Root(); len(suggest) == 0 && cmd.Parent() == root {
		for _, s := range nestedCommands(root, strings.ToLower(args[0])) {
			suggest = append(suggest, root.Name()+" "+s)
		}
		// "bam build open" meant the top-level "bam open"; groups are left out,
		// since "bam plan build" names no command.
		if c, _, err := root.Find([]string{strings.ToLower(args[0])}); len(suggest) == 0 && err == nil && c != root && c.IsAvailableCommand() && !c.HasSubCommands() {
			suggest = []string{c.CommandPath()}
		}
	}
	if len(suggest) > 0 {
		last := len(suggest) - 1
		said := suggest[last]
		if last > 0 {
			said = strings.Join(suggest[:last], ", ") + " or " + said
		}
		try = fmt.Sprintf("did you mean %s? %s shows them all", said, try)
	}
	return errs.Usagef("unknown command %q for %q", args[0], path).WithTry(try)
}

// withFlag adds the flag a SuggestFor word names, so "bam build compare"
// suggests "bam build investigate --compare", not a bare investigate.
func withFlag(name string, c *cobra.Command, word string) string {
	if c.LocalFlags().Lookup(word) != nil {
		return name + " --" + word
	}
	return name
}

// nestedCommands finds the grandchildren that "bam cancel" or "bam rerun"
// meant, by name, alias or SuggestFor: "build cancel", or "build rerun" and
// "set rerun". More than two matches are kept only when they share one name,
// as "show" does in most groups; a word that means different things returns nil.
func nestedCommands(cmd *cobra.Command, word string) []string {
	var found []string
	for _, group := range cmd.Commands() {
		if !group.IsAvailableCommand() {
			continue
		}
		for _, c := range group.Commands() {
			if c.IsAvailableCommand() && (c.Name() == word || slices.Contains(c.Aliases, word) || slices.Contains(c.SuggestFor, word)) {
				found = append(found, withFlag(group.Name()+" "+c.Name(), c, word))
			}
		}
	}
	if len(found) > 2 && slices.ContainsFunc(found, func(f string) bool {
		return strings.Fields(f)[1] != strings.Fields(found[0])[1]
	}) {
		return nil
	}
	return found
}

// flagValueMessage rewrites pflag's 'invalid argument "x" for "--limit" flag:
// strconv.ParseInt: ...' into plain words for number and duration flags.
func flagValueMessage(err error) string {
	var iv *pflag.InvalidValueError
	if !errors.As(err, &iv) {
		return err.Error()
	}
	want := map[string]string{
		"int":      "a whole number, like 10",
		"count":    "a whole number, like 10",
		"duration": "a duration, like 90s, 10m or 1h30m",
	}[iv.GetFlag().Value.Type()]
	if want == "" {
		return err.Error()
	}
	return fmt.Sprintf("--%s wants %s, not %q", iv.GetFlag().Name, want, iv.GetValue())
}

// flagNames lists the visible long flags cmd accepts, its own and inherited.
// flagSynonyms maps a flag name bam does not have to the ones that mean the
// same thing: plan stats counts builds with --last, build list with --limit.
var flagSynonyms = map[string][]string{
	"limit": {"last"},
	"last":  {"limit"},
	"count": {"limit", "last"},
	"max":   {"limit", "last"},
}

// argFlags maps a flag name users guess to how the command's Use line names
// that argument.
var argFlags = map[string]string{
	"project": "PROJ",
	"plan":    "plan",
	"build":   "build",
	"preset":  "preset",
}

func flagNames(cmd *cobra.Command) []string {
	var names []string
	add := func(f *pflag.Flag) {
		if !f.Hidden {
			names = append(names, f.Name)
		}
	}
	cmd.LocalFlags().VisitAll(add)
	cmd.InheritedFlags().VisitAll(add)
	return names
}

// siblingWithFlag names the first command in c's group that has the flag,
// such as bam build investigate for bam build show --compare. Top-level
// commands are skipped: their flags rarely carry over to another command.
func siblingWithFlag(c *cobra.Command, name string) string {
	if !c.HasParent() || !c.Parent().HasParent() {
		return ""
	}
	for _, sib := range c.Parent().Commands() {
		if f := sib.LocalFlags().Lookup(name); sib != c && !sib.Hidden && f != nil && !f.Hidden {
			return sib.CommandPath()
		}
	}
	return ""
}

// argCountError turns cobra's "accepts 1 arg(s), received 0" into a usage
// error that names the missing argument and shows the command's synopsis.
func argCountError(cmd *cobra.Command, err error) error {
	msg := err.Error()
	use := strings.Fields(cmd.Use)
	args := cmd.Flags().Args()
	if len(args) == 0 && len(use) > 1 && strings.HasPrefix(use[1], "<") {
		msg = "missing " + use[1]
	} else if extra := extraArgs(msg, args); len(extra) > 0 {
		quoted := make([]string, len(extra))
		for i, a := range extra {
			quoted[i] = strconv.Quote(a)
		}
		msg = "unexpected argument " + strings.Join(quoted, ", ")
		if len(extra) > 1 {
			msg = "unexpected arguments " + strings.Join(quoted, ", ")
		}
	}
	synopsis := strings.TrimSuffix(cmd.UseLine(), " [flags]")
	// "bam build show PROJ-PLAN 482" (or "#482"): the plan and number as two arguments
	if len(args) == 2 && strings.Contains(cmd.Use, "<build>") {
		if k := args[0] + "-" + strings.TrimPrefix(args[1], "#"); app.IsBuildKey(k) {
			return errs.Usagef("%s", msg).WithTry(fmt.Sprintf("did you mean %s %s?", cmd.CommandPath(), k))
		}
	}
	// "bam preset add web PROJ-PLAN": the plan as an argument of a command that takes --plan
	if extra := extraArgs(err.Error(), args); len(extra) == 1 && app.IsPlanKey(extra[0]) && cmd.Flags().Lookup("plan") != nil {
		fixed := append([]string{cmd.CommandPath()}, args[:len(args)-1]...)
		return errs.Usagef("%s", msg).WithTry(fmt.Sprintf("did you mean %s --plan %s?", strings.Join(fixed, " "), extra[0]))
	}
	return errs.Usagef("%s", msg).WithTry(fmt.Sprintf("%s; %s --help shows more", synopsis, cmd.CommandPath()))
}

// acceptsRe matches cobra's ExactArgs/MaximumNArgs/RangeArgs messages.
var acceptsRe = regexp.MustCompile(`^accepts (?:at most |between \d+ and )?(\d+) arg\(s\), received \d+$`)

// extraArgs is the arguments past what the command accepts, read from cobra's
// error: NoArgs says "unknown command", the others "accepts N arg(s)".
func extraArgs(msg string, args []string) []string {
	limit := -1
	if strings.HasPrefix(msg, "unknown command ") {
		limit = 0
	} else if m := acceptsRe.FindStringSubmatch(msg); m != nil {
		limit, _ = strconv.Atoi(m[1])
	}
	if limit < 0 || limit >= len(args) {
		return nil
	}
	return args[limit:]
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
		Use:        "version",
		Short:      "Print the bam version",
		SuggestFor: []string{"ver"},
		Args:       cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := resolveVersion(version, debug.ReadBuildInfo)
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, map[string]string{"version": v})
			}
			_, err := fmt.Fprintf(r.env.Stdout, "bam %s\n", v)
			return err
		},
	}
}
