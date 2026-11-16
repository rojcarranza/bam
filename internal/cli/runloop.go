package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/rojcarranza/bam/internal/app"
	"github.com/rojcarranza/bam/internal/config"
	"github.com/rojcarranza/bam/internal/credential"
	"github.com/rojcarranza/bam/internal/errs"
	"github.com/rojcarranza/bam/internal/provider"
	"github.com/rojcarranza/bam/internal/view"
	"github.com/spf13/cobra"
)

// anyKeyRe matches project, plan, branch plan, build and job result keys.
var anyKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)*$`)

var jobKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

func init() { commandSets = append(commandSets, addRunLoop) }

func addRunLoop(root *cobra.Command, r *runtime) {
	build := subgroup(root, "build", "Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel", groupRun)
	build.AddCommand(newRunCmd(r), newWatchCmd(r), newLogsCmd(r), newInvestigateCmd(r), newCancelCmd(r))
	// bam why is bam investigate; each shortcut is a second instance of its
	// build subcommand: same flags, same output, same JSON.
	why := newInvestigateCmd(r)
	why.Aliases = []string{"why"}
	for _, c := range []*cobra.Command{newRunCmd(r), newWatchCmd(r), newLogsCmd(r), newBuildTreeCmd(r), why, newCancelCmd(r)} {
		c.GroupID = groupShortcut
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newOpenCmd(r), newURLCmd(r)} {
		c.GroupID = groupRun
		root.AddCommand(c)
	}
}

// revisionFromBuild is the --revision value that reuses the commit of the
// --from build instead of naming one.
const revisionFromBuild = "from"

// runFlags holds bam run's flags; its methods are the steps of the command.
type runFlags struct {
	vars, repositories     []string
	from, branch, revision string
	watch, dryRun, edit    bool
	timeout                time.Duration
}

// check validates the flags on their own, before any config or network.
func (f *runFlags) check(r *runtime, cmd *cobra.Command, arg string) error {
	// The editor inherits stdin and stdout; on a pipe it would hang or
	// write into the pipe.
	if f.edit && (!r.env.StdinTTY || !r.env.StdoutTTY) {
		return errs.Usagef("--edit needs a terminal").WithTry("pass the values with --var")
	}
	if cmd.Flags().Changed("revision") && f.revision == "" {
		return errs.Usagef("--revision needs a value")
	}
	if strings.EqualFold(f.revision, revisionFromBuild) {
		f.revision = revisionFromBuild
	}
	if f.revision == revisionFromBuild && f.from == "" {
		return errs.Usagef("--revision from needs --from").WithTry("bam run " + arg + " --from last --revision from")
	}
	return nil
}

// resolveFrom turns --from into a build key, once: --from last must name
// the same build for the variables and for the commit. It also sets the
// --revision from commit on ref.
func (f *runFlags) resolveFrom(ctx context.Context, svc *app.Service, ref *app.PlanRef) error {
	if key, ok := pageKey(f.from); ok {
		// The link names its server; reading another server's build of the same key would be wrong.
		if o, err := credential.Origin(f.from); err != nil || o != svc.Origin {
			return errs.Usagef("--from %s is on another server than %s", key, ref.PlanKey).
				WithTry("pass a build of " + svc.Origin)
		}
		f.from = key
	}
	var err error
	if f.from != "" {
		if f.from, err = svc.ResolveFrom(ctx, *ref, f.from); err != nil {
			return err
		}
	}
	if f.revision == revisionFromBuild {
		if ref.Revision, err = svc.BuildRevision(ctx, f.from); err != nil {
			return err
		}
		ref.RevisionFrom = f.from
	}
	return nil
}

// resolveVars resolves the variables to send, through $EDITOR with --edit.
func (f *runFlags) resolveVars(ctx context.Context, r *runtime, svc *app.Service, ref app.PlanRef) (app.VarSet, error) {
	if !f.edit {
		return svc.ResolveVars(ctx, ref, app.VarOptions{From: f.from, Flags: f.vars})
	}
	base, err := svc.VarBase(ctx, ref, f.from)
	if err != nil {
		return app.VarSet{}, err
	}
	return r.editVars(ctx, ref, base, f.vars, svc.Server.Alias, f.dryRun)
}

// follow reports whether to watch the build and for how long: the preset's
// watch and timeout apply unless the flags are given.
func (f *runFlags) follow(cmd *cobra.Command, t *config.ResolvedTarget) (bool, time.Duration) {
	watch, limit := f.watch, f.timeout
	if t == nil {
		return watch, limit
	}
	if !cmd.Flags().Changed("watch") && t.Watch != nil {
		watch = *t.Watch
	}
	if !cmd.Flags().Changed("timeout") {
		limit = time.Duration(t.Timeout)
	}
	return watch, limit
}

func newRunCmd(r *runtime) *cobra.Command {
	var rf runFlags
	cmd := &cobra.Command{
		Use:        "run <plan|preset>",
		Short:      "Trigger a plan or preset with variables",
		SuggestFor: []string{"trigger", "start"},
		Example: "  bam run deploy-qa --watch\n" +
			"  bam run PROJ-BUILD --branch develop --var cluster_type=k8s\n" +
			"  bam run deploy-qa --from last --dry-run\n" +
			"  bam run PROJ-BUILD --from 482 --revision from\n" +
			"  bam run deploy-qa --repository repo-a=feature/PROJ-101",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if !rf.dryRun {
				if err := r.refuseWrite("run a plan"); err != nil {
					return err
				}
			}
			if err := rf.check(r, cmd, args[0]); err != nil {
				return err
			}
			if _, err := app.RepositoryWants(rf.repositories, nil); err != nil {
				return err
			}
			ctx := cmd.Context()
			svc, arg, err := r.resolvePlanArg(ctx, args[0])
			if err != nil {
				return err
			}
			ref, err := svc.ResolvePlanToRun(ctx, arg, rf.branch)
			if err != nil {
				return err
			}
			ref.Revision = rf.revision
			if err := rf.resolveFrom(ctx, svc, &ref); err != nil {
				return err
			}
			vs, err := rf.resolveVars(ctx, r, svc, ref)
			if err != nil {
				return err
			}
			if ref.Repositories, err = app.RepositoryWants(rf.repositories, ref.Target); err != nil {
				return err
			}
			if ref.Repositories, err = svc.ExpandRepositories(ref.Repositories); err != nil {
				return err
			}
			check, err := svc.CheckRepositories(ctx, ref, ref.Repositories)
			if err != nil {
				return err
			}
			if rf.dryRun {
				if w := svc.DisabledWarning(ctx, ref.PlanKey); w != "" {
					vs.Warnings = append(vs.Warnings, w)
				}
			}
			view.PrintWarnings(r.env.Stderr, vs.Warnings)
			if vs.FromBuild != "" {
				r.note("variables from "+vs.FromBuild, "")
			}
			doWatch, limit := rf.follow(cmd, ref.Target)
			var o view.Out
			if !r.flags.json {
				if o, err = r.out(); err != nil {
					return err
				}
				view.RunPlan(o, ref, vs)
				view.RepoCheck(o, check)
			}
			if rf.dryRun {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, view.RunJSON(provider.Build{}, ref, vs))
				}
				r.note("dry run: nothing triggered", "")
				return nil
			}
			if err := r.switchRepositories(ctx, check); err != nil {
				return err
			}
			b, err := svc.Run(ctx, ref, vs)
			if err != nil {
				return err
			}
			switch {
			case !doWatch && r.flags.json:
				return view.WriteJSON(r.env.Stdout, view.RunJSON(b, ref, vs))
			case !doWatch:
				view.Queued(o, b, false, ref.RevisionShort())
				return nil
			case r.flags.json:
				return r.watchLoop(ctx, svc, b.Key, limit)
			}
			view.Queued(o, b, true, ref.RevisionShort())
			err = r.treeLoop(ctx, svc, b.Key, app.TreeOptions{}, false, limit, "bam watch "+b.Key, ref.RevisionShort())
			r.confirmRepositories(ctx, svc, b.Key, ref.Repositories)
			return err
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&rf.vars, "var", nil, "variable name=value (repeatable)")
	f.StringVar(&rf.from, "from", "", "reuse variables of a build: number, key or last")
	f.StringVarP(&rf.branch, "branch", "b", "", `plan branch to run; @ is the branch checked out here`)
	f.StringArrayVar(&rf.repositories, "repository", nil, "branch NAME=BRANCH a repository of the plan branch must build for this run, over the preset's repositories; BRANCH @ is the branch checked out here (repeatable)")
	f.StringVar(&rf.revision, "revision", "", `commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository`)
	f.BoolVarP(&rf.watch, "watch", "w", false, "follow the build and the builds it triggers until they finish")
	f.DurationVar(&rf.timeout, "timeout", 0, "stop watching after this long (the build keeps running)")
	f.BoolVar(&rf.dryRun, "dry-run", false, "resolve and validate variables, trigger nothing")
	addNotifyFlag(cmd, r)
	f.BoolVar(&rf.edit, "edit", false, "review and change the variables in $EDITOR before running")
	_ = cmd.RegisterFlagCompletionFunc("var", r.completeVar)
	_ = cmd.RegisterFlagCompletionFunc("revision", r.completeRevision)
	return cmd
}

// watchLoop writes a build's events as NDJSON until it finishes. Interrupts
// and timeouts stop watching only; the build keeps running.
func (r *runtime) watchLoop(ctx context.Context, svc *app.Service, key string, limit time.Duration) error {
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		wctx, cancel = context.WithTimeout(ctx, limit)
	}
	defer cancel()

	rend := view.NewNDJSON(view.Out{W: r.env.Stdout})
	for e := range svc.Watch(wctx, key) {
		rend.Event(e)
		switch e.Type {
		case app.EventDone:
			r.notifyDone(e.Build.Key, e.Build.State)
			if e.Build.State != provider.StateSuccess {
				return &resultError{State: e.Build.State}
			}
			return nil
		case app.EventError:
			return e.Err
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintf(r.env.Stderr, "still running: bam watch %s\n", key)
		return ctx.Err()
	}
	if wctx.Err() == nil {
		return nil
	}
	return errs.New(errs.KindTimeout, fmt.Sprintf("timed out after %s", limit)).
		WithWhy(key + " is still running").WithTry("bam watch " + key)
}

// followBuild watches key as bam watch does: the build tree in text mode,
// NDJSON events with --json. A preset argument's timeout applies unless
// --timeout is given.
func (r *runtime) followBuild(cmd *cobra.Command, args []string, svc *app.Service, key string, timeout time.Duration) error {
	limit := timeout
	if len(args) == 1 && !cmd.Flags().Changed("timeout") {
		if t, ok, _ := r.cfg.Target(app.PresetName(r.cfg, args[0])); ok {
			limit = time.Duration(t.Timeout)
		}
	}
	if r.flags.json {
		return r.watchLoop(cmd.Context(), svc, key, limit)
	}
	return r.treeLoop(cmd.Context(), svc, key, app.TreeOptions{}, false, limit, "bam watch "+key, "")
}

// watchCommit is bam watch @: wait for the build of the checked-out commit,
// then follow it.
func (r *runtime) watchCommit(cmd *cobra.Command, arg string, last bool, branch string, timeout time.Duration) error {
	ctx := cmd.Context()
	if last {
		return errs.Usagef("--last and %s both name a build", arg).WithTry("drop --last")
	}
	var svc *app.Service
	var err error
	if arg == app.CheckedOutMarker {
		svc, _, err = r.connectLast(ctx)
	} else {
		svc, _, err = r.connectFor(ctx, arg)
	}
	if err != nil {
		return err
	}
	plan, err := svc.CommitPlan(ctx, arg, branch)
	if err != nil {
		return err
	}
	key, err := svc.BuildOfCommit(ctx, plan, timeout, func(s string) { fmt.Fprintln(r.env.Stderr, s) })
	if err != nil {
		return err
	}
	var preset []string
	if arg != app.CheckedOutMarker {
		preset = []string{arg}
	}
	return r.followBuild(cmd, preset, svc, key, timeout)
}

func newWatchCmd(r *runtime) *cobra.Command {
	var last bool
	var branch string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:        "watch [<build>|<plan>|<preset>]",
		SuggestFor: []string{"wait", "tail", "follow"},
		Short:      "Follow a build and the builds it triggers; exit 1 if any fails",
		Long: `Follow a build and the builds it triggers; exit 1 if any fails.

