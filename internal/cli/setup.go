package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider/bamboo"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

var aliasRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// lowerAlias returns alias lowercased when that makes it valid, so a hint
// for Lab names lab; otherwise it returns alias as typed.
func lowerAlias(alias string) string {
	if a := strings.ToLower(alias); aliasRe.MatchString(a) && a != config.EnvServerAlias {
		return a
	}
	return alias
}

func init() { commandSets = append(commandSets, addSetup) }

func addSetup(root *cobra.Command, r *runtime) {
	server := &cobra.Command{Use: "server", Short: "Manage Bamboo server aliases", GroupID: groupSetup}
	server.AddCommand(newServerAddCmd(r), newServerListCmd(r), newServerRmCmd(r), newServerDefaultCmd(r))
	for _, c := range []*cobra.Command{server, newLoginCmd(r), newLogoutCmd(r), newWhoamiCmd(r)} {
		c.GroupID = groupSetup
		root.AddCommand(c)
	}
}

// schemeTry points a URL typed without a scheme (bamboo.example.com) at the
// same command with https:// in front.
func schemeTry(err error, url string, try func(url string) string) error {
	var e *errs.Error
	if !strings.Contains(url, "://") && errors.As(err, &e) {
		e.Try = try("https://" + url)
	}
	return err
}

// tokenPageURL is where a Bamboo user creates a personal access token.
func tokenPageURL(serverURL string) string {
	return strings.TrimRight(serverURL, "/") + "/profile/userAccessTokens.action"
}

func newServerAddCmd(r *runtime) *cobra.Command {
	var url string
	var projects []string
	var force bool
	cmd := &cobra.Command{
		Use:   "add <alias> --url URL",
		Short: "Add a server alias to the machine config, then log in",
		Args: func(cmd *cobra.Command, args []string) error {
			// git remote add muscle memory: show the exact command with --url.
			if len(args) == 2 && url == "" && strings.Contains(args[1], "://") {
				return errs.Usagef("the server URL goes in --url").
					WithTry("bam server add " + lowerAlias(args[0]) + " --url " + args[1])
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			alias := args[0]
			if !aliasRe.MatchString(alias) || alias == config.EnvServerAlias {
				e := errs.Usagef("invalid server alias %q", alias).
					WithWhy("aliases are lowercase letters, digits, - and _; \"env\" is reserved for BAM_URL")
				if a := lowerAlias(alias); a != alias {
					e = e.WithTry("bam server add " + a + " --url " + cmp.Or(url, "URL"))
				}
				return e
			}
			if err := checkProjectKeys(projects); err != nil {
				return err
			}
			if url == "" {
				return errs.Usagef("--url is required").WithTry("bam server add " + alias + " --url https://bamboo.example.com")
			}
			// A trailing slash would list http://host/ beside http://host.
			url = strings.TrimRight(strings.TrimSpace(url), "/")
			origin, err := credential.Origin(url)
			if err != nil {
				return schemeTry(err, url, func(u string) string { return "bam server add " + alias + " --url " + u })
			}
			cfg, err := r.config()
			if err != nil {
				return err
			}
			if _, exists := cfg.Machine.Servers[alias]; exists && !force {
				return errs.Usagef("server %q already exists in %s", alias, r.env.Paths.MachineConfig).WithTry("pass --force to replace it")
			}
			servers := cfg.Servers()
			for _, n := range slices.Sorted(maps.Keys(servers)) {
				if o, err := credential.Origin(servers[n].URL); err == nil && o == origin && n != alias {
					fmt.Fprintf(r.env.Stderr, "note: server %s already points to %s; both aliases share one token\n", n, origin)
				}
			}
			if err := config.SetMachineServer(r.env.Paths.MachineConfig, alias, config.Server{URL: url, Projects: projects}); err != nil {
				return err
			}
			r.cfg = nil // reload with the new alias
			fmt.Fprintf(r.env.Stdout, "added server %s (%s) to %s\n", alias, url, r.env.Paths.MachineConfig)
			if r.env.StdinTTY && r.env.StdoutTTY {
				return login(cmd, r, alias, false)
			}
			fmt.Fprintf(r.env.Stderr, "next: bam login %s\n", alias)
			return nil
		}),
	}
	cmd.Flags().StringVar(&url, "url", "", "server URL, e.g. https://bamboo.example.com")
	cmd.Flags().StringSliceVar(&projects, "project", nil, "project key to scope navigation (repeatable)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing alias")
	return cmd
}

