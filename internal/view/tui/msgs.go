package tui

import (
	"context"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/r0jjames/bam-cli/internal/app"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// buildsPerPlan is how many builds the Builds panel asks for.
const buildsPerPlan = 25

type connectedMsg struct {
	Gen   int
	Alias string
	Svc   *app.Service
	Info  provider.ServerInfo
	User  provider.User
}

// Every asynchronous load carries the generation of the request that started
// it. The model bumps a stream's generation whenever it issues a new request
// or abandons one, and drops any message from an older generation: otherwise
// a slow response for the server, project, branch or build the user has just
// left lands on the one they are now looking at.
type plansLoadedMsg struct {
	Gen   int
	Plans []provider.Plan
	// Failed is the projects whose plans could not be listed, and Err the
	// first such error. The model keeps those projects' previous rows, so one
	// failing project does not blank the others (home spec §4.1).
	Failed []string
	Err    error
	// Missing is the configured projects the server does not have.
	Missing []string
}

type buildsLoadedMsg struct {
	Gen     int
	PlanKey string
	Builds  []provider.Build
}

type presetsLoadedMsg struct {
	Gen     int
	Targets []app.TargetInfo
}

// stream names an asynchronous conversation with the server. Each has its own
// generation, so a message from one the user has left can be dropped.
type stream int

const (
	streamNone stream = iota
	streamConnect
	streamPlans
	streamBuilds
	streamLogs
	streamDetail
	streamPicker
	streamPresets
	streamSets
	streamRun
	streamCancel
	streamProjects
)

// errMsg is a failure to show, never a failure to exit on. Where names the
// panel so the status line can say what did not load; Stream and Gen say
// which request it belongs to, so a failure from a request the user has
// abandoned cannot replace the status of the one they are waiting on.
type errMsg struct {
	Err    error
	Where  string
	Stream stream
	Gen    int
}

func connectCmd(ctx context.Context, d Deps, alias string, gen int) tea.Cmd {
	return func() tea.Msg {
		svc, err := d.Connect(ctx, alias)
		if err != nil {
			return errMsg{Err: err, Where: "connect", Stream: streamConnect, Gen: gen}
		}
		info, err := svc.P.ServerInfo(ctx)
		if err != nil {
			return errMsg{Err: err, Where: "connect", Stream: streamConnect, Gen: gen}
		}
		user, err := svc.P.CurrentUser(ctx)
		if err != nil {
			return errMsg{Err: err, Where: "connect", Stream: streamConnect, Gen: gen}
		}
		return connectedMsg{Gen: gen, Alias: alias, Svc: svc, Info: info, User: user}
	}
}

// loadPlansCmd lists the plans of the configured projects (home spec §3.1),
// or of every project on the server when none is configured. project, when
// set, narrows it to one.
func loadPlansCmd(ctx context.Context, svc *app.Service, project string, gen int) tea.Cmd {
	return func() tea.Msg {
		projects, err := svc.P.ListProjects(ctx)
		if err != nil {
			return errMsg{Err: err, Where: "plans", Stream: streamPlans, Gen: gen}
		}
		onServer := map[string]bool{}
		for _, p := range projects {
			onServer[p.Key] = true
		}
		msg := plansLoadedMsg{Gen: gen}
		keys := svc.ProjectKeys()
		if len(keys) == 0 {
			for _, p := range projects {
				keys = append(keys, p.Key)
			}
		}
		for _, k := range keys {
			if !onServer[k] {
				msg.Missing = append(msg.Missing, k)
				continue
			}
			if project != "" && k != project {
				continue
			}
			plans, err := svc.P.ListPlans(ctx, k)
			if err != nil {
				msg.Failed = append(msg.Failed, k)
				if msg.Err == nil {
					msg.Err = err
				}
				continue
			}
			msg.Plans = append(msg.Plans, plans...)
		}
		sort.Slice(msg.Plans, func(i, j int) bool { return msg.Plans[i].Key < msg.Plans[j].Key })
		return msg
	}
}

func loadBuildsCmd(ctx context.Context, svc *app.Service, planKey string, limit int, gen int) tea.Cmd {
	return func() tea.Msg {
		builds, err := svc.P.ListBuilds(ctx, planKey, provider.ListOptions{Limit: limit})
		if err != nil {
			return errMsg{Err: err, Where: "builds", Stream: streamBuilds, Gen: gen}
		}
		return buildsLoadedMsg{Gen: gen, PlanKey: planKey, Builds: builds}
	}
}

func loadPresetsCmd(d Deps, gen int) tea.Cmd {
	return func() tea.Msg {
		ts, err := d.Targets()
		if err != nil {
			return errMsg{Err: err, Where: "presets", Stream: streamPresets, Gen: gen}
		}
		return presetsLoadedMsg{Gen: gen, Targets: ts}
	}
}

type logsLoadedMsg struct {
	Gen     int
	JobKey  string
	Title   string
	URL     string
	Lines   []string
	All     bool
	Multi   bool           // several jobs, or a stage: every line carries its job's prefix
	Stage   string         // a stage screen's stage; empty otherwise
	Live    bool           // a stage screen on a build still running: follow at once
	Offsets map[string]int // the provider's next-read offset per job, for follow
	Build   provider.Build // the build the logs were read from, with its jobs
}

// loadLogsCmd reads one build's logs. An empty jobKey with all=false means the
// failed jobs, which is what bam logs --last --failed prints.
func loadLogsCmd(ctx context.Context, svc *app.Service, b provider.Build, jobKey string, all bool, gen int) tea.Cmd {
	return func() tea.Msg {
		jobs, err := svc.Logs(ctx, b, app.LogsOptions{Failed: !all && jobKey == "", Job: jobKey})
		if err != nil {
			return errMsg{Err: err, Where: "logs", Stream: streamLogs, Gen: gen}
		}
		if len(jobs) == 0 {
			// Say what is actually missing: advising "press a" when a was
			// already pressed is worse than saying nothing.
			if all {
				return errMsg{Err: errs.Bamboof("%s has no job logs", b.Key).
					WithWhy("the build has no jobs, or the server kept no log for them"),
					Where: "logs", Stream: streamLogs, Gen: gen}
			}
			return errMsg{Err: errs.Bamboof("no failed job in %s", b.Key).
				WithTry("press a for every job's log"),
				Where: "logs", Stream: streamLogs, Gen: gen}
		}
		return logsMsg(b, jobs, all, "", gen)
	}
}

// logsMsg turns read logs into the screen's message. Offsets carry every
// job's own offset, because a follow resumes each job where it was read.
func logsMsg(b provider.Build, jobs []app.JobLog, all bool, stage string, gen int) logsLoadedMsg {
	multi := len(jobs) > 1 || stage != ""
	offsets := make(map[string]int, len(jobs))
	for _, j := range jobs {
		offsets[j.Job.Key] = j.Next
	}
	title, url := jobs[0].Job.Key, jobs[0].Job.URL
	if multi {
		title, url = b.Key, b.URL
	}
	return logsLoadedMsg{Gen: gen, JobKey: jobs[0].Job.Key, Title: title, URL: url,
		Lines: composeLogs(b, jobs, multi, stage), All: all, Multi: multi, Stage: stage,
		Offsets: offsets, Build: b}
}

// composeLogs lays jobs out for the screen. A single job is bare. Several
// jobs carry a [JOB] prefix on every line, because once they stream their
// lines interleave; outside a stage screen a "== stage ==" line opens each
// stage's jobs. A stage screen's header already names its stage.
func composeLogs(b provider.Build, jobs []app.JobLog, multi bool, stage string) []string {
	if !multi {
		return append([]string(nil), jobs[0].Lines...)
	}
	var lines []string
	last := ""
	for i, j := range jobs {
		if stage == "" && (i == 0 || j.Stage != last) {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, "== "+j.Stage+" ==")
		}
		last = j.Stage
		prefix := "[" + app.ShortJobKey(b, j.Job) + "] "
		for _, l := range j.Lines {
			lines = append(lines, prefix+l)
		}
	}
	return lines
}

