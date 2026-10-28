package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addBrowse) }

// subgroup returns root's subcommand name, creating it on first use.
func subgroup(root *cobra.Command, name, short, group string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	c := &cobra.Command{Use: name, Short: short, GroupID: group}
	root.AddCommand(c)
	return c
}

func addBrowse(root *cobra.Command, r *runtime) {
	subgroup(root, "project", "Bamboo projects", groupBrowse).AddCommand(newProjectListCmd(r))
	subgroup(root, "plan", "Plans: list, show, vars, branches, stats", groupBrowse).
		AddCommand(newPlanListCmd(r), newPlanShowCmd(r), newPlanVarsCmd(r), newPlanBranchesCmd(r), newPlanStatsCmd(r))
	subgroup(root, "build", "Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel", groupRun).
		AddCommand(newBuildListCmd(r), newBuildShowCmd(r), newBuildTreeCmd(r), newBuildRerunCmd(r))
}

func (r *runtime) note(msg, try string) {
	fmt.Fprintln(r.env.Stderr, msg)
	if try != "" {
		fmt.Fprintf(r.env.Stderr, "  try: %s\n", try)
	}
}

// completeTargets offers target names for a <plan|preset> argument.
func (r *runtime) completeTargets(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := r.config()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	all, _ := cfg.Targets()
	var names []string
	for n := range all {
		names = append(names, n)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeServers offers server aliases that have a url; machineOnly limits
// them to the machine file, the only one server rm and server default edit.
func (r *runtime) completeServers(machineOnly bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		cfg, err := r.config()
		if len(args) > 0 || err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for n, s := range cfg.Servers() {
			if s.URL != "" && (!machineOnly || cfg.Machine.Servers[n].URL != "") {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProjects offers the server's project keys not already on the line.
// It costs one request, like the project list itself.
func (r *runtime) completeProjects(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	var keys []string
	if svc, _, err := r.connect(cmd.Context(), ""); err == nil {
		projects, _ := svc.P.ListProjects(cmd.Context())
		for _, p := range projects {
			if !slices.Contains(args, p.Key) {
				keys = append(keys, p.Key)
			}
		}
	}
	return keys, cobra.ShellCompDirectiveNoFileComp
}

// completePlans offers the plan keys of the server's configured projects (or
// of init's --project values), each described by its name: one request per
// project, like doctor's default. Keys already given to a repeatable --plan
// are left out.
func (r *runtime) completePlans(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	cfg, err := r.config()
	if err != nil {
		return nil, none
	}
	server, err := cfg.SelectServer(r.flags.server, "")
	if err != nil {
		return nil, none
	}
	_, backend, err := r.connectServer(server)
	if err != nil {
		return nil, none
	}
	projects, _ := cmd.Flags().GetStringSlice("project")
	// Completion parses flags twice, so a slice flag holds each value twice.
	slices.Sort(projects)
	projects = slices.Compact(projects)
	if len(projects) == 0 {
		projects = cfg.ProjectKeys(server)
	}
	var given []string
	if vals, err := cmd.Flags().GetStringArray("plan"); err == nil {
		for _, v := range vals {
			key, _, _ := strings.Cut(v, "=")
			given = append(given, key)
		}
	}
	var keys []string
	for _, project := range projects {
		plans, _ := backend.ListPlans(cmd.Context(), project)
		for _, p := range plans {
			if !slices.Contains(given, p.Key) {
				keys = append(keys, p.Key+"\t"+p.Name)
			}
		}
	}
	return keys, none
}

// completeBranch offers the branch names of the plan a command names: its
// --plan flag (preset add) or its first argument, a plan key or preset.
func (r *runtime) completeBranch(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	arg := ""
	if p, err := cmd.Flags().GetString("plan"); err == nil && p != "" {
		arg = p
	} else if len(args) > 0 {
		arg = args[0]
	}
	svc, arg, err := r.resolvePlanArg(cmd.Context(), arg)
	if err != nil {
		return nil, none
	}
	// Not ResolvePlan: it would also resolve a preset's stored branch, one more request that can fail.
	master := arg
	if t, ok, err := r.cfg.Target(arg); err == nil && ok {
		master = t.Plan
	}
	if !app.IsPlanKey(master) {
		return nil, none
	}
	branches, _ := svc.P.ListBranches(cmd.Context(), master)
	var names []string
	for _, b := range branches {
		names = append(names, b.ShortName)
	}
	return names, none
}

// completeFrom offers "last" and the recent build numbers of the plan branch
// a command resolves, the numbers --from reads relative to that branch.
func (r *runtime) completeFrom(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	out := []string{"last\tmost recent manual build"}
	for _, b := range r.recentBuilds(cmd, args) {
		out = append(out, fmt.Sprintf("%d\t%s", b.Number, style.Label(b.State)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeRevision offers "from" and the commits the recent builds of the
// plan branch a command resolves were built from, newest first.
func (r *runtime) completeRevision(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	out := []string{"from\tthe commit of the --from build"}
	seen := map[string]bool{}
	for _, b := range r.recentBuilds(cmd, args) {
		if b.Revision == "" || seen[b.Revision] {
			continue
		}
		seen[b.Revision] = true
		out = append(out, fmt.Sprintf("%s\t#%d %s", b.Revision, b.Number, style.Label(b.State)))
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// recentBuilds lists the last 20 builds of the plan branch a command names
// through --plan or its first argument, honouring --branch; nil when it names
// none or the plan cannot be resolved.
func (r *runtime) recentBuilds(cmd *cobra.Command, args []string) []provider.Build {
	arg := ""
	if p, err := cmd.Flags().GetString("plan"); err == nil && p != "" {
		arg = p
	} else if len(args) > 0 {
		arg = args[0]
	}
	key := arg
	if k, ok := pageKey(arg); ok {
		key = k
	}
	if !config.ValidTargetName(key) && !app.IsPlanKey(key) {
		return nil
	}
	svc, arg, err := r.resolvePlanArg(cmd.Context(), arg)
	if err != nil {
		return nil
	}
	branch, _ := cmd.Flags().GetString("branch")
	ref, err := svc.ResolvePlan(cmd.Context(), arg, branch)
	if err != nil {
		return nil
	}
	builds, _ := svc.P.ListBuilds(cmd.Context(), ref.PlanKey, provider.ListOptions{Limit: 20})
	return builds
}

// completeJob offers the short job keys of the build a command names (a build
// key, or the latest build of a plan or preset, or --last), each described by
// its job name.
func (r *runtime) completeJob(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	last, _ := cmd.Flags().GetBool("last")
	branch, _ := cmd.Flags().GetString("branch")
	svc, key, err := r.resolveBuildArg(cmd.Context(), args, last, branch)
	if err != nil {
		return nil, none
	}
	b, err := svc.P.GetBuild(cmd.Context(), key)
	if err != nil {
		return nil, none
	}
	var out []string
	for _, s := range b.Stages {
		for _, j := range s.Jobs {
			out = append(out, app.ShortJobKey(b, j)+"\t"+j.Name)
		}
	}
	return out, none | cobra.ShellCompDirectiveKeepOrder
}

func newProjectListCmd(r *runtime) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:        "list [text]",
		SuggestFor: []string{"ls"},
		Short:      "List configured projects (--all: every project on the server)",
		Long: "List configured projects (--all: every project on the server). With text, list\n" +
			"only the projects whose key or name contains it, ignoring case.",
		Example: "  bam project list\n  bam project list --all ops",
		Args:    cobra.MaximumNArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			projects, err := svc.P.ListProjects(cmd.Context())
			if err != nil {
				return err
			}
			keys := r.cfg.ProjectKeys(svc.Server)
			if !all && len(keys) == 0 {
				r.note("no projects configured; showing every project", "add projects: [KEY] to .bam.yaml")
			}
			if !all && len(keys) > 0 {
				byKey := map[string]provider.Project{}
				for _, p := range projects {
					byKey[p.Key] = p
				}
				projects = nil
				for _, k := range keys {
					if p, ok := byKey[k]; ok {
						projects = append(projects, p)
					} else {
						view.PrintWarnings(r.env.Stderr, []string{fmt.Sprintf("project %s is configured but not on %s", k, svc.Server.Alias)})
					}
				}
			}
			empty := "no projects on " + svc.Server.Alias
			if len(args) == 1 {
				text, found := strings.ToLower(args[0]), projects
				projects = nil
				for _, p := range found {
					if strings.Contains(strings.ToLower(p.Key), text) || strings.Contains(strings.ToLower(p.Name), text) {
						projects = append(projects, p)
					}
				}
				empty = fmt.Sprintf("no projects on %s match %q", svc.Server.Alias, args[0])
			}
			if r.flags.json {
				docs := []view.ProjectDoc{}
				for _, p := range projects {
					docs = append(docs, view.ProjectJSON(p))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(projects) == 0 {
				r.note(empty, "")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Projects(o, projects)
		}),
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every project on the server")
	return cmd
}

func newPlanListCmd(r *runtime) *cobra.Command {
	var match string
	cmd := &cobra.Command{
		Use:        "list [PROJ...]",
		SuggestFor: []string{"ls"},
		Short:      "List plans of the configured projects, grouped by project",
		Long: "List plans of the configured projects, or of the projects given, grouped by\n" +
			"project. With --match, list only the plans whose key or name contains the\n" +
			"text, ignoring case. A project can also be a link copied from its Bamboo page.",
		Example:           "  bam plan list\n  bam plan list OPS LAB\n  bam plan list https://bamboo.example.com/browse/OPS\n  bam plan list --match deploy",
		ValidArgsFunction: r.completeProjects,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			keys, link, linkKey := slices.Clone(args), "", ""
			for i, a := range args {
				m := projectLinkRe.FindStringSubmatch(a)
				if m == nil {
					continue
				}
				keys[i] = m[1]
				if link == "" {
					link, linkKey = a, m[1]
					continue
				}
				o1, _ := credential.Origin(link)
				if o2, _ := credential.Origin(a); o1 != o2 {
					return errs.Usagef("project links point at two servers, %s and %s", o1, o2).
						WithTry("list each server's projects in its own bam plan list")
				}
			}
			var svc *app.Service
			var err error
			if link != "" {
				svc, err = r.connectPage(link, linkKey)
			} else {
				svc, _, err = r.connect(cmd.Context(), "")
			}
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				keys = r.cfg.ProjectKeys(svc.Server)
			}
			names := map[string]string{}
			projects, perr := svc.P.ListProjects(cmd.Context())
			for _, p := range projects {
				names[p.Key] = p.Name
			}
			if len(keys) == 0 {
				e, k := errs.Usagef("no projects configured"), "PROJ"
				if perr == nil && len(projects) > 0 {
					onServer := make([]string, len(projects))
					for i, p := range projects {
						onServer[i] = p.Key
					}
					why := "projects on " + svc.Server.Alias + ": " + strings.Join(onServer[:min(5, len(onServer))], ", ")
					if n := len(onServer) - 5; n > 0 {
						why += fmt.Sprintf(" and %d more (bam project list)", n)
					}
					e, k = e.WithWhy(why), onServer[0]
				}
				return e.WithTry("bam plan list " + k + ", or add projects: [" + k + "] to .bam.yaml")
			}
			var groups []view.PlanGroup
			for _, k := range keys {
				plans, err := svc.P.ListPlans(cmd.Context(), k)
				if err != nil {
					var e *errs.Error
					if errors.Is(err, errs.ErrNotFound) && errors.As(err, &e) {
						if c := errs.Closest(k, slices.Sorted(maps.Keys(names)), 1); len(c) > 0 {
							_ = e.WithTry("did you mean bam plan list " + c[0] + "?")
						}
					}
					return err
				}
				if match != "" {
					plans = slices.DeleteFunc(slices.Clone(plans), func(p provider.Plan) bool {
						return !strings.Contains(strings.ToLower(p.Key), strings.ToLower(match)) && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(match))
					})
					if len(plans) == 0 {
						continue
					}
				}
				groups = append(groups, view.PlanGroup{Project: provider.Project{Key: k, Name: names[k], URL: svc.P.URL(k)}, Plans: plans})
			}
			if r.flags.json {
				docs := []view.PlanDoc{}
				for _, g := range groups {
					for _, p := range g.Plans {
						docs = append(docs, view.PlanJSON(p))
					}
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(groups) == 0 {
				r.note(fmt.Sprintf("no plans match %q", match), "")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.PlanList(o, groups)
		}),
	}
	cmd.Flags().StringVar(&match, "match", "", "only plans whose key or name contains this text, ignoring case")
	return cmd
}

func newPlanShowCmd(r *runtime) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:               "show <plan|preset>",
		Short:             "Show a plan: URL, branches, variables, recent builds",
		SuggestFor:        showSuggest,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, arg, branch)
			if err != nil {
				return err
			}
			plan, err := svc.P.GetPlan(ctx, ref.MasterKey)
			if err != nil {
				return err
			}
			branches, err := svc.P.ListBranches(ctx, ref.MasterKey)
			if err != nil {
				return err
			}
			d := view.PlanDetail{Plan: plan, Branches: branches}
			if vars, err := svc.PlanVars(ctx, ref, ""); err != nil {
				d.VarsErr = err.Error()
			} else {
				d.Vars = vars
			}
			if d.Builds, err = svc.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: 5, Unfinished: true}); err != nil {
				return err
			}
			app.WithBranch(d.Builds, ref.Branch)
			if r.flags.json {
				branchDocs := []view.BranchDoc{}
				for _, b := range branches {
					branchDocs = append(branchDocs, view.BranchJSON(b))
				}
				doc := map[string]any{
					"plan": view.PlanJSON(plan), "branches": branchDocs,
					"variables": view.VarRowsJSON(d.Vars), "builds": view.BuildsJSON(d.Builds),
				}
				if d.VarsErr != "" {
					doc["variables_error"] = d.VarsErr
				}
				return view.WriteJSON(r.env.Stdout, doc)
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.PlanDetailView(o, d)
		}),
	}
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch for the recent builds")
	return cmd
}

func newPlanVarsCmd(r *runtime) *cobra.Command {
	var branch, from string
	cmd := &cobra.Command{
		Use:   "vars <plan|preset> [text]",
		Short: "List a plan's variables with the values a recent build used",
		Long: "List a plan's variables with the values a recent build used. With text, list\n" +
			"only the variables whose name contains it, ignoring case.",
		Example:           "  bam plan vars PROJ-BUILD\n  bam plan vars PROJ-BUILD cluster",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, arg, branch)
			if err != nil {
				return err
			}
			res, err := svc.PlanVars(ctx, ref, from)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, res.Warnings)
			empty := "no variables declared on " + ref.MasterKey
			if len(args) == 2 {
				// Match names only: values may be secrets.
				text, all := strings.ToLower(args[1]), res.Rows
				res.Rows = nil
				for _, v := range all {
					if strings.Contains(strings.ToLower(v.Name), text) {
						res.Rows = append(res.Rows, v)
					}
				}
				empty = fmt.Sprintf("no variables on %s match %q", ref.MasterKey, args[1])
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.VarRowsJSON(res))
			}
			if len(res.Rows) == 0 {
				r.note(empty, "")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Vars(o, res)
		}),
	}
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch whose builds supply last-used values")
	cmd.Flags().StringVar(&from, "from", "", "build for last-used values: number, key or last")
	return cmd
}

func newPlanBranchesCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "branches <plan|preset> [text]",
		Short: "List a plan's branches",
		Long: "List a plan's branches. With text, list only the branches whose name or key\n" +
			"contains it, ignoring case.",
		Example:           "  bam plan branches PROJ-BUILD\n  bam plan branches PROJ-BUILD feature",
		SuggestFor:        []string{"branch"},
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, arg, "")
			if err != nil {
				return err
			}
			branches, err := svc.P.ListBranches(ctx, ref.MasterKey)
			if err != nil {
				return err
			}
			empty := "no branches on " + ref.MasterKey
			if len(args) == 2 {
				text, all := strings.ToLower(args[1]), branches
				branches = nil
				for _, b := range all {
					if strings.Contains(strings.ToLower(b.ShortName), text) || strings.Contains(strings.ToLower(b.Key), text) {
						branches = append(branches, b)
					}
				}
				empty = fmt.Sprintf("no branches on %s match %q", ref.MasterKey, args[1])
			}
			if r.flags.json {
				docs := []view.BranchDoc{}
				for _, b := range branches {
					docs = append(docs, view.BranchJSON(b))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(branches) == 0 {
				r.note(empty, "")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Branches(o, branches)
		}),
	}
}

func stateNames() []string {
	names := []string{}
	for _, st := range provider.AllStates() {
		names = append(names, string(st))
	}
	return names
}

