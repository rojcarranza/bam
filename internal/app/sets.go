package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/r0jjames/bam-cli/internal/config"
	"github.com/r0jjames/bam-cli/internal/credential"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// SetOptions are the command-line inputs to a set run.
type SetOptions struct {
	Vars   []string // --var name=value, in order; applies to every run
	Server string   // --server, so every run's server is chosen as bam run would
	File   string   // a train file to read the set from instead of the configuration

	// recordedName is the set a resumed train file run was recorded under;
	// a file without name: takes it, so renaming the file does not orphan
	// the run.
	recordedName string
}

// SetRunPlan is one run of a stage, resolved and ready to trigger.
type SetRunPlan struct {
	Name    string // the run's name: the preset, or preset[k=v,…]
	Target  string
	Ref     PlanRef
	Vars    VarSet
	Timeout time.Duration // the preset's; zero means none
}

// SetPlan is a set with every run resolved and checked. RunSet does no
// further resolving.
type SetPlan struct {
	Name     string
	Origin   string
	Timeout  time.Duration
	VarNames []string // names given with --var, kept in the record for resume
	Stages   [][]SetRunPlan
	Warnings []string // each run's variable warnings, prefixed with its preset
	File     string   // the train file the set came from; empty for a configured set
}

// PlanSet resolves and checks every run of a set, so that every usage error
// shows before the first trigger. It triggers nothing.
func (s *Service) PlanSet(ctx context.Context, name string, o SetOptions) (SetPlan, error) {
	var set config.ResolvedSet
	if o.File != "" {
		abs, err := filepath.Abs(o.File)
		if err != nil {
			return SetPlan{}, errs.Configf("cannot resolve %s", o.File).WithWhy(err.Error())
		}
		if set, err = s.Cfg.LoadSetFileNamed(abs, o.recordedName); err != nil {
			return SetPlan{}, err
		}
		o.File, name = abs, set.Name
	} else {
		var ok bool
		var err error
		if set, ok, err = s.Cfg.Set(name); err != nil {
			return SetPlan{}, err
		}
		if !ok {
			return SetPlan{}, errs.Usagef("unknown set %q", name).WithTry("bam set list")
		}
	}
	p := SetPlan{Name: name, Origin: s.Origin, Timeout: time.Duration(set.Timeout), VarNames: varNames(o.Vars), File: o.File}
	var problems []string
	for i, st := range set.Stages {
		runs := make([]SetRunPlan, 0, len(st))
		for _, sr := range st {
			for _, x := range sr.Expand() {
				rp, err := s.planRun(ctx, config.SetRun{Target: x.Target, Vars: x.Vars}, o)
				if err != nil {
					if errs.KindOf(err) != errs.KindUsage {
						return SetPlan{}, err
					}
					problems = append(problems, fmt.Sprintf("stage %d %s: %s", i+1, x.Name, describeErr(err)))
					continue
				}
				rp.Name = x.Name
				for _, w := range rp.Vars.Warnings {
					p.Warnings = append(p.Warnings, x.Name+": "+w)
				}
				runs = append(runs, rp)
			}
		}
		p.Stages = append(p.Stages, runs)
	}
	if len(problems) > 0 {
		return SetPlan{}, errs.Usagef("set %q cannot run: %d problem(s)", name, len(problems)).
			WithWhy(strings.Join(problems, "\n")).WithTry("fix the presets or pass --var, then try again")
	}
	return p, nil
}

