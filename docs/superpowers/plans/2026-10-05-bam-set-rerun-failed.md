# bam set rerun --failed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam set rerun [set|id] --failed` retries the failed runs of a failed set run through Bamboo's build restart, then finishes the train.

**Architecture:** `provider.RestartBuild` (`PUT queue/{key}`, capability `Rerun`) restarts a build; `app.Service.Restart` waits until Bamboo has requeued it; `app.Service.RerunSet` shares resume's checks, marks the failed stage's runs for restart and reuses the stage loop, which restarts instead of triggering for those runs. Record and events gain `reruns` / `rerun`; `view` marks `(rerun N)` and the live block starts from the record.

**Tech Stack:** Go, cobra, testify.

**Spec:** `docs/superpowers/specs/2026-10-05-bam-set-rerun-failed-design.md`

## Global Constraints

- Placeholders only: `bamboo.example.com`, `PROJ`, `OPS`, `LAB`, `jdoe`. Recordings are scrubbed by the recorder.
- `--json` fields are added, never renamed or removed. New: `rerun` on set events, `reruns` on record runs.
- `app` never prints, reads stdin or imports cobra (`TestAppStaysUIReady`).
- Interrupting never stops a build; a rerun cancels nothing.
- bam never restarts or triggers a build it cannot record: the record is saved before each restart call.
- No new dependencies. No test sleeps or touches the network.
- Conventional Commits, one commit per task; the owner's identity only, no `Co-Authored-By` or session trailers.
- Before finishing: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.
- In non-interactive shells: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. Bamboo still showing the old failed result right after the restart call must not end the run at once — test `TestRestartWaitsForTheOldResultToGo` (Task 2) and the CLI end-to-end harness test (Task 5).
2. A rerun must never touch runs that succeeded, in the failed stage or before it — `TestRerunRestartsTheFailedRunAndFinishesTheTrain` asserts stage 1 untouched and `Triggered` empty (Task 3).
3. The `reruns` count must be on disk before Bamboo is asked to restart — `TestRerunSavesTheCountBeforeRestarting` (Task 3).
4. `bam set rerun` without `--failed` must fail before config or network — `TestSetRerunNeedsFailed` asserts `connectOpts` empty (Task 5).
5. A Bamboo that cannot restart must fail that run and stop the train, not crash or hang — `TestRerunThatCannotRestartFailsTheRun` (Task 3).

---

### Task 1: `RestartBuild` in the provider

**Files:**
- Modify: `internal/provider/provider.go` (interface)
- Modify: `internal/provider/bamboo/run.go` (new method after `StopBuild`)
- Modify: `internal/provider/bamboo/capabilities.go` (`Capabilities.Rerun`)
- Modify: `internal/provider/bamboo/probe.go` (keep and report `Rerun`)
- Modify: `internal/provider/fake/fake.go`
- Create: `internal/provider/bamboo/testdata/queue_restart.json`
- Test: `internal/provider/bamboo/run_test.go`, `internal/provider/bamboo/probe_test.go`

**Interfaces:**
- Produces: `Provider.RestartBuild(ctx context.Context, buildKey string) (provider.Build, error)`; `Capabilities.Rerun string` (`yes|no`); fake fields `RestartResult provider.Build`, `RestartErr error`, `RestartResults map[string]provider.Build`, `RestartErrs map[string]error`, `Restarted []string`.

- [ ] **Step 1: Fixture** `internal/provider/bamboo/testdata/queue_restart.json`

```json
{
  "planKey": "PROJ-BUILD",
  "buildNumber": 45,
  "buildResultKey": "PROJ-BUILD-45",
  "triggerReason": "Rerun build"
}
```

- [ ] **Step 2: Failing tests** — append to `internal/provider/bamboo/run_test.go` (add `"net/http"` to its imports):

```go
func TestRestartBuild(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {fixture: "queue_restart.json"}})
	saved := knownCaps(c, Capabilities{})
	b, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	require.NoError(t, err)
	assert.Equal(t, "PROJ-BUILD-45", b.Key)
	assert.Equal(t, 45, b.Number)
	assert.Equal(t, provider.StateQueued, b.State)
	r := rec.all()[0]
	assert.Equal(t, http.MethodPut, r.Method)
	assert.Equal(t, "true", r.URL.Query().Get("executeAllStages"))
	assert.Equal(t, "yes", (*saved)[len(*saved)-1].Rerun)
}

func TestRestartBuildUnsupported(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 405}})
	saved := knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Contains(t, err.Error(), "cannot restart a build over REST")
	assert.Equal(t, "no", (*saved)[len(*saved)-1].Rerun)
}

func TestRestartBuildKnownUnsupportedSendsNothing(t *testing.T) {
	c, rec := newTestServer(t, map[string]*route{})
	knownCaps(c, Capabilities{Rerun: "no"})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrUnsupported))
	assert.Empty(t, rec.all())
}

func TestRestartBuildRefusedNamesTheBuild(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 400,
		body: `{"message":"Build PROJ-BUILD-45 cannot be restarted"}`}})
	knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo would not restart PROJ-BUILD-45", e.What)
	assert.Equal(t, "Build PROJ-BUILD-45 cannot be restarted", e.Why)
	assert.False(t, errors.Is(err, errs.ErrUnsupported))
}

func TestRestartBuildOfAMissingBuild(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/PROJ-BUILD-45": {status: 404, body: `{"message":"not found"}`}})
	knownCaps(c, Capabilities{})
	_, err := c.RestartBuild(ctx, "PROJ-BUILD-45")
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}

// TestRecordedRestart checks the hand-written shapes against what the
// recorder's -probe-rerun recorded. It skips until that probe has run.
func TestRecordedRestart(t *testing.T) {
	const answer = "recorded/queue_restart.json"
	if _, err := os.Stat(filepath.Join("testdata", answer)); err != nil {
		t.Skipf("%s not recorded", answer)
	}
	c, _ := newTestServer(t, map[string]*route{"PUT /rest/api/latest/queue/REC-PLAN-1": {fixture: answer}})
	knownCaps(c, Capabilities{})
	b, err := c.RestartBuild(ctx, "REC-PLAN-1")
	require.NoError(t, err)
	assert.NotEmpty(t, b.Key)

	results, _ := filepath.Glob(filepath.Join("testdata", "recorded", "result_restart_*.json"))
	for _, f := range results {
		rel, _ := filepath.Rel("testdata", f)
		c, _ := newTestServer(t, map[string]*route{"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: rel}})
		b, err := c.GetBuild(ctx, "REC-PLAN-1")
		require.NoError(t, err, rel)
		assert.NotEmpty(t, b.State, rel)
	}
}
```