type logEventMsg struct {
	Gen   int
	Event app.LogEvent
}

type followEndedMsg struct {
	Gen int
	Err error
}

// followCmd runs app.FollowLogs in its own goroutine and adapts its emit
// callback into a channel. FollowLogs blocks until its jobs finish, so this
// is the one place the UI starts a goroutine, and ending ctx ends it.
func followCmd(ctx context.Context, svc *app.Service, buildKey string, from map[string]int, advance bool) (<-chan app.LogEvent, <-chan error) {
	events := make(chan app.LogEvent, 8)
	done := make(chan error, 1)
	go func() {
		err := svc.FollowLogs(ctx, buildKey, from, advance, func(ev app.LogEvent) {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		})
		// The error is published before the channel closes, so a drain that
		// sees the close always finds the outcome waiting. Closing first
		// would let a real failure be read as a clean end.
		done <- err
		close(events)
	}()
	return events, done
}

// drainFollowCmd takes one event off the channel, or reports the end. The
// model re-issues it after each event, so Update never ranges over a channel.
func drainFollowCmd(events <-chan app.LogEvent, done <-chan error, gen int) tea.Cmd {
	return func() tea.Msg {
		if ev, ok := <-events; ok {
			return logEventMsg{Gen: gen, Event: ev}
		}
		select {
		case err := <-done:
			return followEndedMsg{Gen: gen, Err: err}
		default:
			return followEndedMsg{Gen: gen}
		}
	}
}

