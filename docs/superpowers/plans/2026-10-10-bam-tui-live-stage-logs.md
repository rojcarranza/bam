# Live stage logs in the terminal UI: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the terminal UI, `l` on a stage (or on a running build) streams every job of that stage live, moves on stage by stage, and `/` search highlights and counts matches as lines arrive.

**Architecture:** A new use case `app.Service.FollowLogs` follows several jobs with one build poll per tick and emits job-tagged `LogEvent`s; it never prints. The TUI adapts it into a channel exactly as it adapted `FollowLog`, and every follow (single job included) goes through it. `logState` gains per-job offsets, an explicit `pinned` flag, live matching and render-time highlighting.

**Tech Stack:** Go 1.26, Bubble Tea / bubbles viewport / lipgloss (already in the module), testify.

**Spec:** [`docs/superpowers/specs/2026-10-10-bam-tui-live-stage-logs-design.md`](../specs/2026-10-10-bam-tui-live-stage-logs-design.md) (issue #53). Read it before Task 1; this plan argues from it.

## Global Constraints

- Layering (CLAUDE.md): `app` never prints, reads stdin or imports cobra/TUI; `TestAppStaysUIReady` must keep passing.
- Placeholders only: `bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`.
- No new dependencies. `maps` and `errors` are standard library.
- No test sleeps or touches the network: `fake.Provider`, the instant `fakeClock` in `app`, messages sent directly in `tui`.
- Interrupting or leaving a follow never stops a build; nothing new calls `StopBuild`.
- `bam logs` (CLI) behaviour is unchanged: `FollowLog` stays, and `Logs` without `Stage` behaves as today.
- Commits: Conventional Commits, Roj's identity only, **no** `Co-Authored-By` or other trailer.
- Before finishing: `go test -race ./...` and `make lint`. In a non-interactive shell first run `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. **`f` off and on again mid-stage** must resume without repeating lines: offsets are tracked from `LogEvent.Next` and `StageStart` (test in Task 4, `TestRefollowResumesFromTheStreamedOffsets`).
2. **A build stopped while a job is still queued** must end the follow instead of polling for ever (Task 2, `TestFollowLogsEndsWhenTheBuildStopsWithAJobQueued`).
3. **Resizing the terminal with a search active** must keep the query, matches and highlight (Task 3, `TestResizeKeepsTheSearch`).
4. **Leaving a live stage screen** must never stop the build (Task 5, `TestLeavingALiveStageNeverStopsTheBuild`).
5. **A queued build whose stage has no log yet** must open (not error) and say it is waiting (Task 5, `TestLOnAQueuedBuildWaitsForJobs`).

---

## File map

| File | Change |
| --- | --- |
| `internal/provider/fake/fake.go` | `LogGrowth`, `LogCalls` |
| `internal/app/logs.go` | `LogsOptions.Stage`, `CurrentStage`, `LogEvent*`, `FollowLogs` and helpers |
| `internal/view/tui/theme.go` | `matchStyle` |
| `internal/view/tui/logs.go` | `logState` fields, pinning, live matching, highlight, header, waiting line, `apply` |
| `internal/view/tui/msgs.go` | `logsLoadedMsg` offsets/stage/live, `composeLogs`, `logsMsg`, `stageLogsCmd`, `followCmd`/`drainFollowCmd` on `app.LogEvent`, `logEventMsg` |
| `internal/view/tui/model.go` | follow fields, `startFollow`, `toggleFollow`, `logEventMsg` handling, `openSelectionLogs`, `openStageLogs`, key handling |
| `internal/view/tui/keys.go` | help text |
| tests | `fake_test.go`, `app/logs_test.go`, `tui/logs_test.go`, `tui/stale_test.go` |
| `CHANGELOG.md`, navigate spec | Task 6 |

---

### Task 1: Stage logs and the current stage in `app`

**Files:**
- Modify: `internal/provider/fake/fake.go`
- Modify: `internal/app/logs.go`
- Test: `internal/provider/fake/fake_test.go`, `internal/app/logs_test.go`

**Interfaces:**
- Produces: `fake.Provider.LogGrowth map[string][][]string`, `(*fake.Provider).LogCalls(key string) int`; `app.LogsOptions.Stage string`; `app.CurrentStage(b provider.Build) (string, bool)`.

- [ ] **Step 1: Write the failing fake test** (append to `internal/provider/fake/fake_test.go`; add imports `context`, `provider` if missing)

```go
func TestLogGrowthWalksSnapshotsAndCountsCalls(t *testing.T) {
	f := &Provider{LogGrowth: map[string][][]string{"PROJ-P-J-1": {{"a"}, {"a", "b"}}}}
	ctx := context.Background()

	c, err := f.FetchLog(ctx, "PROJ-P-J-1", provider.LogOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"a"}, c.Lines)

	c, err = f.FetchLog(ctx, "PROJ-P-J-1", provider.LogOptions{Offset: c.Next})
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, c.Lines)

	c, err = f.FetchLog(ctx, "PROJ-P-J-1", provider.LogOptions{Offset: c.Next})
	require.NoError(t, err)
	require.Empty(t, c.Lines, "the last snapshot repeats")
	require.Equal(t, 3, f.LogCalls("PROJ-P-J-1"))
	require.Equal(t, 0, f.LogCalls("PROJ-P-OTHER-1"))
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/provider/fake/ -run TestLogGrowth -v`
Expected: FAIL to compile, `unknown field LogGrowth`.

- [ ] **Step 3: Implement in `fake.go`**

Add next to `Logs`:

```go
	// LogGrowth is a log that grows: the n-th FetchLog of a key sees the n-th
	// snapshot of the whole log, the last one repeating. It wins over Logs.
	LogGrowth map[string][][]string // by job result key
```

Add `logCalls map[string]int` to the unexported counters, and replace `FetchLog`:

```go
func (f *Provider) FetchLog(_ context.Context, key string, o provider.LogOptions) (provider.LogChunk, error) {
	f.mu.Lock()
	if f.logCalls == nil {
		f.logCalls = map[string]int{}
	}
	n := f.logCalls[key]
	f.logCalls[key] = n + 1
	if len(f.LogErrs) > 0 {
		err := f.LogErrs[0]
		f.LogErrs = f.LogErrs[1:]
		if err != nil {
			f.mu.Unlock()
			return provider.LogChunk{}, err
		}
	}
	lines, ok := f.Logs[key]
	if seq := f.LogGrowth[key]; len(seq) > 0 {
		if n >= len(seq) {
			n = len(seq) - 1
		}
		lines, ok = seq[n], true
	}
	f.mu.Unlock()
	if !ok {
		return provider.LogChunk{}, notFound("job", key)
	}
	if o.Offset > len(lines) {
		o.Offset = len(lines)
	}
	return provider.LogChunk{Lines: lines[o.Offset:], Next: len(lines)}, nil
}

// LogCalls reports how many times FetchLog was called for key.
func (f *Provider) LogCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logCalls[key]
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/provider/fake/ -v`
Expected: PASS.

- [ ] **Step 5: Write the failing `app` tests** (append to `internal/app/logs_test.go`)

```go
func TestLogsOfOneStageWinOverFailed(t *testing.T) {
	s := newService(t, logsProvider())
	logs, err := s.Logs(bg, failedBuild(), LogsOptions{Stage: "Checkout", Failed: true})
	require.NoError(t, err)
	require.Len(t, logs, 1, "the stage's jobs, not only failed ones")
	assert.Equal(t, "Checkout", logs[0].Stage)
	assert.Equal(t, []string{"c1"}, logs[0].Lines)
	assert.Equal(t, 1, logs[0].Next)
}

func TestLogsOfAnUnknownStage(t *testing.T) {
	s := newService(t, logsProvider())
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Stage: "Nope"})
	require.Error(t, err)
	assert.Equal(t, errs.KindUsage, errs.KindOf(err))
	assert.Contains(t, err.Error(), "stages: Checkout, Test")
}