In `internal/provider/bamboo/probe_test.go`, the expected names list at line 27 becomes:

```go
	assert.Equal(t, []string{"server version", "plan variables", "build variables", "logs", "duration estimate", "failed tests", "stop builds", "rerun builds"}, names)
```

and after the `stop builds` assertion at line 35 add:

```go
	assert.Equal(t, ProbeSkipped, byName["rerun builds"].Status)
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/provider/bamboo/ -run 'Restart|Probe' 2>&1 | tail -5`
Expected: compile error `c.RestartBuild undefined`.

- [ ] **Step 4: Implement**

`internal/provider/provider.go`, after `StopBuild`:

```go
	// RestartBuild asks Bamboo to run a finished build's failed jobs again,
	// in the same build. It returns the queued build.
	RestartBuild(ctx context.Context, buildKey string) (Build, error)
```

`internal/provider/bamboo/capabilities.go`, `Capabilities`, after `Stop`:

```go
	Rerun       string    `json:"rerun,omitempty"`        // yes | no
```

`internal/provider/bamboo/run.go`, after `dequeue`:

```go
// RestartBuild asks Bamboo to run a finished build's failed jobs again, in
// the same build (PUT queue/{key}). Whether the server can is learned the
// first time, like stopping a build.
func (c *Client) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	caps := c.fresh(ctx)
	unsupported := errs.Bamboof("this Bamboo cannot restart a build over REST").Wrap(errs.ErrUnsupported)
	if caps.Rerun == "no" {
		return provider.Build{}, unsupported
	}
	body, err := c.do(ctx, request{
		method: http.MethodPut,
		path:   api + "/queue/" + url.PathEscape(key),
		query:  url.Values{"executeAllStages": {"true"}},
	})
	switch {
	case err == nil:
	case statusOf(err) == http.StatusMethodNotAllowed || statusOf(err) == http.StatusNotImplemented:
		c.learn(ctx, func(cp *Capabilities) { cp.Rerun = "no" })
		return provider.Build{}, unsupported
	case errors.Is(err, errs.ErrNotFound):
		return provider.Build{}, c.notFoundAs(err, "build", key, "bam build show "+key)
	default:
		var e *errs.Error
		if errors.As(err, &e) && e.Kind == errs.KindBamboo {
			e.What = "Bamboo would not restart " + key
		}
		return provider.Build{}, err
	}
	var q queueDTO
	if err := json.Unmarshal(body, &q); err != nil || q.BuildResultKey == "" {
		return provider.Build{}, errs.Bamboof("unexpected response when restarting %s", key).Wrap(err)
	}
	if caps.Rerun == "" {
		c.learn(ctx, func(cp *Capabilities) { cp.Rerun = "yes" })
	}
	planKey := q.PlanKey
	if planKey == "" {
		planKey = key[:strings.LastIndex(key, "-")]
	}
	return provider.Build{Key: q.BuildResultKey, URL: c.URL(q.BuildResultKey), PlanKey: planKey,
		Number: q.BuildNumber, State: provider.StateQueued, Reason: plainReason(q.TriggerReason)}, nil
}
```

