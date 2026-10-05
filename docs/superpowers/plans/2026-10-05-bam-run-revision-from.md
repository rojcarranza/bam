# bam run --revision from Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam run --from N --revision from` builds the commit build N built, and the TUI run form offers that commit on its Revision row behind one key.

**Architecture:** Bamboo's build result already carries the default repository's commit (`vcsRevisionKey`); `provider/bamboo` maps it to `Build.Revision`. `app` gains `BuildRevision` and two display helpers on `PlanRef`; `cli` resolves `--from` once and fills `ref.Revision` / `ref.RevisionFrom`; `view` labels it; the TUI reads the hint when the form loads.

**Tech Stack:** Go, cobra, bubbletea, testify.

**Spec:** `docs/superpowers/specs/2026-10-05-bam-run-revision-from-design.md`

## Global Constraints

- Placeholders only: `bamboo.example.com`, `PROJ`, `jdoe`; commits in tests are made up (`9b30b9aba03a4f45881629b03531006b89c1e0e7` is the lab recording's and may be reused).
- `--json` fields are added, never renamed or removed. New field: `revision_from`.
- `app` never prints, reads stdin or imports cobra (`TestAppStaysUIReady`).
- No new dependencies.
- No test sleeps or touches the network.
- Conventional Commits, one commit per task; commits carry the owner's identity only, no `Co-Authored-By` or session trailers.
- Before finishing: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.
- In non-interactive shells: `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. `--from last` with `--revision from` must use one build for both the variables and the commit — test: `ListCalls("PROJ-BUILD") == 1` (Task 4).
2. A `--from` build that never started has no commit — must exit 2 before any trigger, not send an empty `customRevision` (Task 4, `TestRunRevisionFromABuildWithoutARevision`).
3. A literal `--revision from` without `--from` must fail before config or network — test asserts `connectOpts` empty (Task 4).
4. The TUI hint read failing must not show an error or block the form — test: `BuildRevision` fails, form still loads with no hint (Task 5).
5. Typing over a reused revision must drop the `from #N` label and `RevisionFrom` — test: edit the row after `f`, `ctrl-R` sends `RevisionFrom == ""` (Task 5).

---

### Task 1: Read a build's default-repository revision

**Files:**
- Modify: `internal/provider/types.go` (`Build`)
- Modify: `internal/provider/bamboo/dto.go` (`resultDTO`)
- Modify: `internal/provider/bamboo/mapping.go` (`mapBuild`)
- Create: `internal/provider/bamboo/testdata/result_revision.json`
- Test: `internal/provider/bamboo/run_test.go`

**Interfaces:**
- Produces: `provider.Build.Revision string` — default repository's commit, `""` when Bamboo reports none.

- [ ] **Step 1: Create the hand-written fixture** `internal/provider/bamboo/testdata/result_revision.json`

```json
{
  "key": "PROJ-BUILD-46",
  "planResultKey": {"key": "PROJ-BUILD-46", "entityKey": {"key": "PROJ-BUILD"}, "resultNumber": 46},
  "buildResultKey": "PROJ-BUILD-46",
  "buildNumber": 46,
  "lifeCycleState": "Finished",
  "buildState": "Successful",
  "state": "Successful",
  "finished": true,
  "notRunYet": false,
  "onceOff": true,
  "buildStartedTime": "2026-09-24T17:20:01.000Z",
  "buildCompletedTime": "2026-09-24T17:20:41.000Z",
  "buildDuration": 40000,
  "buildDurationInSeconds": 40,
  "buildReason": "Custom build by <a href=\"https://bamboo.example.com/browse/user/jdoe\">jdoe</a>&nbsp;with revision <a href=\"https://bamboo.example.com/browse/PROJ-BUILD-46#changesSummary\">9b30b9aba03a4f45881629b03531006b89c1e0e7</a>",
  "plan": {"key": "PROJ-BUILD", "shortName": "Publish"},
  "vcsRevisionKey": "9b30b9aba03a4f45881629b03531006b89c1e0e7",
  "vcsRevisions": {"size": 1, "vcsRevision": [{"repositoryId": 1, "repositoryName": "app", "vcsRevisionKey": "9b30b9aba03a4f45881629b03531006b89c1e0e7"}]},
  "stages": {"size": 1, "stage": [{"name": "Publish", "state": "Successful", "lifeCycleState": "Finished",
    "results": {"size": 1, "result": [{"buildResultKey": "PROJ-BUILD-JOB1-46", "key": "PROJ-BUILD-JOB1-46", "state": "Successful", "lifeCycleState": "Finished",
      "plan": {"shortName": "Publish"}}]}}]}
}
```

- [ ] **Step 2: Write the failing tests** (append to `internal/provider/bamboo/run_test.go`)

```go
func TestBuildCarriesTheDefaultRepositoryRevision(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD-46?expand=" + buildExpand: {fixture: "result_revision.json"},
	})
	b, err := c.GetBuild(ctx, "PROJ-BUILD-46")
	require.NoError(t, err)
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", b.Revision)
}

func TestBuildWithoutARevisionHasNone(t *testing.T) {
	c, _ := newTestServer(t, map[string]*route{
		"GET /rest/api/latest/result/PROJ-BUILD-47?expand=" + buildExpand: {fixture: "result_revision_not_built.json"},
	})
	b, err := c.GetBuild(ctx, "PROJ-BUILD-47")
	require.NoError(t, err)
	assert.Equal(t, "", b.Revision)
}

// TestRecordedBuildRevision: Bamboo 12.1.8 reports the default repository's
// commit at the top level, equal to the first vcsRevisions entry.
func TestRecordedBuildRevision(t *testing.T) {
	for _, f := range []string{"result_revision.json", "recorded/result_revision.json", "recorded/result_revision_short.json", "recorded/result_detail.json"} {
		t.Run(f, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join("testdata", f)); err != nil {
				t.Skipf("%s not recorded", f)
			}
			c, _ := newTestServer(t, map[string]*route{
				"GET /rest/api/latest/result/REC-PLAN-1?expand=" + buildExpand: {fixture: f},
			})
			b, err := c.GetBuild(ctx, "REC-PLAN-1")
			require.NoError(t, err, f)
			require.NotEmpty(t, b.Revisions, f)
			assert.Equal(t, b.Revisions[0].Revision, b.Revision, f)
		})
	}
}
```

Also add one line to the existing `TestRecordedInvalidRevisionIsNotBuilt` loop body, after `assert.Empty(t, b.Revisions, f)`:

```go
			assert.Equal(t, "", b.Revision, f)
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/provider/bamboo/ -run 'Revision' -v`
Expected: compile error `b.Revision undefined`.

- [ ] **Step 4: Implement**

`internal/provider/types.go`, in `Build`, after `Revisions`:

```go
	Revision      string // default repository's commit; empty when Bamboo reports none
	Revisions     []Revision
```

(keep `Revisions` where it is; insert `Revision` directly above it.)

`internal/provider/bamboo/dto.go`, in `resultDTO`, after `ReasonSummary`:

```go
	VcsRevisionKey         string        `json:"vcsRevisionKey"`
```

`internal/provider/bamboo/mapping.go`, in `mapBuild`'s `provider.Build{...}` literal, after `Duration: durationOf(r),`:

```go
		Revision:   r.VcsRevisionKey,
```

- [ ] **Step 5: Run to verify they pass, then the package**

Run: `go test ./internal/provider/... && make check-fixtures`
Expected: PASS. If a test comparing a whole `provider.Build` from a fixture now fails because that fixture has a top-level `vcsRevisionKey`, add `Revision: "<that value>"` to its expected struct.

- [ ] **Step 6: Commit**

```bash
git add internal/provider
git commit -m "feat: read the commit of a build's default repository"
```

---

### Task 2: `app` — reuse a build's revision

**Files:**
- Modify: `internal/app/resolve.go` (`PlanRef`, new methods, `BuildLabel`)
- Modify: `internal/app/run.go` (`BuildRevision`)
- Modify: `internal/app/vars.go:173` (use `BuildLabel`)
- Modify: `internal/app/edit.go:127-129` (header)
- Test: `internal/app/resolve_test.go`, `internal/app/run_test.go`, `internal/app/edit_test.go`

**Interfaces:**
- Consumes: `provider.Build.Revision` (Task 1).
- Produces:
  - `PlanRef.RevisionFrom string` — build key the revision came from; `""` when typed.
  - `func (r PlanRef) RevisionShort() string` — typed revision as given; reused one as its first 7 characters.
  - `func (r PlanRef) RevisionLabel() string` — `RevisionShort()`, plus ` (from #N)` when reused.
  - `func BuildLabel(key string) string` — `"PROJ-BUILD-41"` → `"#41"`.
  - `func (s *Service) BuildRevision(ctx context.Context, key string) (string, error)` — `""` and a usage error when the build has no revision.

- [ ] **Step 1: Write the failing tests**

`internal/app/resolve_test.go`:

```go
func TestRevisionLabels(t *testing.T) {
	typed := PlanRef{Revision: "abc1234ffff"}
	assert.Equal(t, "abc1234ffff", typed.RevisionShort(), "a typed revision prints as given")
	assert.Equal(t, "abc1234ffff", typed.RevisionLabel())

	reused := PlanRef{Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}
	assert.Equal(t, "9b30b9a", reused.RevisionShort())
	assert.Equal(t, "9b30b9a (from #41)", reused.RevisionLabel())

	assert.Equal(t, "", PlanRef{}.RevisionLabel())
	assert.Equal(t, "#41", BuildLabel("PROJ-BUILD12-41"))
}
```

`internal/app/run_test.go`:

```go
func TestBuildRevision(t *testing.T) {
	p := fakeBamboo()
	p.History["PROJ-BUILD"][0].Revision = "9b30b9aba03a4f45881629b03531006b89c1e0e7"
	s := newService(t, p)

	rev, err := s.BuildRevision(bg, "PROJ-BUILD-482")
	require.NoError(t, err)
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", rev)
}

func TestBuildRevisionOfABuildWithoutOne(t *testing.T) {
	s := newService(t, fakeBamboo())
	rev, err := s.BuildRevision(bg, "PROJ-BUILD-482")
	assert.Equal(t, "", rev)
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, errs.KindUsage, e.Kind)
	assert.Equal(t, "PROJ-BUILD-482 has no revision to reuse", e.What)
	assert.Equal(t, "pass a commit with --revision", e.Try)
}

func TestBuildRevisionPassesAReadErrorThrough(t *testing.T) {
	s := newService(t, fakeBamboo())
	_, err := s.BuildRevision(bg, "PROJ-BUILD-999")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errs.ErrNotFound))
}
```

(Add `"errors"` to the file's imports if it is not there.)

`internal/app/edit_test.go`, append to `TestEditBufferHeaderNamesTheRevision`:

```go
	ref.Revision, ref.RevisionFrom = "9b30b9aba03a4f45881629b03531006b89c1e0e7", "PROJ-BUILD-41"
	got = string(EditBuffer(ref, VarSet{}, func(string) string { return "" }, "lab", false, nil))
	assert.Contains(t, got, "# bam run PROJ-BUILD · default branch · revision 9b30b9a (from #41) · server lab\n")
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/app/ -run 'Revision' -v`
Expected: compile errors (`RevisionShort`, `RevisionFrom`, `BuildRevision`, `BuildLabel` undefined).

- [ ] **Step 3: Implement**

`internal/app/resolve.go`, `PlanRef`:

```go
type PlanRef struct {
	PlanKey      string // branch plan key when a branch is chosen, else MasterKey
	MasterKey    string
	Branch       string // branch short name; empty for the default branch
	Revision     string // commit to build, set by the caller for one run; ResolvePlan never sets it
	RevisionFrom string // build key Revision was taken from; empty when it was typed
	Target       *config.ResolvedTarget
}

// RevisionShort is the revision as text output prints it: as typed, or, when
// it was taken from a build, its first seven characters, as build show does.
func (r PlanRef) RevisionShort() string {
	if r.RevisionFrom == "" {
		return r.Revision
	}
	return provider.Revision{Revision: r.Revision}.Short()
}

// RevisionLabel is RevisionShort with the build it came from, like a
// variable's "from #N" source.
func (r PlanRef) RevisionLabel() string {
	if r.RevisionFrom == "" {
		return r.Revision
	}
	return r.RevisionShort() + " (from " + BuildLabel(r.RevisionFrom) + ")"
}

// BuildLabel is a build key's short form, "#41" for PROJ-BUILD-41.
func BuildLabel(key string) string {
	return "#" + key[strings.LastIndex(key, "-")+1:]
}
```

(`resolve.go` already imports `provider` and `strings`; add them if not.)

`internal/app/vars.go`, replace

```go
		label := "from #" + key[strings.LastIndex(key, "-")+1:]
```

with

```go
		label := "from " + BuildLabel(key)
```

`internal/app/run.go`, append:

```go
// BuildRevision is the commit of key's default repository, for reusing it
// with a new run. A build that never started has none, which is the caller's
// input to fix, not a Bamboo fault.
func (s *Service) BuildRevision(ctx context.Context, key string) (string, error) {
	b, err := s.P.GetBuild(ctx, key)
	if err != nil {
		return "", err
	}
	if b.Revision == "" {
		return "", errs.Usagef("%s has no revision to reuse", key).WithTry("pass a commit with --revision")
	}
	return b.Revision, nil
}
```

`internal/app/edit.go`, replace

```go
		head = append(head, "revision "+ref.Revision)
```

with

```go
		head = append(head, "revision "+ref.RevisionLabel())
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/app/`
Expected: PASS (including `TestAppStaysUIReady`).

- [ ] **Step 5: Commit**

```bash
git add internal/app
git commit -m "feat: look up the revision a build used, for reuse"
```

---

### Task 3: `view` — label a reused revision

**Files:**
- Modify: `internal/view/watch.go:21-23` (`RunPlan`)
- Modify: `internal/view/json.go` (`RunDoc`, `RunJSON`)
- Test: `internal/view/watch_test.go`, `internal/view/json_test.go`

**Interfaces:**
- Consumes: `PlanRef.RevisionLabel()`, `PlanRef.RevisionFrom` (Task 2).
- Produces: `RunDoc.RevisionFrom string` (`json:"revision_from"`).

- [ ] **Step 1: Write the failing tests**

`internal/view/watch_test.go`:

```go
func TestRunPlanLabelsAReusedRevision(t *testing.T) {
	o, buf := testOut(true)
	RunPlan(o, app.PlanRef{PlanKey: "PROJ-BUILD", MasterKey: "PROJ-BUILD",
		Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}, app.VarSet{})
	assert.Equal(t, ""+
		"Plan     PROJ-BUILD\n"+
		"Revision 9b30b9a (from #41)\n"+
		"Vars     none\n", buf.String())
}
```

`internal/view/json_test.go`:

```go
func TestRunJSONNamesTheBuildARevisionCameFrom(t *testing.T) {
	d := RunJSON(provider.Build{Key: "PROJ-BUILD-46"}, app.PlanRef{PlanKey: "PROJ-BUILD",
		Revision: "9b30b9aba03a4f45881629b03531006b89c1e0e7", RevisionFrom: "PROJ-BUILD-41"}, app.VarSet{})
	assert.Equal(t, "9b30b9aba03a4f45881629b03531006b89c1e0e7", d.Revision, "the full commit, as sent")
	assert.Equal(t, "PROJ-BUILD-41", d.RevisionFrom)

	raw, err := json.Marshal(RunJSON(provider.Build{}, app.PlanRef{PlanKey: "PROJ-BUILD"}, app.VarSet{}))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"revision":"","revision_from":""`)
}
```

The existing full-document assertion in `json_test.go` (the line with `"revision":"","variables"`) must become `"revision":"","revision_from":"","variables"` — update it in this step.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/view/ -run 'Revision|RunJSON' -v`
Expected: FAIL / compile error `d.RevisionFrom undefined`.

