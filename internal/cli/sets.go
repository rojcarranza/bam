package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

func init() { commandSets = append(commandSets, addSets) }

func addSets(root *cobra.Command, r *runtime) {
	set := subgroup(root, "set", "Release trains: presets run in ordered stages", groupRun)
	set.AddCommand(newSetListCmd(r), newSetShowCmd(r), newSetRunCmd(r), newSetStatusCmd(r), newSetResumeCmd(r), newSetRerunCmd(r))
	set.Args = cobra.ArbitraryArgs
	set.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		// Sets are edited as YAML; without this, "set rm" would suggest "set run", which starts a train.
		if slices.Contains([]string{"add", "new", "create", "rm", "remove", "delete", "edit"}, args[0]) {
			return errs.Usagef("bam set has no %q command", args[0]).
				WithWhy("sets are edited in YAML: under sets: in .bam.yaml, or in a train file").
				WithTry("see bam set run --help")
		}
		return unknownCommand(cmd, args)
	}
}

func (r *runtime) setStore() *app.SetStore {
	return &app.SetStore{Dir: r.env.Paths.Sets, Host: os.Hostname, PID: os.Getpid}
}

func newSetListCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List release train sets",
		Args:  cobra.NoArgs,
		RunE: r.wrap(func(cmd *cobra.Command, _ []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			infos, err := app.DescribeSets(cfg, r.env.Getenv)
			if err != nil {
				return err
			}
			if r.flags.json {
				docs := []view.SetDoc{}
				for _, s := range infos {
					docs = append(docs, view.SetJSON(s))
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			if len(infos) == 0 {
				r.note("no sets configured", "add sets: to .bam.yaml; see bam set run --help")
				return nil
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Sets(o, infos)
		}),
	}
}

func newSetShowCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "show <set>",
		Short:             "Show a set's stages and variables",
		SuggestFor:        showSuggest,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeSets(false),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			cfg, err := r.config()
			if err != nil {
				return err
			}
			info, err := app.DescribeSet(cfg, r.env.Getenv, args[0])
			if err != nil {
				return err
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.SetJSON(info))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.SetDetail(o, info)
		}),
	}
}