// TestLogsOfARunningStageToleratesJobsWithNoLogYet, spec §3.1.
func TestLogsOfARunningStageToleratesJobsWithNoLogYet(t *testing.T) {
	p := logsProvider()
	delete(p.Logs, "PROJ-BUILD12-UNIT-44") // running, but Bamboo has no log yet
	b := failedBuild()
	b.State = provider.StateRunning
	b.Stages[1].Jobs[0].State = provider.StateRunning
	b.Stages[1].Jobs[1].State = provider.StateQueued
	s := newService(t, p)

	logs, err := s.Logs(bg, b, LogsOptions{Stage: "Test"})
	require.NoError(t, err)
	require.Len(t, logs, 2)
	for _, l := range logs {
		assert.Empty(t, l.Lines, l.Job.Name)
		assert.Zero(t, l.Next, l.Job.Name)
	}
	assert.Zero(t, p.LogCalls("PROJ-BUILD12-INT-44"), "a queued job is not fetched")
}

func TestLogsWithoutAStageStillFailOnAMissingLog(t *testing.T) {
	p := logsProvider()
	delete(p.Logs, "PROJ-BUILD12-UNIT-44")
	s := newService(t, p)
	_, err := s.Logs(bg, failedBuild(), LogsOptions{Failed: true})
	require.ErrorIs(t, err, errs.ErrNotFound, "bam logs is unchanged")
}

func TestCurrentStage(t *testing.T) {
	st := func(name string, s provider.State) provider.Stage { return provider.Stage{Name: name, State: s} }
	for _, tc := range []struct {
		name   string
		stages []provider.Stage
		want   string
		ok     bool
	}{
		{"running wins", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateQueued), st("C", provider.StateRunning)}, "C", true},
		{"first not finished", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateQueued)}, "B", true},
		{"skipped is passed over", []provider.Stage{st("A", provider.StateSkipped), st("B", provider.StateQueued)}, "B", true},
		{"all finished", []provider.Stage{st("A", provider.StateSuccess), st("B", provider.StateNotBuilt)}, "", false},
		{"no stages", nil, "", false},
	} {
		got, ok := CurrentStage(provider.Build{Stages: tc.stages})
		assert.Equal(t, tc.want, got, tc.name)
		assert.Equal(t, tc.ok, ok, tc.name)
	}
}
```

- [ ] **Step 6: Run them to verify they fail**

Run: `go test ./internal/app/ -run 'TestLogsOf|TestLogsWithout|TestCurrentStage' -v`
Expected: FAIL to compile, `unknown field Stage`, `undefined: CurrentStage`.

- [ ] **Step 7: Implement in `internal/app/logs.go`**

Add `"errors"` to the imports. Extend `LogsOptions`:

```go
type LogsOptions struct {
	Failed bool   // only failed jobs
	Job    string // JOB1, PROJ-PLAN-JOB1 or PROJ-PLAN-JOB1-44
	Stage  string // one stage's jobs, read while they run; wins over Failed
	Tail   int    // last N lines per job; 0 = all
}
```

In `selectJobs`, after the `if o.Job != "" { ... }` block and before `if !o.Failed`:

```go
	if o.Stage != "" {
		names := make([]string, 0, len(b.Stages))
		for _, st := range b.Stages {
			if st.Name == o.Stage {
				out := make([]stagedJob, 0, len(st.Jobs))
				for _, j := range st.Jobs {
					out = append(out, stagedJob{st.Name, j})
				}
				return out, nil
			}
			names = append(names, st.Name)
		}
		return nil, errs.Usagef("stage %q is not in %s", o.Stage, b.Key).WithWhy("stages: " + strings.Join(names, ", "))
	}
```

In `Logs`, replace the loop body's fetch:

```go
	for _, sj := range jobs {
		// A stage is read while it runs: a job that has not started has no
		// log, and a running one may not have one yet. Only then; bam logs
		// keeps reporting a missing log.
		if o.Stage != "" && notStarted(sj.job.State) {
			out = append(out, JobLog{Job: sj.job, Stage: sj.stage})
			continue
		}
		chunk, err := s.P.FetchLog(ctx, sj.job.Key, provider.LogOptions{})
		if err != nil {
			if o.Stage == "" || !errors.Is(err, errs.ErrNotFound) {
				return nil, err
			}
			chunk = provider.LogChunk{}
		}
		lines := chunk.Lines
		if o.Tail > 0 && len(lines) > o.Tail {
			lines = lines[len(lines)-o.Tail:]
		}
		out = append(out, JobLog{Job: sj.job, Stage: sj.stage, Lines: lines, Next: chunk.Next})
	}
```

Add below `Logs`:

```go
func notStarted(s provider.State) bool {
	return s == provider.StateQueued || s == provider.StateSkipped || s == provider.StateNotBuilt
}

// CurrentStage names the stage a live log shows: the first running stage, or
// else the first that has neither finished nor been skipped.
func CurrentStage(b provider.Build) (string, bool) {
	for _, st := range b.Stages {
		if st.State == provider.StateRunning {
			return st.Name, true
		}
	}
	for _, st := range b.Stages {
		if !st.State.Finished() && st.State != provider.StateSkipped {
			return st.Name, true
		}
	}
	return "", false
}
```

- [ ] **Step 8: Run the package to verify it passes**

Run: `go test ./internal/app/ ./internal/provider/fake/`
Expected: PASS (existing log tests included).

- [ ] **Step 9: Commit**

```bash
git add internal/provider/fake/ internal/app/logs.go internal/app/logs_test.go
git commit -m "feat: read one stage's logs while it runs, and name a build's current stage"
```

---

### Task 2: `app.FollowLogs`

**Files:**
- Modify: `internal/app/logs.go`
- Test: `internal/app/logs_test.go`

**Interfaces:**
- Consumes: `fake.Provider.LogGrowth`, `LogCalls` (Task 1).
- Produces:

```go
type LogEventKind int
const ( LogLines LogEventKind = iota; StageStart; LogStatus )
type LogEvent struct {
	Kind  LogEventKind
	Stage string
	Job   provider.Job   // LogLines
	Lines []string       // LogLines
	Next  int            // LogLines: the job's offset after these lines
	Build provider.Build // LogStatus, StageStart
}
func (s *Service) FollowLogs(ctx context.Context, buildKey string, from map[string]int, advance bool, emit func(LogEvent)) error
```

- [ ] **Step 1: Write the failing tests** (append to `internal/app/logs_test.go`; add `"context"` and `"strings"` to its imports)

```go
const liveKey = "PROJ-BUILD12-45"

func liveJob(short string, st provider.State) provider.Job {
	return provider.Job{Key: "PROJ-BUILD12-" + short + "-45", Name: short, State: st}
}

func liveStage(name string, st provider.State, jobs ...provider.Job) provider.Stage {
	return provider.Stage{Name: name, State: st, Jobs: jobs}
}

func liveBuild(st provider.State, stages ...provider.Stage) provider.Build {
	return provider.Build{Key: liveKey, PlanKey: "PROJ-BUILD12", Number: 45, State: st, Stages: stages}
}

func liveJobKey(short string) string { return "PROJ-BUILD12-" + short + "-45" }

func recordTo(evs *[]LogEvent) func(LogEvent) {
	return func(e LogEvent) { *evs = append(*evs, e) }
}

// logTrail flattens events to one readable string each.
func logTrail(evs []LogEvent) []string {
	var out []string
	for _, e := range evs {
		switch e.Kind {
		case LogLines:
			for _, l := range e.Lines {
				out = append(out, e.Job.Name+" "+l)
			}
		case StageStart:
			out = append(out, "stage "+e.Stage)
		case LogStatus:
			out = append(out, "status "+e.Stage+" "+string(stageState(e.Build, e.Stage)))
		}
	}
	return out
}

const (
	stRun  = provider.StateRunning
	stOK   = provider.StateSuccess
	stQue  = provider.StateQueued
	stFail = provider.StateFailed
)