// planRun resolves one run: the preset's plan and branch, then its
// variables with the set's vars laid over the preset's defaults.
func (s *Service) planRun(ctx context.Context, r config.SetRun, o SetOptions) (SetRunPlan, error) {
	t, _, err := s.Cfg.Target(r.Target)
	if err != nil {
		return SetRunPlan{}, err
	}
	server, err := s.Cfg.SelectServer(o.Server, t.Server)
	if err != nil {
		return SetRunPlan{}, err
	}
	if origin, err := credential.Origin(server.URL); err != nil || origin != s.Origin {
		return SetRunPlan{}, errs.Configf("preset %q runs on %s, not %s", r.Target, server.URL, s.Origin).
			WithWhy("a set runs on one server").WithTry("give every preset of the set the same server")
	}
	ref, err := s.ResolvePlan(ctx, r.Target, "")
	if err != nil {
		return SetRunPlan{}, err
	}
	if len(r.Vars) > 0 {
		// A copy: the set's vars must not leak into the preset itself.
		rt := *ref.Target
		merged := config.StringMap{}
		for k, v := range rt.Defaults {
			merged[k] = v
		}
		for k, v := range r.Vars {
			merged[k] = v
		}
		rt.Defaults = merged
		ref.Target = &rt
	}
	vs, err := s.ResolveVars(ctx, ref, VarOptions{Flags: o.Vars})
	if err != nil {
		return SetRunPlan{}, err
	}
	return SetRunPlan{Target: r.Target, Ref: ref, Vars: vs, Timeout: time.Duration(t.Timeout)}, nil
}