func squashState(s string) string {
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(s)
}

func parseState(s string) (provider.State, error) {
	if s == "" {
		return "", nil
	}
	for _, st := range provider.AllStates() {
		// Bamboo's own UI and API spell states "Failed" and "NotBuilt", so accept
		// any case and ignore _, - and spaces.
		if strings.EqualFold(squashState(string(st)), squashState(s)) {
			return st, nil
		}
	}
	// Bamboo's UI says "Successful" and "In progress"; the rest are common words for the same states.
	aliases := map[string]provider.State{
		"successful": provider.StateSuccess, "passed": provider.StateSuccess,
		"failure": provider.StateFailed, "fail": provider.StateFailed,
		"inprogress": provider.StateRunning, "building": provider.StateRunning,
		"cancelled": provider.StateStopped, "canceled": provider.StateStopped,
	}
	if st, ok := aliases[strings.ToLower(squashState(s))]; ok {
		return st, nil
	}
	names := stateNames()
	e := errs.Usagef("--state must be one of %s, not %q", strings.Join(names, ", "), s)
	if c := errs.Closest(s, names, 1); len(c) > 0 {
		e = e.WithTry("did you mean --state " + c[0] + "?")
	}
	return "", e
}

func newPlanStatsCmd(r *runtime) *cobra.Command {
	var branch string
	var last int
	cmd := &cobra.Command{
		Use:               "stats <plan|preset>",
		Short:             "Success rate, durations and failing jobs over a plan's last builds",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			// Checked before any config or network.
			if last < 1 || last > 100 {
				return errs.Usagef("--last takes 1 to 100, not %d", last).
					WithTry(fmt.Sprintf("bam plan stats %s -n %d", args[0], min(max(last, 1), 100)))
			}
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, arg, branch)
			if err != nil {
				return err
			}
			st, err := svc.PlanStats(ctx, ref, last)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, st.Warnings)
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.PlanStatsJSON(st))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.PlanStatsView(o, st)
		}),
	}
	f := cmd.Flags()
	f.IntVarP(&last, "last", "n", 20, "how many of the newest builds to read (1 to 100)")
	f.StringVar(&branch, "branch", "", "plan branch to read")
	return cmd
}