// TestFollowLogsInterleavesJobsWithOneBuildPollPerTick, spec §3.2.
func TestFollowLogsInterleavesJobsWithOneBuildPollPerTick(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun), liveJob("INT", stRun), liveJob("E2E", stRun))),
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stOK), liveJob("INT", stRun), liveJob("E2E", stRun))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK), liveJob("INT", stOK), liveJob("E2E", stOK))),
	}}
	p.LogGrowth = map[string][][]string{
		liveJobKey("UNIT"): {{"u1"}, {"u1", "u2"}},
		liveJobKey("INT"):  {{"i1"}, {"i1", "i2"}, {"i1", "i2", "i3"}},
	}
	p.Logs = map[string][]string{liveJobKey("E2E"): {"e1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0, liveJobKey("INT"): 0, liveJobKey("E2E"): 0}, false, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"status Test running", "UNIT u1", "INT i1", "E2E e1",
		"UNIT u2", "INT i2",
		"status Test success", "INT i3",
	}, logTrail(evs))
	waits := s.Clock.(*fakeClock).Waits()
	assert.Equal(t, []time.Duration{MinPoll, MinPoll}, waits)
	assert.Equal(t, len(waits)+1, p.GetCalls(liveKey), "one build request per tick, however many jobs")
	last := evs[len(evs)-1]
	assert.Equal(t, 3, last.Next, "LogLines carries the job's offset")
}

func TestFollowLogsAdvancesToTheNextStage(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Build", stRun, liveJob("JOB1", stRun)), liveStage("Test", stQue, liveJob("UNIT", stQue))),
		liveBuild(stRun, liveStage("Build", stOK, liveJob("JOB1", stOK)), liveStage("Test", stRun, liveJob("UNIT", stRun))),
		liveBuild(stOK, liveStage("Build", stOK, liveJob("JOB1", stOK)), liveStage("Test", stOK, liveJob("UNIT", stOK))),
	}}
	p.LogGrowth = map[string][][]string{
		liveJobKey("JOB1"): {{"c1"}, {"c1", "c2"}},
		liveJobKey("UNIT"): {{"u1"}, {"u1", "u2"}},
	}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("JOB1"): 0}, true, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{
		"status Build running", "JOB1 c1",
		"status Build success", "JOB1 c2", "stage Test", "UNIT u1",
		"status Test success", "UNIT u2",
	}, logTrail(evs))
}

func TestFollowLogsStopsWhenAStageFailsTheBuild(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)), liveStage("Deploy", stQue, liveJob("DEP", stQue))),
		liveBuild(stFail, liveStage("Test", stFail, liveJob("UNIT", stFail)), liveStage("Deploy", provider.StateNotBuilt, liveJob("DEP", provider.StateNotBuilt))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, true, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{"status Test running", "UNIT u1", "status Test failed"}, logTrail(evs))
	assert.Zero(t, p.LogCalls(liveJobKey("DEP")))
}

func TestFollowLogsWaitsForQueuedJobsAndDropsSkippedOnes(t *testing.T) {
	p := fakeBamboo()
	skipped := provider.StateSkipped
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stQue), liveJob("SKIP", skipped))),
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun), liveJob("SKIP", skipped))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK), liveJob("SKIP", skipped))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0, liveJobKey("SKIP"): 0}, false, recordTo(&evs))
	require.NoError(t, err)
	assert.Equal(t, []string{"status Test running", "UNIT u1", "status Test success"}, logTrail(evs))
	assert.Equal(t, 2, p.LogCalls(liveJobKey("UNIT")), "not fetched while queued")
	assert.Zero(t, p.LogCalls(liveJobKey("SKIP")))
}

// TestFollowLogsEndsWhenTheBuildStopsWithAJobQueued: Review Focus 2.
func TestFollowLogsEndsWhenTheBuildStopsWithAJobQueued(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stQue))),
		liveBuild(provider.StateStopped, liveStage("Test", provider.StateStopped, liveJob("UNIT", stQue))),
	}}
	s := newService(t, p)

	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, true, func(LogEvent) {})
	require.NoError(t, err, "no log at all is not an error once the build is over")
	assert.Equal(t, 1, p.LogCalls(liveJobKey("UNIT")), "one last read, then the follow ends")
}

func TestFollowLogsTreatsNotFoundOnARunningJobAsNoLinesYet(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {
		liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun))),
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK))),
	}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	p.LogErrs = []error{errs.Bamboof("job not found").Wrap(errs.ErrNotFound)}
	s := newService(t, p)

	var evs []LogEvent
	require.NoError(t, s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, recordTo(&evs)))
	assert.Contains(t, logTrail(evs), "UNIT u1")
}

func TestFollowLogsReturnsOtherLogErrors(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))}}
	boom := errs.Bamboof("log read failed")
	p.LogErrs = []error{boom}
	s := newService(t, p)

	err := s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, func(LogEvent) {})
	require.ErrorIs(t, err, boom)
}

func TestFollowLogsReportsStatusOnlyOnAChange(t *testing.T) {
	p := fakeBamboo()
	same := liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))
	p.Sequences = map[string][]provider.Build{liveKey: {same, same, same,
		liveBuild(stOK, liveStage("Test", stOK, liveJob("UNIT", stOK)))}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)

	var evs []LogEvent
	require.NoError(t, s.FollowLogs(bg, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, recordTo(&evs)))
	var statuses []string
	for _, line := range logTrail(evs) {
		if strings.HasPrefix(line, "status") {
			statuses = append(statuses, line)
		}
	}
	assert.Equal(t, []string{"status Test running", "status Test success"}, statuses)
}

