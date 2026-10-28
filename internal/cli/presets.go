package cli

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addPresets) }

func addPresets(root *cobra.Command, r *runtime) {
	p := subgroup(root, "preset", "Run presets: list, show, add, rm, sync, migrate", groupRun)
	p.SuggestFor = []string{"target", "targets"} // bam target was renamed to bam preset
	p.AddCommand(newTargetListCmd(r), newTargetShowCmd(r), newTargetAddCmd(r), newPresetRmCmd(r), newTargetSyncCmd(r), newPresetMigrateCmd(r))
	c := newInitCmd(r)
	c.GroupID = groupSetup
	root.AddCommand(c)
}

// defaultTargetName is the lowercased plan part of a plan key.
func defaultTargetName(planKey string) string {
	if i := strings.Index(planKey, "-"); i >= 0 {
		return strings.ToLower(planKey[i+1:])
	}
	return strings.ToLower(planKey)
}

func newTargetListCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List run presets from your presets files, .bam.yaml and the machine config",
		Args:  cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			infos, err := app.DescribeTargets(cfg, r.env.Getenv)
			if err != nil {
				return err
			}
			loc := r.presets()
			if !r.flags.json {
				fmt.Fprintln(r.env.Stderr, "presets file: "+loc.Path)
				fmt.Fprintln(r.env.Stderr, "presets dir:  "+config.PresetsDir(loc.Path))
				if loc.Note != "" {
					fmt.Fprintln(r.env.Stderr, "  note: "+loc.Note)
				}
			}
			if r.flags.json {
				docs := []view.TargetDoc{}
				for _, t := range infos {
					docs = append(docs, view.TargetJSON(t))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(infos) == 0 {
				r.noPresets(cfg)
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Targets(o, infos)
		}),
	}
}

// noPresets explains an empty preset list, naming presets that only apply in other repositories.
func (r *runtime) noPresets(cfg *config.Config) {
	if other := cfg.OtherRepoTargets(); len(other) > 0 {
		n, roots := 0, []string{}
		for _, root := range slices.Sorted(maps.Keys(other)) {
			n += len(other[root])
			roots = append(roots, root+" ("+strings.Join(other[root], ", ")+")")
		}
		r.note(fmt.Sprintf("no presets apply here; %d are defined for other repositories: %s", n, strings.Join(roots, ", ")),
			"run bam from that repository, or bam preset migrate to use them anywhere")
		return
	}
	r.note("no presets configured", "bam preset add <name> --plan PROJ-PLAN")
}

func newTargetShowCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "show <name>",
		Short:             "Show a run preset with its defaults, options and origin",
		SuggestFor:        showSuggest,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			info, err := app.DescribeTarget(cfg, r.env.Getenv, args[0])
			if err != nil {
				return err
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.TargetJSON(info))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.TargetDetail(o, info)
		}),
	}
}