// resolveTargetBuildsCmd resolves a preset to its plan, honouring the branch
// the preset names, and then lists that plan's builds. It is one command so
// the two requests share a generation.
func resolveTargetBuildsCmd(ctx context.Context, svc *app.Service, target app.TargetInfo, gen int) tea.Cmd {
	return func() tea.Msg {
		// From the TargetInfo the panel is showing, not from the service's
		// configuration: the panel may have been refreshed since the UI
		// started, and the service's copy would be the older one.
		ref, err := svc.RefFromTarget(ctx, target)
		if err != nil {
			// gen is the builds generation: this command is a builds load,
			// whichever of its two steps fails. Tagging it as a presets
			// failure would have Update check it against presetsGen and drop
			// a real error as stale.
			return errMsg{Err: err, Where: "presets", Stream: streamBuilds, Gen: gen}
		}
		builds, err := svc.P.ListBuilds(ctx, ref.PlanKey, provider.ListOptions{Limit: buildsPerPlan})
		if err != nil {
			return errMsg{Err: err, Where: "builds", Stream: streamBuilds, Gen: gen}
		}
		return buildsLoadedMsg{Gen: gen, PlanKey: ref.PlanKey, Builds: builds}
	}
}

type formLoadedMsg struct {
	Gen          int
	Ref          app.PlanRef
	Target       string
	Base         app.VarSet
	FromBuild    string // the build the variables came from; empty for none
	FromRevision string // that build's commit, offered on the Revision row; empty for none
}

// openFormCmd resolves what the cursor is on into a plan reference and
// fetches the variables the form starts from.
//
// A preset resolves from the TargetInfo the Presets panel is showing, not
// from the service's configuration, for the same reason the preset drill
// does: the panel may have been refreshed since the UI started.
//
// from prefills the form from a previous build. It is a convenience, not a
// requirement: a server that cannot read a build's variables, or a repository
// with no last build, opens the form on the plan's own values instead.
func openFormCmd(ctx context.Context, svc *app.Service, planKey string, target *app.TargetInfo, from string, gen int) tea.Cmd {
	return func() tea.Msg {
		var (
			ref  app.PlanRef
			err  error
			name string
		)
		if target != nil {
			name = target.Name
			ref, err = svc.RefFromTarget(ctx, *target)
		} else {
			ref, err = svc.ResolvePlan(ctx, planKey, "")
		}
		if err != nil {
			return errMsg{Err: err, Where: "run", Stream: streamRun, Gen: gen}
		}
		base, err := svc.VarBase(ctx, ref, from)
		if err != nil && from != "" {
			base, err = svc.VarBase(ctx, ref, "")
		}
		if err != nil {
			return errMsg{Err: err, Where: "run", Stream: streamRun, Gen: gen}
		}
		// The revision is offered, never required: any error just means no
		// hint on the Revision row.
		var rev string
		if base.FromBuild != "" {
			rev, _ = svc.BuildRevision(ctx, base.FromBuild)
		}
		return formLoadedMsg{Gen: gen, Ref: ref, Target: name, Base: base, FromBuild: base.FromBuild, FromRevision: rev}
	}
}