func TestFollowLogsEndsWithTheContext(t *testing.T) {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{liveKey: {liveBuild(stRun, liveStage("Test", stRun, liveJob("UNIT", stRun)))}}
	p.Logs = map[string][]string{liveJobKey("UNIT"): {"u1"}}
	s := newService(t, p)
	ctx, cancel := context.WithCancel(bg)
	defer cancel()

	err := s.FollowLogs(ctx, liveKey, map[string]int{liveJobKey("UNIT"): 0}, false, func(e LogEvent) {
		if e.Kind == LogLines {
			cancel()
		}
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, s.Clock.(*fakeClock).Waits(), "a cancelled follow does not wait another tick")
	assert.Empty(t, p.Stopped, "ending a follow never stops the build")
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/app/ -run TestFollowLogs -v`
Expected: FAIL to compile, `undefined: LogEvent`, `s.FollowLogs undefined`.

- [ ] **Step 3: Implement in `internal/app/logs.go`** (add `"maps"` to the imports; place after `FollowLog`)

```go
// LogEventKind tells a FollowLogs consumer what a LogEvent carries.
type LogEventKind int

const (
	LogLines   LogEventKind = iota // new lines of one job
	StageStart                     // following moved on to a new stage
	LogStatus                      // the followed stage's or the build's state changed
)

// LogEvent is one thing FollowLogs saw.
type LogEvent struct {
	Kind  LogEventKind
	Stage string         // the stage the event belongs to
	Job   provider.Job   // LogLines only
	Lines []string       // LogLines only
	Next  int            // LogLines only: the job's offset after these lines
	Build provider.Build // LogStatus and StageStart: the poll's snapshot
}

// FollowLogs emits new log lines of the jobs in from (job result key →
// offset) until every one of them has finished. With advance set, while the
// build is still running it then follows the next stage's jobs from offset 0,
// emitting StageStart first. It polls the build once per tick, however many
// jobs it follows, and never stops the build.
func (s *Service) FollowLogs(ctx context.Context, buildKey string, from map[string]int, advance bool, emit func(LogEvent)) error {
	offsets := maps.Clone(from)
	if offsets == nil {
		offsets = map[string]int{}
	}
	stage := ""
	var lastStage, lastBuild provider.State
	first := true
	for {
		b, err := s.Build(ctx, buildKey)
		if err != nil {
			return err
		}
		if stage == "" {
			stage = followedStage(b, offsets)
		}
		if st := stageState(b, stage); first || st != lastStage || b.State != lastBuild {
			emit(LogEvent{Kind: LogStatus, Stage: stage, Build: b})
			first, lastStage, lastBuild = false, st, b.State
		}
		for {
			if err := s.readFollowed(ctx, b, offsets, emit); err != nil {
				return err
			}
			if len(offsets) > 0 {
				break
			}
			if !advance || stage == "" || b.State.Finished() {
				return nil
			}
			next, ok := stageAfter(b, stage)
			if !ok {
				return nil
			}
			stage, lastStage = next.Name, next.State
			emit(LogEvent{Kind: StageStart, Stage: stage, Build: b})
			for _, j := range next.Jobs {
				offsets[j.Key] = 0
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.Clock.After(MinPoll):
		}
	}
}

// readFollowed reads every followed job once, in stage order, and drops the
// jobs whose log will not grow again.
func (s *Service) readFollowed(ctx context.Context, b provider.Build, offsets map[string]int, emit func(LogEvent)) error {
	seen := make(map[string]bool, len(offsets))
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			off, ok := offsets[j.Key]
			if !ok {
				continue
			}
			seen[j.Key] = true
			if j.State == provider.StateSkipped || j.State == provider.StateNotBuilt {
				delete(offsets, j.Key) // it will never have a log
				continue
			}
			if j.State == provider.StateQueued && !b.State.Finished() {
				continue
			}
			chunk, err := s.P.FetchLog(ctx, j.Key, provider.LogOptions{Offset: off})
			if err != nil {
				if !errors.Is(err, errs.ErrNotFound) {
					return err
				}
				chunk = provider.LogChunk{Next: off} // no log yet
			}
			if len(chunk.Lines) > 0 {
				emit(LogEvent{Kind: LogLines, Stage: st.Name, Job: j, Lines: chunk.Lines, Next: chunk.Next})
			}
			offsets[j.Key] = chunk.Next
			// A finished job, or any job of a finished build, has had its
			// last read. The second case ends a build stopped before some
			// jobs ran.
			if j.State.Finished() || b.State.Finished() {
				delete(offsets, j.Key)
			}
		}
	}
	for k := range offsets {
		if !seen[k] {
			delete(offsets, k)
		}
	}
	return nil
}

// followedStage is the last stage, in build order, holding a followed job.
func followedStage(b provider.Build, offsets map[string]int) string {
	name := ""
	for _, st := range b.Stages {
		for _, j := range st.Jobs {
			if _, ok := offsets[j.Key]; ok {
				name = st.Name
			}
		}
	}
	return name
}

func stageState(b provider.Build, name string) provider.State {
	for _, st := range b.Stages {
		if st.Name == name {
			return st.State
		}
	}
	return ""
}

func stageAfter(b provider.Build, name string) (provider.Stage, bool) {
	for i, st := range b.Stages {
		if st.Name == name && i+1 < len(b.Stages) {
			return b.Stages[i+1], true
		}
	}
	return provider.Stage{}, false
}
```

- [ ] **Step 4: Run them to verify they pass**

Run: `go test ./internal/app/ -run 'TestFollowLogs|TestAppStaysUIReady' -v`
Expected: PASS.

- [ ] **Step 5: Run the package with the race detector**

Run: `go test -race ./internal/app/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/app/logs.go internal/app/logs_test.go
git commit -m "feat: follow several jobs' logs stage by stage with one build poll per tick"
```

---

### Task 3: Log screen search, pinning and highlighting

**Files:**
- Modify: `internal/view/tui/theme.go`, `internal/view/tui/logs.go`, `internal/view/tui/model.go` (`handleLogKey`, `handleInputKey`)
- Test: `internal/view/tui/logs_test.go`

**Interfaces:**
- Produces on `logState`: fields `stage string`, `live provider.State`, `offsets map[string]int`, `rendered []string`, `pinned bool`; methods `refresh()`, `rerender()`; package funcs `matchesQuery(line, q string) bool`, `highlightSpans(line, q string) [][2]int`, `highlight(line, q string) string`; const `waitingLine = "waiting for jobs to start"`. The `offset int` field is **removed** in Task 4, not here.

- [ ] **Step 1: Write the failing tests** (append to `internal/view/tui/logs_test.go`; add imports `fmt`, `github.com/charmbracelet/x/ansi`)

```go
func tallLogModel(n int) Model {
	m := logModel()
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.logs.setLines(m.width, m.logHeight(), lines)
	return m
}

func TestAChunkScrollsWhilePinned(t *testing.T) {
	m := tallLogModel(60)
	require.True(t, m.logs.pinned, "a log opens pinned to the bottom")
	m.logs.appendLines([]string{"line 60"})
	require.True(t, m.logs.vp.AtBottom())
}

// TestAJumpUnpinsAndGRepins: a match on screen must not be scrolled away by
// the next chunk, spec §4.3.
func TestAJumpUnpinsAndGRepins(t *testing.T) {
	m := tallLogModel(60)
	m.logs.search("line 05")
	require.False(t, m.logs.pinned)
	y := m.logs.vp.YOffset
	m.logs.appendLines([]string{"line 60"})
	require.Equal(t, y, m.logs.vp.YOffset, "the reader stays on the match")

	m, _ = send(m, mkKey("G"))
	require.True(t, m.logs.pinned)
	m.logs.appendLines([]string{"line 61"})
	require.True(t, m.logs.vp.AtBottom())
}

func TestAViewportKeyAtTheBottomRepins(t *testing.T) {
	m := tallLogModel(60)
	m.logs.search("line 05")
	m.logs.vp.GotoBottom()
	m, _ = send(m, mkKey("down"))
	require.True(t, m.logs.pinned)
	m, _ = send(m, mkKey("up"))
	require.False(t, m.logs.pinned)
}

func TestASearchCountsLinesThatArriveLater(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	require.Equal(t, []int{1, 2}, m.logs.matches)
	m.logs.appendLines([]string{"12:07:09  [ERROR] again"})
	require.Equal(t, []int{1, 2, 4}, m.logs.matches)
	require.Equal(t, 0, m.logs.match, "the current match does not move")
	require.Contains(t, m.View(), "match 1/3")
}

func TestEscInTheSearchInputClearsTheSearch(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	m, _ = send(m, mkKey("/"))
	m, _ = send(m, mkKey("esc"))
	require.Empty(t, m.logs.query)
	require.Empty(t, m.logs.matches)
	require.NotContains(t, m.View(), "match")
}

func TestKeyLineSaysWhatNDoes(t *testing.T) {
	m := logModel()
	require.Contains(t, m.View(), "n next failure")
	m.logs.search("error")
	require.Contains(t, m.View(), "n next match")
}

func TestHighlightSpans(t *testing.T) {
	require.Equal(t, [][2]int{{3, 8}, {16, 21}}, highlightSpans("an Error and an ERROR", "error"))
	line := "ÉCHEC du test"
	require.Equal(t, [][2]int{{0, len(line)}}, highlightSpans(line, "échec"),
		"a non-ASCII case match highlights the whole line")
	require.Nil(t, highlightSpans("nothing here", "zzz"))
	require.Nil(t, highlightSpans("anything", ""))
}

func TestHighlightLeavesTheTextIntact(t *testing.T) {
	line := "12:06:58  [ERROR] ConnectionRefused"
	require.Equal(t, line, ansi.Strip(highlight(line, "error")))
}

// TestResizeKeepsTheSearch: Review Focus 3.
func TestResizeKeepsTheSearch(t *testing.T) {
	m := logModel()
	m.logs.search("error")
	m.logs.resize(100, 30)
	require.Equal(t, "error", m.logs.query)
	require.Equal(t, []int{1, 2}, m.logs.matches)
	require.Len(t, m.logs.rendered, len(m.logs.lines))
}

func TestAnEmptyFollowedLogSaysItIsWaiting(t *testing.T) {
	m := logModel()
	m.logs.setLines(m.width, m.logHeight(), nil)
	m.logs.following = true
	m.logs.refresh()
	require.Contains(t, m.View(), waitingLine)
	require.Empty(t, m.logs.lines, "the waiting line is not part of the log")
	m.logs.search("waiting")
	require.Empty(t, m.logs.matches)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'Pinned|Repins|Unpins|LinesThatArrive|ClearsTheSearch|WhatNDoes|Highlight|ResizeKeeps|Waiting' -v`
Expected: FAIL to compile, `m.logs.pinned undefined`, `undefined: highlightSpans`.

- [ ] **Step 3: Add `matchStyle` to `theme.go`** inside the existing `var ( ... )` block:

```go
	// matchStyle marks a search match in the log. Reverse video reads on any
	// palette and needs no colour of its own.
	matchStyle = lipgloss.NewStyle().Reverse(true)
```

- [ ] **Step 4: Rewrite the state and helpers in `logs.go`**

Replace the `logState` struct, `setLines`, `resize`, `appendLines`, `search` and `goTo` with the following, and add `"github.com/r0jjames/bam-cli/internal/view/style"` to the imports (keep `offset int` for now; Task 4 removes it):

```go
// logState is the log screen. It keeps the raw lines as well as the viewport,
// so a search can find a match the viewport has scrolled past.
type logState struct {
	vp        viewport.Model
	title     string
	jobKey    string
	url       string
	all       bool
	multi     bool            // several jobs: every line carries its job's prefix
	stage     string          // the stage a stage screen follows; empty otherwise
	live      provider.State  // the followed stage's (or build's) state; empty when not live
	build     *provider.Build // the build the logs came from, with its jobs
	offset    int             // the provider's next-read offset for the followed job
	offsets   map[string]int  // the provider's next-read offset per job on screen
	lines     []string
	rendered  []string // lines with the search highlighted, one per line
	query     string
	matches   []int
	match     int
	line      int // the line search and next-failure last landed on
	following bool
	pinned    bool // the view stays at the bottom as lines arrive
	ready     bool
}

// waitingLine fills a followed log that has no line yet. It is drawn, not
// stored, so search never matches it.
const waitingLine = "waiting for jobs to start"

func (l *logState) setLines(width, height int, lines []string) {
	if !l.ready {
		l.vp = viewport.New(width, height)
		l.ready = true
	}
	l.vp.Width, l.vp.Height = width, height
	l.lines = lines
	l.rerender()
	l.pinned = true
	l.vp.GotoBottom()
}

// resize reflows an already-loaded log for a new terminal size.
func (l *logState) resize(width, height int) {
	if !l.ready {
		return
	}
	l.vp.Width, l.vp.Height = width, height
	l.refresh()
}

// rerender highlights every line again, for a new query or a new log.
func (l *logState) rerender() {
	l.rendered = make([]string, len(l.lines))
	for i, line := range l.lines {
		l.rendered[i] = highlight(line, l.query)
	}
	l.refresh()
}

// refresh hands the rendered lines to the viewport.
func (l *logState) refresh() {
	if !l.ready {
		return
	}
	if len(l.lines) == 0 && l.following {
		l.vp.SetContent(dimStyle.Render(waitingLine))
		return
	}
	l.vp.SetContent(strings.Join(l.rendered, "\n"))
}

// appendLines adds streamed lines, matches them against the search, and
// keeps the view at the bottom only while it is pinned.
func (l *logState) appendLines(more []string) {
	start := len(l.lines)
	l.lines = append(l.lines, more...)
	for i, line := range more {
		l.rendered = append(l.rendered, highlight(line, l.query))
		if l.query != "" && matchesQuery(line, l.query) {
			l.matches = append(l.matches, start+i)
		}
	}
	l.refresh()
	if l.pinned {
		l.vp.GotoBottom()
	}
}
```

```go
// search records every matching line and jumps to the first one. Matching is
// case-insensitive substring, the same rule the panel filters use.
func (l *logState) search(q string) {
	l.query = q
	l.matches = l.matches[:0]
	l.match = 0
	if q != "" {
		for i, line := range l.lines {
			if matchesQuery(line, q) {
				l.matches = append(l.matches, i)
			}
		}
	}
	l.rerender()
	l.jumpToMatch()
}

func matchesQuery(line, q string) bool {
	return strings.Contains(strings.ToLower(line), strings.ToLower(q))
}
```

```go
// goTo records the line landed on and scrolls to it. The line is tracked
// separately because the viewport clamps its offset when the log is shorter
// than the screen, and a search must still know where it is. Landing anywhere
// unpins: the next chunk must not scroll the reader away from it.
func (l *logState) goTo(i int) {
	l.line = i
	l.vp.SetYOffset(i)
	l.pinned = false
}
```

Add at the end of the file:

```go
// highlightSpans is where q appears in line, as byte ranges of line. They are
// found with an ASCII-only case fold, which keeps the offsets valid for line
// itself. When the matching rule finds the line but the fold cannot place it
// (a non-ASCII case difference), the whole line is the span.
func highlightSpans(line, q string) [][2]int {
	if q == "" || !matchesQuery(line, q) {
		return nil
	}
	hay, needle := asciiLower(line), asciiLower(q)
	var out [][2]int
	for from := 0; ; {
		i := strings.Index(hay[from:], needle)
		if i < 0 {
			break
		}
		out = append(out, [2]int{from + i, from + i + len(needle)})
		from += i + len(needle)
	}
	if len(out) == 0 {
		return [][2]int{{0, len(line)}}
	}
	return out
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// highlight draws line with every match of q in matchStyle.
func highlight(line, q string) string {
	spans := highlightSpans(line, q)
	if len(spans) == 0 {
		return line
	}
	var sb strings.Builder
	last := 0
	for _, s := range spans {
		sb.WriteString(line[last:s[0]])
		sb.WriteString(matchStyle.Render(line[s[0]:s[1]]))
		last = s[1]
	}
	sb.WriteString(line[last:])
	return sb.String()
}
```

- [ ] **Step 5: Rewrite the header and key line in `logsView`**

Replace from `source := "failed"` down to the `foot := ...` line with:

```go
	pos := fmt.Sprintf("%d/%d", shown, len(m.logs.lines))
	parts := []string{m.logs.title}
	if m.logs.stage != "" {
		parts = append(parts, m.logs.stage)
	} else {
		source := "failed"
		if m.logs.all {
			source = "all"
		}
		parts = append(parts, "logs", source)
	}
	if m.logs.live != "" {
		parts = append(parts, style.Label(m.logs.live))
	}
	if m.logs.query != "" {
		cur := 0
		if len(m.logs.matches) > 0 {
			cur = m.logs.match + 1
		}
		parts = append(parts, fmt.Sprintf("match %d/%d", cur, len(m.logs.matches)))
	}
	title := truncate(strings.Join(parts, " · "), m.width-lipgloss.Width(pos)-2)
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(pos)
	if gap < 1 {
		gap = 1
	}
	head := titleStyle.Render(title) + strings.Repeat(" ", gap) + dimStyle.Render(pos)

	nKey := "n next failure"
	if m.logs.query != "" {
		nKey = "n next match"
	}
	keyLine := "esc back  / search  G end  f follow  " + nKey + "  o open"
	if m.logs.following {
		keyLine = "following · " + keyLine
	}
	foot := truncate(statusStyle.Render(keyLine), m.width)
```

Keep the `shown` computation above it and the input/error overrides of `foot` below it.

- [ ] **Step 6: Wire the keys in `model.go`**

In `handleLogKey`, replace the `Bottom` and `Top` cases and the fall-through:

```go
	case key.Matches(msg, keys.Bottom):
		m.logs.vp.GotoBottom()
		m.logs.pinned = true
		return m, nil
	case key.Matches(msg, keys.Top):
		m.logs.vp.GotoTop()
		m.logs.pinned = m.logs.vp.AtBottom()
		return m, nil
```

```go
	var cmd tea.Cmd
	m.logs.vp, cmd = m.logs.vp.Update(msg)
	m.logs.pinned = m.logs.vp.AtBottom()
	return m, cmd
```

In `handleInputKey`, the `tea.KeyEsc` case becomes:

```go
	case tea.KeyEsc:
		if m.inputFor == inputSearch {
			m.logs.search("") // esc drops the search and its highlight
		}
		m.inputFor = inputNone
		m.input.SetValue("")
		m.input.Blur()
		return m, nil
```

- [ ] **Step 7: Run the package to verify it passes**

Run: `go test ./internal/view/tui/`
Expected: PASS, including `TestLogGolden` unchanged (no query, so the header and key line render as before). If `TestLogGolden` fails, diff it; the only acceptable difference is none.

- [ ] **Step 8: Commit**

```bash
git add internal/view/tui/theme.go internal/view/tui/logs.go internal/view/tui/model.go internal/view/tui/logs_test.go
git commit -m "feat(tui): highlight and count log matches as lines arrive, and keep the reader on a match"
```

---

### Task 4: Follow through `FollowLogs`, with job prefixes

**Files:**
- Modify: `internal/view/tui/msgs.go`, `internal/view/tui/model.go`, `internal/view/tui/logs.go`
- Test: `internal/view/tui/logs_test.go`, `internal/view/tui/stale_test.go`

**Interfaces:**
- Consumes: `app.FollowLogs`, `app.LogEvent`, `app.LogLines`, `app.StageStart`, `app.LogStatus` (Task 2); `logState` fields from Task 3.
- Produces: `logsLoadedMsg{..., Stage string, Live bool, Offsets map[string]int}` (field `Offset` removed); `composeLogs(b provider.Build, jobs []app.JobLog, multi bool, stage string) []string`; `logsMsg(b provider.Build, jobs []app.JobLog, all bool, stage string, gen int) logsLoadedMsg`; `logEventMsg{Gen int; Event app.LogEvent}` (replaces `logChunkMsg`); `followCmd(ctx, svc, buildKey string, from map[string]int, advance bool) (<-chan app.LogEvent, <-chan error)`; `drainFollowCmd(events <-chan app.LogEvent, done <-chan error, gen int) tea.Cmd`; `(Model).startFollow() (tea.Model, tea.Cmd)`; `(*logState).apply(ev app.LogEvent)`; `Model.followFrom map[string]int`; `Model.followEvents <-chan app.LogEvent`.

- [ ] **Step 1: Update the existing tests to the new shapes**

In `logs_test.go`, `logModel` sends offsets:

```go
	m, _ = send(m, logsLoadedMsg{Gen: m.logsGen, JobKey: "PROJ-BUILD-INT-44", Title: "PROJ-BUILD-INT-44",
		URL: labOrigin + "/browse/PROJ-BUILD-INT-44", Lines: logLines(),
		Offsets: map[string]int{"PROJ-BUILD-INT-44": 4}})
```

Replace `TestDrainFollowDeliversOneChunkPerCall`, `TestLogChunkAppendsAndStaysPinnedToTheBottom`, `TestFollowingAJobThatIsNotInTheBuildDoesNothing`, `TestFollowStartsFromTheProviderOffsetNotTheLineCount` and `TestFollowIsRefusedWhileSeveralJobsAreShown` with:

```go
func linesEvent(job string, next int, lines ...string) app.LogEvent {
	return app.LogEvent{Kind: app.LogLines, Job: provider.Job{Key: job}, Lines: lines, Next: next}
}

// TestDrainFollowDeliversOneEventPerCall, the same pattern the watch uses.
func TestDrainFollowDeliversOneEventPerCall(t *testing.T) {
	events := make(chan app.LogEvent, 2)
	done := make(chan error, 1)
	events <- linesEvent("PROJ-BUILD-INT-44", 5, "12:07:02  still running")
	events <- linesEvent("PROJ-BUILD-INT-44", 6, "12:07:05  done")
	close(events)
	done <- nil

	first := drainFollowCmd(events, done, 0)().(logEventMsg)
	require.Equal(t, []string{"12:07:02  still running"}, first.Event.Lines)
	second := drainFollowCmd(events, done, 0)().(logEventMsg)
	require.Equal(t, []string{"12:07:05  done"}, second.Event.Lines)
	end := drainFollowCmd(events, done, 0)()
	require.IsType(t, followEndedMsg{}, end)
	require.NoError(t, end.(followEndedMsg).Err)
}

func TestLogEventAppendsAndStaysPinnedToTheBottom(t *testing.T) {
	m := logModel()
	before := len(m.logs.lines)
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 5, "12:07:05  done")})
	require.Len(t, m.logs.lines, before+1)
	require.Equal(t, "12:07:05  done", m.logs.lines[before], "a single job's lines stay bare")
	require.True(t, m.logs.vp.AtBottom())
}