func newTargetAddCmd(r *runtime) *cobra.Command {
	var plan, from, branch, file string
	var shared, printOnly, force, full bool
	cmd := &cobra.Command{
		Use:        "add <name> --plan KEY",
		Short:      "Generate a run preset from a plan's declared variables",
		SuggestFor: []string{"new", "create"},
		Long: "add reads the plan's declared variables from Bamboo and writes a preset named <name>.\n" +
			"It writes the plan's project file in your presets directory (~/bam/presets/proj.yaml for\n" +
			"PROJ-DEPLOY, or beside BAM_PRESETS), creating it if needed; those presets work in every\n" +
			"directory. --file NAME writes NAME.yaml in that directory instead, for presets that belong to no\n" +
			"one project; --shared writes the repository's .bam.yaml. Edit the values under variables,\n" +
			"then run it with bam run <name>. --from with a full build key implies --plan and the build's\n" +
			"branch. The preset lists only variables that differ from the plan defaults or have no default;\n" +
			"--full also lists the rest, with commented branch, options and watch lines to fill in.",
		Example: "  bam preset add deploy-qa --plan PROJ-DEPLOY\n" +
			"  bam preset add deploy-qa --plan PROJ-DEPLOY --branch main --from last\n" +
			"  bam preset add deploy-eu --from PROJ-DEPLOY-41\n" +
			"  bam preset add deploy-full --plan PROJ-DEPLOY --full\n" +
			"  bam preset add deploy-package --plan PROJ-PKG --file custom-presets\n" +
			"  bam preset add provision-lab --plan PROJ-PROV --shared\n" +
			"  bam run deploy-qa --dry-run",
		Args: cobra.ExactArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if plan == "" && !app.IsBuildKey(from) {
				return errs.Usagef("--plan is required").
					WithTry(fmt.Sprintf("bam preset add %s --plan PROJ-PLAN, or --from PROJ-PLAN-41", args[0]))
			}
			if shared && file != "" {
				return errs.Usagef("--shared and --file cannot be used together").
					WithTry("--file for a file in your presets directory, --shared for .bam.yaml")
			}
			if file != "" {
				if _, err := config.PresetsDirFile(r.presetsWritePath(), file); err != nil {
					return err
				}
			}
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			d, err := svc.GenerateTarget(cmd.Context(), app.GenerateOptions{Name: args[0], PlanKey: plan, From: from, Branch: branch, Full: full})
			if err != nil {
				return err
			}
			if printOnly {
				key := "presets"
				if shared {
					key = "targets"
				}
				_, err := fmt.Fprint(r.env.Stdout, config.DraftYAML(key, d))
				return err
			}
			var path string
			if shared {
				path = r.cfg.ProjectPath
				if path == "" {
					return errs.Usagef("no .bam.yaml in this repository").
						WithTry("bam init --server ALIAS --project KEY, or drop --shared to write your presets file")
				}
				err = config.WriteTarget(path, []string{"targets"}, d, force)
			} else {
				path, err = r.writePreset(r.cfg, d, file, force)
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "added preset %s to %s\n", d.Name, path)
			if d.Note != "" {
				fmt.Fprintln(r.env.Stderr, "note: "+d.Note)
			}
			if len(d.Vars) > 0 {
				fmt.Fprintln(r.env.Stderr, "review before commit: values copied from Bamboo")
			}
			return nil
		}),
	}
	f := cmd.Flags()
	f.StringVar(&plan, "plan", "", "plan key to generate from; optional when --from is a build key")
	f.StringVar(&from, "from", "", "build whose variable values fill the preset: number, key or last")
	f.StringVar(&branch, "branch", "", "default branch for the preset; defaults to the --from build's branch")
	f.BoolVar(&shared, "shared", false, "write to the repository's .bam.yaml instead of your presets directory")
	f.StringVar(&file, "file", "", "write NAME.yaml in your presets directory instead of the plan's project file")
	f.BoolVar(&printOnly, "print", false, "print the YAML and write nothing")
	f.BoolVar(&force, "force", false, "replace an existing preset")
	f.BoolVar(&full, "full", false, "also write variables equal to the plan default and commented branch, options and watch lines")
	_ = cmd.RegisterFlagCompletionFunc("plan", r.completePlans)
	return cmd
}

func newPresetRmCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "rm <name>",
		Short:             "Remove a run preset from every file that defines it",
		SuggestFor:        []string{"remove", "delete"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			t, ok, err := cfg.Target(args[0])
			if err != nil {
				return err
			}
			if !ok {
				if all, _ := cfg.Targets(); len(all) == 0 && len(cfg.OtherRepoTargets()) == 0 {
					// UnknownTarget's hint would be to create the preset, which is wrong for rm.
					return errs.Usagef("unknown preset %q", args[0]).WithWhy("no presets are configured, so there is nothing to remove")
				}
				return app.UnknownTarget(cfg, args[0])
			}
			// A set naming the preset would fail every set command once it is gone.
			sets, err := cfg.Sets()
			if err != nil {
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(sets)) {
				for _, st := range sets[name].Stages {
					for _, run := range st {
						if run.Target == t.Name {
							return errs.Usagef("preset %q is used by set %q", t.Name, name).
								WithWhy("defined in " + sets[name].DefinedIn[len(sets[name].DefinedIn)-1]).
								WithTry("remove it from the set first")
						}
					}
				}
			}
			for _, l := range t.Layers {
				if err := config.RemoveTarget(l.Path, l.KeyPath, t.Name); err != nil {
					return err
				}
				fmt.Fprintf(r.env.Stdout, "removed preset %s from %s\n", t.Name, l.Path)
			}
			return nil
		}),
	}
}

