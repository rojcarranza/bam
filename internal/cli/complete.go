package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view/style"
	"github.com/spf13/cobra"
)

// completeTargets offers target names for a <plan|preset> argument, and once
// a project key and dash are typed (PROJ-), that project's plan keys: one
// request, made only then so a bare Tab stays offline.
func (r *runtime) completeTargets(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := r.config()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	all, _ := cfg.Targets()
	names := slices.Sorted(maps.Keys(all))
	if project, _, ok := strings.Cut(toComplete, "-"); ok && app.IsProjectKey(project) {
		if svc, _, err := r.connect(cmd.Context(), ""); err == nil {
			plans, _ := svc.P.ListPlans(cmd.Context(), project)
			for _, p := range plans {
				names = append(names, p.Key+"\t"+p.Name)
			}
		}
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
	arg := planArg(cmd, args)
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

// planArg is the plan or preset a command names: its --plan flag (preset
// add) when set, else its first argument, else "".
func planArg(cmd *cobra.Command, args []string) string {
	if p, err := cmd.Flags().GetString("plan"); err == nil && p != "" {
		return p
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// recentBuilds lists the last 20 builds of the plan branch a command names
// through --plan or its first argument, honouring --branch; nil when it names
// none or the plan cannot be resolved.
func (r *runtime) recentBuilds(cmd *cobra.Command, args []string) []provider.Build {
	arg := planArg(cmd, args)
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

// completeVar completes --var names from the target and values from its
// options, or, for a plan key, the names of the plan's Bamboo variables.
func (r *runtime) completeVar(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	if len(args) == 0 {
		return nil, none
	}
	cfg, err := r.config()
	if err != nil {
		return nil, none
	}
	t, ok, err := cfg.Target(args[0])
	if err == nil && ok {
		return targetVars(toComplete, t)
	}
	if !app.IsPlanKey(args[0]) || strings.Contains(toComplete, "=") {
		return nil, none
	}
	svc, key, err := r.resolvePlanArg(cmd.Context(), args[0])
	if err != nil {
		return nil, none
	}
	vars, _ := svc.P.ListVariables(cmd.Context(), key)
	var out []string
	for _, v := range vars {
		out = append(out, v.Name+"=")
	}
	slices.Sort(out)
	return out, none | cobra.ShellCompDirectiveNoSpace
}

// targetVars offers name= for every variable the presets declare, or the
// declared options once toComplete has a name=.
func targetVars(toComplete string, targets ...config.ResolvedTarget) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	seen := map[string]bool{}
	if name, _, hasEq := strings.Cut(toComplete, "="); hasEq {
		var out []string
		for _, t := range targets {
			for _, v := range t.Options[name] {
				if !seen[v] {
					seen[v] = true
					out = append(out, name+"="+v)
				}
			}
		}
		return out, none
	}
	for _, t := range targets {
		for n := range t.Defaults {
			seen[n] = true
		}
		for n := range t.Options {
			seen[n] = true
		}
		for _, n := range t.Required {
			seen[n] = true
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n+"=")
	}
	slices.Sort(out)
	return out, none | cobra.ShellCompDirectiveNoSpace
}

// completeSets offers set names, and record ids too when ids is set.
func (r *runtime) completeSets(ids bool) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		if cfg, err := r.config(); err == nil {
			if sets, err := cfg.Sets(); err == nil {
				out = slices.Sorted(maps.Keys(sets))
			}
		}
		if ids {
			if all, err := r.setStore().IDs(); err == nil {
				out = append(out, all...)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeSetVar offers the variables of every preset the named set runs.
func (r *runtime) completeSetVar(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	if len(args) == 0 {
		return nil, none
	}
	cfg, err := r.config()
	if err != nil {
		return nil, none
	}
	s, ok, err := cfg.Set(args[0])
	if err != nil || !ok {
		return nil, none
	}
	var names []string
	for _, stage := range s.Stages {
		for _, run := range stage {
			names = append(names, run.Target)
		}
	}
	return presetVars(cfg, toComplete, names)
}

// completeRecordVar offers the variables of every preset a recorded set run
// (named by set or id, or the latest) ran, for resume and rerun.
func (r *runtime) completeRecordVar(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	rec, err := r.findSetRun(args, nil, nil)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, _ := r.config() // findSetRun already loaded it
	var names []string
	for _, st := range rec.Stages {
		for _, run := range st.Runs {
			names = append(names, run.Target)
		}
	}
	return presetVars(cfg, toComplete, names)
}

// presetVars completes --var from the named presets that still resolve.
func presetVars(cfg *config.Config, toComplete string, names []string) ([]string, cobra.ShellCompDirective) {
	var targets []config.ResolvedTarget
	for _, name := range names {
		if t, ok, err := cfg.Target(name); err == nil && ok {
			targets = append(targets, t)
		}
	}
	return targetVars(toComplete, targets...)
}