type cancelledMsg struct {
	Gen             int
	Build           provider.Build
	AlreadyFinished bool
}

// cancelCmd stops a queued or running build. It is the only thing in the UI
// that stops a build: leaving a screen, switching server and quitting all
// cancel watches, and a watch is not a build.
//
// The request is stopped, not the reply: gen is what the model checks, so a
// cancel confirmed just before the user switched server or left the build
// cannot report "cancelled OLD" over the build they are on now.
func cancelCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		b, alreadyFinished, err := svc.Cancel(ctx, key)
		if err != nil {
			return errMsg{Err: err, Where: "cancel", Stream: streamCancel, Gen: gen}
		}
		return cancelledMsg{Gen: gen, Build: b, AlreadyFinished: alreadyFinished}
	}
}

type triggeredMsg struct {
	Gen   int
	Build provider.Build
}

// runCmd triggers the build. It sends vs.Changed(), the same set bam run
// sends, so a variable equal to the plan's value is not transmitted.
func runCmd(ctx context.Context, svc *app.Service, ref app.PlanRef, vs app.VarSet, gen int) tea.Cmd {
	return func() tea.Msg {
		b, err := svc.Run(ctx, ref, vs)
		if err != nil {
			return errMsg{Err: err, Where: "run", Stream: streamRun, Gen: gen}
		}
		return triggeredMsg{Gen: gen, Build: b}
	}
}

// logsForKeyCmd reads a build in full and then its logs, for a row that came
// from a listing and therefore carries no stages.
func logsForKeyCmd(ctx context.Context, svc *app.Service, buildKey, jobKey string, all bool, gen int) tea.Cmd {
	return func() tea.Msg {
		b, err := svc.Build(ctx, buildKey)
		if err != nil {
			return errMsg{Err: err, Where: "logs", Stream: streamLogs, Gen: gen}
		}
		return loadLogsCmd(ctx, svc, b, jobKey, all, gen)()
	}
}

type buildLoadedMsg struct {
	Gen      int
	Build    provider.Build
	Progress provider.Progress
}

// reloadBuildCmd re-reads one build. It goes through Service.Build rather
// than the provider directly, so a build the server has dropped is forgotten
// the same way the commands forget it.
func reloadBuildCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		b, err := svc.Build(ctx, key)
		if err != nil {
			return errMsg{Err: err, Where: "build", Stream: streamDetail, Gen: gen}
		}
		// A missing estimate is decoration lost, never a failed refresh.
		pr, _ := svc.Progress(ctx, b)
		return buildLoadedMsg{Gen: gen, Build: b, Progress: pr}
	}
}

type projectsLoadedMsg struct {
	Gen      int
	Stream   stream // streamPicker for the P picker, streamProjects for the projects view
	Projects []provider.Project
	Missing  []string // configured projects the server does not have
}