// writePreset adds d to file in the presets directory, by default the plan's
// project file, and returns the path it wrote. preset add and the UI share it.
func (r *runtime) writePreset(cfg *config.Config, d config.TargetDraft, file string, force bool) (string, error) {
	if file == "" {
		file = config.ProjectPresetsName(d.Plan)
	}
	path, err := config.PresetsDirFile(r.presetsWritePath(), file)
	if err != nil {
		return "", err
	}
	if err := checkPresetElsewhere(cfg, d.Name, path); err != nil {
		return "", err
	}
	return path, config.WriteTarget(path, []string{"presets"}, d, force)
}

// checkPresetElsewhere refuses to add name to path when another presets file
// already defines it: names are global across presets files, and --force
// replaces a preset only in the file that holds it.
func checkPresetElsewhere(cfg *config.Config, name, path string) error {
	all, err := cfg.Targets()
	if err != nil {
		return err
	}
	for _, l := range all[name].Layers {
		if l.Path != path && cfg.IsPresetsPath(l.Path) {
			return errs.Usagef("preset %q already exists in %s", name, l.Path).
				WithWhy("preset names are shared by every presets file").
				WithTry("pick another name, or edit or remove it in " + l.Path)
		}
	}
	return nil
}

func newTargetSyncCmd(r *runtime) *cobra.Command {
	var all, dryRun bool
	cmd := &cobra.Command{
		Use:               "sync <name> | --all",
		Short:             "Add new plan variables to a preset and mark removed ones",
		Long:              "sync compares a preset with its plan's declared variables. New variables that preset add\nwould write (no plan default, or an empty secret) are added to the preset's variables (or\ndefaults, if the preset uses that older key) with the same values and comments. Variables the plan no longer declares are kept and marked\n\"not declared on PLAN\". Existing values are never changed.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == all {
				return errs.Usagef("pass a preset name or --all").WithTry("bam preset sync <name>, or bam preset sync --all")
			}
			cfg, err := r.config()
			if err != nil {
				return err
			}
			var targets []config.ResolvedTarget
			if all {
				m, err := cfg.Targets()
				if err != nil {
					return err
				}
				for _, name := range slices.Sorted(maps.Keys(m)) {
					targets = append(targets, m[name])
				}
			} else {
				t, ok, err := cfg.Target(args[0])
				if err != nil {
					return err
				}
				if !ok {
					return app.UnknownTarget(cfg, args[0])
				}
				targets = append(targets, t)
			}
			if len(targets) == 0 {
				r.noPresets(cfg)
				return nil
			}

			services := map[string]*app.Service{}
			var results []view.SyncResult
			var worst error
			wrote := false
			for _, t := range targets {
				res := r.syncTarget(cmd, cfg, services, t, dryRun)
				if res.Err != nil && (worst == nil || exitCode(res.Err) > exitCode(worst)) {
					worst = res.Err
				}
				wrote = wrote || res.Status == view.SyncSynced
				results = append(results, res)
			}
			if r.flags.json {
				if err := view.WriteJSON(r.env.Stdout, view.SyncJSON(results)); err != nil {
					return err
				}
			} else {
				o, err := r.out()
				if err != nil {
					return err
				}
				if err := view.Sync(o, results, dryRun); err != nil {
					return err
				}
			}
			if wrote {
				fmt.Fprintln(r.env.Stderr, "review before commit: values copied from Bamboo")
			}
			if worst != nil {
				return silentError{worst}
			}
			return nil
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&all, "all", false, "sync every preset")
	f.BoolVar(&dryRun, "dry-run", false, "show the changes and write nothing")
	return cmd
}