// varNames is the distinct names of --var flags, in order.
func varNames(flags []string) []string {
	out := []string{}
	for _, f := range flags {
		if name, _, ok := strings.Cut(f, "="); ok && name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// describeErr is an error's What and Why on one line, for a list of problems.
func describeErr(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		if e.Why != "" {
			return e.What + " (" + e.Why + ")"
		}
		return e.What
	}
	return err.Error()
}

// SetEventType names a step of a set run.
type SetEventType string

const (
	SetStageStart   SetEventType = "stage_start"
	SetRunTriggered SetEventType = "run_triggered"
	SetRunEvent     SetEventType = "run_event" // one watch Event of a run
	SetRunDone      SetEventType = "run_done"
	SetStageDone    SetEventType = "stage_done"
	SetDone         SetEventType = "set_done"  // last event of a set that finished
	SetError        SetEventType = "set_error" // last event of a set that could not go on
)

// SetEvent is one change in a set run. Stage is zero-based; Target is empty
// for stage and set events.
type SetEvent struct {
	Type   SetEventType
	Time   time.Time
	Set    string
	ID     string
	Stage  int
	Target string
	Run    string         // run name; empty for stage and set events
	Build  provider.Build // run_triggered, run_event, run_done
	Event  *Event         // run_event only: the watch event, unchanged
	State  provider.State // run_done, stage_done, set_done
	Rerun  int            // run_triggered of a restart: the run's reruns; 0 otherwise
	Err    error
}

// SetLockOptions are how a set run takes its lock.
type SetLockOptions struct {
	Force bool // replace a lock left behind by a process that is gone
}

// setKeep is how many records of one set are kept.
const setKeep = 20

// RunSet runs a planned set stage by stage and returns the record id. It
// fails, with no channel, only when the set cannot start: a held lock or a
// record that cannot be written. Ending ctx stops watching and triggering;
// no build is ever stopped.
func (s *Service) RunSet(ctx context.Context, p SetPlan, lock SetLockOptions) (string, <-chan SetEvent, error) {
	now := s.Clock.Now()
	rec := SetRecord{Version: 1, ID: NewSetID(p.Name, now), Set: p.Name, File: p.File, Origin: p.Origin,
		Repo: s.Cfg.RepoRoot, StartedAt: now.UTC(), Status: SetRunning, VarNames: p.VarNames}
	if rec.VarNames == nil {
		rec.VarNames = []string{}
	}
	for _, st := range p.Stages {
		var sr StageRecord
		for _, r := range st {
			sr.Runs = append(sr.Runs, RunRecord{Target: r.Target, Name: r.Name, PlanKey: r.Ref.PlanKey})
		}
		rec.Stages = append(rec.Stages, sr)
	}
	l, err := s.Sets.Lock([]string{p.Name, rec.ID}, lock.Force, now)
	if err != nil {
		return "", nil, lockErr(err)
	}
	if err := s.Sets.Save(rec); err != nil {
		l.Release()
		return "", nil, recordErr(err)
	}
	_ = s.Sets.Prune(p.Name, setKeep)
	return rec.ID, s.startSet(ctx, p, rec, l, 0, nil), nil
}

func (s *Service) startSet(ctx context.Context, p SetPlan, rec SetRecord, l *SetLock, from int, restart map[int]bool) <-chan SetEvent {
	ch := make(chan SetEvent, 16)
	go func() {
		defer close(ch)
		defer l.Release()
		(&setRun{s: s, p: p, rec: rec, ch: ch, restartStage: from, restart: restart}).run(ctx, from)
	}()
	return ch
}

func lockErr(err error) error {
	if errs.KindOf(err) == errs.KindUsage {
		return err
	}
	return errs.Configf("cannot lock the set run").WithWhy(err.Error()).Wrap(err)
}

func recordErr(err error) error {
	return errs.Configf("cannot record the set run").WithWhy(err.Error()).Wrap(err)
}

// setRun is one execution of a set. Only the goroutine running it touches
// rec; watchers report through channels.
type setRun struct {
	s   *Service
	p   SetPlan
	rec SetRecord
	ch  chan<- SetEvent
	// restart holds the runs of stage restartStage that Bamboo restarts
	// instead of bam triggering them (bam set rerun --failed).
	restartStage int
	restart      map[int]bool
}

func (r *setRun) send(ctx context.Context, e SetEvent) bool {
	e.Time, e.Set, e.ID = r.s.Clock.Now(), r.p.Name, r.rec.ID
	select {
	case r.ch <- e:
		return true
	case <-ctx.Done():
		return false
	}
}

// save writes the record. A failure ends the set before anything else
// triggers: bam never triggers a build it cannot record.
func (r *setRun) save(ctx context.Context, stage int) bool {
	if err := r.s.Sets.Save(r.rec); err != nil {
		r.send(ctx, SetEvent{Type: SetError, Stage: stage, Err: recordErr(err)})
		return false
	}
	return true
}

func (r *setRun) finish(status SetStatus) {
	t := r.s.Clock.Now().UTC()
	r.rec.Status, r.rec.FinishedAt = status, &t
	_ = r.s.Sets.Save(r.rec)
}

// run executes the stages from `from` on.
func (r *setRun) run(parent context.Context, from int) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	timedOut := make(chan struct{})
	if r.p.Timeout > 0 {
		deadline := r.s.Clock.After(r.p.Timeout)
		go func() {
			select {
			case <-deadline:
				close(timedOut)
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	for i := from; i < len(r.p.Stages); i++ {
		ok, recorded := r.stage(ctx, i)
		select {
		case <-timedOut:
			r.finish(SetTimedOut)
			r.send(parent, SetEvent{Type: SetError, Stage: i,
				Err: errs.New(errs.KindTimeout, fmt.Sprintf("set %s timed out after %s", r.p.Name, r.p.Timeout)).
					WithWhy("its builds keep running").WithTry("bam set resume " + r.rec.ID)})
			return
		default:
		}
		switch {
		case parent.Err() != nil:
			r.finish(SetInterrupted)
			return
		case !recorded:
			r.finish(SetFailed)
			return
		case !ok:
			r.finish(SetFailed)
			r.send(ctx, SetEvent{Type: SetDone, Stage: i, State: provider.StateFailed})
			return
		}
	}
	r.finish(SetSuccessful)
	r.send(ctx, SetEvent{Type: SetDone, Stage: len(r.p.Stages) - 1, State: provider.StateSuccess})
}

type runResult struct {
	idx   int
	build provider.Build
	err   error
}

// stage runs stage i to its end. ok is whether every run succeeded;
// recorded is false when the record could not be written, which already
// ended the set with set_error.
func (r *setRun) stage(ctx context.Context, i int) (ok, recorded bool) {
	if !r.send(ctx, SetEvent{Type: SetStageStart, Stage: i}) {
		return false, true
	}
	runs := r.p.Stages[i]
	recs := r.rec.Stages[i].Runs
	results := make(chan runResult, len(runs))
	var wg sync.WaitGroup
	defer wg.Wait()
	// Leaving early (a record that cannot be written) must end the stage's
	// watchers, or wg.Wait would wait for their builds to finish. Deferred
	// after wg.Wait, so it runs first.
	stageCtx, stop := context.WithCancel(ctx)
	defer stop()
	pending := 0
	for j, run := range runs {
		if recs[j].State.Finished() || recs[j].Error != "" {
			continue // resumed: its end is already known
		}
		key := recs[j].BuildKey
		if key != "" && i == r.restartStage && r.restart[j] {
			delete(r.restart, j)
			if ctx.Err() != nil {
				return false, true
			}
			// Counted and saved first: bam never restarts a build it has
			// not recorded.
			recs[j].Reruns++
			if !r.save(ctx, i) {
				return false, false
			}
			b, restarted, err := r.s.Restart(ctx, key)
			if err != nil {
				if ctx.Err() != nil {
					return false, true
				}
				recs[j].State, recs[j].Error = provider.StateFailed, describeErr(err)
				if !r.save(ctx, i) {
					return false, false
				}
				r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, Run: run.Name, Build: provider.Build{Key: key},
					State: provider.StateFailed, Err: err})
				continue
			}
			if restarted {
				r.send(ctx, SetEvent{Type: SetRunTriggered, Stage: i, Target: run.Target, Run: run.Name, Build: b, Rerun: recs[j].Reruns})
			} else {
				// Running or succeeded after all: nothing was restarted, so
				// nothing counts; the build is watched as it is.
				recs[j].Reruns--
				if !r.save(ctx, i) {
					return false, false
				}
			}
		}
		if key == "" {
			if ctx.Err() != nil {
				return false, true
			}
			b, err := r.s.Run(ctx, run.Ref, run.Vars)
			if err != nil {
				recs[j].State, recs[j].Error = provider.StateFailed, describeErr(err)
				if !r.save(ctx, i) {
					return false, false
				}
				r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, Run: run.Name, State: provider.StateFailed, Err: err})
				continue
			}
			key = b.Key
			recs[j].BuildKey = key
			if !r.save(ctx, i) {
				return false, false
			}
			r.send(ctx, SetEvent{Type: SetRunTriggered, Stage: i, Target: run.Target, Run: run.Name, Build: b})
		}
		pending++
		wg.Add(1)
		go func(j int, run SetRunPlan, key string) {
			defer wg.Done()
			results <- r.watchRun(stageCtx, i, j, run, key)
		}(j, run, key)
	}
	for ; pending > 0; pending-- {
		res := <-results
		if ctx.Err() != nil && res.err == nil && !res.build.State.Finished() {
			continue // interrupted: the build keeps running and its end stays unknown
		}
		run := runs[res.idx]
		st := res.build.State
		if res.err != nil {
			st = provider.StateFailed
			recs[res.idx].Error = describeErr(res.err)
		}
		recs[res.idx].State = st
		if !r.save(ctx, i) {
			return false, false
		}
		r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, Run: run.Name, Build: res.build, State: st, Err: res.err})
	}
	if ctx.Err() != nil {
		return false, true
	}
	ok = true
	for _, rr := range recs {
		ok = ok && rr.State == provider.StateSuccess
	}
	state := provider.StateSuccess
	if !ok {
		state = provider.StateFailed
	}
	r.send(ctx, SetEvent{Type: SetStageDone, Stage: i, State: state})
	return ok, true
}