- [ ] **Step 3: Implement**

`internal/view/watch.go`, in `RunPlan`:

```go
	if ref.Revision != "" {
		fmt.Fprintf(o.W, "%-8s %s\n", "Revision", ref.RevisionLabel())
	}
```

`internal/view/json.go`, `RunDoc`:

```go
type RunDoc struct {
	Key          string            `json:"key"`
	URL          string            `json:"url"`
	PlanKey      string            `json:"plan_key"`
	Branch       string            `json:"branch"`
	Revision     string            `json:"revision"`
	RevisionFrom string            `json:"revision_from"`
	Variables    map[string]string `json:"variables"`
}
```

`RunJSON`'s literal gains `RevisionFrom: ref.RevisionFrom,` after `Revision: ref.Revision,`.

- [ ] **Step 4: Run the package**

Run: `go test ./internal/view/`
Expected: PASS. If a golden file holds a `RunDoc`, run `go test ./internal/view/ -update`, check the diff adds only `"revision_from": ""`, and keep it.

- [ ] **Step 5: Add `revision_from` to the JSON contract doc**

In `docs/json.md`, find the `bam run` document's field list and add `revision_from` next to `revision`: `build key the revision was taken from with --revision from; "" otherwise`.

- [ ] **Step 6: Commit**

```bash
git add internal/view docs/json.md
git commit -m "feat: label a reused revision in run output and JSON"
```