// syncTarget plans and applies the sync of one target. Errors land in the
// result so that --all can move on to the next target.
func (r *runtime) syncTarget(cmd *cobra.Command, cfg *config.Config, services map[string]*app.Service, t config.ResolvedTarget, dryRun bool) view.SyncResult {
	res := view.SyncResult{Sync: app.SyncPlan{Name: t.Name, Plan: t.Plan, Stale: []string{}}, Status: view.SyncError}
	server, err := cfg.SelectServer(r.flags.server, t.Server)
	if err != nil {
		res.Err = err
		return res
	}
	key := server.Alias + "\x00" + server.URL
	svc := services[key]
	if svc == nil {
		if svc, _, err = r.connectServer(server); err != nil {
			res.Err = err
			return res
		}
		services[key] = svc
	}
	p, err := svc.PlanSync(cmd.Context(), t)
	if err != nil {
		res.Err = err
		return res
	}
	res.Sync, res.File = p, displayPath(cfg.RepoRoot, p.File)
	changed := false
	for _, e := range p.Edits {
		ch, err := config.SyncTarget(e.Path, e.KeyPath, t.Name, e.Edit, !dryRun)
		if err != nil {
			res.Err, res.Status = err, view.SyncError
			return res
		}
		changed = changed || !ch.Empty()
		res.Redeclared = append(res.Redeclared, ch.Unmarked...)
	}
	switch {
	case !changed:
		res.Status = view.SyncUpToDate
	case dryRun:
		res.Status = view.SyncWouldSync
	default:
		res.Status = view.SyncSynced
	}
	return res
}

// displayPath shows path relative to the repository root when it is inside it.
func displayPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func newInitCmd(r *runtime) *cobra.Command {
	var url string
	var projects, plans []string
	var force bool
	cmd := &cobra.Command{
		Use:   "init --server ALIAS --project KEY [--plan KEY[=name]]...",
		Short: "Write .bam.yaml for this repository, with generated presets",
		Args:  cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			alias := r.flags.server
			if alias == "" {
				return errs.Usagef("--server ALIAS is required").WithTry("bam init --server work --project PROJ")
			}
			if len(projects) == 0 {
				return errs.Usagef("--project is required").WithTry("bam init --server " + alias + " --project PROJ")
			}
			if err := checkProjectKeys(projects); err != nil {
				return err
			}
			cfg, err := r.config()
			if err != nil {
				return err
			}
			path := filepath.Join(cfg.RepoRoot, config.ProjectFileName)
			known := cfg.Servers()[alias]
			if url == "" {
				url = known.URL
			}
			if url == "" {
				return errs.Configf("no URL for server alias %q", alias).
					WithTry(fmt.Sprintf("pass --url, or bam server add %s --url URL", alias))
			}
			if _, err := credential.Origin(url); err != nil {
				return err
			}
			var drafts []config.TargetDraft
			if len(plans) > 0 {
				// known.AuthEnv was learned for known.URL; only carry it over
				// when --url still resolves to that same origin, so a --url
				// override never pairs a stored env token with a host it
				// was not created for.
				authEnv := known.AuthEnv
				if authEnv != "" {
					knownOrigin, kerr := credential.Origin(known.URL)
					newOrigin, nerr := credential.Origin(url)
					if kerr != nil || nerr != nil || knownOrigin != newOrigin {
						authEnv = ""
					}
				}
				svc, _, err := r.connectServer(config.ResolvedServer{Alias: alias, URL: url, AuthEnv: authEnv})
				if err != nil {
					return err
				}
				for _, p := range plans {
					key, name, _ := strings.Cut(p, "=")
					if name == "" {
						name = defaultTargetName(key)
					}
					d, err := svc.GenerateTarget(cmd.Context(), app.GenerateOptions{Name: name, PlanKey: key, Full: true})
					if err != nil {
						if errs.KindOf(err) == errs.KindUsage && !config.ValidTargetName(name) {
							return errs.Usagef("cannot derive a preset name from %s", key).WithTry(fmt.Sprintf("--plan %s=NAME", key))
						}
						return err
					}
					drafts = append(drafts, d)
				}
			}
			if err := config.WriteInitFile(path, config.InitFile{ServerAlias: alias, ServerURL: url, Projects: projects}, drafts, force); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "wrote %s\n", path)
			if slices.ContainsFunc(drafts, func(d config.TargetDraft) bool { return len(d.Vars) > 0 }) {
				fmt.Fprintln(r.env.Stderr, "review before commit: values copied from Bamboo")
			}
			return nil
		}),
	}
	f := cmd.Flags()
	f.StringVar(&url, "url", "", "server URL (default: the URL of --server in the machine config)")
	f.StringSliceVar(&projects, "project", nil, "project key (repeatable; the first is the default)")
	f.StringArrayVar(&plans, "plan", nil, "plan to generate a preset from, KEY or KEY=name (repeatable)")
	_ = cmd.RegisterFlagCompletionFunc("project", func(c *cobra.Command, _ []string, s string) ([]string, cobra.ShellCompDirective) {
		return r.completeProjects(c, projects, s)
	})
	_ = cmd.RegisterFlagCompletionFunc("plan", r.completePlans)
	f.BoolVar(&force, "force", false, "overwrite an existing .bam.yaml")
	return cmd
}