func newSetRunCmd(r *runtime) *cobra.Command {
	var vars []string
	var file string
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "run [set] [-f FILE]",
		Short: "Run a set: each stage starts when every build of the one before succeeded",
		Long: `Run a set: each stage starts when every build of the one before succeeded.

A failed build ends the set once its stage has finished; no build is ever
stopped. Interrupting stops watching only; continue with bam set resume.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeSets(false),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			switch {
			case name != "" && file != "":
				return errs.Usagef("give a set name or -f, not both")
			case name == "" && file == "":
				return errs.Usagef("give a set name or -f FILE").WithTry("bam set list")
			}
			ctx := cmd.Context()
			var svc *app.Service
			var err error
			if file != "" {
				svc, err = r.connectSetFile(ctx, file)
			} else {
				svc, err = r.connectSet(ctx, name)
			}
			if err != nil {
				return err
			}
			p, err := svc.PlanSet(ctx, name, app.SetOptions{Vars: vars, Server: r.flags.server, File: file})
			if err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, p.Warnings)
			if dryRun {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, view.SetPlanJSON(p))
				}
				o, err := r.out()
				if err != nil {
					return err
				}
				return view.SetDryRun(o, p)
			}
			id, events, err := svc.RunSet(ctx, p, app.SetLockOptions{Force: force})
			if err != nil {
				return err
			}
			return r.setLoop(ctx, id, p, nil, events)
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&vars, "var", nil, "variable name=value for every run that declares it (repeatable)")
	f.StringVarP(&file, "file", "f", "", "run the set defined in this train file instead of a configured set")
	f.BoolVar(&dryRun, "dry-run", false, "resolve and check every run, trigger nothing")
	f.BoolVar(&force, "force", false, "take over the lock of a set run whose process is gone")
	return cmd
}

func newSetStatusCmd(r *runtime) *cobra.Command {
	return &cobra.Command{
		Use:               "status [set|id]",
		Short:             "Show a set run and the current state of its builds",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeSets(true),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			rec, err := r.findSetRun(args, nil, nil)
			if err != nil {
				return err
			}
			svc, err := r.connectOrigin(cmd.Context(), rec.Origin)
			if err != nil {
				return err
			}
			rec, builds, err := svc.SetStatus(cmd.Context(), rec.ID)
			if err != nil {
				return err
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.SetRecordJSON(rec, builds))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.SetStatusView(o, rec, builds)
		}),
	}
}

func newSetResumeCmd(r *runtime) *cobra.Command {
	var vars []string
	var file string
	var force bool
	cmd := &cobra.Command{
		Use:               "resume [set|id]",
		Short:             "Continue an interrupted or timed-out set run",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeSets(true),
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rec, err := r.findSetRun(args, resumable, nil)
			if err != nil {
				return err
			}
			svc, err := r.connectOrigin(ctx, rec.Origin)
			if err != nil {
				return err
			}
			events, err := svc.ResumeSet(ctx, rec.ID, app.SetOptions{Vars: vars, Server: r.flags.server, File: file}, app.SetLockOptions{Force: force})
			if err != nil {
				return err
			}
			return r.setLoop(ctx, rec.ID, planFromRecord(rec), &rec, events)
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&vars, "var", nil, "variable name=value; give again every --var the run was started with (repeatable)")
	f.StringVar(&file, "file", "", "the train file of a file set, when it has moved since the run")
	f.BoolVar(&force, "force", false, "take over the lock of a set run whose process is gone")
	return cmd
}

func newSetRerunCmd(r *runtime) *cobra.Command {
	var vars []string
	var file string
	var force, failed bool
	cmd := &cobra.Command{
		Use:   "rerun [set|id] --failed",
		Short: "Retry the failed runs of a set run, then finish the train",
		Long: `Retry a failed set run under its own id. In the stage that failed, Bamboo
restarts each failed build: its failed jobs run again, with the same variables
and commit, under the same build key. A run that never triggered is triggered.
When that stage succeeds, the later stages run. Runs that succeeded are left
alone, and no build is ever stopped.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeSets(true),
		SuggestFor:        []string{"retry", "restart"},
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			// Checked before any config or network: --failed is what keeps a
			// future bare rerun (every run again) from changing meaning.
			if !failed {
				return errs.Usagef("rerun needs --failed").WithTry("bam set rerun --failed")
			}
			ctx := cmd.Context()
			rec, err := r.findSetRun(args, isFailedRun, noFailedRun)
			if err != nil {
				return err
			}
			svc, err := r.connectOrigin(ctx, rec.Origin)
			if err != nil {
				return err
			}
			events, err := svc.RerunSet(ctx, rec.ID, app.SetOptions{Vars: vars, Server: r.flags.server, File: file}, app.SetLockOptions{Force: force})
			if err != nil {
				return err
			}
			return r.setLoop(ctx, rec.ID, planFromRecord(rec), &rec, events)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&failed, "failed", false, "retry the runs that failed (required)")
	f.StringArrayVar(&vars, "var", nil, "variable name=value; give again every --var the run was started with (repeatable)")
	f.StringVar(&file, "file", "", "the train file of a file set, when it has moved since the run")
	f.BoolVar(&force, "force", false, "take over the lock of a set run whose process is gone")
	return cmd
}

func isFailedRun(rec app.SetRecord) bool { return rec.Status == app.SetFailed }

func noFailedRun(arg string) error {
	if arg == "" {
		return errs.Usagef("no failed set run to rerun in this repository").WithTry("bam set status")
	}
	return errs.Usagef("no failed run of %s to rerun", arg).WithTry("bam set status " + arg)
}

func resumable(rec app.SetRecord) bool {
	return rec.Status == app.SetInterrupted || rec.Status == app.SetTimedOut || rec.Status == app.SetRunning
}

// planFromRecord is enough of a plan for the live display: the set's name
// and every stage's presets.
func planFromRecord(rec app.SetRecord) app.SetPlan {
	p := app.SetPlan{Name: rec.Set}
	for _, st := range rec.Stages {
		var runs []app.SetRunPlan
		for _, run := range st.Runs {
			runs = append(runs, app.SetRunPlan{Name: run.RunName(), Target: run.Target, Ref: app.PlanRef{PlanKey: run.PlanKey}})
		}
		p.Stages = append(p.Stages, runs)
	}
	return p
}

// connectSetFile connects to the server of a train file's first preset, as
// connectSet does for a configured set.
func (r *runtime) connectSetFile(ctx context.Context, path string) (*app.Service, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	s, err := cfg.LoadSetFile(path)
	if err != nil {
		return nil, err
	}
	t, _, err := cfg.Target(s.Stages[0][0].Target)
	if err != nil {
		return nil, err
	}
	svc, _, err := r.connect(ctx, t.Server)
	return svc, err
}