// loadProjectsCmd lists the projects: the configured projects that are
// actually on the server, in configured order, or every project on the server
// when none is configured. Missing names the configured ones the server does
// not have; the picker ignores it, the projects view shows it.
func loadProjectsCmd(ctx context.Context, svc *app.Service, s stream, gen int) tea.Cmd {
	return func() tea.Msg {
		ps, err := svc.P.ListProjects(ctx)
		if err != nil {
			return errMsg{Err: err, Where: "projects", Stream: s, Gen: gen}
		}
		keys := svc.ProjectKeys()
		if len(keys) == 0 {
			return projectsLoadedMsg{Gen: gen, Stream: s, Projects: ps}
		}
		byKey := map[string]provider.Project{}
		for _, p := range ps {
			byKey[p.Key] = p
		}
		msg := projectsLoadedMsg{Gen: gen, Stream: s}
		for _, k := range keys {
			if p, ok := byKey[k]; ok {
				msg.Projects = append(msg.Projects, p)
			} else {
				msg.Missing = append(msg.Missing, k)
			}
		}
		return msg
	}
}

type branchesLoadedMsg struct {
	Gen       int
	MasterKey string
	Branches  []provider.Branch
}

func loadBranchesCmd(ctx context.Context, svc *app.Service, masterKey string, gen int) tea.Cmd {
	return func() tea.Msg {
		bs, err := svc.P.ListBranches(ctx, masterKey)
		if err != nil {
			return errMsg{Err: err, Where: "branches", Stream: streamPicker, Gen: gen}
		}
		return branchesLoadedMsg{Gen: gen, MasterKey: masterKey, Branches: bs}
	}
}

type watchEventMsg struct {
	Gen   int
	Event app.Event
}

// watchClosedMsg says the watch channel ended without a done event, which
// happens when the context was cancelled.
type watchClosedMsg struct{ Gen int }

// watchCmd takes exactly one event off the channel. The model re-issues it
// after each watchEventMsg, which is bubbletea's channel pattern: Update
// stays pure and nothing ranges over a channel inside it.
func watchCmd(ch <-chan app.Event, gen int) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return watchClosedMsg{Gen: gen}
		}
		return watchEventMsg{Gen: gen, Event: e}
	}
}

// planRestMsg fires once the Builds cursor has stayed on Key for
// planRestDelay. Gen is the plan screen's restGen when it was scheduled.
type planRestMsg struct {
	Gen int
	Key string
}

// treePreviewMsg is the Tree panel's one-level tree of build Key.
type treePreviewMsg struct {
	Gen  int
	Key  string
	Tree app.Tree
	Err  error
}

// treePreviewCmd reads what build key triggered, one level down. It is read
// once, not watched: the plan screen reads it again on r and when the open
// build finishes.
func treePreviewCmd(ctx context.Context, svc *app.Service, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		t, err := svc.BuildTree(ctx, key, app.TreeOptions{Depth: 1})
		return treePreviewMsg{Gen: gen, Key: key, Tree: t, Err: err}
	}
}

// stageLogsCmd reads one stage's logs. An empty stage means the build's
// current stage. A listing row carries no stages, so it is read in full
// first; a build with no current stage, or one that turns out to be
// finished, gets its failed jobs, which is what l always showed.
func stageLogsCmd(ctx context.Context, svc *app.Service, b provider.Build, stage string, gen int) tea.Cmd {
	return func() tea.Msg {
		if len(b.Stages) == 0 {
			full, err := svc.Build(ctx, b.Key)
			if err != nil {
				return errMsg{Err: err, Where: "logs", Stream: streamLogs, Gen: gen}
			}
			b = full
		}
		if stage == "" {
			cur, ok := app.CurrentStage(b)
			if !ok || b.State.Finished() {
				return loadLogsCmd(ctx, svc, b, "", false, gen)()
			}
			stage = cur
		}
		jobs, err := svc.Logs(ctx, b, app.LogsOptions{Stage: stage})
		if err != nil {
			return errMsg{Err: err, Where: "logs", Stream: streamLogs, Gen: gen}
		}
		if len(jobs) == 0 {
			return errMsg{Err: errs.Bamboof("stage %s of %s has no jobs", stage, b.Key),
				Where: "logs", Stream: streamLogs, Gen: gen}
		}
		msg := logsMsg(b, jobs, true, stage, gen)
		msg.Live = !b.State.Finished()
		return msg
	}
}