// migrateDoc is bam preset migrate --json.
type migrateDoc struct {
	PresetsFile string           `json:"presets_file"`
	MachineFile string           `json:"machine_file"`
	DryRun      bool             `json:"dry_run"`
	Moved       []migrateMoveDoc `json:"moved"`
	Skipped     []migrateSkipDoc `json:"skipped"`
}

type migrateMoveDoc struct {
	Name string `json:"name"`
	From string `json:"from"`
}

type migrateSkipDoc struct {
	Name   string `json:"name"`
	From   string `json:"from"`
	Reason string `json:"reason"`
}

func newPresetMigrateCmd(r *runtime) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move presets from the machine config into your presets file",
		Long: "migrate moves the presets in your machine config (top-level targets and every repos entry)\n" +
			"into your presets file (~/bam/presets.yaml, or BAM_PRESETS), renaming defaults to variables\n" +
			"and keeping comments, so they work from every directory. A name already in the presets file or\n" +
			"the presets directory, or defined more than once in the machine config, stays where it is.\n" +
			".bam.yaml is never touched.",
		Example: "  bam preset migrate --dry-run\n  bam preset migrate",
		Args:    cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			machine, presets := r.env.Paths.MachineConfig, r.presetsWritePath()
			res, err := config.MigrateTargets(machine, presets, !dryRun)
			if err != nil {
				return err
			}
			if r.flags.json {
				doc := migrateDoc{PresetsFile: presets, MachineFile: machine, DryRun: dryRun,
					Moved: []migrateMoveDoc{}, Skipped: []migrateSkipDoc{}}
				for _, m := range res.Moved {
					doc.Moved = append(doc.Moved, migrateMoveDoc{Name: m.Name, From: m.From})
				}
				for _, s := range res.Skipped {
					doc.Skipped = append(doc.Skipped, migrateSkipDoc{Name: s.Name, From: s.From, Reason: s.Reason})
				}
				return view.WriteJSON(r.env.Stdout, doc)
			}
			if len(res.Moved) == 0 && len(res.Skipped) == 0 {
				fmt.Fprintf(r.env.Stdout, "no presets to migrate in %s\n", machine)
				return nil
			}
			verb := "moved"
			if dryRun {
				verb = "would move"
			}
			type row struct{ action, name, detail string }
			var rows []row
			for _, m := range res.Moved {
				rows = append(rows, row{verb, m.Name, "from " + m.From})
			}
			for _, s := range res.Skipped {
				rows = append(rows, row{"kept", s.Name, s.Reason})
			}
			aw, nw := 0, 0
			for _, x := range rows {
				aw, nw = max(aw, len(x.action)), max(nw, len(x.name))
			}
			for _, x := range rows {
				fmt.Fprintf(r.env.Stdout, "%-*s  %-*s  %s\n", aw, x.action, nw, x.name, x.detail)
			}
			noun := "presets"
			if len(res.Moved) == 1 {
				noun = "preset"
			}
			fmt.Fprintf(r.env.Stdout, "%s %d %s to %s\n", verb, len(res.Moved), noun, presets)
			if dryRun {
				fmt.Fprintln(r.env.Stdout, "dry run: nothing written")
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would move and write nothing")
	return cmd
}