(Add `"strings"` to `run.go`'s imports if it is missing.)

`internal/provider/bamboo/probe.go`: replace

```go
	stop := c.caps.Stop
	c.caps = Capabilities{Stop: stop}
```

with

```go
	stop, rerun := c.caps.Stop, c.caps.Rerun
	c.caps = Capabilities{Stop: stop, Rerun: rerun}
```

and after the `switch stop { … }` block, before `return out`:

```go
	switch rerun {
	case "yes":
		add("rerun builds", nil, "confirmed by an earlier rerun", "")
	case "no":
		add("rerun builds", errs.ErrUnsupported, "", "bam set rerun --failed is unavailable")
	default:
		skip("rerun builds", "learned the first time bam set rerun --failed runs")
	}
```

Update the doc comment above `Probe` to say: `Build stop and restart are never probed: they have side effects, so they are learned the first time bam build cancel and bam set rerun --failed run.`

`internal/provider/fake/fake.go`: in the struct, after `StopErrs`:

```go
	RestartResult  provider.Build
	RestartErr     error
	RestartResults map[string]provider.Build // by build key; RestartResult and RestartErr apply when a key is missing
	RestartErrs    map[string]error
```

and next to `Stopped []string`:

```go
	Restarted    []string
```

and the method, after `StopBuild`:

```go
// RestartBuild records the key. With nothing scripted it answers the build
// as queued under the same key, as Bamboo does.
func (f *Provider) RestartBuild(_ context.Context, key string) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Restarted = append(f.Restarted, key)
	if err, ok := f.RestartErrs[key]; ok {
		return provider.Build{}, err
	}
	if b, ok := f.RestartResults[key]; ok {
		return b, nil
	}
	if f.RestartErr != nil {
		return provider.Build{}, f.RestartErr
	}
	if f.RestartResult.Key != "" {
		return f.RestartResult, nil
	}
	return provider.Build{Key: key, State: provider.StateQueued}, nil
}
```

- [ ] **Step 5: Run to verify they pass, then everything**

Run: `go test ./internal/provider/... && go build ./... && go vet ./...`
Expected: PASS, no output from build and vet (every `Provider` implementation compiles). If `internal/cli/doctor_test.go` fails on a row list, append the `rerun builds` skipped row to its expected list.

- [ ] **Step 6: Commit**

```bash
git add internal/provider internal/cli
git commit -m "feat: restart a failed build in Bamboo"
```

---

### Task 2: `app.Service.Restart` — wait until Bamboo requeued the build

**Files:**
- Create: `internal/app/restart.go`
- Test: `internal/app/restart_test.go`

**Interfaces:**
- Consumes: `Provider.RestartBuild` (Task 1); fake `Sequences`, `Restarted`, `RestartErr`, `GetCalls`.
- Produces: `func (s *Service) Restart(ctx context.Context, key string) (provider.Build, error)`; constants `restartPoll = 2 * time.Second`, `restartWait = 2 * time.Minute`.

- [ ] **Step 1: Failing tests** — create `internal/app/restart_test.go`:

```go
package app

import (
	"context"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/r0jjames/bam-cli/internal/provider/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var failedAt = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)

func oldResult() provider.Build {
	return provider.Build{Key: "PROJ-BUILD-9", State: provider.StateFailed, FinishedAt: failedAt}
}

// restartBamboo answers GetBuild of PROJ-BUILD-9 with seq, one per call.
func restartBamboo(seq ...provider.Build) *fake.Provider {
	p := fakeBamboo()
	p.Sequences = map[string][]provider.Build{"PROJ-BUILD-9": seq}
	return p
}

func TestRestartWaitsForTheOldResultToGo(t *testing.T) {
	p := restartBamboo(oldResult(), oldResult(), oldResult(), provider.Build{Key: "PROJ-BUILD-9", State: provider.StateQueued})
	s := newService(t, p)
	b, err := s.Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	assert.Equal(t, provider.StateQueued, b.State)
	assert.Equal(t, []string{"PROJ-BUILD-9"}, p.Restarted)
	assert.Equal(t, []time.Duration{2 * time.Second, 2 * time.Second}, s.Clock.(*fakeClock).Waits())
}

func TestRestartCountsANewFinishAsRestarted(t *testing.T) {
	again := provider.Build{Key: "PROJ-BUILD-9", State: provider.StateSuccess, FinishedAt: failedAt.Add(time.Minute)}
	b, err := newService(t, restartBamboo(oldResult(), again)).Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	assert.Equal(t, provider.StateSuccess, b.State)
}

func TestRestartGivesUpAfterTwoMinutes(t *testing.T) {
	s := newService(t, restartBamboo(oldResult()))
	_, err := s.Restart(bg, "PROJ-BUILD-9")
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindBamboo, e.Kind)
	assert.Equal(t, "Bamboo did not restart PROJ-BUILD-9", e.What)
	assert.Equal(t, "the build still shows its earlier result", e.Why)
	assert.Equal(t, "bam open PROJ-BUILD-9", e.Try)
	var total time.Duration
	for _, w := range s.Clock.(*fakeClock).Waits() {
		total += w
	}
	assert.Equal(t, 2*time.Minute, total)
}

func TestRestartPassesARefusalThrough(t *testing.T) {
	p := restartBamboo(oldResult())
	p.RestartErr = errs.Bamboof("Bamboo would not restart PROJ-BUILD-9")
	_, err := newService(t, p).Restart(bg, "PROJ-BUILD-9")
	require.ErrorContains(t, err, "would not restart")
	assert.Equal(t, 1, p.GetCalls("PROJ-BUILD-9"), "no polling after a refusal")
}

func TestRestartLeavesTheLastBuildAlone(t *testing.T) {
	s := newService(t, restartBamboo(oldResult(), provider.Build{Key: "PROJ-BUILD-9", State: provider.StateQueued}))
	_, err := s.Restart(bg, "PROJ-BUILD-9")
	require.NoError(t, err)
	_, ok, _ := s.State.Last(s.Cfg.RepoRoot)
	assert.False(t, ok, "a restart is not a trigger")
}

func TestRestartWithAnEndedContextRestartsNothing(t *testing.T) {
	p := restartBamboo(oldResult())
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := newService(t, p).Restart(ctx, "PROJ-BUILD-9")
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, p.Restarted)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/app/ -run Restart 2>&1 | tail -3`
Expected: compile error `s.Restart undefined`.

- [ ] **Step 3: Implement** — create `internal/app/restart.go`:

```go
package app

import (
	"context"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

// restartPoll and restartWait bound how long Restart waits for Bamboo to
// requeue a restarted build.
const (
	restartPoll = 2 * time.Second
	restartWait = 2 * time.Minute
)

// Restart asks Bamboo to run a finished build's failed jobs again and waits
// until Bamboo has requeued it: until then the build still reads as its old
// result, and a Watch started on it would end at once. It is not a trigger,
// so the repository's last build is left alone.
func (s *Service) Restart(ctx context.Context, key string) (provider.Build, error) {
	if err := ctx.Err(); err != nil {
		return provider.Build{}, err
	}
	before, err := s.P.GetBuild(ctx, key)
	if err != nil {
		return provider.Build{}, err
	}
	if _, err := s.P.RestartBuild(ctx, key); err != nil {
		return provider.Build{}, err
	}
	for waited := time.Duration(0); ; waited += restartPoll {
		b, err := s.P.GetBuild(ctx, key)
		if err != nil {
			return provider.Build{}, err
		}
		if !b.State.Finished() || !b.FinishedAt.Equal(before.FinishedAt) {
			return b, nil
		}
		if waited >= restartWait {
			return provider.Build{}, errs.Bamboof("Bamboo did not restart %s", key).
				WithWhy("the build still shows its earlier result").WithTry("bam open " + key)
		}
		select {
		case <-ctx.Done():
			return provider.Build{}, ctx.Err()
		case <-s.Clock.After(restartPoll):
		}
	}
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/app/`
Expected: PASS (including `TestAppStaysUIReady`).

- [ ] **Step 5: Commit**

```bash
git add internal/app/restart.go internal/app/restart_test.go
git commit -m "feat: wait for Bamboo to requeue a restarted build"
```

---

### Task 3: `app.Service.RerunSet`

**Files:**
- Modify: `internal/app/setstore.go` (`RunRecord.Reruns`)
- Modify: `internal/app/sets.go` (`SetEvent.Rerun`, `setRun` restart fields, `startSet`, `stage`, `ResumeSet` → `continueSet`, new `RerunSet`, `firstUnfinished`)
- Test: `internal/app/sets_test.go`

**Interfaces:**
- Consumes: `Service.Restart` (Task 2); fake `Restarted`, `RestartErr`.
- Produces: `RunRecord.Reruns int` (`json:"reruns"`); `SetEvent.Rerun int`; `func (s *Service) RerunSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error)`.

- [ ] **Step 1: Failing tests** — in `internal/app/sets_test.go`, change the `"failed"` row of `TestResumeRefusals` to:

```go
		"failed":       {SetRecord{Status: SetFailed, Stages: twoStages("")}, errs.KindUsage, "bam set rerun --failed"},
```

and append:

```go
// failedRecord is release with stage 1 done and stage 2's run failed as
// PROJ-PROV12-1.
func failedRecord() []StageRecord {
	return []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateFailed}}},
	}
}

// restartable lets key restart: Restart reads the old failed result, then
// the build is queued, then it ends in final.
func restartable(p *fake.Provider, key string, final provider.State) {
	p.Sequences[key] = []provider.Build{
		{Key: key, State: provider.StateFailed},
		{Key: key, State: provider.StateQueued},
		{Key: key, State: final},
	}
}

func TestRerunRestartsTheFailedRunAndFinishesTheTrain(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, []string{"PROJ-PROV12-1"}, fp.Restarted)
	require.Empty(t, fp.Triggered, "a restart is not a trigger, and stage 1 is left alone")
	require.Equal(t, []string{
		"stage_start:1:", "run_triggered:1:provision-lab", "run_done:1:provision-lab", "stage_done:1:", "set_done:1:",
	}, kinds(evs))
	for _, e := range evs {
		require.Equal(t, rec.ID, e.ID, "rerun keeps the id")
		if e.Type == SetRunTriggered {
			require.Equal(t, 1, e.Rerun)
		}
	}
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 1, got.Stages[1].Runs[0].Reruns)
	require.Equal(t, provider.StateSuccess, got.Stages[1].Runs[0].State)
	require.Equal(t, "PROJ-PROV12-1", got.Stages[1].Runs[0].BuildKey, "the same build")
	require.Equal(t, provider.StateSuccess, got.Stages[0].Runs[0].State)
}

func TestRerunContinuesIntoTheStagesThatNeverRan(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	restartable(fp, "PROJ-BUILD-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := twoStages("PROJ-BUILD-1")
	stages[0].Runs[0].State = provider.StateFailed
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: stages})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, []string{"PROJ-BUILD-1"}, fp.Restarted)
	require.Len(t, fp.Triggered, 1)
	require.Equal(t, "PROJ-PROV12", fp.Triggered[0].PlanKey)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
}

func TestRerunTriggersARunThatNeverTriggered(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	stages := failedRecord()
	stages[1].Runs[0].BuildKey, stages[1].Runs[0].Error = "", "queue full"
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: stages})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Empty(t, fp.Restarted)
	require.Len(t, fp.Triggered, 1)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
	require.Equal(t, 0, got.Stages[1].Runs[0].Reruns, "a fresh trigger is not a rerun")
	require.Equal(t, "", got.Stages[1].Runs[0].Error)
}

// restartSpy notes how many reruns the record held when Bamboo was asked.
type restartSpy struct {
	*fake.Provider
	load func() SetRecord
	seen []int
}

func (r *restartSpy) RestartBuild(ctx context.Context, key string) (provider.Build, error) {
	r.seen = append(r.seen, r.load().Stages[1].Runs[0].Reruns)
	return r.Provider.RestartBuild(ctx, key)
}

func TestRerunSavesTheCountBeforeRestarting(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-1", provider.StateSuccess)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	spy := &restartSpy{Provider: fp, load: func() SetRecord { r, _ := svc.Sets.Load(rec.ID); return r }}
	svc.P = spy
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Equal(t, []int{1}, spy.seen)
}

func TestRerunThatCannotRestartFailsTheRun(t *testing.T) {
	fp := trainBamboo(nil)
	fp.Sequences["PROJ-PROV12-1"] = []provider.Build{{Key: "PROJ-PROV12-1", State: provider.StateFailed}}
	fp.RestartErr = errs.Bamboof("this Bamboo cannot restart a build over REST").Wrap(errs.ErrUnsupported)
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: failedRecord()})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	last := evs[len(evs)-1]
	require.Equal(t, SetDone, last.Type)
	require.Equal(t, provider.StateFailed, last.State)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetFailed, got.Status)
	require.Contains(t, got.Stages[1].Runs[0].Error, "cannot restart")
	require.Equal(t, 1, got.Stages[1].Runs[0].Reruns)
	require.Equal(t, "PROJ-PROV12-1", got.Stages[1].Runs[0].BuildKey)
}

func TestRerunRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		rec  SetRecord
		kind errs.Kind
		want string
	}{
		"successful":  {SetRecord{Status: SetSuccessful, Stages: failedRecord()}, errs.KindUsage, "nothing to rerun"},
		"interrupted": {SetRecord{Status: SetInterrupted, Stages: failedRecord()}, errs.KindUsage, "bam set resume"},
		"timed out":   {SetRecord{Status: SetTimedOut, Stages: failedRecord()}, errs.KindUsage, "was timed out, not failed"},
		"missing var": {SetRecord{Status: SetFailed, Stages: failedRecord(), VarNames: []string{"env"}}, errs.KindUsage, "--var env="},
		"changed set": {SetRecord{Status: SetFailed, Stages: failedRecord()[:1]}, errs.KindConfig, "changed since"},
	} {
		t.Run(name, func(t *testing.T) {
			fp := trainBamboo(nil)
			svc := setService(t, fp)
			svc.Getenv = func(string) string { return "x" }
			rec := saveRecord(t, svc, tc.rec)
			_, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
			require.Error(t, err)
			require.Equal(t, tc.kind, errs.KindOf(err), "%v", err)
			require.Contains(t, err.Error()+" "+errTry(err), tc.want)
			require.Empty(t, fp.Restarted)
			require.Empty(t, fp.Triggered)
		})
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/app/ -run 'Rerun|ResumeRefusals' 2>&1 | tail -3`
Expected: compile errors (`RerunSet`, `Reruns`, `e.Rerun` undefined).

- [ ] **Step 3: Implement**

`internal/app/setstore.go`, `RunRecord`, after `State`:

```go
	Reruns   int            `json:"reruns"` // how many times bam restarted this run's build
```

`internal/app/sets.go`:

(a) `SetEvent`, after `State`:

```go
	Rerun  int            // run_triggered of a restart: the run's reruns; 0 otherwise
```

(b) `setRun` struct gains, after `ch`:

```go
	// restart holds the runs of stage restartStage that Bamboo restarts
	// instead of bam triggering them (bam set rerun --failed).
	restartStage int
	restart      map[int]bool
```

(c) `startSet` takes the restart set; replace the function with:

```go
func (s *Service) startSet(ctx context.Context, p SetPlan, rec SetRecord, l *SetLock, from int, restart map[int]bool) <-chan SetEvent {
	ch := make(chan SetEvent, 16)
	go func() {
		defer close(ch)
		defer l.Release()
		(&setRun{s: s, p: p, rec: rec, ch: ch, restartStage: from, restart: restart}).run(ctx, from)
	}()
	return ch
}
```

and change `RunSet`'s call to `s.startSet(ctx, p, rec, l, 0, nil)`.

(d) In `stage`, replace

```go
		key := recs[j].BuildKey
		if key == "" {
```

with

```go
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
			b, err := r.s.Restart(ctx, key)
			if err != nil {
				if ctx.Err() != nil {
					return false, true
				}
				recs[j].State, recs[j].Error = provider.StateFailed, describeErr(err)
				if !r.save(ctx, i) {
					return false, false
				}
				r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, Build: provider.Build{Key: key},
					State: provider.StateFailed, Err: err})
				continue
			}
			r.send(ctx, SetEvent{Type: SetRunTriggered, Stage: i, Target: run.Target, Build: b, Rerun: recs[j].Reruns})
		}
		if key == "" {
```

(e) Replace the whole `ResumeSet` function (from its doc comment `// ResumeSet continues an interrupted or timed-out set run under its own id:` to its closing brace) with:

```go
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
	p, err := s.PlanSet(ctx, rec.Set, o)
	if err != nil {
		return SetPlan{}, nil, err
	}
	if !sameShape(p, rec) {
		return SetPlan{}, nil, errs.Configf("set %s has changed since run %s", rec.Set, rec.ID).
			WithWhy("its stages no longer list the same presets").WithTry("bam set run " + rec.Set)
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
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test -race ./internal/app/`
Expected: PASS. Every existing `TestResume*` and `TestRunSet*` passes unchanged except the `"failed"` row edited in Step 1.

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat: rerun the failed runs of a set run"
```

---

### Task 4: `view` — rerun marks, JSON fields, and a board that starts from the record

**Files:**
- Modify: `internal/view/sets.go` (`SetEventDoc.Rerun`, `SetRunRecordDoc.Reruns`, `SetEventJSON`, `SetRecordJSON`, `SetStatusView`)
- Modify: `internal/view/setwatch.go` (`setRow.rerun`, `apply`, `summary`, `SetLive.draw`, `SetLive.Seed`, `SetLines.Event`)
- Test: `internal/view/setwatch_test.go`, `internal/view/sets_test.go`; golden `internal/view/testdata/set_failed.golden`

**Interfaces:**
- Consumes: `SetEvent.Rerun`, `RunRecord.Reruns` (Task 3).
- Produces: `func (l *SetLive) Seed(rec app.SetRecord)`; JSON `rerun` (events, always present), `reruns` (record runs, always present).

- [ ] **Step 1: Failing tests**

`internal/view/sets_test.go`: in `TestSetEventJSONFieldNames`, the `JSONEq` expectation gains `"rerun":0`:

```go
	require.JSONEq(t, `{"type":"run_done","time":"2026-09-29T10:16:00Z","set":"release","id":"release-20260929T101500Z",
	  "stage":1,"target":"deploy","state":"failed","rerun":0,"error":{"kind":"bamboo","what":"queue full","try":"bam doctor"}}`, string(data))
```

and append:

```go
func TestSetEventJSONCarriesTheRerun(t *testing.T) {
	e := app.SetEvent{Type: app.SetRunTriggered, Time: time.Date(2026, 9, 29, 10, 16, 0, 0, time.UTC),
		Stage: 1, Target: "deploy", Build: provider.Build{Key: "OPS-DEP-9"}, Rerun: 2}
	data, err := json.Marshal(SetEventJSON(e))
	require.NoError(t, err)
	require.Contains(t, string(data), `"rerun":2`)
}

func TestSetStatusViewShowsReruns(t *testing.T) {
	rec := app.SetRecord{ID: "release-20260929T101500Z", Set: "release", Status: app.SetFailed,
		Stages: []app.StageRecord{{Runs: []app.RunRecord{
			{Target: "deploy", PlanKey: "OPS-DEP", BuildKey: "OPS-DEP-9", State: provider.StateFailed, Reruns: 2}}}}}
	o, buf := testOut(false)
	require.NoError(t, SetStatusView(o, rec, nil))
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+Failed \(rerun 2\)`, buf.String())
	require.Contains(t, buf.String(), "try: bam set rerun --failed release-20260929T101500Z\n")
	data, err := json.Marshal(SetRecordJSON(rec, nil))
	require.NoError(t, err)
	require.Contains(t, string(data), `"reruns":2`)
}
```

`internal/view/setwatch_test.go`: in `TestSetLinesOneLinePerChange`, after the `try: bam logs` assertion add:

```go
	require.Contains(t, out, "  try: bam set rerun --failed release-20260929T101500Z\n")
```

and append:

```go
func TestSetLinesNamesARerun(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	e := setEv(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	e.Rerun = 1
	l.Event(e)
	require.Equal(t, "2026-09-29T10:15:00Z stage 2 deploy OPS-DEP-9 rerun 1\n", buf.String())
}

func TestSetLiveStartsFromTheRecordAndMarksARerun(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 80
	l := NewSetLive(o, setPlan())
	l.Seed(app.SetRecord{ID: "release-20260929T101500Z", Stages: []app.StageRecord{
		{Runs: []app.RunRecord{{Target: "publish-specs", BuildKey: "PROJ-SPEC-12", State: provider.StateSuccess}}},
		{Runs: []app.RunRecord{{Target: "provision", BuildKey: "OPS-PROV-40", State: provider.StateSuccess},
			{Target: "deploy", BuildKey: "OPS-DEP-9", State: provider.StateFailed}}},
	}})
	l.Event(setEv(app.SetStageStart, 1, "", provider.Build{}, ""))
	out := lastFrame(buf.String())
	require.Regexp(t, `publish-specs\s+PROJ-SPEC-12\s+Successful`, out, "not pending: the record knows it")
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+Failed`, out)

	e := setEv(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, "")
	e.Rerun = 1
	l.Event(e)
	require.Regexp(t, `deploy\s+OPS-DEP-9\s+\S+ \(rerun 1\)`, lastFrame(buf.String()))
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/view/ -run 'Set' 2>&1 | tail -5`
Expected: compile errors (`l.Seed` undefined), then failures.

- [ ] **Step 3: Implement**

`internal/view/sets.go`:

- `SetEventDoc` gains, after `State`: `Rerun  int       \`json:"rerun"\``; `SetEventJSON`'s literal gains `Rerun: e.Rerun,`.
- `SetRunRecordDoc` gains, after `State`: `Reruns   int       \`json:"reruns"\``; in `SetRecordJSON`, the run literal gains `Reruns: r.Reruns,`.
- `SetStatusView`: replace `stateTitle(state)` in the row with `rerunTitle(state, r.Reruns)`, and after the `if rec.Status == app.SetInterrupted …` block add:

```go
	if rec.Status == app.SetFailed {
		fmt.Fprintf(o.W, "try: bam set rerun --failed %s\n", rec.ID)
	}
```

- Add next to `stateTitle`:

```go
// rerunTitle is a run's state title, marked when bam restarted its build.
func rerunTitle(s provider.State, reruns int) string {
	if reruns > 0 {
		return fmt.Sprintf("%s (rerun %d)", stateTitle(s), reruns)
	}
	return stateTitle(s)
}
```

`internal/view/setwatch.go`:

- `setRow` gains `rerun int // how many times its build was restarted`.
- In `apply`, the `app.SetRunTriggered` case becomes:

```go
	case app.SetRunTriggered:
		r := b.row(e.Stage, e.Target)
		r.build, r.err = e.Build, nil
		if e.Rerun > 0 {
			r.rerun = e.Rerun
		}
```

- In `summary`, after the `for _, r := range st { … }` loop at the end of the function add:

```go
	fmt.Fprintf(o.W, "  try: bam set rerun --failed %s\n", b.id)
```

and in the `if b.final.Stage >= len(b.stages)` branch, before its `return`, add the same line.

- In `SetLive.draw`, replace `stateTitle(state)` in the row with `rerunTitle(state, r.rerun)`.
- Add after `NewSetLive`:

```go
// Seed fills in what a record already knows, so a resumed or rerun set shows
// the runs that already have a build instead of pending ones.
func (l *SetLive) Seed(rec app.SetRecord) {
	l.b.id = rec.ID
	for i, st := range rec.Stages {
		for _, run := range st.Runs {
			if run.BuildKey == "" && run.Error == "" {
				continue
			}
			r := l.b.row(i, run.Target)
			r.build = provider.Build{Key: run.BuildKey, State: run.State}
			r.rerun = run.Reruns
			if run.Error != "" {
				r.err = errors.New(run.Error)
			}
		}
	}
}
```

- In `SetLines.Event`, the `app.SetRunTriggered` case becomes:

```go
	case app.SetRunTriggered:
		if e.Rerun > 0 {
			fmt.Fprintf(l.o.W, "%s %s rerun %d\n", head, e.Build.Key, e.Rerun)
			return
		}
		fmt.Fprintf(l.o.W, "%s %s triggered\n", head, e.Build.Key)
```

- [ ] **Step 4: Run, regenerate the failed-set golden, review**

Run: `go test ./internal/view/ -run 'Set'` — expected: only `TestSetLiveGoldens/set_failed` fails, on the new `try: bam set rerun --failed` line. Then `go test ./internal/view/ -update` and `git diff internal/view/testdata` — the only change must be that one added line in `set_failed.golden`. Then `go test ./internal/view/...` — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/view
git commit -m "feat: show reruns in set output and JSON"
```

---

### Task 5: `bam set rerun --failed`

**Files:**
- Modify: `internal/cli/sets.go` (new command, `findSetRun` gains a `missing` argument, `setLoop` gains a `seed`)
- Modify: `README.md`, `docs/json.md`; regenerate `docs/cli/`
- Test: `internal/cli/sets_test.go`

**Interfaces:**
- Consumes: `Service.RerunSet` (Task 3), `SetLive.Seed` (Task 4).
- Produces: `newSetRerunCmd`; `findSetRun(args []string, match func(app.SetRecord) bool, missing func(arg string) error)`; `setLoop(ctx, id string, p app.SetPlan, seed *app.SetRecord, events <-chan app.SetEvent)`.

- [ ] **Step 1: Failing tests** — append to `internal/cli/sets_test.go`:

```go
// failedSetRecord saves a failed run of release: stage 1 done, stage 2's
// provision-lab failed as PROJ-PROV12-1.
func failedSetRecord(t *testing.T, h *harness) string {
	t.Helper()
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	id := app.NewSetID("release", fixedNow)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: id, Set: "release", Origin: workOrigin, Repo: h.root,
		StartedAt: fixedNow, Status: app.SetFailed, VarNames: []string{},
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateFailed}}},
		}}))
	return id
}