---

### Task 4: `cli` — `bam run --revision from`

**Files:**
- Modify: `internal/cli/runloop.go` (`newRunCmd`)
- Modify: `internal/provider/fake/fake.go` (`ListBuilds` call counter)
- Modify: `README.md` (everyday commands table + note)
- Regenerate: `docs/cli/` via `make docs`
- Test: `internal/cli/runloop_test.go`, `internal/cli/runedit_test.go`

**Interfaces:**
- Consumes: `Service.ResolveFrom`, `Service.BuildRevision`, `PlanRef.RevisionFrom`, `PlanRef.RevisionShort()`.
- Produces: `func (f *Provider) ListCalls(key string) int` in `provider/fake`.

- [ ] **Step 1: Add the fake's counter**

`internal/provider/fake/fake.go`: add field `listCalls map[string]int` next to `getCalls`, and make `ListBuilds` count under the lock:

```go
func (f *Provider) ListBuilds(_ context.Context, key string, o provider.ListOptions) ([]provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listCalls == nil {
		f.listCalls = map[string]int{}
	}
	f.listCalls[key]++
	var out []provider.Build
	for _, b := range f.History[key] {
		if o.State != "" && b.State != o.State {
			continue
		}
		out = append(out, b)
		if o.Limit > 0 && len(out) == o.Limit {
			break
		}
	}
	return out, nil
}

// ListCalls reports how many times ListBuilds was called for key.
func (f *Provider) ListCalls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls[key]
}
```