func TestFollowingAJobThatIsNotInTheBuildDoesNothing(t *testing.T) {
	m := logModel()
	m.logs.offsets = map[string]int{"PROJ-BUILD-GONE-44": 0}
	m, cmd := send(m, mkKey("f"))
	require.False(t, m.logs.following)
	require.Nil(t, cmd)
}

// TestFollowStartsFromTheProviderOffsets. app.JobLog.Next is the provider's
// offset contract; the rendered line count is not.
func TestFollowStartsFromTheProviderOffsets(t *testing.T) {
	m := logModel()
	m.logs.offsets = map[string]int{"PROJ-BUILD-INT-44": 41}
	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	require.Equal(t, map[string]int{"PROJ-BUILD-INT-44": 41}, m.followFrom)
	m.stopFollow()
}

// TestFollowWorksWhileSeveralJobsAreShown: the prefixes keep interleaved jobs
// apart, so the old refusal is gone (spec §4.3).
func TestFollowWorksWhileSeveralJobsAreShown(t *testing.T) {
	m := logModel()
	m.logs.multi = true
	m.logs.offsets = map[string]int{"PROJ-BUILD-INT-44": 4, "PROJ-BUILD-COMP-44": 1}
	m, cmd := send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.NotNil(t, cmd)
	m.stopFollow()
}
```

Add the import `github.com/r0jjames/bam-cli/internal/app` to `logs_test.go`.

In `stale_test.go`:
- `TestStaleFollowChunkIsDropped`: replace the `logChunkMsg{...}` with `logEventMsg{Gen: 3, Event: linesEvent("PROJ-BUILD-INT-44", 5, "from a job we left")}`.
- The reopen test around line 497: replace `logChunkMsg{Gen: staleGen, Lines: []string{"from the job we left"}}` with `logEventMsg{Gen: staleGen, Event: linesEvent("PROJ-BUILD-INT-44", 5, "from the job we left")}`.
- `TestFollowSurfacesItsError` becomes:

```go
func TestFollowSurfacesItsError(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.LogErrs = []error{errBoom}
	f.Sequences = map[string][]provider.Build{"PROJ-BUILD-44": {sampleBuild()}}

	events, done := followCmd(t.Context(), svc, "PROJ-BUILD-44", map[string]int{"PROJ-BUILD-INT-44": 0}, false)

	var msg tea.Msg
	for {
		msg = drainFollowCmd(events, done, 0)()
		if _, ok := msg.(logEventMsg); !ok {
			break
		}
	}
	ended, ok := msg.(followEndedMsg)
	require.True(t, ok, "got %T", msg)
	require.Error(t, ended.Err, "the fetch failed, so the end must say so")
}
```

- [ ] **Step 2: Write the new failing tests** (append to `logs_test.go`)

```go
func runningBuild() provider.Build {
	b := sampleBuild()
	b.State = provider.StateRunning
	b.Stages[1].State = provider.StateRunning
	b.Stages[1].Jobs[0].State = provider.StateRunning
	b.Stages[2].State = provider.StateQueued
	b.Stages[2].Jobs = []provider.Job{{Key: "PROJ-BUILD-DEP-44", Name: "Deploy", State: provider.StateQueued}}
	return b
}