// checkProjectKeys rejects --project values that are not project keys, so
// they never reach a config file as YAML that does not parse or scopes
// navigation to nothing.
func checkProjectKeys(projects []string) error {
	for _, p := range projects {
		if app.IsProjectKey(p) {
			continue
		}
		try := "project keys are uppercase letters and digits, like PROJ"
		switch up := strings.ToUpper(p); {
		case app.IsProjectKey(up):
			try = "keys are uppercase: did you mean " + up + "?"
		case app.IsPlanKey(up):
			try = fmt.Sprintf("%s is a plan key; its project is %s", up, strings.SplitN(up, "-", 2)[0])
		}
		return errs.Usagef("--project %q is not a project key", p).WithTry(try)
	}
	return nil
}

func newServerListCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:        "list",
		SuggestFor: []string{"ls"},
		Short:      "List server aliases, whether a token is stored, and which one is selected here",
		Args:       cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			servers := cfg.Servers()
			names := make([]string, 0, len(servers))
			for n := range servers {
				names = append(names, n)
			}
			sort.Strings(names)
			type row struct {
				Alias     string   `json:"alias"`
				URL       string   `json:"url"`
				DefinedIn []string `json:"defined_in"`
				Token     bool     `json:"token"`
				Selected  bool     `json:"selected"`
			}
			// No selection (several servers, no default) marks none.
			sel, _ := cfg.SelectServer(r.flags.server, "")
			var rows []row
			for _, n := range names {
				s := servers[n]
				has := s.Token != ""
				if o, err := credential.Origin(s.URL); err == nil && !has {
					has = r.store().Has(o, s.AuthEnv)
				}
				rows = append(rows, row{Alias: n, URL: s.URL, DefinedIn: s.DefinedIn, Token: has, Selected: n == sel.Alias})
			}
			if r.flags.json {
				if rows == nil {
					rows = []row{}
				}
				return view.WriteJSON(r.env.Stdout, rows)
			}
			if len(rows) == 0 {
				fmt.Fprintln(r.env.Stderr, "no servers configured\n  try: bam server add <alias> --url URL")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			t := view.Table{Headers: []string{"ALIAS", "URL", "DEFINED IN", "TOKEN", "SELECTED"}}
			for _, x := range rows {
				t.Rows = append(t.Rows, []string{x.Alias, x.URL, strings.Join(x.DefinedIn, ", "), yesNo(x.Token), yesNo(x.Selected)})
			}
			return t.Render(o)
		}),
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func newServerRmCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "rm <alias>",
		Short:             "Remove a server alias from the machine config",
		SuggestFor:        []string{"remove", "delete"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeServers(true),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			alias := cfg.Alias(args[0])
			if _, inMachine := cfg.Machine.Servers[alias]; !inMachine {
				if cfg.Project != nil {
					if _, inProject := cfg.Project.Servers[alias]; inProject {
						return errs.Usagef("server %q is defined in %s", alias, cfg.ProjectPath).
							WithWhy("bam server rm edits only the machine config").
							WithTry("edit " + cfg.ProjectPath)
					}
				}
				if len(cfg.Servers()) == 0 {
					// RemoveMachineServer's hint, bam server list, would only say there are none.
					return errs.Usagef("unknown server %q", alias).WithWhy("no servers are configured, so there is nothing to remove")
				}
				if near := nearMachineServer(cfg, alias); near != "" {
					return errs.Usagef("no server alias %q in %s", alias, r.env.Paths.MachineConfig).WithTry("did you mean bam server rm " + near + "?")
				}
			}
			if err := config.RemoveMachineServer(r.env.Paths.MachineConfig, alias); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "removed server %s from %s\n", alias, r.env.Paths.MachineConfig)
			if cfg.Machine.DefaultServer == alias {
				fmt.Fprintln(r.env.Stdout, "cleared the machine default_server, which named it")
				if len(cfg.Machine.Servers) > 2 {
					fmt.Fprintln(r.env.Stdout, "next: bam server default <alias>")
				}
			}
			return nil
		}),
	}
}

func newServerDefaultCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "default [alias]",
		Short:             "Show or set the machine-wide default server alias",
		SuggestFor:        []string{"use"},
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeServers(true),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				cur := cfg.Machine.DefaultServer
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, map[string]string{"default_server": cur})
				}
				if cur == "" {
					fmt.Fprintln(r.env.Stderr, "no machine default server set\n  try: bam server default <alias>")
					return nil
				}
				fmt.Fprintln(r.env.Stdout, cur)
				return nil
			}
			alias := cfg.Alias(args[0])
			// A machine default naming a repo's alias would break every other repo.
			if _, ok := cfg.Machine.Servers[alias]; !ok {
				if len(cfg.Servers()) == 0 {
					// The machine config may not exist yet, and bam server list would only say there are none.
					return errs.Usagef("unknown server %q", alias).WithWhy("no servers are configured").WithTry("bam server add " + alias + " --url URL")
				}
				if cfg.Project != nil {
					if _, inProject := cfg.Project.Servers[alias]; inProject {
						return errs.Usagef("server %q is defined in %s", alias, cfg.ProjectPath).
							WithWhy("the machine default must be a machine server, or every other repo would break").
							WithTry("set default_server: " + alias + " in " + cfg.ProjectPath)
					}
				}
				try := "bam server list"
				if near := nearMachineServer(cfg, alias); near != "" {
					try = "did you mean bam server default " + near + "?"
				}
				return errs.Usagef("no server alias %q in %s", alias, r.env.Paths.MachineConfig).WithTry(try)
			}
			if err := config.SetMachineDefaultServer(r.env.Paths.MachineConfig, alias); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "default server is now %s in %s\n", alias, r.env.Paths.MachineConfig)
			return nil
		}),
	}
}

func newLoginCmd(r *runtime) *cobra.Command {
	var withToken bool
	cmd := &cobra.Command{
		Use:               "login <alias>",
		Short:             "Store a verified personal access token for a server",
		SuggestFor:        []string{"auth", "token", "signin"},
		Long:              "Store a verified personal access token for a server. The alias defaults to --server.",
		Args:              r.aliasArg,
		ValidArgsFunction: r.completeServers(false),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			return login(cmd, r, r.alias(args), withToken)
		}),
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read the token from stdin instead of prompting")
	return cmd
}

// aliasArg lets login and logout take their alias from --server instead.
func (r *runtime) aliasArg(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && r.flags.server != "" {
		return nil
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func (r *runtime) alias(args []string) string {
	if len(args) == 0 {
		return r.flags.server
	}
	return args[0]
}

// nearMachineServer is the machine server alias closest to a mistyped one, or "".
func nearMachineServer(cfg *config.Config, alias string) string {
	if near := app.Closest(alias, slices.Sorted(maps.Keys(cfg.Machine.Servers)), 1); len(near) > 0 {
		return near[0]
	}
	return ""
}

func login(cmd *cobra.Command, r *runtime, alias string, withToken bool) error {
	cfg, err := r.config()
	if err != nil {
		return err
	}
	alias = cfg.Alias(alias)
	s, ok := cfg.Servers()[alias]
	if !ok || s.URL == "" {
		e := cfg.UnknownServer(alias)
		if near := app.Closest(alias, slices.Sorted(maps.Keys(cfg.Servers())), 1); len(near) > 0 && near[0] != alias {
			e.Try = "did you mean bam login " + near[0] + "?"
		}
		return e
	}
	origin, err := credential.Origin(s.URL)
	if err != nil {
		return err
	}
	var token string
	switch {
	case withToken:
		data, err := io.ReadAll(r.env.Stdin)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(data))
	case !r.env.StdinTTY:
		return errs.Usagef("no terminal to prompt for a token").
			WithTry(fmt.Sprintf("printf %%s \"$TOKEN\" | bam login %s --with-token", alias))
	default:
		fmt.Fprintf(r.env.Stderr, "Create a personal access token at %s\nToken: ", tokenPageURL(s.URL))
		token, err = r.env.ReadSecret()
		fmt.Fprintln(r.env.Stderr)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(token)
	}
	if token == "" {
		return errs.Usagef("empty token")
	}
	backend, err := r.env.Connect(bamboo.Options{BaseURL: s.URL, Token: token, Logger: r.logger()})
	if err != nil {
		return err
	}
	user, err := backend.CurrentUser(cmd.Context())
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindAuth {
			e.What = fmt.Sprintf("token rejected by %s (%s)", alias, e.What)
			e.Try = "create a new token at " + tokenPageURL(s.URL)
		}
		return err
	}
	src, err := r.store().Save(origin, token)
	if err != nil {
		return err
	}
	where := "the keychain"
	if src == credential.SourceFile {
		where = r.env.Paths.Credentials + " (no keychain available)"
	}
	fmt.Fprintf(r.env.Stdout, "logged in to %s as %s (%s); token stored in %s\n", alias, user.Name, user.FullName, where)
	if s.AuthEnv != "" && r.env.Getenv(s.AuthEnv) != "" {
		fmt.Fprintf(r.env.Stderr, "note: %s is set and takes precedence over the stored token\n", s.AuthEnv)
	}
	return nil
}

func newLogoutCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "logout <alias>",
		Short:             "Delete the stored token for a server",
		SuggestFor:        []string{"signout"},
		Long:              "Delete the stored token for a server. The alias defaults to --server.",
		Args:              r.aliasArg,
		ValidArgsFunction: r.completeServers(false),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			alias := cfg.Alias(r.alias(args))
			s, ok := cfg.Servers()[alias]
			if !ok {
				// UnknownServer's hint, bam server add, is no way to log out.
				e := cfg.UnknownServer(alias).WithTry("")
				if e.Why == "" {
					e = e.WithWhy("no servers are configured, so there is nothing to log out of")
				} else if near := app.Closest(alias, slices.Sorted(maps.Keys(cfg.Servers())), 1); len(near) > 0 {
					e.Try = "did you mean bam logout " + near[0] + "?"
				}
				return e
			}
			origin, err := credential.Origin(s.URL)
			if err != nil {
				return err
			}
			deleted, err := r.store().Delete(origin)
			if err != nil {
				return err
			}
			if deleted {
				fmt.Fprintf(r.env.Stdout, "logged out of %s\n", alias)
			} else {
				fmt.Fprintf(r.env.Stdout, "no stored token for %s\n", alias)
			}
			if s.AuthEnv != "" {
				fmt.Fprintf(r.env.Stderr, "note: a token in $%s is not affected\n", s.AuthEnv)
			}
			return nil
		}),
	}
}

func newWhoamiCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:        "whoami",
		SuggestFor: []string{"user", "me"},
		Short:      "Show the authenticated user on each configured server",
		Args:       cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			servers := cfg.Servers()
			var names []string
			if r.flags.server != "" {
				if _, ok := servers[r.flags.server]; !ok {
					return cfg.UnknownServer(r.flags.server)
				}
				names = []string{r.flags.server}
			} else {
				for n := range servers {
					names = append(names, n)
				}
				sort.Strings(names)
			}
			type row struct {
				Alias    string `json:"alias"`
				URL      string `json:"url"`
				User     string `json:"user"`
				FullName string `json:"full_name"`
				Error    string `json:"error"`
			}
			rows := []row{}
			var first error
			for _, n := range names {
				s := servers[n]
				x := row{Alias: n, URL: s.URL}
				_, backend, err := r.connectServer(s)
				if err == nil {
					u, uerr := backend.CurrentUser(cmd.Context())
					x.User, x.FullName, err = u.Name, u.FullName, uerr
				}
				if err != nil {
					x.Error = err.Error()
					var e *errs.Error
					if errors.As(err, &e) && e.Try != "" {
						x.Error += " (try: " + e.Try + ")"
					}
					if first == nil {
						first = err
					}
				}
				rows = append(rows, x)
			}
			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, rows); err != nil {
					return err
				}
			} else if len(rows) == 0 {
				fmt.Fprintln(r.env.Stderr, "no servers configured\n  try: bam server add <alias> --url URL")
			} else {
				o, err := r.out()
				if err != nil {
					return err
				}
				t := view.Table{Headers: []string{"ALIAS", "URL", "USER"}, Flex: []int{2}}
				for _, x := range rows {
					who := fmt.Sprintf("%s (%s)", x.User, x.FullName)
					if x.Error != "" {
						who = "error: " + x.Error
					}
					t.Rows = append(t.Rows, []string{x.Alias, x.URL, who})
				}
				if err := t.Render(o); err != nil {
					return err
				}
			}
			if first != nil {
				return silentError{first}
			}
			return nil
		}),
	}
}
