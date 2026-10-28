package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
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

// completeTargets offers target names for a <plan> argument.
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

func newProjectListCmd(r *runtime) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured projects (--all: every project on the server)",
		Args:  cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
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
			if r.flags.json {
				docs := []view.ProjectDoc{}
				for _, p := range projects {
					docs = append(docs, view.ProjectJSON(p))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(projects) == 0 {
				r.note("no projects on "+svc.Server.Alias, "")
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
	return &cobra.Command{
		Use:               "list [PROJ...]",
		Short:             "List plans of the configured projects, grouped by project",
		ValidArgsFunction: r.completeProjects,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			keys := args
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
					return err
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
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.PlanList(o, groups)
		}),
	}
}

func newPlanShowCmd(r *runtime) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:               "show <plan>",
		Short:             "Show a plan: URL, branches, variables, recent builds",
		SuggestFor:        showSuggest,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], branch)
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
		Use:               "vars <plan>",
		Short:             "List a plan's variables with the values a recent build used",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], branch)
			if err != nil {
				return err
			}
			res, err := svc.PlanVars(ctx, ref, from)
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, res.Warnings)
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.VarRowsJSON(res))
			}
			if len(res.Rows) == 0 {
				r.note("no variables declared on "+ref.MasterKey, "")
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
		Use:               "branches <plan>",
		Short:             "List a plan's branches",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], "")
			if err != nil {
				return err
			}
			branches, err := svc.P.ListBranches(ctx, ref.MasterKey)
			if err != nil {
				return err
			}
			if r.flags.json {
				docs := []view.BranchDoc{}
				for _, b := range branches {
					docs = append(docs, view.BranchJSON(b))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(branches) == 0 {
				r.note("no branches on "+ref.MasterKey, "")
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

func parseState(s string) (provider.State, error) {
	if s == "" {
		return "", nil
	}
	for _, st := range provider.AllStates() {
		if string(st) == s {
			return st, nil
		}
	}
	names := stateNames()
	e := errs.Usagef("--state must be one of %s, not %q", strings.Join(names, ", "), s)
	if c := app.Closest(s, names, 1); len(c) > 0 {
		e = e.WithTry("did you mean --state " + c[0] + "?")
	}
	return "", e
}

func newPlanStatsCmd(r *runtime) *cobra.Command {
	var branch string
	var last int
	cmd := &cobra.Command{
		Use:               "stats <plan>",
		Short:             "Success rate, durations and failing jobs over a plan's last builds",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			// Checked before any config or network.
			if last < 1 || last > 100 {
				return errs.Usagef("--last takes 1 to 100, not %d", last)
			}
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], branch)
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
	f.IntVar(&last, "last", 20, "how many of the newest builds to read (1 to 100)")
	f.StringVar(&branch, "branch", "", "plan branch to read")
	return cmd
}

func newBuildListCmd(r *runtime) *cobra.Command {
	var branch, state string
	var limit int
	cmd := &cobra.Command{
		Use:               "list <plan>",
		Short:             "List recent builds of a plan",
		SuggestFor:        []string{"history", "builds"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			st, err := parseState(state)
			if err != nil {
				return err
			}
			if limit < 1 {
				return errs.Usagef("--limit must be at least 1, not %d", limit)
			}
			ctx := cmd.Context()
			svc, _, err := r.connectFor(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlan(ctx, args[0], branch)
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
				r.note(msg, "bam run "+args[0])
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
	cmd.Flags().IntVar(&limit, "limit", 10, "number of builds")
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

// resolveBuildArg connects to the right server and turns [<build>] / --last into a build key.
func (r *runtime) resolveBuildArg(ctx context.Context, args []string, last bool, branch string) (*app.Service, string, error) {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	var svc *app.Service
	var err error
	if last {
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

func newBuildShowCmd(r *runtime) *cobra.Command {
	var last bool
	var branch string
	cmd := &cobra.Command{
		Use:               "show [<build>|<plan>|<preset>]",
		Short:             "Show a build: state, stages, jobs, failure summary",
		SuggestFor:        showSuggest,
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
