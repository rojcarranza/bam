package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/view"
	"github.com/spf13/cobra"
)

// anyKeyRe matches project, plan, branch plan, build and job result keys.
var anyKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)*$`)

var jobKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

func init() { commandSets = append(commandSets, addRunLoop) }

func addRunLoop(root *cobra.Command, r *runtime) {
	build := subgroup(root, "build", "Builds: list, show, tree, run, rerun, watch, logs, investigate, cancel", groupRun)
	build.AddCommand(newRunCmd(r), newWatchCmd(r), newLogsCmd(r), newInvestigateCmd(r), newCancelCmd(r))
	for _, c := range []*cobra.Command{newRunCmd(r), newWatchCmd(r), newLogsCmd(r)} {
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

func newRunCmd(r *runtime) *cobra.Command {
	var vars []string
	var from, branch, revision string
	var watch, dryRun, edit bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:               "run <plan|preset>",
		Short:             "Trigger a plan or preset with variables",
		SuggestFor:        []string{"trigger", "start"},
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			// The editor inherits stdin and stdout; on a pipe it would hang
			// or write into the pipe. Checked before any config or network.
			if edit && (!r.env.StdinTTY || !r.env.StdoutTTY) {
				return errs.Usagef("--edit needs a terminal").WithTry("pass the values with --var")
			}
			// Checked before any config or network, like --edit's terminal.
			if cmd.Flags().Changed("revision") && revision == "" {
				return errs.Usagef("--revision needs a value")
			}
			if strings.EqualFold(revision, revisionFromBuild) {
				revision = revisionFromBuild
			}
			if revision == revisionFromBuild && from == "" {
				return errs.Usagef("--revision from needs --from").WithTry("bam run " + args[0] + " --from last --revision from")
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
			ref.Revision = revision
			// Resolved once: --from last must name the same build for the
			// variables and for the commit.
			if from != "" {
				if from, err = svc.ResolveFrom(ctx, ref, from); err != nil {
					return err
				}
			}
			if revision == revisionFromBuild {
				if ref.Revision, err = svc.BuildRevision(ctx, from); err != nil {
					return err
				}
				ref.RevisionFrom = from
			}
			var vs app.VarSet
			if edit {
				base, err := svc.VarBase(ctx, ref, from)
				if err != nil {
					return err
				}
				vs, err = r.editVars(ctx, ref, base, vars, svc.Server.Alias, dryRun)
				if err != nil {
					return err
				}
			} else if vs, err = svc.ResolveVars(ctx, ref, app.VarOptions{From: from, Flags: vars}); err != nil {
				return err
			}
			view.PrintWarnings(r.env.Stderr, vs.Warnings)
			if vs.FromBuild != "" {
				r.note("variables from "+vs.FromBuild, "")
			}
			doWatch, limit := watch, timeout
			if t := ref.Target; t != nil {
				if !cmd.Flags().Changed("watch") && t.Watch != nil {
					doWatch = *t.Watch
				}
				if !cmd.Flags().Changed("timeout") {
					limit = time.Duration(t.Timeout)
				}
			}
			var o view.Out
			if !r.flags.json {
				if o, err = r.out(); err != nil {
					return err
				}
				view.RunPlan(o, ref, vs)
			}
			if dryRun {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, view.RunJSON(provider.Build{}, ref, vs))
				}
				r.note("dry run: nothing triggered", "")
				return nil
			}
			b, err := svc.Run(ctx, ref, vs)
			if err != nil {
				return err
			}
			if !doWatch {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, view.RunJSON(b, ref, vs))
				}
				view.Queued(o, b, false, ref.RevisionShort())
				return nil
			}
			if r.flags.json {
				return r.watchLoop(ctx, svc, b.Key, limit, "")
			}
			view.Queued(o, b, true, ref.RevisionShort())
			return r.treeLoop(ctx, svc, b.Key, app.TreeOptions{}, false, limit, "bam watch "+b.Key, ref.RevisionShort())
		}),
	}
	f := cmd.Flags()
	f.StringArrayVar(&vars, "var", nil, "variable name=value (repeatable)")
	f.StringVar(&from, "from", "", "reuse variables of a build: number, key or last")
	f.StringVar(&branch, "branch", "", "plan branch to run")
	f.StringVar(&revision, "revision", "", `commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository`)
	f.BoolVar(&watch, "watch", false, "follow the build and the builds it triggers until they finish")
	f.DurationVar(&timeout, "timeout", 0, "stop watching after this long (the build keeps running)")
	f.BoolVar(&dryRun, "dry-run", false, "resolve and validate variables, trigger nothing")
	f.BoolVar(&edit, "edit", false, "review and change the variables in $EDITOR before running")
	_ = cmd.RegisterFlagCompletionFunc("var", r.completeVar)
	_ = cmd.RegisterFlagCompletionFunc("revision", r.completeRevision)
	return cmd
}

// watchLoop renders a build until it finishes. Interrupts and timeouts stop
// watching only; the build keeps running. revision, when set, is the
// revision the build was asked for; a not-built result then says Bamboo
// could not build it.
func (r *runtime) watchLoop(ctx context.Context, svc *app.Service, key string, limit time.Duration, revision string) error {
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if limit > 0 {
		wctx, cancel = context.WithTimeout(ctx, limit)
	}
	defer cancel()

	var rend view.WatchRenderer
	var tick <-chan time.Time
	if r.flags.json {
		rend = view.NewNDJSON(view.Out{W: r.env.Stdout})
	} else {
		o, err := r.out()
		if err != nil {
			return err
		}
		if o.TTY {
			rend = view.NewLive(o)
			t := time.NewTicker(time.Second)
			defer t.Stop()
			tick = t.C
		} else {
			rend = view.NewLines(o)
		}
	}

	events := svc.Watch(wctx, key)
	for {
		select {
		case e, ok := <-events:
			if !ok {
				switch {
				case ctx.Err() != nil:
					fmt.Fprintf(r.env.Stderr, "still running: bam watch %s\n", key)
					return ctx.Err()
				case wctx.Err() != nil:
					return errs.New(errs.KindTimeout, fmt.Sprintf("timed out after %s", limit)).
						WithWhy(key + " is still running").WithTry("bam watch " + key)
				}
				return nil
			}
			rend.Event(e)
			switch e.Type {
			case app.EventDone:
				if e.Build.State == provider.StateNotBuilt && revision != "" && !r.flags.json {
					if o, err := r.out(); err == nil {
						view.RevisionNotBuilt(o, revision)
					}
				}
				if e.Build.State != provider.StateSuccess {
					return &resultError{State: e.Build.State}
				}
				return nil
			case app.EventError:
				return e.Err
			}
		case <-tick:
			rend.Tick()
		}
	}
}

// followBuild watches key as bam watch does: the build tree in text mode,
// NDJSON events with --json. A preset argument's timeout applies unless
// --timeout is given.
func (r *runtime) followBuild(cmd *cobra.Command, args []string, svc *app.Service, key string, timeout time.Duration) error {
	limit := timeout
	if len(args) == 1 && !cmd.Flags().Changed("timeout") && config.ValidTargetName(args[0]) {
		if t, ok, _ := r.cfg.Target(args[0]); ok {
			limit = time.Duration(t.Timeout)
		}
	}
	if r.flags.json {
		return r.watchLoop(cmd.Context(), svc, key, limit, "")
	}
	return r.treeLoop(cmd.Context(), svc, key, app.TreeOptions{}, false, limit, "bam watch "+key, "")
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
every build in it has finished, then lists each build that did not succeed.
Interrupting or timing out never stops a build. --json follows the one
build and writes its events as NDJSON.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, key, err := r.resolveBuildArg(cmd.Context(), args, last, branch)
			if err != nil {
				return err
			}
			return r.followBuild(cmd, args, svc, key, timeout)
		}),
	}
	cmd.Flags().BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "stop watching after this long (the build keeps running)")
	return cmd
}

type jobLogDoc struct {
	Job   string   `json:"job"`
	Name  string   `json:"name"`
	Stage string   `json:"stage"`
	State string   `json:"state"`
	Lines []string `json:"lines"`
}

func newLogsCmd(r *runtime) *cobra.Command {
	var last, failed, follow bool
	var branch, job string
	var tail int
	cmd := &cobra.Command{
		Use:               "logs [<build>|<plan>|<preset>]",
		Short:             "Print job logs; --failed for failed jobs only",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				return errs.Usagef("--tail takes 0 or more, not %d", tail)
			}
			ctx := cmd.Context()
			// A job result key copied from Bamboo's job page names the build and the job.
			if len(args) == 1 && job == "" {
				k := args[0]
				if pk, ok := pageKey(k); ok {
					k = pk
				}
				if build, _, ok := app.SplitJobResult(k); ok {
					job, args = k, []string{strings.Replace(args[0], k, build, 1)}
				}
			}
			svc, key, err := r.resolveBuildArg(ctx, args, last, branch)
			if err != nil {
				return err
			}
			b, err := svc.Build(ctx, key)
			if err != nil {
				return err
			}
			if follow {
				return r.followLogs(ctx, svc, b, job, tail)
			}
			logs, err := svc.Logs(ctx, b, app.LogsOptions{Failed: failed, Job: job, Tail: tail})
			if err != nil {
				return err
			}
			if failed && len(logs) == 0 {
				if r.flags.json {
					return view.WriteJSON(r.env.Stdout, []jobLogDoc{})
				}
				r.note("no failed jobs in "+key, "")
				return nil
			}
			if r.flags.json {
				docs := []jobLogDoc{}
				for _, l := range logs {
					docs = append(docs, jobLogDoc{Job: l.Job.Key, Name: l.Job.Name, Stage: l.Stage, State: string(l.Job.State), Lines: append([]string{}, l.Lines...)})
				}
				return view.WriteJSON(r.env.Stdout, docs)
			}
			var buf bytes.Buffer
			if err := view.PrintLogs(&buf, logs); err != nil {
				return err
			}
			if r.env.StdoutTTY {
				pager := view.PagerCommand(r.env.Getenv, r.cfg.Machine.Pager, r.env.GOOS)
				if pager != "" {
					return r.env.RunPager(pager, &buf)
				}
			}
			_, err = io.Copy(r.env.Stdout, &buf)
			return err
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	f.BoolVar(&failed, "failed", false, "only failed jobs")
	f.StringVar(&job, "job", "", "one job: JOB1, PROJ-PLAN-JOB1 or its result key")
	f.BoolVar(&follow, "follow", false, "keep printing new lines until the job finishes")
	f.IntVarP(&tail, "tail", "n", 0, "last N lines per job; 0 prints the whole log")
	return cmd
}

func newInvestigateCmd(r *runtime) *cobra.Command {
	var last bool
	var branch string
	var compare bool
	var opts app.InvestigateOptions
	cmd := &cobra.Command{
		Use:               "investigate [<build>|<plan>|<preset>]",
		Short:             "Find the error lines of a failed build and its likely cause",
		SuggestFor:        []string{"test", "tests", "failures", "errors", "diagnose", "debug", "why", "compare"},
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			if opts.Context < 0 {
				return errs.Usagef("--context takes 0 or more, not %d", opts.Context)
			}
			if opts.Max < 0 {
				return errs.Usagef("--max takes 0 or more, not %d", opts.Max)
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
			if r.flags.json {
				return view.WriteJSON(r.env.Stdout, view.InvestigationJSON(inv))
			}
			o, err := r.out()
			if err != nil {
				return err
			}
			return view.Investigation(o, inv)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	f.StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	f.IntVar(&opts.Context, "context", 3, "lines of context around each error")
	f.IntVar(&opts.Max, "max", 5, "error blocks per job; 0 for all")
	f.BoolVar(&compare, "compare", false, "show revisions and variables changed since the last successful build")
	return cmd
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
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			svc, key, err := r.resolveBuildArg(cmd.Context(), args, last, branch)
			if err != nil {
				return err
			}
			var b provider.Build
			var done bool
			if dryRun {
				b, err = svc.Build(cmd.Context(), key)
				done = b.State.Finished()
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
	cmd.Flags().BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	cmd.Flags().StringVar(&branch, "branch", "", "plan branch when a plan or preset is given")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show which build would stop, stop nothing")
	return cmd
}

// urlFor builds a Bamboo browse URL without any network call; job, when
// set, turns a build key into that job's result key.
func (r *runtime) urlFor(args []string, last bool, job string) (string, error) {
	base, key, err := r.browseKey(args, last)
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
func (r *runtime) browseKey(args []string, last bool) (string, string, error) {
	cfg, err := r.config()
	if err != nil {
		return "", "", err
	}
	var key, targetServer string
	switch {
	case last:
		rec, ok, err := (&app.StateStore{Path: r.env.Paths.State}).Last(cfg.RepoRoot)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", errs.Usagef("no build triggered from this repository yet").WithTry("bam run <preset>")
		}
		for _, s := range cfg.Servers() {
			if o, err := credential.Origin(s.URL); err == nil && o == rec.Origin {
				return strings.TrimRight(s.URL, "/"), rec.BuildKey, nil
			}
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
		key, targetServer = t.Plan, t.Server
	case anyKeyRe.MatchString(args[0]):
		key = args[0]
	default:
		return "", "", errs.Usagef("%q is not a Bamboo key or a preset", args[0])
	}
	server, err := cfg.SelectServer(r.flags.server, targetServer)
	if err != nil {
		return "", "", err
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
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			url, err := r.urlFor(args, last, job)
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
	cmd.Flags().BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	cmd.Flags().StringVar(&job, "job", "", "a job of the plan or build, by short key (JOB1)")
	return cmd
}

func newURLCmd(r *runtime) *cobra.Command {
	var last bool
	var job string
	cmd := &cobra.Command{
		Use:               "url [<key>|<preset>]",
		Short:             "Print the Bamboo URL of a project, plan, build or job",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: r.completeTargets,
		RunE: r.wrap(func(cmd *cobra.Command, args []string) error {
			url, err := r.urlFor(args, last, job)
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
	cmd.Flags().BoolVar(&last, "last", false, "the last build bam triggered from this repository")
	cmd.Flags().StringVar(&job, "job", "", "a job of the plan or build, by short key (JOB1)")
	return cmd
}

// completeVar completes --var names from the target and values from its options.
func (r *runtime) completeVar(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	none := cobra.ShellCompDirectiveNoFileComp
	if len(args) == 0 {
		return nil, none
	}
	cfg, err := r.config()
	if err != nil {
		return nil, none
	}
	t, ok, err := cfg.Target(args[0])
	if err != nil || !ok {
		return nil, none
	}
	return targetVars(toComplete, t)
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
	sort.Strings(out)
	return out, none | cobra.ShellCompDirectiveNoSpace
}