// watchRun follows one build to its end, forwarding every watch event. A
// preset timeout ends watching with a timeout error; the build keeps running.
func (r *setRun) watchRun(ctx context.Context, i, j int, run SetRunPlan, key string) runResult {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var deadline <-chan time.Time
	if run.Timeout > 0 {
		deadline = r.s.Clock.After(run.Timeout)
	}
	events := r.s.Watch(wctx, key)
	var last provider.Build
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return runResult{idx: j, build: last}
			}
			ev := e
			last = e.Build
			r.send(ctx, SetEvent{Type: SetRunEvent, Stage: i, Target: run.Target, Run: run.Name, Build: e.Build, Event: &ev})
			switch e.Type {
			case EventDone:
				return runResult{idx: j, build: e.Build}
			case EventError:
				return runResult{idx: j, build: last, err: e.Err}
			}
		case <-deadline:
			return runResult{idx: j, build: last, err: errs.New(errs.KindTimeout,
				fmt.Sprintf("%s timed out after %s", run.Target, run.Timeout)).WithWhy(key + " is still running")}
		}
	}
}

// ResumeSet continues an interrupted or timed-out set run under its own id:
// it watches the runs that triggered and triggers the ones that did not.
func (s *Service) ResumeSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error) {
	rec, err := s.Sets.Load(id)
	if err != nil {
		return nil, err
	}
	switch rec.Status {
	case SetFailed:
		return nil, errs.Usagef("set run %s failed; resume continues interrupted runs", id).
			WithTry("bam set rerun --failed " + id)
	case SetSuccessful:
		return nil, errs.Usagef("set run %s succeeded; nothing to resume", id)
	}
	p, l, err := s.continueSet(ctx, rec, o, lock, "resume")
	if err != nil {
		return nil, err
	}
	if rec.File != "" {
		rec.File = p.File // resumed from a moved file: remember where it is now
	}
	rec.Status, rec.FinishedAt = SetRunning, nil
	if err := s.Sets.Save(rec); err != nil {
		l.Release()
		return nil, recordErr(err)
	}
	return s.startSet(ctx, p, rec, l, firstUnfinished(rec), nil), nil
}