Run `go test ./internal/provider/fake/ ./internal/app/ ./internal/cli/` — expected PASS (no deadlock: `ListBuilds` calls nothing else that locks).

- [ ] **Step 2: Write the failing tests** (`internal/cli/runloop_test.go`)

```go
const lastCommit = "9b30b9aba03a4f45881629b03531006b89c1e0e7"

func withRevision(h *harness) {
	h.fake.History["PROJ-BUILD"][0].Revision = lastCommit
}

func TestRunRevisionFromDryRun(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from", "--dry-run"))
	assert.Contains(t, h.stdout.String(), "Revision 9b30b9a (from #482)\n")
	assert.Empty(t, h.fake.Triggered)

	h = newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from", "--dry-run", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, lastCommit, doc["revision"])
	assert.Equal(t, "PROJ-BUILD-482", doc["revision_from"])
}

func TestRunRevisionFromSendsTheFullCommit(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--from", "482", "--revision", "from"))
	require.Len(t, h.fake.Triggered, 1)
	assert.Equal(t, lastCommit, h.fake.Triggered[0].Revision)
	assert.Contains(t, h.stdout.String(), "revision 9b30b9a is checked when the build starts: bam watch PROJ-BUILD-483")
}

// --from last is resolved once, so the variables and the commit cannot come
// from two different builds.
func TestRunRevisionFromLastListsBuildsOnce(t *testing.T) {
	h := newHarness(t)
	withRevision(h)
	assert.Equal(t, 0, h.run("run", "build", "--from", "last", "--revision", "from", "--dry-run", "--json"))
	assert.Equal(t, 1, h.fake.ListCalls("PROJ-BUILD"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "PROJ-BUILD-482", doc["revision_from"])
}

func TestRunRevisionFromNeedsFrom(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--revision", "from"))
	assert.Contains(t, h.stderr.String(), "--revision from needs --from")
	assert.Empty(t, h.connectOpts, "no network before the check")
}

func TestRunRevisionFromABuildWithoutARevision(t *testing.T) {
	h := newHarness(t)
	assert.Equal(t, 2, h.run("run", "build", "--from", "482", "--revision", "from"))
	assert.Contains(t, h.stderr.String(), "PROJ-BUILD-482 has no revision to reuse")
	assert.Empty(t, h.fake.Triggered)
}

func TestRunJSONHasAnEmptyRevisionFromByDefault(t *testing.T) {
	h := newHarness(t)
	h.fake.TriggerResult = queuedBuild("PROJ-BUILD-483")
	assert.Equal(t, 0, h.run("run", "build", "--revision", "abc1234", "--json"))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &doc))
	assert.Equal(t, "abc1234", doc["revision"])
	assert.Equal(t, "", doc["revision_from"])
}
```