// restartsTo makes PROJ-PROV12-1 restart and end in final.
func restartsTo(h *harness, final provider.State) {
	b := func(s provider.State) provider.Build {
		return provider.Build{Key: "PROJ-PROV12-1", PlanKey: "PROJ-PROV12", State: s, Duration: 30 * time.Second}
	}
	h.fake.Sequences["PROJ-PROV12-1"] = []provider.Build{b(provider.StateFailed), b(provider.StateQueued), b(final)}
}

func TestSetRerunFailed(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	id := failedSetRecord(t, h)
	restartsTo(h, provider.StateSuccess)
	require.Equal(t, 0, h.run("set", "rerun", "--failed"), h.stderr.String())
	require.Equal(t, []string{"PROJ-PROV12-1"}, h.fake.Restarted)
	require.Empty(t, h.fake.Triggered)
	require.Contains(t, h.stdout.String(), "provision-lab PROJ-PROV12-1 rerun 1")
	require.Contains(t, h.stdout.String(), "set succeeded")

	require.Equal(t, 0, h.run("set", "status", id, "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"reruns": 1`)

	require.Equal(t, 2, h.run("set", "rerun", "--failed", id))
	require.Contains(t, h.stderr.String(), "nothing to rerun")
}

func TestSetRerunStillFailingExits1(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	failedSetRecord(t, h)
	restartsTo(h, provider.StateFailed)
	require.Equal(t, 1, h.run("set", "rerun", "--failed", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"type":"run_triggered"`)
	require.Contains(t, h.stdout.String(), `"rerun":1`)
}

func TestSetRerunNeedsFailed(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "rerun"))
	require.Contains(t, h.stderr.String(), "rerun needs --failed")
	require.Empty(t, h.connectOpts, "no network before the check")
}

func TestSetRerunWithNoFailedRun(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.Equal(t, 2, h.run("set", "rerun", "--failed", "release"))
	require.Contains(t, h.stderr.String(), "no failed run of release to rerun")
	require.Contains(t, h.stderr.String(), "bam set status release")
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/cli/ -run SetRerun 2>&1 | tail -5`
Expected: FAIL — `unknown command "rerun"` (exit 2 where 0 or 1 is expected).

- [ ] **Step 3: Implement** in `internal/cli/sets.go`

(a) `addSets`: add `newSetRerunCmd(r)` after `newSetResumeCmd(r)` in the `AddCommand` list.

(b) `findSetRun` gains a third parameter and uses it when nothing matches:

```go
// findSetRun resolves [set|id]: a record id, else this repository's newest
// record of that set, else its newest record of any set; match filters, and
// missing, when set, words the error when nothing matches.
func (r *runtime) findSetRun(args []string, match func(app.SetRecord) bool, missing func(arg string) error) (app.SetRecord, error) {
```

and as the first statement inside `if !ok {`:

```go
		if missing != nil {
			return app.SetRecord{}, missing(arg)
		}
```

Update the two callers: `r.findSetRun(args, nil, nil)` in status and `r.findSetRun(args, resumable, nil)` in resume.

(c) `setLoop` gains `seed *app.SetRecord` after `p`; in its TTY branch replace `rend = view.NewSetLive(o, p)` with:

```go
			live := view.NewSetLive(o, p)
			if seed != nil {
				live.Seed(*seed)
			}
			rend = live
```

Callers: set run passes `nil` (`r.setLoop(ctx, id, p, nil, events)`); resume passes `&rec` (`r.setLoop(ctx, rec.ID, planFromRecord(rec), &rec, events)`).

(d) The command, after `newSetResumeCmd`:

```go
func newSetRerunCmd(r *runtime) *cobra.Command {
	var vars []string
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
			events, err := svc.RerunSet(ctx, rec.ID, app.SetOptions{Vars: vars, Server: r.flags.server}, app.SetLockOptions{Force: force})
			if err != nil {
				return err
			}
			return r.setLoop(ctx, rec.ID, planFromRecord(rec), &rec, events)
		}),
	}
	f := cmd.Flags()
	f.BoolVar(&failed, "failed", false, "retry the runs that failed (required)")
	f.StringArrayVar(&vars, "var", nil, "variable name=value; give again every --var the run was started with (repeatable)")
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
```

- [ ] **Step 4: Run to verify they pass, then the module**

Run: `go test ./internal/cli/ && go test ./...`
Expected: PASS.

- [ ] **Step 5: Docs**

Run `make docs` (adds `docs/cli/bam_set_rerun.md`, updates `bam_set.md`).

`README.md`, everyday commands table, after `| continue a train after Ctrl-C | \`bam set resume\` |`:

```markdown
| retry a failed train's failed runs, then finish it | `bam set rerun --failed` |
```

and replace the paragraph after the release-train example:

```markdown
A failure never cancels a build. Ctrl-C stops watching; `bam set status` and `bam set resume` pick the train up again. See [Sets](docs/configuration.md#sets-release-trains) for the rules.
```

with

```markdown
A failure never cancels a build. Ctrl-C stops watching; `bam set status` and `bam set resume` pick the train up again. After a failure, `bam set rerun --failed` has Bamboo restart the failed builds (only their failed jobs run again, with the same variables and commit) and then finishes the train. See [Sets](docs/configuration.md#sets-release-trains) for the rules.
```

`docs/json.md`, Sets section: in the paragraph beginning `` `stage` counts from 0.``, after its first sentence add: `` `rerun` is the run's restart count on a `run_triggered` that restarted a build (`bam set rerun --failed`), and 0 on every other event. ``. In the `set status --json` paragraph, change `` `target`, `plan_key`, `build_key`, `state` and `error` `` to `` `target`, `plan_key`, `build_key`, `state`, `reruns` and `error` ``.

- [ ] **Step 6: Commit**

```bash
git add internal/cli README.md docs/json.md docs/cli
git commit -m "feat: add bam set rerun --failed"
```

---

### Task 6: Recorder probe, e2e, and the whole-branch gate

**Files:**
- Modify: `tools/record/main.go` (flag), `tools/record/probe.go` (`probeRerun`)
- Modify: `e2e/e2e_test.go` (flag + `TestSetRerunSmoke`)
- Modify: `docs/superpowers/specs/2026-09-29-bam-run-sets-core-design.md` §12, `docs/superpowers/specs/2026-10-05-bam-set-rerun-failed-design.md` (status)

- [ ] **Step 1: Recorder flag** — in `tools/record/main.go`, after the `probe` flag:

```go
		probeRerun  = flag.Bool("probe-rerun", false, "probe build restart (PUT queue/{key}); triggers the plan once and restarts it if it failed")
```

and after the `if *probe { … }` block:

```go
	if *probeRerun {
		if err := r.probeRerun(plan); err != nil {
			return err
		}
		return r.finish()
	}
```

Add to the usage comment at the top of the file, under `-probe-run`:

```go
//	-probe-rerun    trigger the plan, and if the build fails restart it with
//	                PUT queue/{key}, recording the answer and every state
//	                change until it finishes; then record how Bamboo refuses
//	                to restart a successful build
```

- [ ] **Step 2: The probe** — append to `tools/record/probe.go`:

```go
// probeRerun triggers plan and waits for the build. If it failed, it
// restarts it with PUT queue/{key} and records the answer, then the result
// at each change of state until it finishes again. Last, it restarts the
// newest successful build of plan, to record how Bamboo refuses.
func (r *recorder) probeRerun(plan string) error {
	body, status, err := r.call(http.MethodPost, "/rest/api/latest/queue/"+plan, url.Values{"executeAllStages": {"true"}})
	if err != nil || status/100 != 2 {
		return fmt.Errorf("trigger %s: status %d: %v", plan, status, err)
	}
	var queued struct {
		Key string `json:"buildResultKey"`
	}
	if err := json.Unmarshal(body, &queued); err != nil || queued.Key == "" {
		return errors.New("queue answer has no buildResultKey")
	}
	life, state, err := r.waitFinished(queued.Key)
	if err != nil {
		return err
	}
	if state == "Failed" {
		if err := r.restart(queued.Key, "queue_restart.json"); err != nil {
			return err
		}
		if err := r.recordRestart(queued.Key); err != nil {
			return err
		}
	} else {
		fmt.Printf("%s ended %s/%s; a restart needs a failed build, so pass a plan that fails\n", queued.Key, life, state)
	}
	ok, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+plan, url.Values{"buildstate": {"Successful"}, "max-result": {"1"}})
	if err != nil || status != 200 {
		return fmt.Errorf("successful builds of %s: status %d: %v", plan, status, err)
	}
	var list struct {
		Results struct {
			Result []struct {
				Key string `json:"buildResultKey"`
			} `json:"result"`
		} `json:"results"`
	}
	if err := json.Unmarshal(ok, &list); err != nil || len(list.Results.Result) == 0 {
		fmt.Printf("%s has no successful build; not recording a refusal\n", plan)
		return nil
	}
	return r.restart(list.Results.Result[0].Key, "queue_restart_refused.json")
}

// restart sends PUT queue/{key} and records the answer as file, or, when
// Bamboo refuses, its status and message as file.status.
func (r *recorder) restart(key, file string) error {
	body, status, err := r.call(http.MethodPut, "/rest/api/latest/queue/"+key, url.Values{"executeAllStages": {"true"}})
	if err != nil {
		return fmt.Errorf("restart %s: %w", key, err)
	}
	if status/100 != 2 {
		fmt.Printf("%-28s %d (recorded as .status): Bamboo refused the restart\n", file, status)
		snippet := r.scrub.Scrub(body)
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return os.WriteFile(filepath.Join(r.out, file+".status"), []byte(fmt.Sprintf("%d\n%s\n", status, snippet)), 0o644)
	}
	fmt.Printf("%-28s %d\n", file, status)
	return writeScrubbed(r.out, file, body, r.scrub)
}

// recordRestart records result_restart_N.json at once and at each change of
// lifeCycleState/buildState, until the build finishes again or 5 minutes pass.
func (r *recorder) recordRestart(key string) error {
	expand := url.Values{"expand": {"stages.stage.results.result,vcsRevisions"}}
	last, n := "", 0
	for i := 0; i < 150; i++ {
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+key, expand)
		if err == nil && status == 200 {
			life, state := resultStates(body)
			if now := life + "/" + state; now != last {
				if err := r.record(fmt.Sprintf("result_restart_%d.json", n), http.MethodGet, "/rest/api/latest/result/"+key, expand); err != nil {
					return err
				}
				last, n = now, n+1
				if i > 0 && life == "Finished" {
					return nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("%s did not finish again within 5 minutes", key)
}

// waitFinished polls key until it is Finished or NotBuilt.
func (r *recorder) waitFinished(key string) (life, state string, err error) {
	for i := 0; i < 150; i++ {
		time.Sleep(2 * time.Second)
		body, status, err := r.call(http.MethodGet, "/rest/api/latest/result/"+key, nil)
		if err == nil && status == 200 {
			life, state = resultStates(body)
			if life == "Finished" || life == "NotBuilt" {
				return life, state, nil
			}
		}
	}
	return "", "", fmt.Errorf("%s did not finish within 5 minutes", key)
}

func resultStates(body []byte) (life, state string) {
	var res struct {
		Life  string `json:"lifeCycleState"`
		State string `json:"buildState"`
	}
	_ = json.Unmarshal(body, &res)
	return res.Life, res.State
}
```

Run: `go vet ./tools/record/ && go test ./tools/record/` — expected: no vet output, PASS (the probe itself needs a live Bamboo and is run by the owner).

- [ ] **Step 3: e2e** — in `e2e/e2e_test.go`, after the `setPlans` flag:

```go
	setRerunPlan = flag.String("set-rerun-plan", "", "plan key whose build fails, safe to run; TestSetRerunSmoke runs it as a set and reruns it")
```

and append:

```go
func TestSetRerunSmoke(t *testing.T) {
	if *setRerunPlan == "" {
		t.Skip("pass -set-rerun-plan KEY (a plan whose build fails), e.g. make e2e ARGS='-run TestSetRerunSmoke -set-rerun-plan LAB-FLAKY'")
	}
	opts, err := toolcfg.SystemOptions()
	if err != nil {
		t.Fatalf("resolve paths: %v", err)
	}
	cfg, err := toolcfg.Load(opts)
	if err != nil {
		t.Fatalf("read bam config: %v", err)
	}
	server, err := cfg.ServerFor(*serverAlias, "")
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	dir := t.TempDir()
	yaml := fmt.Sprintf("version: 1\ntargets:\n  flaky: {plan: %s}\nsets:\n  e2e-rerun:\n    timeout: 40m\n    stages: [[flaky]]\n", *setRerunPlan)
	if err := os.WriteFile(filepath.Join(dir, ".bam.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	run := bam(t, server.URL, server.Token, dir)

	if code, _, _ := run("set", "run", "e2e-rerun"); code != 1 {
		t.Skipf("set run exited %d; the plan did not fail, so there is nothing to rerun", code)
	}
	_, before, _ := run("set", "status", "--json")
	code, _, _ := run("set", "rerun", "--failed", "--json")
	if code != 0 && code != 1 {
		t.Fatalf("set rerun --failed exited %d; want 0 or 1 (the set result)", code)
	}
	_, after, _ := run("set", "status", "--json")
	var b, a struct {
		Stages []struct {
			Runs []struct {
				BuildKey string `json:"build_key"`
				Reruns   int    `json:"reruns"`
			} `json:"runs"`
		} `json:"stages"`
	}
	if json.Unmarshal([]byte(before), &b) != nil || json.Unmarshal([]byte(after), &a) != nil {
		t.Fatalf("set status --json is not JSON")
	}
	if a.Stages[0].Runs[0].Reruns != 1 || a.Stages[0].Runs[0].BuildKey != b.Stages[0].Runs[0].BuildKey {
		t.Fatalf("want the same build rerun once: before %+v, after %+v", b.Stages[0].Runs[0], a.Stages[0].Runs[0])
	}
}
```

Run: `go vet -tags e2e ./e2e/` — expected: no output.

- [ ] **Step 4: Specs** — in the run sets design §12, replace the bullet `` - `bam set rerun --failed` (it will read the §5.1 record). `` with:

```markdown
- `bam set rerun --failed`: done, see [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md).
```

In the run sets design §5.3 step 1, replace `` "set run failed; rerun --failed is not built yet", with a try line of `bam set run <set>` `` with `` "set run <id> failed; resume continues interrupted runs", with a try line of `bam set rerun --failed <id>` ``.

Set `**Status:** Implemented` in `2026-10-05-bam-set-rerun-failed-design.md`.

- [ ] **Step 5: Whole-branch gate**

Run: `go test -race ./... && make lint && make check-fixtures && make docs && git status --short docs/cli`
Expected: all pass; nothing new under `docs/cli` (regenerated in Task 5).

- [ ] **Step 6: Commit**

```bash
git add tools/record e2e docs/superpowers/specs
git commit -m "test: probe build restart and cover set rerun in e2e"
```