bam draws the build tree, as "bam build tree <build> --watch" does, until
every build in it has finished, then lists each build that did not succeed
with its likely cause (up to five builds).
Interrupting or timing out never stops a build. --json follows the one
build and writes its events as NDJSON.

bam watch @ follows the build of the commit you have checked out: it waits
(up to --timeout, 10 minutes by default) until Bamboo starts one, then
follows it. The plan is the one you last ran in this repository; bam watch
<plan|preset> --branch @ names another. A commit that is not pushed has no
build, so bam says so and follows the newest build of the branch.`,
		Example: "  bam watch PROJ-BUILD-482\n" +
			"  bam watch --last\n" +
			"  bam watch deploy-qa --branch develop\n" +
			"  bam watch PROJ-BUILD-482 --timeout 30m\n" +
			"  git push && bam watch @",
		Args:              buildArgs,
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1 && args[0] == app.CheckedOutMarker) || (branch == app.CheckedOutMarker && len(args) == 1) {
				return r.watchCommit(cmd, args[0], last, branch, timeout)
			}
			svc, key, err := r.resolveBuildArg(cmd.Context(), args, last, branch)
			if err != nil {
				return err
			}
			return r.followBuild(cmd, args, svc, key, timeout)
		}),
	}
	addBuildArgFlags(cmd, &last, &branch)
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "stop watching after this long (the build keeps running)")
	addNotifyFlag(cmd, r)
	return cmd
}

type jobLogDoc struct {
	Job   string   `json:"job"`
	Name  string   `json:"name"`
	Stage string   `json:"stage"`
	State string   `json:"state"`
	Lines []string `json:"lines"`
}

// logsFlags holds the flags of bam logs.
type logsFlags struct {
	last, failed, follow bool
	branch, job          string
	tail                 int
}

func newLogsCmd(r *runtime) *cobra.Command {
	var f logsFlags
	cmd := &cobra.Command{
		Use:   "logs [<build>|<plan>|<preset>]",
		Short: "Print job logs; --failed for failed jobs only",
		Example: "  bam logs PROJ-BUILD-482 --failed\n" +
			"  bam logs --last --failed -n 50\n" +
			"  bam logs PROJ-BUILD-482 --job JOB1 --follow\n" +
			"  bam logs PROJ-BUILD-JOB1-482",
		Args:              buildArgs,
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			return f.run(cmd.Context(), r, args)
		}),
	}
	fl := cmd.Flags()
	addBuildArgFlags(cmd, &f.last, &f.branch)
	fl.BoolVar(&f.failed, "failed", false, "only failed jobs")
	fl.StringVar(&f.job, "job", "", "one job: JOB1, PROJ-PLAN-JOB1 or its result key")
	fl.BoolVar(&f.follow, "follow", false, "keep printing new lines until the job finishes")
	fl.IntVarP(&f.tail, "tail", "n", 0, "last N lines per job; 0 prints the whole log")
	return cmd
}

func (f *logsFlags) run(ctx context.Context, r *runtime, args []string) error {
	if f.tail < 0 {
		return errs.Usagef("--tail takes 0 or more, not %d", f.tail).WithTry("--tail 0 prints the whole log")
	}
	args = f.jobResult(args)
	svc, key, err := r.resolveBuildArg(ctx, args, f.last, f.branch)
	if err != nil {
		return err
	}
	b, err := svc.Build(ctx, key)
	if err != nil {
		return err
	}
	// A plan, preset or --last picks the build; name it so the log is not anonymous.
	if !r.flags.json && !namesBuild(args) {
		r.note(fmt.Sprintf("%s (%s)", key, b.State), "")
	}
	if f.follow {
		return r.followLogs(ctx, svc, b, f.job, f.tail)
	}
	logs, err := svc.Logs(ctx, b, app.LogsOptions{Failed: f.failed, Job: f.job, Tail: f.tail})
	if err != nil {
		return err
	}
	if f.failed && len(logs) == 0 {
		return noFailedJobs(r, key, b)
	}
	return printLogs(r, logs)
}

// jobResult handles a job result key copied from Bamboo's job page, which
// names both the build and the job: it sets --job and returns the build.
func (f *logsFlags) jobResult(args []string) []string {
	if len(args) != 1 || f.job != "" {
		return args
	}
	k, link := args[0], ""
	if pk, ok := pageKey(k); ok {
		k = pk
		link, _ = credential.Origin(args[0])
	}
	build, _, ok := app.SplitJobResult(k)
	if !ok {
		return args
	}
	f.job = k
	if link != "" {
		return []string{link + "/browse/" + build}
	}
	return []string{build}
}

// namesBuild reports whether the argument is a build key or a build page link.
func namesBuild(args []string) bool {
	if len(args) != 1 {
		return false
	}
	k := args[0]
	if pk, ok := pageKey(k); ok {
		k = pk
	}
	return app.IsBuildKey(k)
}

func noFailedJobs(r *runtime, key string, b provider.Build) error {
	switch {
	case r.flags.json:
		return view.WriteJSON(r.env.Stdout, []jobLogDoc{})
	case !b.State.Finished():
		r.note(fmt.Sprintf("no failed jobs in %s yet; it is %s", key, b.State), "bam watch "+key)
	default:
		r.note("no failed jobs in "+key, "")
	}
	return nil
}

// printLogs writes the logs as JSON, or as text through the pager on a terminal.
func printLogs(r *runtime, logs []app.JobLog) error {
	if r.flags.json {
		return view.WriteJSON(r.env.Stdout, view.Docs(logs, func(l app.JobLog) jobLogDoc {
			return jobLogDoc{Job: l.Job.Key, Name: l.Job.Name, Stage: l.Stage, State: string(l.Job.State), Lines: append([]string{}, l.Lines...)}
		}))
	}
	var buf bytes.Buffer
	if err := view.PrintLogs(&buf, logs); err != nil {
		return err
	}
	if r.env.StdoutTTY {
		if pager := view.PagerCommand(r.env.Getenv, r.cfg.Machine.Pager, r.env.GOOS); pager != "" {
			return r.env.RunPager(pager, &buf)
		}
	}
	_, err := io.Copy(r.env.Stdout, &buf)
	return err
}

func newInvestigateCmd(r *runtime) *cobra.Command {
	var last bool
	var branch string
	var compare bool
	var opts app.InvestigateOptions
	cmd := &cobra.Command{
		Use:               "investigate [<build>|<plan>|<preset>]",
		Short:             "Find the error lines of a failed build and its likely cause",
		Example:           "  bam build investigate PROJ-BUILD-482\n  bam build investigate --last --compare\n  bam build investigate provision --context 0 --max 0",
		SuggestFor:        []string{"test", "tests", "failures", "errors", "diagnose", "debug", "why", "compare"},
		Args:              buildArgs,
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if opts.Context < 0 {
				return errs.Usagef("--context takes 0 or more, not %d", opts.Context).WithTry("--context 0 shows only the error lines")
			}
			if opts.Max < 0 {
				return errs.Usagef("--max takes 0 or more, not %d", opts.Max).WithTry("--max 0 shows all error blocks")
			}
			ctx := cmd.Context()
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			b, err := svc.Build(ctx, key)
			if err != nil {
				return err
			}
			if !r.flags.json {
				// A build whose child failed is not itself failed: investigate the tree.
				if more := r.failedInTree(ctx, svc, b); len(more) > 0 && (b.State != provider.StateFailed || len(more) > 1 || more[0].Key != b.Key) {
					return r.investigateAll(ctx, svc, more, opts, compare)
				}
			}
			if b.State != provider.StateFailed && !r.flags.json {
				if b.State.Finished() {
					r.note(fmt.Sprintf("%s did not fail (%s), nothing to investigate", key, b.State), "")
					return nil
				}
				r.note(fmt.Sprintf("%s has not finished (%s)", key, b.State), "bam watch "+key)
				return nil
			}
			inv, err := svc.Investigate(ctx, b, opts)
			if err != nil {
				return err
			}
			if compare && b.State == provider.StateFailed {
				c, err := svc.CompareLastSuccess(ctx, b)
				if err != nil {
					return err
				}
				inv.Changes = &c
			}
			return r.show(view.InvestigationJSON(inv), func(o view.Out) error { return view.Investigation(o, inv) })
		}),
	}
	f := cmd.Flags()
	addBuildArgFlags(cmd, &last, &branch)
	f.IntVar(&opts.Context, "context", 3, "lines of context around each error")
	f.IntVar(&opts.Max, "max", 5, "error blocks per job; 0 for all")
	f.BoolVar(&compare, "compare", false, "show revisions and variables changed since the last successful build")
	return cmd
}

// failedInTree is every failed build with a failed job in b's tree, in tree
// order; empty when the tree cannot be read.
func (r *runtime) failedInTree(ctx context.Context, svc *app.Service, b provider.Build) []provider.Build {
	t, err := r.loadTree(ctx, svc, b.Key, app.TreeOptions{})
	if err != nil || t.Root == nil {
		return nil
	}
	var out []provider.Build
	t.Root.Walk(func(n *app.TreeNode, _ int) {
		if !n.NotRun && !n.Loop && !n.Superseded && n.Build.State == provider.StateFailed && len(n.Build.FailedJobs()) > 0 {
			out = append(out, n.Build)
		}
	})
	return out
}

// investigateAll prints the investigation of each build, one after another.
func (r *runtime) investigateAll(ctx context.Context, svc *app.Service, builds []provider.Build, opts app.InvestigateOptions, compare bool) error {
	for i, b := range builds {
		inv, err := svc.Investigate(ctx, b, opts)
		if err != nil {
			return err
		}
		if compare {
			c, err := svc.CompareLastSuccess(ctx, b)
			if err != nil {
				return err
			}
			inv.Changes = &c
		}
		if i > 0 {
			fmt.Fprintln(r.env.Stdout)
		}
		if err := r.show(view.InvestigationJSON(inv), func(o view.Out) error { return view.Investigation(o, inv) }); err != nil {
			return err
		}
	}
	return nil
}

func (r *runtime) followLogs(ctx context.Context, svc *app.Service, b provider.Build, job string, tail int) error {
	if job == "" {
		var all []provider.Job
		for _, s := range b.Stages {
			all = append(all, s.Jobs...)
		}
		if len(all) != 1 {
			names := make([]string, 0, len(all))
			for _, j := range all {
				names = append(names, app.ShortJobKey(b, j))
			}
			return errs.Usagef("--follow needs --job when a build has %d jobs", len(all)).WithWhy("jobs: " + strings.Join(names, ", "))
		}
		job = all[0].Key
	}
	logs, err := svc.Logs(ctx, b, app.LogsOptions{Job: job, Tail: tail})
	if err != nil {
		return err
	}
	emit := func(lines []string) {
		for _, l := range lines {
			fmt.Fprintln(r.env.Stdout, l)
		}
	}
	emit(logs[0].Lines)
	return svc.FollowLog(ctx, b.Key, logs[0].Job, logs[0].Next, emit)
}

func newCancelCmd(r *runtime) *cobra.Command {
	var last, dryRun bool
	var branch string
	cmd := &cobra.Command{
		Use:        "cancel [<build>|<plan>|<preset>]",
		Short:      "Stop a queued or running build",
		SuggestFor: []string{"stop", "kill", "abort"},
		Long: "Stop a queued or running build.\n\n" +
			"Bamboo stops a build by removing its unfinished jobs from the queue, so a\n" +
			"stage that has not started yet can still be queued afterwards. Check the\n" +
			"result with bam build show.",
		Example: "  bam build cancel PROJ-BUILD-482\n" +
			"  bam build cancel --last --dry-run\n" +
			"  bam build cancel deploy-qa --branch develop",
		Args:              buildArgs,
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if !dryRun {
				if err := r.refuseWrite("stop a build"); err != nil {
					return err
				}
			}
			svc, key, err := r.resolveNamedBuild(cmd.Context(), args, last, branch)
			if err != nil {
				return err
			}
			var b provider.Build
			var done bool
			if dryRun {
				b, err = svc.Build(cmd.Context(), key)
				done = b.State.Finished()
				if err == nil && !done && svc.RefusesStop(cmd.Context()) {
					err = errs.Bamboof("this Bamboo cannot stop a build over REST").
						WithTry("bam open " + key + ", then stop it from Bamboo's web UI")
				}
			} else {
				b, done, err = svc.Cancel(cmd.Context(), key)
			}
			if err != nil {
				return err
			}
			if done {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, map[string]any{"key": key, "url": svc.P.URL(key), "stopped": false, "state": string(b.State)})
				}
				r.note(fmt.Sprintf("%s already finished (%s)", key, b.State), "")
				return nil
			}
			if dryRun {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, map[string]any{"key": key, "url": svc.P.URL(key), "state": string(b.State), "stopped": false})
				}
				fmt.Fprintf(r.env.Stdout, "would stop %s (%s)\n", key, strings.ReplaceAll(string(b.State), "_", " "))
				r.note("dry run: nothing stopped", "")
				return nil
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, map[string]any{"key": key, "url": svc.P.URL(key), "stopped": true})
			}
			fmt.Fprintf(r.env.Stdout, "stopping %s\n  watch: bam watch %s\n", key, key)
			return nil
		}),
	}
	addBuildArgFlags(cmd, &last, &branch)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show which build would stop, stop nothing")
	return cmd
}

// urlFor builds a Bamboo browse URL, with no network call unless a preset
// names a branch; job, when set, turns a build key into that job's result key.
func (r *runtime) urlFor(ctx context.Context, args []string, last bool, job string) (string, error) {
	base, key, err := r.browseKey(ctx, args, last)
	if err != nil {
		return "", err
	}
	if job != "" {
		if !jobKeyRe.MatchString(job) {
			return "", errs.Usagef("--job takes a short job key such as JOB1, not %q", job)
		}
		job = strings.ToUpper(job)
		switch {
		case app.IsBuildKey(key):
			i := strings.LastIndex(key, "-")
			key = key[:i] + "-" + job + key[i:]
		case app.IsPlanKey(key):
			key += "-" + job
		default:
			return "", errs.Usagef("--job needs a plan or build, not %s", key).WithTry("a build key such as PROJ-PLAN-12")
		}
	}
	return base + "/browse/" + key, nil
}

// browseKey returns the server URL and the key that urlFor browses.
func (r *runtime) browseKey(ctx context.Context, args []string, last bool) (string, string, error) {
	if len(args) == 0 && !last {
		last = r.hasLastBuild()
	}
	if err := lastOrArg(args, last); err != nil {
		return "", "", err
	}
	cfg, err := r.config()
	if err != nil {
		return "", "", err
	}
	var key, targetServer, branch string
	if len(args) > 0 {
		args = []string{app.PresetName(cfg, args[0])}
	}
	switch {
	case last:
		rec, ok, err := (&app.StateStore{Path: r.env.Paths.State}).Last(cfg.RepoRoot)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", errs.Usagef("no build triggered from this repository yet").WithTry("bam run <preset>")
		}
		if s, ok := serverForOrigin(cfg, rec.Origin); ok {
			return strings.TrimRight(s.URL, "/"), rec.BuildKey, nil
		}
		return rec.Origin, rec.BuildKey, nil
	case len(args) == 0:
		return "", "", errs.Usagef("which Bamboo page?").WithTry("pass a key such as PROJ-PLAN, a preset, or --last")
	case config.ValidTargetName(args[0]):
		t, ok, err := cfg.Target(args[0])
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", app.UnknownTarget(cfg, args[0])
		}
		key, targetServer, branch = t.Plan, t.Server, t.Branch
	case anyKeyRe.MatchString(args[0]):
		key = args[0]
		if app.IsBuildKey(key) {
			var err error
			if key, err = app.CanonicalBuildKey(key); err != nil {
				return "", "", err
			}
		}
	default:
		return "", "", errs.Usagef("%q is not a Bamboo key or a preset", args[0])
	}
	server, err := cfg.SelectServer(r.flags.server, targetServer)
	if err != nil {
		return "", "", err
	}
	if branch != "" {
		svc, _, err := r.connectServer(server)
		if err != nil {
			return "", "", err
		}
		if key, err = svc.ResolveBranch(ctx, key, branch); err != nil {
			return "", "", err
		}
	}
	return strings.TrimRight(server.URL, "/"), key, nil
}

func newOpenCmd(r *runtime) *cobra.Command {
	var last bool
	var job string
	cmd := &cobra.Command{
		Use:               "open [<key>|<preset>]",
		SuggestFor:        []string{"web", "browse"},
		Short:             "Open a project, plan, build or job in the browser",
		Example:           "  bam open PROJ-BUILD\n  bam open PROJ-BUILD-482 --job JOB1\n  bam open --last\n  bam open provision",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			url, err := r.urlFor(cmd.Context(), args, last, job)
			if err != nil {
				return err
			}
			if err := r.env.OpenBrowser(url); err != nil {
				fmt.Fprintln(r.env.Stdout, url)
				view.PrintWarnings(r.env.Stderr, []string{"could not open a browser: " + err.Error()})
				return nil
			}
			fmt.Fprintf(r.env.Stderr, "opening %s\n", url)
			return nil
		}),
	}
	addLastFlag(cmd, &last)
	cmd.Flags().StringVar(&job, "job", "", "a job of the plan or build, by short key (JOB1)")
	return cmd
}

func newURLCmd(r *runtime) *cobra.Command {
	var last bool
	var job string
	cmd := &cobra.Command{
		Use:               "url [<key>|<preset>]",
		Short:             "Print the Bamboo URL of a project, plan, build or job",
		Example:           "  bam url PROJ\n  bam url PROJ-BUILD-482\n  bam url --last --job JOB1\n  bam url provision --json",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			url, err := r.urlFor(cmd.Context(), args, last, job)
			if err != nil {
				return err
			}
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, map[string]string{"url": url})
			}
			_, err = fmt.Fprintln(r.env.Stdout, url)
			return err
		}),
	}
	addLastFlag(cmd, &last)
	cmd.Flags().StringVar(&job, "job", "", "a job of the plan or build, by short key (JOB1)")
	return cmd
}