func newBuildListCmd(r *runtime) *cobra.Command {
	var branch, state string
	var limit int
	cmd := &cobra.Command{
		Use:               "list <plan|preset>",
		Short:             "List recent builds of a plan",
		SuggestFor:        []string{"history", "builds", "ls"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			st, err := parseState(state)
			if err != nil {
				return err
			}
			if limit < 1 {
				return errs.Usagef("--limit takes 1 or more, not %d", limit).WithTry("--limit 1 lists only the newest build")
			}
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, arg, branch)
			if err != nil {
				return err
			}
			builds, err := svc.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: limit, State: st, Unfinished: true})
			if err != nil {
				return err
			}
			app.WithBranch(builds, ref.Branch)
			progress := runningProgress(ctx, svc, builds)
			if r.flags.json {
				docs := []view.BuildDoc{}
				for _, b := range builds {
					docs = append(docs, view.BuildJSONWithProgress(b, progress[b.Key]))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(builds) == 0 {
				msg := "no builds for " + ref.PlanKey
				if st != "" {
					msg = fmt.Sprintf("no %s builds for %s", st, ref.PlanKey)
				}
				if ref.Branch != "" {
					msg += " on branch " + ref.Branch
				}
				// With a --state filter, the plan may well have builds: offer the unfiltered list.
				try := "bam run " + arg
				if st != "" {
					try = "bam build list " + arg
				}
				if branch != "" {
					try += " --branch " + branch
				}
				r.note(msg, try)
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.BuildListWithProgress(o, builds, progress)
		}),
	}
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch")
	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "number of builds")
	cmd.Flags().StringVar(&state, "state", "", "only builds in this state, e.g. failed")
	_ = cmd.RegisterFlagCompletionFunc("state", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return stateNames(), cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// runningProgress asks the server for an estimate for the running builds
// only: no other state can have one, and each answer costs a request.
func runningProgress(ctx context.Context, svc *app.Service, builds []provider.Build) map[string]provider.Progress {
	out := map[string]provider.Progress{}
	for _, b := range builds {
		if b.State != provider.StateRunning {
			continue
		}
		if p, err := svc.Progress(ctx, b); err == nil && p.Valid {
			out[b.Key] = p
		}
	}
	return out
}

// resolvePlanArg connects to the right server for a <plan|preset> argument
// and returns the argument to resolve: a pasted page link's key, looked up on
// the link's server, or arg itself.
func (r *runtime) resolvePlanArg(ctx context.Context, arg string) (*app.Service, string, error) {
	if key, ok := pageKey(arg); ok {
		svc, err := r.connectPage(arg, key)
		return svc, key, err
	}
	svc, _, err := r.connectFor(ctx, arg)
	return svc, arg, err
}

// resolveBuildArg connects to the right server and turns [<build>] / --last into a build key.
func (r *runtime) resolveBuildArg(ctx context.Context, args []string, last bool, branch string) (*app.Service, string, error) {
	if err := lastOrArg(args, last); err != nil {
		return nil, "", err
	}
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	var svc *app.Service
	var err error
	if key, ok := pageKey(arg); ok {
		arg = key
		svc, err = r.connectPage(args[0], key)
	} else if last {
		svc, _, err = r.connectLast(ctx)
	} else {
		svc, _, err = r.connectFor(ctx, arg)
	}
	if err != nil {
		return nil, "", err
	}
	key, err := svc.ResolveBuild(ctx, app.BuildArg{Arg: arg, Last: last, Branch: branch})
	return svc, key, err
}

// lastOrArg refuses --last next to a key or preset, which would otherwise be
// silently ignored.
func lastOrArg(args []string, last bool) error {
	if !last || len(args) == 0 {
		return nil
	}
	return errs.Usagef("--last and %s both name a build", args[0]).
		WithTry("drop " + args[0] + " for the last build bam triggered here, or drop --last")
}

// pageKeyRe is the key in a Bamboo page link: .../browse/PROJ-PLAN-42, or a
// job's .../browse/PROJ-PLAN-JOB1-42/log.
var pageKeyRe = regexp.MustCompile(`^https?://[^/]+(?:/[^?#]*)?/browse/([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+)(?:[/?#]|$)`)

// projectLinkRe is the project key in a Bamboo project page link: .../browse/PROJ.
var projectLinkRe = regexp.MustCompile(`^https?://[^/]+(?:/[^?#]*)?/browse/([A-Z][A-Z0-9]*)(?:[/?#]|$)`)

// actionKeyRe is a plan or job key in an older .action link's planKey or buildKey.
var actionKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:-[A-Z0-9]+)+$`)

// pageKey returns the key of a pasted Bamboo page link: a .../browse/KEY link,
// or an .action link such as viewChainResult.action?planKey=PROJ-PLAN&buildNumber=42.
// An .action link without a buildNumber, such as a plan's configuration page
// (editChainDetails.action?buildKey=PROJ-PLAN), names the plan or job itself.
func pageKey(arg string) (string, bool) {
	if m := pageKeyRe.FindStringSubmatch(arg); m != nil {
		return m[1], true
	}
	u, err := url.Parse(arg)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	q := u.Query()
	plan, n := cmp.Or(q.Get("planKey"), q.Get("buildKey")), q.Get("buildNumber")
	if !actionKeyRe.MatchString(plan) || (q.Has("buildNumber") && (n == "" || strings.Trim(n, "0123456789") != "")) {
		return "", false
	}
	if n == "" {
		return plan, true
	}
	return plan + "-" + n, true
}

// connectPage connects to the configured server that a page link points at,
// so a link's key is never looked up on another server.
func (r *runtime) connectPage(link, key string) (*app.Service, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	origin, err := credential.Origin(link)
	if err != nil {
		return nil, err
	}
	for _, s := range cfg.Servers() {
		if o, err := credential.Origin(s.URL); err == nil && o == origin {
			svc, _, err := r.connectServer(s)
			return svc, err
		}
	}
	return nil, errs.Configf("%s is on %s, which no configured server matches", key, origin).
		WithTry("bam server add <alias> --url " + origin)
}

func newBuildShowCmd(r *runtime) *cobra.Command {
	var last bool
	var branch string
	cmd := &cobra.Command{
		Use:               "show [<build>|<plan>|<preset>]",
		Short:             "Show a build: state, stages, jobs, failure summary",
		SuggestFor:        append(slices.Clone(showSuggest), "status"),
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, key, err := r.resolveBuildArg(cmd.Context(), args, last, branch)
			if err != nil {
				return err
			}
			b, err := svc.Build(cmd.Context(), key)
			if err != nil {
				return err
			}
			// A missing estimate is decoration lost, never a failed read.
			pr, _ := svc.Progress(cmd.Context(), b)
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.BuildJSONWithProgress(b, pr))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.BuildDetailWithProgress(o, b, pr)
		}),
	}
	cmd.Flags().BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	return cmd
}