// stageModel is a live stage screen on runningBuild's Test stage.
func stageModel() Model {
	m := goldenModel(80, 24)
	m.svc = testService()
	b := runningBuild()
	m.detail = &b
	m, _ = send(m, logsLoadedMsg{Gen: m.logsGen, Title: b.Key, Stage: "Test", Multi: true, Build: b,
		Lines:   []string{"[INT] 12:04:31  [INFO] Running integration suite"},
		Offsets: map[string]int{"PROJ-BUILD-INT-44": 1}})
	m.screen = screenLogs
	return m
}

func TestLogLinesArePrefixedOnAMultiJobScreen(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 2, "12:06:58  [ERROR] refused")})
	require.Equal(t, "[INT] 12:06:58  [ERROR] refused", m.logs.lines[len(m.logs.lines)-1])
}

func TestLogLinesRecordTheJobsOffset(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 7, "x")})
	require.Equal(t, 7, m.logs.offsets["PROJ-BUILD-INT-44"])
}

func TestStageStartAddsASeparatorAndItsJobs(t *testing.T) {
	m := stageModel()
	next := runningBuild()
	next.Stages[1].State = provider.StateSuccess
	next.Stages[2].State = provider.StateRunning
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.StageStart, Stage: "Deploy", Build: next}})
	n := len(m.logs.lines)
	require.Equal(t, []string{"", "== Deploy =="}, m.logs.lines[n-2:])
	require.Equal(t, "Deploy", m.logs.stage)
	require.Equal(t, provider.StateRunning, m.logs.live)
	require.Contains(t, m.logs.offsets, "PROJ-BUILD-DEP-44")
	require.Zero(t, m.logs.offsets["PROJ-BUILD-DEP-44"])
}

func TestLogStatusShowsTheStageStateInTheHeader(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.LogStatus, Stage: "Test", Build: runningBuild()}})
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Test · running")
}