// RerunSet retries a failed set run under its own id. In the stage that
// failed, Bamboo restarts each failed build and a run that never triggered
// is triggered; once that stage succeeds the later stages run as on resume.
// Runs that succeeded are left alone.
func (s *Service) RerunSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error) {
	rec, err := s.Sets.Load(id)
	if err != nil {
		return nil, err
	}
	switch rec.Status {
	case SetFailed:
	case SetSuccessful:
		return nil, errs.Usagef("set run %s succeeded; nothing to rerun", id)
	default:
		return nil, errs.Usagef("set run %s was %s, not failed", id, strings.ReplaceAll(string(rec.Status), "_", " ")).
			WithTry("bam set resume " + id)
	}
	p, l, err := s.continueSet(ctx, rec, o, lock, "rerun")
	if err != nil {
		return nil, err
	}
	from := firstUnfinished(rec)
	restart := map[int]bool{}
	if from < len(rec.Stages) {
		runs := rec.Stages[from].Runs
		for j := range runs {
			if runs[j].State == provider.StateSuccess {
				continue
			}
			if runs[j].BuildKey != "" {
				restart[j] = true
			}
			runs[j].State, runs[j].Error = "", ""
		}
	}
	if rec.File != "" {
		rec.File = p.File // resumed from a moved file: remember where it is now
	}
	rec.Status, rec.FinishedAt = SetRunning, nil
	if err := s.Sets.Save(rec); err != nil {
		l.Release()
		return nil, recordErr(err)
	}
	return s.startSet(ctx, p, rec, l, from, restart), nil
}