`internal/cli/runedit_test.go`:

```go
func TestRunEditHeaderNamesAReusedRevision(t *testing.T) {
	h := editHarness(t, keepBuffer)
	h.fake.History["PROJ-PROV12"][0].Revision = "9b30b9aba03a4f45881629b03531006b89c1e0e7"
	key := h.fake.History["PROJ-PROV12"][0].Key
	assert.Equal(t, 0, h.run("run", "provision-lab", "--edit", "--from", key, "--revision", "from", "--var", "cluster_name=a", "--dry-run"))
	require.Len(t, h.editorSeen, 1)
	assert.Contains(t, h.editorSeen[0], "· revision 9b30b9a (from "+app.BuildLabel(key)+") · server ")
}
```

(If `runedit_test.go` does not import `app`, add `"github.com/r0jjames/bam-cli/internal/app"`. Read the harness's `History["PROJ-PROV12"]` first: if it is empty, set it to `[]provider.Build{{Key: "PROJ-PROV12-8", Number: 8, Revision: "9b30…"}}` instead.)

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/cli/ -run 'RevisionFrom|ReusedRevision' -v`
Expected: FAIL — `--revision from` is currently sent to Bamboo as a literal revision.

- [ ] **Step 4: Implement** in `internal/cli/runloop.go`

Above `newRunCmd`:

```go
// revisionFromBuild is the --revision value that reuses the commit of the
// --from build instead of naming one.
const revisionFromBuild = "from"
```

In `RunE`, after the `--revision ""` check:

```go
			if revision == revisionFromBuild && from == "" {
				return errs.Usagef("--revision from needs --from").WithTry("bam run " + args[0] + " --from last --revision from")
			}
```

Replace

```go
			ref.Revision = revision
```

with

```go
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
```

Replace the three `ref.Revision` uses after the trigger with the short form:

```go
				view.Queued(o, b, false, ref.RevisionShort())
...
				view.Queued(o, b, true, ref.RevisionShort())
...
			return r.watchLoop(ctx, svc, b.Key, limit, ref.RevisionShort())
```

Flag help:

```go
	f.StringVar(&revision, "revision", "", `commit to build instead of the newest, or "from" to reuse the commit of the --from build; Bamboo applies it to the plan's default repository`)
```

- [ ] **Step 5: Run to verify they pass, then the whole module**

Run: `go test ./internal/cli/ && go test ./...`
Expected: PASS.

- [ ] **Step 6: Docs**

Run: `make docs` (regenerates `docs/cli/bam_run.md` and `bam_build_run.md`).

`README.md`, everyday commands table, after the `--revision abc1234` row:

```markdown
| rebuild the commit an earlier build used | `bam run provision-lab --from 41 --revision from` |
```

and append to the `--revision` paragraph below the table:

```markdown
`--revision from` reuses the commit of the `--from` build, together with its variables.
```

- [ ] **Step 7: Commit**

```bash
git add internal/cli internal/provider/fake README.md docs/cli
git commit -m "feat: add bam run --revision from"
```

---

### Task 5: TUI — offer the build's revision in the run form

**Files:**
- Modify: `internal/view/tui/msgs.go` (`formLoadedMsg`, `openFormCmd`)
- Modify: `internal/view/tui/form.go` (`formState`, Revision row)
- Modify: `internal/view/tui/model.go` (`formLoadedMsg` case, `handleFormKey`, `trigger`, `openTriggered`)
- Modify: `internal/view/tui/panels.go` (`keyLine`)
- Modify: `internal/view/tui/keys.go` (`helpRows`)
- Modify: `README.md` (key table)
- Test: `internal/view/tui/form_test.go`; goldens `help-80x24.txt` (and `form-80x24.txt` only if it changes)

**Interfaces:**
- Consumes: `Service.BuildRevision`, `app.BuildLabel`, `PlanRef.RevisionFrom`, `PlanRef.RevisionShort()`, `provider.Revision.Short()`.
- Produces: `formLoadedMsg.FromBuild`, `formLoadedMsg.FromRevision`; `formState.fromBuild`, `formState.fromRevision`; `func (f formState) revisionReused() bool`; `func (f formState) planRef() app.PlanRef`.

The `f` key is the existing `keys.Follow` binding (it means "follow" on the log screen). `TestNoKeyIsBoundTwice` forbids a second binding on `f`; keys already act per screen, so the form handles `keys.Follow` itself.

- [ ] **Step 1: Write the failing tests** (`internal/view/tui/form_test.go`)

```go
const hintCommit = "9b30b9aba03a4f45881629b03531006b89c1e0e7"

func hintedForm() Model {
	m := formModel()
	m.form.fromBuild, m.form.fromRevision = "PROJ-PROV12-41", hintCommit
	return m
}

func TestFormOffersTheBuildsRevision(t *testing.T) {
	m := hintedForm()
	v := m.View()
	require.Contains(t, v, "newest")
	require.Contains(t, v, "#41 built 9b30b9a")
	require.Equal(t, "", m.form.revision, "offered, not filled")
}

func TestFormWithoutAHintSaysCommitToBuild(t *testing.T) {
	require.Contains(t, formModel().View(), "commit to build")
}

func TestFFillsTheRevisionOnlyOnTheRevisionRow(t *testing.T) {
	m := hintedForm()
	m, _ = send(m, mkKey("f"))
	require.Equal(t, "", m.form.revision, "f on a variable does nothing")

	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	require.Equal(t, hintCommit, m.form.revision)
	require.Contains(t, m.View(), "from #41")

	m = formModel()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	require.Equal(t, "", m.form.revision, "no hint, nothing to use")
}

func TestTheKeyLineOffersFOnlyOnTheRevisionRowWithAHint(t *testing.T) {
	m := hintedForm()
	require.NotContains(t, m.keyLine(), "f use")
	m.form.cursor = len(m.form.fields)
	require.Contains(t, m.keyLine(), "f use #41's revision")
	m = formModel()
	m.form.cursor = len(m.form.fields)
	require.NotContains(t, m.keyLine(), "f use")
}

func TestCtrlRSendsAReusedRevisionWithItsBuild(t *testing.T) {
	m := hintedForm()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	m.revalidate()
	want := sampleRef()
	want.Revision, want.RevisionFrom = hintCommit, "PROJ-PROV12-41"
	require.Equal(t, want, m.form.planRef())
	_, cmd := send(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	require.NotNil(t, cmd)
	cmd()
	f := m.svc.P.(*fake.Provider)
	require.Len(t, f.Triggered, 1)
	require.Equal(t, hintCommit, f.Triggered[0].Revision)
}

// Typing over a reused revision makes it a typed one: no from #N label.
func TestEditingAReusedRevisionDropsItsBuild(t *testing.T) {
	m := hintedForm()
	m.form.cursor = len(m.form.fields)
	m, _ = send(m, mkKey("f"))
	m, _ = send(m, mkKey("enter"))
	m, _ = send(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = send(m, mkKey("enter"))
	require.Equal(t, hintCommit[:len(hintCommit)-1], m.form.revision)
	require.Equal(t, "", m.form.planRef().RevisionFrom)
}

func TestOpenFormCmdReadsTheBuildsRevision(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.History = map[string][]provider.Build{"PROJ-PROV": {{Key: "PROJ-PROV-41", Number: 41, Reason: "Manual run by jdoe", Revision: hintCommit}}}
	f.BuildVars = map[string]map[string]string{"PROJ-PROV-41": {}}
	msg := openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "PROJ-PROV-41", 1)()
	loaded, ok := msg.(formLoadedMsg)
	require.True(t, ok, "%#v", msg)
	require.Equal(t, "PROJ-PROV-41", loaded.FromBuild)
	require.Equal(t, hintCommit, loaded.FromRevision)
}

// The hint is a convenience: a build with no revision (or a failed read)
// opens the form normally, without a hint and without an error.
func TestOpenFormCmdWithoutARevisionHasNoHint(t *testing.T) {
	svc := testService()
	f := svc.P.(*fake.Provider)
	f.History = map[string][]provider.Build{"PROJ-PROV": {{Key: "PROJ-PROV-41", Number: 41, Reason: "Manual run by jdoe"}}}
	f.BuildVars = map[string]map[string]string{"PROJ-PROV-41": {}}
	msg := openFormCmd(t.Context(), svc, "PROJ-PROV", nil, "PROJ-PROV-41", 1)()
	loaded, ok := msg.(formLoadedMsg)
	require.True(t, ok, "%#v", msg)
	require.Equal(t, "", loaded.FromRevision)
}

func TestFormLoadedCarriesTheHintAndReopeningClearsIt(t *testing.T) {
	m := formModel()
	m.form.loading = true
	m, _ = send(m, formLoadedMsg{Gen: m.formGen, Ref: sampleRef(), Target: "provision-lab", Base: sampleBase(),
		FromBuild: "PROJ-PROV12-41", FromRevision: hintCommit})
	require.Equal(t, hintCommit, m.form.fromRevision)
	require.Equal(t, "", m.form.revision)
	m, _ = send(m, mkKey("esc"))
	require.Equal(t, "", m.form.fromRevision)
}
```

(Imports needed in `form_test.go`: `provider` — add `"github.com/r0jjames/bam-cli/internal/provider"` if missing.)

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/view/tui/ -run 'Hint|Revision|FFills|KeyLineOffers' -v`
Expected: compile errors (`fromBuild`, `fromRevision`, `planRef`, `FromRevision` undefined).

- [ ] **Step 3: Implement**

`internal/view/tui/msgs.go`:

```go
type formLoadedMsg struct {
	Gen          int
	Ref          app.PlanRef
	Target       string
	Base         app.VarSet
	FromBuild    string // the build the variables came from; empty for none
	FromRevision string // that build's commit, offered on the Revision row; empty for none
}
```

In `openFormCmd`, replace the final `return formLoadedMsg{...}` with:

```go
		// The revision is offered, never required: any error just means no
		// hint on the Revision row.
		var rev string
		if base.FromBuild != "" {
			rev, _ = svc.BuildRevision(ctx, base.FromBuild)
		}
		return formLoadedMsg{Gen: gen, Ref: ref, Target: name, Base: base, FromBuild: base.FromBuild, FromRevision: rev}
```

`internal/view/tui/form.go`, `formState` gains, after `revision`:

```go
	fromBuild    string // build the form was prefilled from; empty for none
	fromRevision string // its commit, offered on the Revision row by f
```

and add methods:

```go
// revisionReused says whether the Revision row holds the offered commit of
// the build the form came from, unchanged.
func (f formState) revisionReused() bool {
	return f.fromRevision != "" && f.revision == f.fromRevision
}

// planRef is what ctrl-R runs: the loaded plan with the Revision row.
func (f formState) planRef() app.PlanRef {
	ref := f.ref
	ref.Revision = f.revision
	if f.revisionReused() {
		ref.RevisionFrom = f.fromBuild
	}
	return ref
}
```

Revision row rendering — replace the block from `rev := m.form.revision` through `lines = append(lines, strings.TrimRight(revLine, " "))` with:

```go
	rev, note := m.form.revision, "commit to build"
	switch {
	case rev == "" && m.form.fromRevision != "":
		rev = dimStyle.Render("newest")
		note = app.BuildLabel(m.form.fromBuild) + " built " + provider.Revision{Revision: m.form.fromRevision}.Short()
	case rev == "":
		rev = dimStyle.Render("newest")
	case m.form.revisionReused():
		rev = provider.Revision{Revision: rev}.Short()
		note = "from " + app.BuildLabel(m.form.fromBuild)
	}
	if m.form.editing && m.form.onRevision() {
		rev = m.form.input.View()
	}
	revLine := m.cursorFor(true, m.form.onRevision()) +
		lipgloss.NewStyle().Width(nameW).Render("revision") +
		lipgloss.NewStyle().Width(valueW).Render(truncate(rev, valueW)) +
		dimStyle.Render(truncate(note, width-nameW-valueW-4))
	lines = append(lines, strings.TrimRight(revLine, " "))
```

(Add the `provider` import to `form.go` if missing.)

`internal/view/tui/model.go`:

`case formLoadedMsg:` — after `m.form.ref, m.form.target, m.form.base = msg.Ref, msg.Target, msg.Base` add:

```go
		m.form.fromBuild, m.form.fromRevision = msg.FromBuild, msg.FromRevision
```

`handleFormKey`, in the non-editing `switch`, before the `keys.Enter` case:

```go
	case key.Matches(msg, keys.Follow):
		// f is "follow" on the log screen; in the form, on the Revision row,
		// it takes the commit of the build the form was prefilled from.
		if m.form.onRevision() && m.form.fromRevision != "" {
			m.form.revision = m.form.fromRevision
		}
		return m, nil
```

`trigger`, replace

```go
	ref := m.form.ref
	ref.Revision = m.form.revision
```

with

```go
	ref := m.form.planRef()
```

`openTriggered`, replace `m.form.revision` in the `revisionRun` assignment with `m.form.planRef().RevisionShort()`.

`internal/view/tui/panels.go`, `keyLine`:

```go
	if m.screen == screenForm {
		line := "enter edit  tab next  ^R run  d dry-run  esc cancel"
		if m.form.onRevision() && m.form.fromRevision != "" && !m.form.loading {
			line = "f use " + app.BuildLabel(m.form.fromBuild) + "'s revision  " + line
		}
		return line
	}
```

(Add the `app` import to `panels.go` if missing.)

`internal/view/tui/keys.go`, `helpRows`, after the `space` RUN row:

```go
		{"RUN", "f", "use the build's revision (in the form, on the revision row)"},
```

- [ ] **Step 4: Run to verify they pass, then the package**

Run: `go test ./internal/view/tui/`
Expected: the new tests PASS; `TestGolden*` for `help-80x24` fails on the new row. Run `go test ./internal/view/tui/ -update`, read `git diff internal/view/tui/testdata`, and keep it only if the sole change is the new `f` row (and nothing in `form-80x24.txt`, whose fixture has no hint). Then `go test ./internal/view/tui/` — PASS.

- [ ] **Step 5: README key table**

`README.md`, after the `| | \`space\` | cycle an options field (in the form) |` row:

```markdown
| | `f` | use the build's revision (in the form, on the revision row) |
```

- [ ] **Step 6: Commit**

```bash
git add internal/view/tui README.md
git commit -m "feat: offer the build's revision in the TUI run form"
```

---

### Task 6: e2e, docs and the whole-branch gate

**Files:**
- Modify: `e2e/e2e_test.go` (inside the `if *revision != ""` block)
- Modify: `docs/superpowers/specs/2026-09-24-bam-v0.3-run-revision-design.md` §11
- Modify: `docs/superpowers/specs/2026-10-05-bam-run-revision-from-design.md` (status)

- [ ] **Step 1: e2e check** — append inside `if *revision != "" { … }`, after the `build show --last` check:

```go
		// The build just run is a custom build, so --from last picks it.
		code, out, _ = run("run", plan, "--from", "last", "--revision", "from", "--dry-run", "--json")
		if code != 0 {
			t.Fatalf("run --revision from --dry-run exited %d", code)
		}
		var doc struct {
			Revision     string `json:"revision"`
			RevisionFrom string `json:"revision_from"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.RevisionFrom == "" || !strings.HasPrefix(doc.Revision, *revision) {
			t.Fatalf("run --revision from did not reuse revision %s: %s", *revision, out)
		}
```

(Add `"encoding/json"` to the imports if missing.) Compile it: `go vet -tags e2e ./e2e/`. Expected: no output.

- [ ] **Step 2: Point the earlier spec here** — in the run revision spec §11, replace the first bullet with:

```markdown
- Reusing a build's revision: done as `--revision from`, see [`2026-10-05-bam-run-revision-from-design.md`](2026-10-05-bam-run-revision-from-design.md).
```

- [ ] **Step 3: Whole-branch gate**

Run: `go test -race ./... && make lint && make check-fixtures && make docs && git status --short docs/cli`
Expected: all pass; `git status` shows nothing under `docs/cli` (already regenerated in Task 4).

- [ ] **Step 4: Mark the spec implemented** — `**Status:** Approved` → `**Status:** Implemented` in `2026-10-05-bam-run-revision-from-design.md`.

- [ ] **Step 5: Commit**

```bash
git add e2e docs/superpowers/specs
git commit -m "docs: cover run --revision from in e2e and mark its spec implemented"
```
