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
	subgroup(root, "preset", "Run presets: list, show, add, sync", groupRun).
		AddCommand(newTargetListCmd(r), newTargetShowCmd(r), newTargetAddCmd(r), newTargetSyncCmd(r))
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
		Short: "List run presets from your presets file, .bam.yaml and the machine config",
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
				r.note("no presets configured", "bam preset add <name> --plan PROJ-PLAN")
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

func newTargetShowCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "show <name>",
		Short:             "Show a run preset with its defaults, options and origin",
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
	var plan, from, branch string
	var shared, printOnly, force bool
	cmd := &cobra.Command{
		Use:   "add <name> --plan KEY",
		Short: "Generate a run preset from a plan's declared variables",
		Long: "add reads the plan's declared variables from Bamboo and writes a preset named <name>.\n" +
			"It writes your presets file (~/bam/presets.yaml, or BAM_PRESETS), whose presets work in every\n" +
			"directory; --shared writes the repository's .bam.yaml instead. Edit the values under variables,\n" +
			"then run it with bam run <name>.",
		Example: "  bam preset add deploy-qa --plan PROJ-DEPLOY\n" +
			"  bam preset add deploy-qa --plan PROJ-DEPLOY --branch main --from last\n" +
			"  bam preset add provision-lab --plan PROJ-PROV --shared\n" +
			"  bam run deploy-qa --dry-run",
		Args: cobra.ExactArgs(1),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if plan == "" {
				return errs.Usagef("--plan is required").WithTry(fmt.Sprintf("bam preset add %s --plan PROJ-PLAN", args[0]))
			}
			svc, _, err := r.connect(cmd.Context(), "")
			if err != nil {
				return err
			}
			d, err := svc.GenerateTarget(cmd.Context(), app.GenerateOptions{Name: args[0], PlanKey: plan, From: from, Branch: branch})
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
			path, keyPath := r.presetsWritePath(), []string{"presets"}
			if shared {
				path, keyPath = r.cfg.ProjectPath, []string{"targets"}
				if path == "" {
					return errs.Usagef("no .bam.yaml in this repository").
						WithTry("bam init --server ALIAS --project KEY, or drop --shared to write your presets file")
				}
			}
			if err := config.WriteTarget(path, keyPath, d, force); err != nil {
				return err
			}
			fmt.Fprintf(r.env.Stdout, "added preset %s to %s\n", d.Name, path)
			if d.Note != "" {
				fmt.Fprintln(r.env.Stderr, "note: "+d.Note)
			}
			fmt.Fprintln(r.env.Stderr, "review before commit: values copied from Bamboo")
			return nil
		}),
	}
	f := cmd.Flags()
	f.StringVar(&plan, "plan", "", "plan key to generate from")
	f.StringVar(&from, "from", "", "build whose values fill the comments: number, key or last")
	f.StringVar(&branch, "branch", "", "default branch for the preset")
	f.BoolVar(&shared, "shared", false, "write to the repository's .bam.yaml instead of your presets file")
	f.BoolVar(&printOnly, "print", false, "print the YAML and write nothing")
	f.BoolVar(&force, "force", false, "replace an existing preset")
	return cmd
}

func newTargetSyncCmd(r *runtime) *cobra.Command {
	var all, dryRun bool
	cmd := &cobra.Command{
		Use:               "sync <name> | --all",
		Short:             "Add new plan variables to a preset and mark removed ones",
		Long:              "sync compares a preset with its plan's declared variables. New variables are added to defaults\nwith the same values and comments as preset add. Variables the plan no longer declares are kept\nand marked \"not declared on PLAN\". Existing values are never changed.",
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
					return errs.Usagef("unknown preset %q", args[0]).WithTry("bam preset list")
				}
				targets = append(targets, t)
			}
			if len(targets) == 0 {
				r.note("no presets configured", "bam preset add <name> --plan PROJ-PLAN")
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
					d, err := svc.GenerateTarget(cmd.Context(), app.GenerateOptions{Name: name, PlanKey: key})
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
			if len(drafts) > 0 {
				fmt.Fprintln(r.env.Stderr, "review before commit: values copied from Bamboo")
			}
			return nil
		}),
	}
	f := cmd.Flags()
	f.StringVar(&url, "url", "", "server URL (default: the URL of --server in the machine config)")
	f.StringSliceVar(&projects, "project", nil, "project key (repeatable; the first is the default)")
	f.StringArrayVar(&plans, "plan", nil, "plan to generate a preset from, KEY or KEY=name (repeatable)")
	f.BoolVar(&force, "force", false, "overwrite an existing .bam.yaml")
	return cmd
}