// continueSet makes the checks resume and rerun share before they act on a
// recorded set run, and takes its locks. verb names the command in errors.
func (s *Service) continueSet(ctx context.Context, rec SetRecord, o SetOptions, lock SetLockOptions, verb string) (SetPlan, *SetLock, error) {
	if rec.Origin != s.Origin {
		return SetPlan{}, nil, errs.Configf("set run %s ran on %s, not %s", rec.ID, rec.Origin, s.Origin).
			WithTry("pass --server with the alias of " + rec.Origin)
	}
	given := varNames(o.Vars)
	var missing []string
	for _, n := range rec.VarNames {
		if !slices.Contains(given, n) {
			missing = append(missing, "--var "+n+"=...")
		}
	}
	if len(missing) > 0 {
		return SetPlan{}, nil, errs.Usagef("%s needs the variables set run %s was given", verb, rec.ID).
			WithWhy("bam never stores variable values").WithTry(strings.Join(missing, " "))
	}
	if rec.File == "" && o.File != "" {
		return SetPlan{}, nil, errs.Usagef("set run %s is a configured set; --file is for the runs of train files", rec.ID).
			WithTry("bam set " + verb + " " + rec.ID)
	}
	again := "bam set " + verb + " " + rec.ID
	if verb == "rerun" {
		again = "bam set rerun --failed " + rec.ID
	}
	if rec.File != "" {
		o.recordedName = rec.Set
		if o.File == "" {
			o.File = rec.File
		}
		// The recorded path or the one given with --file: either way, a
		// missing file gets the same way out.
		if _, err := os.Stat(o.File); err != nil {
			return SetPlan{}, nil, errs.Configf("the train file of set run %s is gone: %s", rec.ID, o.File).
				WithTry(again + " --file <its new path>")
		}
	}
	p, err := s.PlanSet(ctx, rec.Set, o)
	if err != nil {
		return SetPlan{}, nil, err
	}
	if rec.File != "" && p.Name != rec.Set {
		return SetPlan{}, nil, errs.Configf("%s defines set %s, not %s", p.File, p.Name, rec.Set).
			WithTry(again + " --file <the train file of " + rec.Set + ">")
	}
	if !sameShape(p, rec) {
		return SetPlan{}, nil, errs.Configf("set %s has changed since run %s", rec.Set, rec.ID).
			WithWhy("its stages no longer list the same presets").WithTry(runAgain(p, rec.Set))
	}
	p.VarNames = rec.VarNames
	l, err := s.Sets.Lock([]string{rec.Set, rec.ID}, lock.Force, s.Clock.Now())
	if err != nil {
		return SetPlan{}, nil, lockErr(err)
	}
	return p, l, nil
}

// firstUnfinished is the index of the first stage whose runs did not all
// succeed; len(rec.Stages) when every stage did.
func firstUnfinished(rec SetRecord) int {
	from := 0
	for from < len(rec.Stages) && stageSucceeded(rec.Stages[from]) {
		from++
	}
	return from
}

// sameShape reports whether the set still lists the recorded presets, in
// the same stages and order.
func sameShape(p SetPlan, rec SetRecord) bool {
	if len(p.Stages) != len(rec.Stages) {
		return false
	}
	for i := range p.Stages {
		if len(p.Stages[i]) != len(rec.Stages[i].Runs) {
			return false
		}
		for j := range p.Stages[i] {
			if p.Stages[i][j].Name != rec.Stages[i].Runs[j].RunName() {
				return false
			}
		}
	}
	return true
}

func stageSucceeded(st StageRecord) bool {
	for _, r := range st.Runs {
		if r.State != provider.StateSuccess {
			return false
		}
	}
	return true
}

// SetStatus reads a record and fetches the current state of its builds,
// keyed by build key. A build that cannot be fetched is left out. It never
// triggers anything.
func (s *Service) SetStatus(ctx context.Context, id string) (SetRecord, map[string]provider.Build, error) {
	rec, err := s.Sets.Load(id)
	if err != nil {
		return rec, nil, err
	}
	builds := map[string]provider.Build{}
	for _, st := range rec.Stages {
		for _, r := range st.Runs {
			if r.BuildKey == "" {
				continue
			}
			if b, err := s.P.GetBuild(ctx, r.BuildKey); err == nil {
				builds[r.BuildKey] = b
			}
		}
	}
	return rec, builds, nil
}

// SetInfo describes a set for bam set list and show.
type SetInfo struct {
	Name      string
	Timeout   time.Duration
	Stages    [][]SetRunInfo
	DefinedIn []string
}