// TestRefollowResumesFromTheStreamedOffsets: Review Focus 1.
func TestRefollowResumesFromTheStreamedOffsets(t *testing.T) {
	m := stageModel()
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-INT-44", 9, "x")})
	m, _ = send(m, mkKey("f"))
	require.True(t, m.logs.following)
	require.Equal(t, 9, m.followFrom["PROJ-BUILD-INT-44"], "no line is read twice")
	m.stopFollow()
}

func TestAllJobsAreGroupedByStageAndPrefixed(t *testing.T) {
	b := sampleBuild()
	jobs := []app.JobLog{
		{Stage: "Build", Job: b.Stages[0].Jobs[0], Lines: []string{"compiled"}},
		{Stage: "Build", Job: b.Stages[0].Jobs[1], Lines: []string{"passed"}},
		{Stage: "Test", Job: b.Stages[1].Jobs[0], Lines: []string{"refused"}},
	}
	require.Equal(t, []string{
		"== Build ==", "[COMP] compiled", "[UNIT] passed",
		"", "== Test ==", "[INT] refused",
	}, composeLogs(b, jobs, true, ""))
	require.Equal(t, []string{"[INT] refused"}, composeLogs(b, jobs[2:], true, "Test"),
		"a stage screen's header names the stage, so no separator")
	require.Equal(t, []string{"refused"}, composeLogs(b, jobs[2:], false, ""))
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'Follow|LogEvent|LogLines|StageStart|LogStatus|Refollow|Grouped|Stale' -v`
Expected: FAIL to compile, `undefined: logEventMsg`, `unknown field Offsets`.

- [ ] **Step 4: Implement in `msgs.go`**

Replace the `logsLoadedMsg` type, the body of `loadLogsCmd` after its `len(jobs) == 0` checks, `logChunkMsg`, `followCmd` and `drainFollowCmd`:

```go
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
```

In `loadLogsCmd`, after the two `len(jobs) == 0` returns, replace the rest of the closure with:

```go
		return logsMsg(b, jobs, all, "", gen)
```

Add:

```go
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
```

```go
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
```

- [ ] **Step 5: Add `apply` to `logs.go` and drop `offset`**

Delete the `offset int` field from `logState`. Add:

```go
// apply folds one follow event into the screen.
func (l *logState) apply(ev app.LogEvent) {
	switch ev.Kind {
	case app.LogStatus:
		l.setBuild(ev.Build)
	case app.StageStart:
		l.stage = ev.Stage
		l.setBuild(ev.Build)
		if l.offsets == nil {
			l.offsets = map[string]int{}
		}
		for _, st := range ev.Build.Stages {
			if st.Name == ev.Stage {
				for _, j := range st.Jobs {
					l.offsets[j.Key] = 0
				}
			}
		}
		l.appendLines([]string{"", "== " + ev.Stage + " =="})
	case app.LogLines:
		if l.offsets == nil {
			l.offsets = map[string]int{}
		}
		l.offsets[ev.Job.Key] = ev.Next
		lines := ev.Lines
		if l.multi && l.build != nil {
			prefix := "[" + app.ShortJobKey(*l.build, ev.Job) + "] "
			lines = make([]string, len(ev.Lines))
			for i, s := range ev.Lines {
				lines[i] = prefix + s
			}
		}
		l.appendLines(lines)
	}
}

// setBuild keeps the newest snapshot and the state the header shows: the
// followed stage's on a stage screen, the build's otherwise.
func (l *logState) setBuild(b provider.Build) {
	l.build = &b
	l.live = b.State
	if l.stage == "" {
		return
	}
	for _, st := range b.Stages {
		if st.Name == l.stage {
			l.live = st.State
		}
	}
}
```

Add `"github.com/r0jjames/bam-cli/internal/app"` to `logs.go`'s imports.

- [ ] **Step 6: Implement in `model.go`**

Fields: replace `followLines <-chan []string` with `followEvents <-chan app.LogEvent`, and `followFrom int // ...` with `followFrom map[string]int // the offsets the current follow resumed at`.

In `Update`, replace the `case logChunkMsg:` block with:

```go
	case logEventMsg:
		if msg.Gen != m.followGen {
			return m, nil
		}
		m.logs.apply(msg.Event)
		return m, drainFollowCmd(m.followEvents, m.followDone, m.followGen)
```

In the `logsLoadedMsg` case, replace the `m.logs.multi, m.logs.offset = ...` line with:

```go
		m.logs.multi, m.logs.offsets, m.logs.stage = msg.Multi, msg.Offsets, msg.Stage
```

and its final `return m, nil` with:

```go
		if msg.Live {
			return m.startFollow()
		}
		return m, nil
```

Replace `toggleFollow` with:

```go
func (m Model) toggleFollow() (tea.Model, tea.Cmd) {
	if m.logs.following {
		m.stopFollow()
		return m, nil
	}
	return m.startFollow()
}

// startFollow follows every job on the screen from the provider's own
// offsets, not from the number of lines drawn: only the first is a contract.
// A stage screen also moves on to the next stage.
func (m Model) startFollow() (tea.Model, tea.Cmd) {
	b, ok := m.logBuild()
	if !ok || m.svc == nil || len(m.logs.offsets) == 0 {
		return m, nil
	}
	for k := range m.logs.offsets {
		if _, ok := jobByKey(b, k); !ok {
			return m, nil
		}
	}
	ctx, cancel := context.WithCancel(m.baseCtx())
	m.followCancel = cancel
	m.logs.following = true
	m.followFrom = maps.Clone(m.logs.offsets)
	m.followEvents, m.followDone = followCmd(ctx, m.svc, b.Key, m.followFrom, m.logs.stage != "")
	m.logs.refresh() // an empty stage now says it is waiting
	return m, drainFollowCmd(m.followEvents, m.followDone, m.followGen)
}
```

Add `"maps"` to `model.go`'s imports. In `stopFollow`, replace `m.followLines, m.followDone = nil, nil` with `m.followEvents, m.followDone = nil, nil`, and after `m.logs.following = false` add `m.logs.refresh()`.

- [ ] **Step 7: Run the package to verify it passes**

Run: `go test ./internal/view/tui/`
Expected: PASS. `go vet ./...` reports nothing (the old `logChunkMsg` and `offset` are gone everywhere; `grep -rn "logChunkMsg\|logs.offset\b\|followLines" internal/` prints nothing).

- [ ] **Step 8: Run with the race detector**

Run: `go test -race ./internal/view/tui/ ./internal/app/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/view/tui/
git commit -m "feat(tui): follow every job on the log screen, each line prefixed with its job"
```

---

### Task 5: Open a stage's live log with `l`

**Files:**
- Modify: `internal/view/tui/msgs.go`, `internal/view/tui/model.go`, `internal/view/tui/keys.go`
- Test: `internal/view/tui/logs_test.go`, goldens `logs-stage-80x24.txt` (new), `help-80x24.txt` (regenerated)

**Interfaces:**
- Consumes: `app.CurrentStage`, `app.LogsOptions.Stage` (Task 1); `logsMsg`, `startFollow` (Task 4).
- Produces: `stageLogsCmd(ctx, svc, b provider.Build, stage string, gen int) tea.Cmd`; `(Model).openSelectionLogs() (tea.Model, tea.Cmd)`; `(Model).openStageLogs(stage string) (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests** (append to `logs_test.go`)

```go
func TestLOnAStageRowOfARunningBuildOpensItLive(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	b := runningBuild()
	m.detail = &b
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1 // Build, Test, Deploy: Test

	m, cmd := send(m, mkKey("l"))
	require.Equal(t, screenLogs, m.screen)
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Equal(t, "Test", msg.Stage)
	require.True(t, msg.Live)
	require.True(t, msg.Multi)
	require.Equal(t, "[INT] 12:04:31  [INFO] Running integration suite", msg.Lines[0])

	m, follow := send(m, msg)
	require.True(t, m.logs.following, "a running stage opens already following")
	require.NotNil(t, follow)
	require.Contains(t, m.View(), "PROJ-BUILD-44 · Test")
	m.stopFollow()
}

func TestLOnAStageRowOfAFinishedBuildDoesNotFollow(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "Test", msg.Stage)
	require.False(t, msg.Live)
	m, _ = send(m, msg)
	require.False(t, m.logs.following)
}