// connectSet connects to the server of the set's first preset. PlanSet then
// checks that every other preset resolves to the same server.
func (r *runtime) connectSet(ctx context.Context, name string) (*app.Service, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	s, ok, err := cfg.Set(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, app.UnknownSet(cfg, name)
	}
	t, _, err := cfg.Target(s.Stages[0][0].Target)
	if err != nil {
		return nil, err
	}
	svc, _, err := r.connect(ctx, t.Server)
	return svc, err
}

// connectOrigin connects to the configured server whose origin a set run
// recorded; --server overrides it and ResumeSet checks the origin.
func (r *runtime) connectOrigin(ctx context.Context, origin string) (*app.Service, error) {
	cfg, err := r.config()
	if err != nil {
		return nil, err
	}
	if r.flags.server != "" {
		svc, _, err := r.connect(ctx, "")
		return svc, err
	}
	return r.connectMatching(cfg, origin)
}

func (r *runtime) connectMatching(cfg *config.Config, origin string) (*app.Service, error) {
	for _, s := range cfg.Servers() {
		if o, err := credential.Origin(s.URL); err == nil && o == origin {
			svc, _, err := r.connectServer(s)
			return svc, err
		}
	}
	return nil, errs.Configf("the set run ran on %s, which no configured server matches", origin).
		WithTry("bam server add <alias> --url " + origin)
}

// findSetRun resolves [set|id]: a record id, else this repository's newest
// record of that set, else its newest record of any set; match filters, and
// missing, when set, words the error when nothing matches.
func (r *runtime) findSetRun(args []string, match func(app.SetRecord) bool, missing func(arg string) error) (app.SetRecord, error) {
	cfg, err := r.config()
	if err != nil {
		return app.SetRecord{}, err
	}
	store := r.setStore()
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	if arg != "" {
		ids, err := store.IDs()
		if err != nil {
			return app.SetRecord{}, err
		}
		if slices.Contains(ids, arg) {
			return store.Load(arg)
		}
	}
	rec, ok, err := store.Latest(cfg.RepoRoot, arg, match)
	if err != nil {
		return app.SetRecord{}, err
	}
	if !ok {
		if missing != nil {
			return app.SetRecord{}, missing(arg)
		}
		what := "no set runs in this repository"
		if match != nil {
			what = "no set run to resume in this repository"
		}
		if arg != "" {
			what += fmt.Sprintf(" for %q", arg)
		}
		return app.SetRecord{}, errs.Usagef("%s", what).WithTry("bam set run <set>")
	}
	return rec, nil
}

// setLoop renders a set run until it ends. An interrupt or timeout stops
// watching only; the builds keep running and the run can be resumed.
func (r *runtime) setLoop(ctx context.Context, id string, p app.SetPlan, seed *app.SetRecord, events <-chan app.SetEvent) error {
	var rend view.SetRenderer
	var tick <-chan time.Time
	if r.flags.json {
		rend = view.NewSetNDJSON(view.Out{W: r.env.Stdout})
	} else {
		o, err := r.out()
		if err != nil {
			return err
		}
		if o.TTY {
			live := view.NewSetLive(o, p)
			if seed != nil {
				live.Seed(*seed)
			}
			rend = live
			t := time.NewTicker(time.Second)
			defer t.Stop()
			tick = t.C
		} else {
			rend = view.NewSetLines(o)
		}
	}
	for {
		select {
		case e, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					fmt.Fprintf(r.env.Stderr, "still running: bam set resume %s\n", id)
					return ctx.Err()
				}
				return nil
			}
			rend.Event(e)
			switch e.Type {
			case app.SetDone:
				drain(events)
				if e.State != provider.StateSuccess {
					return &resultError{State: e.State}
				}
				return nil
			case app.SetError:
				drain(events)
				return e.Err
			}
		case <-tick:
			rend.Tick()
		}
	}
}

// drain waits for the set's goroutine to finish, so its lock is released
// before the command returns.
func drain(events <-chan app.SetEvent) {
	for range events {
	}
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
				for name := range sets {
					out = append(out, name)
				}
			}
		}
		slices.Sort(out)
		if ids {
			if all, err := r.setStore().IDs(); err == nil {
				out = append(out, all...)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