// SetRunInfo is one run of a described set with its set vars, safe to
// display.
type SetRunInfo struct {
	Target     string
	Vars       []TargetVar
	Matrix     any      // the configured matrix for JSON: map of value lists, or list of variable maps; nil without one
	MatrixLine string   // the matrix on one line, for text; empty without one
	Runs       []string // the run names it expands to
}

// DescribeSets returns every set, sorted by name.
func DescribeSets(cfg *config.Config, getenv func(string) string) ([]SetInfo, error) {
	all, err := cfg.Sets()
	if err != nil {
		return nil, err
	}
	out := make([]SetInfo, 0, len(all))
	for _, name := range sortedNames(all) {
		out = append(out, describeSet(all[name], getenv))
	}
	return out, nil
}

// DescribeSet returns one set.
func DescribeSet(cfg *config.Config, getenv func(string) string, name string) (SetInfo, error) {
	s, ok, err := cfg.Set(name)
	if err != nil {
		return SetInfo{}, err
	}
	if !ok {
		return SetInfo{}, errs.Usagef("unknown set %q", name).WithTry("bam set list")
	}
	return describeSet(s, getenv), nil
}

func describeSet(s config.ResolvedSet, getenv func(string) string) SetInfo {
	info := SetInfo{Name: s.Name, Timeout: time.Duration(s.Timeout), DefinedIn: s.DefinedIn}
	for _, st := range s.Stages {
		runs := make([]SetRunInfo, 0, len(st))
		for _, r := range st {
			info := SetRunInfo{Target: r.Target, Vars: targetVars(r.Vars, getenv)}
			if r.Matrix != nil {
				m := maskedMatrix(r.Matrix)
				info.MatrixLine = matrixLine(m)
				if m.Sets != nil {
					info.Matrix = m.Sets
				} else {
					info.Matrix = m.Product
				}
			}
			for _, x := range r.Expand() {
				info.Runs = append(info.Runs, x.Name)
			}
			runs = append(runs, info)
		}
		info.Stages = append(info.Stages, runs)
	}
	return info
}

// matrixLine is a matrix on one line: a product's value lists, or how many
// variable sets a list holds.
func matrixLine(m *config.Matrix) string {
	if m.Sets != nil {
		if len(m.Sets) == 1 {
			return "1 variable set"
		}
		return fmt.Sprintf("%d variable sets", len(m.Sets))
	}
	keys := sortedNames(m.Product)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=[" + strings.Join(m.Product[k], " ") + "]"
	}
	return strings.Join(parts, " ")
}

// maskedMatrix is a copy of m safe to display: a secret-looking variable's
// literal values are masked, ${ENV} references stay as written. A machine
// file may hold literal secrets; only the project file refuses them.
func maskedMatrix(m *config.Matrix) *config.Matrix {
	mask := func(name, v string) string {
		if _, ref := config.EnvRef(v); ref || !config.IsMaskedName(name) {
			return v
		}
		return MaskedDisplay
	}
	out := &config.Matrix{}
	if m.Sets != nil {
		for _, e := range m.Sets {
			c := config.StringMap{}
			for k, v := range e {
				c[k] = mask(k, v)
			}
			out.Sets = append(out.Sets, c)
		}
		return out
	}
	out.Product = map[string][]string{}
	for k, vs := range m.Product {
		c := make([]string, len(vs))
		for i, v := range vs {
			c[i] = mask(k, v)
		}
		out.Product[k] = c
	}
	return out
}

// LatestSetRun is this repository's newest record of set.
func (s *Service) LatestSetRun(set string) (SetRecord, bool, error) {
	return s.Sets.Latest(s.Cfg.RepoRoot, set, nil)
}

// runAgain is how to start a set over: by name, or from its train file.
func runAgain(p SetPlan, set string) string {
	if p.File != "" {
		return "bam set run -f " + p.File
	}
	return "bam set run " + set
}