func TestLOnAJobRowOpensThatJob(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{"Build": true}
	m.treeCursor = 2 // Build, Compile, Unit tests, Test, Deploy: Unit tests

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "PROJ-BUILD-UNIT-44", msg.JobKey)
	require.Empty(t, msg.Stage)
}

func TestEnterOnAStageRowStillTogglesIt(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.detail = ptr(sampleBuild())
	m.focus = focusMain
	m.expanded = map[string]bool{}
	m.treeCursor = 1
	m, _ = send(m, mkKey("enter"))
	require.True(t, m.expanded["Test"])
	require.NotEqual(t, screenLogs, m.screen)
}

func TestLOnARunningBuildOpensItsCurrentStage(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{runningBuild()})
	m.focus = focusBuilds

	_, cmd := send(m, mkKey("l"))
	msg := cmd().(logsLoadedMsg)
	require.Equal(t, "Test", msg.Stage)
	require.True(t, msg.Live)
}

// TestLOnAQueuedBuildWaitsForJobs: Review Focus 5.
func TestLOnAQueuedBuildWaitsForJobs(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	queued := provider.Build{Key: "PROJ-BUILD-45", PlanKey: "PROJ-BUILD", Number: 45, State: provider.StateQueued,
		Stages: []provider.Stage{{Name: "Test", State: provider.StateQueued,
			Jobs: []provider.Job{{Key: "PROJ-BUILD-INT-45", Name: "Integration", State: provider.StateQueued}}}}}
	m.builds.setItems([]provider.Build{queued})
	m.focus = focusBuilds

	m, cmd := send(m, mkKey("l"))
	msg, ok := cmd().(logsLoadedMsg)
	require.True(t, ok, "got %T", cmd())
	require.Empty(t, msg.Lines)
	m, _ = send(m, msg)
	require.True(t, m.logs.following)
	require.Contains(t, m.View(), waitingLine)
	m.stopFollow()
}

// TestLeavingALiveStageNeverStopsTheBuild: Review Focus 4.
func TestLeavingALiveStageNeverStopsTheBuild(t *testing.T) {
	m := goldenModel(80, 24)
	m.svc = testService()
	m.builds.setItems([]provider.Build{runningBuild()})
	m.focus = focusBuilds
	m, cmd := send(m, mkKey("l"))
	m, _ = send(m, cmd())
	require.True(t, m.logs.following)

	m, _ = send(m, mkKey("esc"))
	require.False(t, m.logs.following)
	require.Empty(t, m.svc.P.(*fake.Provider).Stopped)
}

func TestStageLogGolden(t *testing.T) {
	m := stageModel()
	m.logs.appendLines([]string{"[INT] 12:06:58  [ERROR] ConnectionRefused: bamboo.lab.example:5432"})
	next := runningBuild()
	next.Stages[1].State = provider.StateSuccess
	next.Stages[2].State = provider.StateRunning
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: app.LogEvent{Kind: app.StageStart, Stage: "Deploy", Build: next}})
	m, _ = send(m, logEventMsg{Gen: m.followGen, Event: linesEvent("PROJ-BUILD-DEP-44", 1, "12:08:00  deploying")})
	m.logs.search("error")
	requireGolden(t, "logs-stage-80x24", m.View())
}
```

Add the import `github.com/r0jjames/bam-cli/internal/provider/fake` to `logs_test.go` if it is not there.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/view/tui/ -run 'TestLOn|TestEnterOnAStage|TestLeavingALive|TestStageLogGolden' -v`
Expected: FAIL: `l` on a stage row opens the failed jobs (no `Stage`); the golden file is missing.

- [ ] **Step 3: Add `stageLogsCmd` to `msgs.go`**

```go
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
```

- [ ] **Step 4: Route `l` in `model.go`**

In `handleKey`, the `keys.Logs` case becomes:

```go
	case key.Matches(msg, keys.Logs):
		return m.openSelectionLogs()
```

Leave the `AllLogs` case, `drill`'s Enter handling and `treescreen.go` untouched. Add next to `openLogs`:

```go
// openSelectionLogs is l: on the tree, the stage or job under the cursor;
// otherwise the current stage of a build still running, live, or else the
// failed jobs.
func (m Model) openSelectionLogs() (tea.Model, tea.Cmd) {
	if m.focus == focusMain {
		if row, ok := m.selectedTreeRow(); ok {
			if row.Kind == rowStage {
				return m.openStageLogs(row.Name)
			}
			return m.openLogs(row.Key, false)
		}
	}
	if b, ok := m.currentBuild(); ok && !b.State.Finished() {
		return m.openStageLogs("")
	}
	return m.openLogs("", false)
}

// openStageLogs takes the screen for one stage's jobs. An empty stage is the
// build's current one, resolved once the build is read in full.
func (m Model) openStageLogs(stage string) (tea.Model, tea.Cmd) {
	b, ok := m.currentBuild()
	if !ok || m.svc == nil {
		return m, nil
	}
	m.screen = screenLogs
	m.stopFollow() // reopening replaces the log state; its follow ends first
	m.logs = logState{}
	m.logsGen++
	return m, stageLogsCmd(m.baseCtx(), m.svc, b, stage, m.logsGen)
}
```

- [ ] **Step 5: Update the help rows in `keys.go`**

```go
		{"VIEW", "l", "logs for the selection: a stage, a job, or failed jobs; a running build: its current stage, live"},
		{"VIEW", "f", "follow the jobs on the log screen"},
		{"VIEW", "n N", "next / previous match, or next failure with no search"},
```

- [ ] **Step 6: Run the new tests, writing the goldens**

Run: `go test ./internal/view/tui/ -run Golden -update`
Then: `git diff internal/view/tui/testdata/golden/` and read `logs-stage-80x24.txt`.
Expected golden content: header `PROJ-BUILD-44 · Deploy · running · match 1/1` on the left and the position on the right; lines `[INT] 12:04:31 ...`, `[INT] 12:06:58  [ERROR] ConnectionRefused...`, a blank line, `== Deploy ==`, `[DEP] 12:08:00  deploying`; key line `esc back  / search  G end  f follow  n next match  o open`. The help golden differs only in the three rows changed in Step 5; every other golden is unchanged (revert any that is not and find out why).

- [ ] **Step 7: Run the package to verify it passes**

Run: `go test ./internal/view/tui/`
Expected: PASS, including the existing `TestLDefaultsToTheFailedJobs` (a finished build) and `TestFollowWorksForLogsOpenedFromASummaryRow` (a listing row with no state: read in full, no current stage, so the failed jobs).

- [ ] **Step 8: Commit**

```bash
git add internal/view/tui/
git commit -m "feat(tui): l on a stage, or on a running build, opens its live stage log"
```

---

### Task 6: Docs, changelog and the full check

**Files:**
- Modify: `CHANGELOG.md`, `docs/superpowers/specs/2026-09-18-bam-v0.2-tui-navigate-design.md`, `docs/superpowers/specs/2026-10-10-bam-tui-live-stage-logs-design.md` (status line)

- [ ] **Step 1: Changelog** — under `## [Unreleased]` → `### Added`, append:

```markdown
- The terminal UI streams a running build's stage logs live (`l` on a stage, or on a running build) and search highlights and counts matches as lines arrive.
```

- [ ] **Step 2: Navigate spec note** — directly under the `### 3.1 Log screen` heading add:

```markdown
> Follow and search are superseded by [the live stage logs design](2026-10-10-bam-tui-live-stage-logs-design.md): `l` on a stage or a running build streams the stage, multi-job screens prefix each line with its job, and search highlights and counts matches as lines arrive.
```

- [ ] **Step 3: Mark the spec implemented** — in the new spec, change `**Status:** Draft` to `**Status:** Implemented`.

- [ ] **Step 4: Full verification**

Run, in order:

```bash
export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"
go test -race ./...
make lint
make check-fixtures
make docs && git diff --exit-code docs/cli/
```

Expected: all pass; `docs/cli/` unchanged (no CLI flag was added).

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/superpowers/specs/
git commit -m "docs: changelog and spec notes for live stage logs"
```

- [ ] **Step 6: Push and open the PR** (CLAUDE.md: a finished branch with passing checks gets a PR against `main` without asking). Body: what changed, `Closes #53`, and the Review Focus list. No Claude attribution line.
