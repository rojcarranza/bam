# Run sets core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam set list|show|run|status|resume`: named release trains of presets, run in ordered stages, recorded so an interrupted train can be resumed.

**Architecture:** `config` parses and checks `sets:`; `app` resolves a set (`PlanSet`), runs it stage by stage on top of `Service.Run` and `Service.Watch` (`RunSet`, `ResumeSet`) and emits `SetEvent`s on a channel; `app.SetStore` persists a record per set run plus a lock; `view` renders sets and set events; `cli` wires the `bam set` commands.

**Tech Stack:** Go, cobra, yaml.v3, testify. No new dependency.

**Spec:** `docs/superpowers/specs/2026-09-29-bam-run-sets-core-design.md`

## Global Constraints

- Placeholders only: `bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`.
- Package import table in `CLAUDE.md` is unchanged: `app` imports only `provider`, `config`, `credential`, `errs`; `app` never prints, reads stdin or imports cobra.
- Errors are `*errs.Error`; only `cli.exitCode` maps kinds to codes (0, 1 via `resultError`, 2 usage, 3 config, 4 auth, 5 Bamboo, 6 timeout, 130 interrupted).
- Secrets print as `********` everywhere; the set record holds no variable values.
- Interrupting or timing out never stops a build. Nothing in this feature calls `StopBuild`.
- `--json` field names are a public contract: add, never rename.
- No test sleeps or touches the network; time comes from the injected `Clock`.
- Conventional Commits, one commit per task step that says "Commit", owner's git identity only, no trailers.
- Run `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"` in non-interactive shells.

## Review Focus

1. **A run whose preset `branch` names a branch that does not exist** — `PlanSet` must report it with every other error, not fail on the first. Test in Task 4.
2. **Two runs in one stage that finish in the same poll** — both `run_done` events arrive and the stage ends once, not twice. Test in Task 5.
3. **Resume when every run of the current stage had already finished while bam was away** — the stage completes from the watch results and the next stage triggers. Test in Task 6.
4. **Lock left behind by a crashed process** — a second run refuses with pid and host, and `--force` recovers. Test in Task 3.
5. **Non-terminal output with many runs** — each change is one line that names the stage and preset, so CI logs stay readable. Test in Task 8.

---

### Task 1: `sets:` in the configuration

**Files:**
- Modify: `internal/config/schema.go` (types, `Sets` fields)
- Create: `internal/config/sets.go` (`ResolvedSet`, `Config.Sets`, `Config.Set`, checks)
- Modify: `internal/config/validate.go` (`validateFiles` calls the name and literal-secret checks for sets)
- Test: `internal/config/sets_test.go`

**Interfaces:**
- Produces:
  ```go
  type Set struct { Stages []Stage `yaml:"stages"`; Timeout Duration `yaml:"timeout,omitempty"` }
  type Stage []SetRun
  type SetRun struct { Target string `yaml:"target"`; Vars StringMap `yaml:"vars,omitempty"` }
  type ResolvedSet struct { Name string; Set; DefinedIn []string }
  func (c *Config) Sets() (map[string]ResolvedSet, error)
  func (c *Config) Set(name string) (ResolvedSet, bool, error)
  ```
  `ProjectFile.Sets`, `MachineFile.Sets`, `Repo.Sets`, each `map[string]Set \`yaml:"sets,omitempty"\``.

- [ ] **Step 1: Write the failing tests**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/require"
)

func loadYAML(t *testing.T, project, machine string) (*Config, error) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	if project != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, ProjectFileName), []byte(project), 0o644))
	}
	mpath := filepath.Join(t.TempDir(), "config.yaml")
	if machine != "" {
		require.NoError(t, os.WriteFile(mpath, []byte(machine), 0o600))
	}
	return Load(LoadOptions{WorkDir: root, Home: t.TempDir(), MachineFile: mpath, Getenv: func(string) string { return "" }})
}

const setsProject = `version: 1
servers: {work: {url: https://bamboo.example.com}}
targets:
  publish-specs: {plan: PROJ-SPEC}
  provision: {plan: OPS-PROV}
  deploy: {plan: OPS-DEP}
sets:
  release:
    timeout: 2h
    stages:
      - [publish-specs]
      - - provision
        - target: deploy
          vars: {env: qa, deploy_token: "${DEPLOY_TOKEN}"}
`

func TestSetsParseShorthandAndLongForm(t *testing.T) {
	cfg, err := loadYAML(t, setsProject, "")
	require.NoError(t, err)
	s, ok, err := cfg.Set("release")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2*time.Hour, time.Duration(s.Timeout))
	require.Equal(t, Stage{{Target: "publish-specs"}}, s.Stages[0])
	require.Equal(t, Stage{{Target: "provision"},
		{Target: "deploy", Vars: StringMap{"env": "qa", "deploy_token": "${DEPLOY_TOKEN}"}}}, s.Stages[1])
	require.Len(t, s.DefinedIn, 1)
}

func TestAHigherLayerReplacesASetWhole(t *testing.T) {
	machine := `version: 1
sets:
  release:
    stages: [[deploy]]
`
	cfg, err := loadYAML(t, setsProject, machine)
	require.NoError(t, err)
	s, _, err := cfg.Set("release")
	require.NoError(t, err)
	require.Equal(t, []Stage{{{Target: "deploy"}}}, s.Stages)
	require.Zero(t, s.Timeout, "nothing of the lower layer survives")
	require.Len(t, s.DefinedIn, 2)
}

func TestSetConfigErrors(t *testing.T) {
	base := "version: 1\nservers: {work: {url: https://bamboo.example.com}, lab: {url: https://bamboo.lab.example}}\n" +
		"targets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B, server: work}\n  c: {plan: PROJ-C, server: lab}\n"
	for name, tc := range map[string]struct{ sets, want string }{
		"unknown preset": {"sets: {rel: {stages: [[nope]]}}", `unknown preset "nope"`},
		"no stages":      {"sets: {rel: {stages: []}}", "has no stages"},
		"empty stage":    {"sets: {rel: {stages: [[a], []]}}", "stage 2 has no runs"},
		"duplicate":      {"sets: {rel: {stages: [[a, a]]}}", `preset "a" twice in stage 1`},
		"servers":        {"sets: {rel: {stages: [[b], [c]]}}", `servers "work" and "lab"`},
		"bad name":       {"sets: {Rel: {stages: [[a]]}}", `invalid set name "Rel"`},
		"bad timeout":    {"sets: {rel: {timeout: soon, stages: [[a]]}}", "not a duration"},
		"literal secret": {"sets: {rel: {stages: [[{target: a, vars: {db_password: hunter2}}]]}}", "holds a literal secret"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadYAML(t, base+tc.sets+"\n", "")
			if err == nil {
				_, err = cfg.Sets()
			}
			require.Error(t, err)
			require.Equal(t, errs.KindConfig, errs.KindOf(err))
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestSetVarsAcceptEnvReferences(t *testing.T) {
	cfg, err := loadYAML(t, setsProject, "")
	require.NoError(t, err)
	_, err = cfg.Sets()
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/config/ -run 'Set' -v`
Expected: compile failure, `cfg.Set undefined`.

- [ ] **Step 3: Implement**

In `schema.go` add the three types and the `Sets` fields (after `Targets` in each struct). `Stage` needs `UnmarshalYAML`:

```go
// Set is a named release train: stages run in order, runs inside a stage
// run in parallel.
type Set struct {
	Stages  []Stage  `yaml:"stages"`
	Timeout Duration `yaml:"timeout,omitempty"`
}

// Stage is one step of a set. A bare name is shorthand for {target: name}.
type Stage []SetRun

func (s *Stage) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return fmt.Errorf("line %d: a stage is a list of presets", n.Line)
	}
	out := make(Stage, 0, len(n.Content))
	for _, item := range n.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			out = append(out, SetRun{Target: item.Value})
		case yaml.MappingNode:
			var r SetRun
			if err := item.Decode(&r); err != nil {
				return err
			}
			out = append(out, r)
		default:
			return fmt.Errorf("line %d: a stage run is a preset name or {target, vars}", item.Line)
		}
	}
	*s = out
	return nil
}

// SetRun is one preset run of a stage, with variables that override the
// preset's defaults for this set only.
type SetRun struct {
	Target string    `yaml:"target"`
	Vars   StringMap `yaml:"vars,omitempty"`
}
```

`decodeStrict` uses `KnownFields`; `item.Decode` does not inherit it, so unknown keys in the long form must be checked by hand: after `item.Decode`, walk `item.Content` keys and reject anything other than `target` and `vars` with `fmt.Errorf("line %d: unknown field %q in a stage run", ...)`.

In `validate.go`, `validateFiles`: call `checkSetNames(path, where, sets)` for the project file, the machine top level and every `repos.<key>`, reusing `targetNameRe` with the message `invalid set name %q`. For the project file only, reject a literal secret in any set run's `vars`, with the same message shape as for target defaults: `%s: sets.%s.stages[%d].%s.vars.%s holds a literal secret`.

Create `sets.go`:

```go
package config

import (
	"fmt"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// ResolvedSet is a set from the highest layer that defines it. Sets are
// replaced whole, never merged: merging stage lists would hide which stages
// run.
type ResolvedSet struct {
	Name string
	Set
	DefinedIn []string // every layer that defines the set, lowest precedence first; the last one won
}

// Sets returns every set, validated against the merged targets.
func (c *Config) Sets() (map[string]ResolvedSet, error) {
	out := map[string]ResolvedSet{}
	layer := func(label string, sets map[string]Set) {
		for name, s := range sets {
			r := out[name]
			r.Name, r.Set = name, s
			r.DefinedIn = append(r.DefinedIn, label)
			out[name] = r
		}
	}
	if c.Project != nil {
		layer(c.ProjectPath, c.Project.Sets)
	}
	layer(c.MachinePath, c.Machine.Sets)
	if _, repo, ok := c.repoEntry(); ok {
		layer(c.MachinePath+" (repos)", repo.Sets)
	}
	if len(out) == 0 {
		return out, nil
	}
	targets, err := c.Targets()
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(out) {
		if err := validateSet(out[name], targets); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Set returns one set.
func (c *Config) Set(name string) (ResolvedSet, bool, error) {
	all, err := c.Sets()
	if err != nil {
		return ResolvedSet{}, false, err
	}
	s, ok := all[name]
	return s, ok, nil
}

func validateSet(s ResolvedSet, targets map[string]ResolvedTarget) error {
	where := s.DefinedIn[len(s.DefinedIn)-1]
	fail := func(format string, a ...any) error {
		return errs.Configf("set %q: "+format, append([]any{s.Name}, a...)...).WithWhy("defined in " + where)
	}
	if len(s.Stages) == 0 {
		return fail("has no stages").WithTry(fmt.Sprintf("add stages: [[<preset>]] under sets.%s", s.Name))
	}
	server, serverFrom := "", ""
	for i, st := range s.Stages {
		if len(st) == 0 {
			return fail("stage %d has no runs", i+1)
		}
		seen := map[string]bool{}
		for _, r := range st {
			t, ok := targets[r.Target]
			if !ok {
				return fail("unknown preset %q in stage %d", r.Target, i+1).WithTry("bam target list")
			}
			if seen[r.Target] {
				return fail("preset %q twice in stage %d", r.Target, i+1)
			}
			seen[r.Target] = true
			if t.Server == "" {
				continue
			}
			if server != "" && t.Server != server {
				return fail("presets %q and %q use servers %q and %q", serverFrom, r.Target, server, t.Server).
					WithTry("a set runs on one server; split it into one set per server")
			}
			server, serverFrom = t.Server, r.Target
		}
	}
	return nil
}
```

A bad `timeout` fails at decode with the existing `Duration` message ("not a duration").

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v -run 'Set'` then `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: read release train sets from the configuration"
```

### Task 2: fake provider triggers per plan

**Files:**
- Modify: `internal/provider/fake/fake.go:15-43,165-170`
- Test: `internal/provider/fake/fake_test.go`

**Interfaces:**
- Produces: `fake.Provider.TriggerResults map[string]provider.Build`, `fake.Provider.TriggerErrs map[string]error`, both keyed by `TriggerRequest.PlanKey`; `TriggerResult`/`TriggerErr` apply when the key is missing.

- [ ] **Step 1: Failing test**

```go
func TestTriggerAnswersPerPlan(t *testing.T) {
	boom := errors.New("boom")
	f := &Provider{
		TriggerResult:  provider.Build{Key: "X-1"},
		TriggerResults: map[string]provider.Build{"PROJ-A": {Key: "PROJ-A-1"}},
		TriggerErrs:    map[string]error{"PROJ-B": boom},
	}
	b, err := f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-A"})
	require.NoError(t, err)
	require.Equal(t, "PROJ-A-1", b.Key)
	_, err = f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-B"})
	require.ErrorIs(t, err, boom)
	b, err = f.Trigger(context.Background(), provider.TriggerRequest{PlanKey: "PROJ-C"})
	require.NoError(t, err)
	require.Equal(t, "X-1", b.Key)
}
```

- [ ] **Step 2: Run** `go test ./internal/provider/fake/ -run TestTriggerAnswersPerPlan` — Expected: compile failure.

- [ ] **Step 3: Implement**

```go
func (f *Provider) Trigger(_ context.Context, req provider.TriggerRequest) (provider.Build, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Triggered = append(f.Triggered, req)
	if err, ok := f.TriggerErrs[req.PlanKey]; ok {
		return provider.Build{}, err
	}
	if b, ok := f.TriggerResults[req.PlanKey]; ok {
		return b, nil
	}
	return f.TriggerResult, f.TriggerErr
}
```

Add the fields under `TriggerErr` with a comment: `// by plan key; TriggerResult and TriggerErr apply when a key is missing`.

- [ ] **Step 4: Run** `go test ./internal/provider/fake/` — PASS.

- [ ] **Step 5: Commit** `git commit -am "test: let the fake provider answer a trigger per plan"`

### Task 3: the set record and lock (`SetStore`)

**Files:**
- Create: `internal/app/setstore.go`
- Modify: `internal/app/service.go` (field `Sets *SetStore`)
- Test: `internal/app/setstore_test.go`

**Interfaces:**
- Produces:
  ```go
  type SetStatus string // "running" | "successful" | "failed" | "interrupted" | "timed_out"
  const (SetRunning SetStatus = "running"; SetSuccessful = "successful"; SetFailed = "failed"; SetInterrupted = "interrupted"; SetTimedOut = "timed_out")
  type SetRecord struct {
  	Version    int           `json:"version"`
  	ID         string        `json:"id"`
  	Set        string        `json:"set"`
  	Origin     string        `json:"origin"`
  	Repo       string        `json:"repo"`
  	StartedAt  time.Time     `json:"started_at"`
  	FinishedAt *time.Time    `json:"finished_at,omitempty"`
  	Status     SetStatus     `json:"status"`
  	VarNames   []string      `json:"var_names"`
  	Stages     []StageRecord `json:"stages"`
  }
  type StageRecord struct { Runs []RunRecord `json:"runs"` }
  type RunRecord struct {
  	Target   string         `json:"target"`
  	PlanKey  string         `json:"plan_key"`
  	BuildKey string         `json:"build_key"`
  	State    provider.State `json:"state"`
  	Error    string         `json:"error,omitempty"`
  }
  type SetStore struct { Dir string; Host func() (string, error); PID func() int }
  func NewSetID(set string, now time.Time) string            // "<set>-20060102T150405Z"
  func (s *SetStore) Save(r SetRecord) error                 // atomic tmp+rename, 0600, dir 0700
  func (s *SetStore) Load(id string) (SetRecord, error)      // ErrNotFound-wrapped usage error when missing
  func (s *SetStore) Latest(repo, set string, match func(SetRecord) bool) (SetRecord, bool, error) // set=="" means any
  func (s *SetStore) IDs() ([]string, error)                 // for completion, newest first
  func (s *SetStore) Prune(set string, keep int) error
  type SetLock struct{ /* paths */ }
  func (s *SetStore) Lock(names []string, force bool, now time.Time) (*SetLock, error)
  func (l *SetLock) Release()
  ```

- [ ] **Step 1: Failing tests**

```go
func newSetStore(t *testing.T) *SetStore {
	return &SetStore{Dir: filepath.Join(t.TempDir(), "sets"),
		Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}
}

func TestSetRecordRoundTripAndMode(t *testing.T) {
	s := newSetStore(t)
	now := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	id := NewSetID("release", now)
	require.Equal(t, "release-20260929T101500Z", id)
	rec := SetRecord{Version: 1, ID: id, Set: "release", Origin: "https://bamboo.example.com", Repo: "/src/app",
		StartedAt: now, Status: SetRunning, VarNames: []string{"env"},
		Stages: []StageRecord{{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-SPEC"}}}}}
	require.NoError(t, s.Save(rec))
	fi, err := os.Stat(filepath.Join(s.Dir, id+".json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	got, err := s.Load(id)
	require.NoError(t, err)
	require.Equal(t, rec, got)
}

func TestLoadMissingRecordIsUsage(t *testing.T) {
	_, err := newSetStore(t).Load("release-20260101T000000Z")
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
}

func TestLatestPicksNewestForRepoAndSet(t *testing.T) {
	s := newSetStore(t)
	at := func(m int) time.Time { return time.Date(2026, 9, 29, 10, m, 0, 0, time.UTC) }
	for _, r := range []SetRecord{
		{ID: NewSetID("release", at(1)), Set: "release", Repo: "/a", StartedAt: at(1), Status: SetFailed},
		{ID: NewSetID("release", at(2)), Set: "release", Repo: "/a", StartedAt: at(2), Status: SetInterrupted},
		{ID: NewSetID("release", at(3)), Set: "release", Repo: "/b", StartedAt: at(3), Status: SetInterrupted},
		{ID: NewSetID("nightly", at(4)), Set: "nightly", Repo: "/a", StartedAt: at(4), Status: SetSuccessful},
	} {
		r.Version = 1
		require.NoError(t, s.Save(r))
	}
	r, ok, err := s.Latest("/a", "", nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "nightly", r.Set)
	r, _, _ = s.Latest("/a", "release", nil)
	require.Equal(t, at(2), r.StartedAt)
	r, _, _ = s.Latest("/a", "", func(r SetRecord) bool { return r.Status == SetFailed })
	require.Equal(t, at(1), r.StartedAt)
}

func TestPruneKeepsNewestOfOneSet(t *testing.T) {
	s := newSetStore(t)
	for m := 0; m < 25; m++ {
		at := time.Date(2026, 9, 29, 10, m, 0, 0, time.UTC)
		require.NoError(t, s.Save(SetRecord{Version: 1, ID: NewSetID("release", at), Set: "release", StartedAt: at}))
	}
	other := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, s.Save(SetRecord{Version: 1, ID: NewSetID("nightly", other), Set: "nightly", StartedAt: other}))
	require.NoError(t, s.Prune("release", 20))
	ids, err := s.IDs()
	require.NoError(t, err)
	require.Len(t, ids, 21)
	require.Equal(t, NewSetID("release", time.Date(2026, 9, 29, 10, 24, 0, 0, time.UTC)), ids[0])
	require.Contains(t, ids, NewSetID("nightly", other))
}

func TestLockRefusesASecondHolderAndForceTakesOver(t *testing.T) {
	s := newSetStore(t)
	now := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	l, err := s.Lock([]string{"release", "release-20260929T101500Z"}, false, now)
	require.NoError(t, err)
	_, err = s.Lock([]string{"release"}, false, now)
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Contains(t, err.Error(), "pid 4242 on laptop")
	// A crashed holder never releases: --force replaces its files.
	l2, err := s.Lock([]string{"release"}, true, now)
	require.NoError(t, err)
	l2.Release()
	l.Release() // releasing twice, or after a takeover, is harmless
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		require.NotContains(t, e.Name(), ".lock")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'SetRecord|LoadMissing|Latest|Prune|Lock' ` — compile failure.

- [ ] **Step 3: Implement `setstore.go`**

```go
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
)

const setIDTime = "20060102T150405Z"

// NewSetID names one run of a set by its start time in UTC.
func NewSetID(set string, now time.Time) string { return set + "-" + now.UTC().Format(setIDTime) }

// SetStore keeps one record per set run in Dir ($XDG_STATE_HOME/bam/sets).
// Records hold no variable values.
type SetStore struct {
	Dir  string
	Host func() (string, error)
	PID  func() int
}

func (s *SetStore) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// Save writes a record atomically.
func (s *SetStore) Save(r SetRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(r.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(r.ID))
}

// Load reads one record.
func (s *SetStore) Load(id string) (SetRecord, error) {
	var r SetRecord
	data, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return r, errs.Usagef("no set run %q", id).WithTry("bam set status").Wrap(errs.ErrNotFound)
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, errs.Configf("set record %s is corrupt", s.path(id)).Wrap(err)
	}
	return r, nil
}

// all returns every readable record, newest first. Corrupt files are skipped.
func (s *SetStore) all() ([]SetRecord, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SetRecord
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		if r, err := s.Load(id); err == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// Latest is the newest record of this repository, of set when set is not
// empty, that match accepts (nil accepts all).
func (s *SetStore) Latest(repo, set string, match func(SetRecord) bool) (SetRecord, bool, error) {
	all, err := s.all()
	if err != nil {
		return SetRecord{}, false, err
	}
	for _, r := range all {
		if r.Repo == repo && (set == "" || r.Set == set) && (match == nil || match(r)) {
			return r, true, nil
		}
	}
	return SetRecord{}, false, nil
}

// IDs lists every record id, newest first.
func (s *SetStore) IDs() ([]string, error) {
	all, err := s.all()
	ids := make([]string, 0, len(all))
	for _, r := range all {
		ids = append(ids, r.ID)
	}
	return ids, err
}

// Prune deletes set's records beyond the newest keep.
func (s *SetStore) Prune(set string, keep int) error {
	all, err := s.all()
	if err != nil {
		return err
	}
	n := 0
	for _, r := range all {
		if r.Set != set {
			continue
		}
		if n++; n > keep {
			if err := os.Remove(s.path(r.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// SetLock is held while a set runs, so two terminals never trigger the same
// train.
type SetLock struct{ paths []string }

type lockInfo struct {
	PID   int       `json:"pid"`
	Host  string    `json:"host"`
	Since time.Time `json:"since"`
}

// Lock creates <name>.lock for every name, or fails naming the holder. With
// force it replaces existing lock files: the way out after a crash.
func (s *SetStore) Lock(names []string, force bool, now time.Time) (*SetLock, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	host, _ := s.Host()
	data, _ := json.Marshal(lockInfo{PID: s.PID(), Host: host, Since: now})
	l := &SetLock{}
	for _, name := range names {
		p := filepath.Join(s.Dir, name+".lock")
		if force {
			_ = os.Remove(p)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			l.Release()
			var held lockInfo
			if b, rerr := os.ReadFile(p); rerr == nil {
				_ = json.Unmarshal(b, &held)
			}
			return nil, errs.Usagef("set run in progress (pid %d on %s, since %s)", held.PID, held.Host, held.Since.Local().Format("15:04")).
				WithWhy(p + " exists").WithTry("pass --force if that process is gone")
		}
		if err != nil {
			l.Release()
			return nil, err
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		l.paths = append(l.paths, p)
		if werr != nil || cerr != nil {
			l.Release()
			return nil, fmt.Errorf("write %s: %w", p, errors.Join(werr, cerr))
		}
	}
	return l, nil
}

// Release removes the lock files. It is safe to call more than once.
func (l *SetLock) Release() {
	if l == nil {
		return
	}
	for _, p := range l.paths {
		_ = os.Remove(p)
	}
	l.paths = nil
}
```

Put `SetStatus`, `SetRecord`, `StageRecord`, `RunRecord` (as in Interfaces) at the top of the file. Add `Sets *SetStore` to `Service` after `State`, and in `apptest_test.go`'s `newService` set `Sets: &SetStore{Dir: filepath.Join(t.TempDir(), "sets"), Host: func() (string, error) { return "laptop", nil }, PID: func() int { return 4242 }}`.

Note on `Release` after a `--force` takeover: the first holder's `Release` would delete the new holder's file. That is acceptable only because a takeover means the first holder is gone; the test documents it.

- [ ] **Step 4: Run** `go test ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: record set runs and lock them in the state directory"`

### Task 4: `PlanSet` resolves every run before anything triggers

**Files:**
- Create: `internal/app/sets.go`
- Test: `internal/app/sets_test.go`

**Interfaces:**
- Consumes: `config.Config.Set`, `Service.ResolvePlan(ctx, name, "")`, `Service.VarBase`, `ValidateVars`, `config.Config.SelectServer`.
- Produces:
  ```go
  type SetOptions struct {
  	Vars   []string // --var k=v, in order
  	Server string   // --server, for the origin check
  }
  type SetRunPlan struct { Target string; Ref PlanRef; Vars VarSet; Timeout time.Duration }
  type SetPlan struct {
  	Name     string
  	Origin   string
  	Timeout  time.Duration
  	VarNames []string
  	Stages   [][]SetRunPlan
  	Warnings []string
  }
  func (s *Service) PlanSet(ctx context.Context, name string, o SetOptions) (SetPlan, error)
  ```

- [ ] **Step 1: Failing tests** (in `sets_test.go`; helper `setService` extends `newService` with a set)

```go
func setService(t *testing.T, p *fake.Provider) *Service {
	t.Helper()
	svc := newService(t, p)
	svc.Cfg.Project.Targets["publish-specs"] = config.Target{Plan: "PROJ-BUILD"}
	svc.Cfg.Project.Sets = map[string]config.Set{"release": {Stages: []config.Stage{
		{{Target: "publish-specs"}},
		{{Target: "provision-lab", Vars: config.StringMap{"cluster_name": "alpha", "compute_nodes": "4"}}},
	}}}
	return svc
}

func TestPlanSetResolvesEveryRunWithSetVarsOverDefaults(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(k string) string { return map[string]string{"LAB_DB_PASSWORD": "s3cret"}[k] }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"compute_nodes=5"}})
	require.NoError(t, err)
	require.Equal(t, "https://bamboo.example.com", p.Origin)
	require.Len(t, p.Stages, 2)
	require.Equal(t, "PROJ-BUILD", p.Stages[0][0].Ref.PlanKey)
	prov := p.Stages[1][0]
	require.Equal(t, "PROJ-PROV12", prov.Ref.PlanKey, "the preset's branch applies")
	name, _ := prov.Vars.Get("cluster_name")
	require.Equal(t, "alpha", name.Value, "set vars beat preset defaults")
	nodes, _ := prov.Vars.Get("compute_nodes")
	require.Equal(t, "5", nodes.Value, "--var beats set vars")
	require.Equal(t, []string{"compute_nodes"}, p.VarNames)
}

func TestPlanSetCollectsEveryError(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Targets["provision-lab"] = func() config.Target {
		tg := svc.Cfg.Project.Targets["provision-lab"]
		tg.Branch = "no-such-branch"
		return tg
	}()
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "provision-lab"}},
		{{Target: "build", Vars: config.StringMap{}}},
		{{Target: "publish-specs"}},
	}}
	svc.Cfg.Project.Targets["build"] = config.Target{Plan: "PROJ-BUILD", Required: []string{"release_tag"}}
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Error(t, err)
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
	require.Contains(t, err.Error(), "provision-lab")
	require.Contains(t, err.Error(), "no-such-branch")
	require.Contains(t, err.Error(), "build")
	require.Contains(t, err.Error(), "release_tag")
	require.Empty(t, svc.P.(*fake.Provider).Triggered)
}

func TestPlanSetWarnsAboutAnUndeclaredVar(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"global_flag=1"}})
	require.NoError(t, err)
	require.NotEmpty(t, p.Warnings)
	require.Contains(t, strings.Join(p.Warnings, "\n"), "global_flag")
}

func TestPlanSetRejectsAnotherServer(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Cfg.Project.Servers["lab"] = config.Server{URL: "https://bamboo.lab.example"}
	tg := svc.Cfg.Project.Targets["publish-specs"]
	tg.Server = "lab"
	svc.Cfg.Project.Targets["publish-specs"] = tg
	_, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "bamboo.lab.example")
}

func TestPlanSetUnknownSet(t *testing.T) {
	_, err := setService(t, fakeBamboo()).PlanSet(context.Background(), "nope", SetOptions{})
	require.Equal(t, errs.KindUsage, errs.KindOf(err))
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run PlanSet` — compile failure.

- [ ] **Step 3: Implement** in `sets.go`:

```go
// PlanSet resolves and checks every run of a set, so every usage error shows
// before the first trigger. It triggers nothing.
func (s *Service) PlanSet(ctx context.Context, name string, o SetOptions) (SetPlan, error) {
	set, ok, err := s.Cfg.Set(name)
	if err != nil {
		return SetPlan{}, err
	}
	if !ok {
		return SetPlan{}, errs.Usagef("unknown set %q", name).WithTry("bam set list")
	}
	p := SetPlan{Name: name, Origin: s.Origin, Timeout: time.Duration(set.Timeout), VarNames: varNames(o.Vars)}
	var problems []string
	for i, st := range set.Stages {
		var runs []SetRunPlan
		for _, r := range st {
			label := fmt.Sprintf("stage %d %s", i+1, r.Target)
			rp, warnings, err := s.planRun(ctx, r, o)
			if err != nil {
				if errs.KindOf(err) == errs.KindConfig || errs.KindOf(err) == errs.KindAuth || errs.KindOf(err) == errs.KindBamboo {
					return SetPlan{}, err
				}
				problems = append(problems, label+": "+describeErr(err))
				continue
			}
			for _, w := range warnings {
				p.Warnings = append(p.Warnings, r.Target+": "+w)
			}
			runs = append(runs, rp)
		}
		p.Stages = append(p.Stages, runs)
	}
	if len(problems) > 0 {
		return SetPlan{}, errs.Usagef("set %q cannot run: %d problem(s)", name, len(problems)).
			WithWhy(strings.Join(problems, "\n")).WithTry("fix the presets or pass --var, then try again")
	}
	return p, nil
}

func (s *Service) planRun(ctx context.Context, r config.SetRun, o SetOptions) (SetRunPlan, []string, error) {
	t, _, err := s.Cfg.Target(r.Target)
	if err != nil {
		return SetRunPlan{}, nil, err
	}
	server, err := s.Cfg.SelectServer(o.Server, t.Server)
	if err != nil {
		return SetRunPlan{}, nil, err
	}
	if origin, err := credential.Origin(server.URL); err != nil || origin != s.Origin {
		return SetRunPlan{}, nil, errs.Configf("preset %q runs on %s, not %s", r.Target, server.URL, s.Origin).
			WithWhy("a set runs on one server").WithTry("give the presets of one set the same server")
	}
	ref, err := s.ResolvePlan(ctx, r.Target, "")
	if err != nil {
		return SetRunPlan{}, nil, err
	}
	if len(r.Vars) > 0 {
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
	base, err := s.VarBase(ctx, ref, "")
	if err != nil {
		return SetRunPlan{}, nil, err
	}
	vs, err := ValidateVars(ref, base, o.Vars, s.Getenv)
	if err != nil {
		return SetRunPlan{}, nil, err
	}
	return SetRunPlan{Target: r.Target, Ref: ref, Vars: vs, Timeout: time.Duration(t.Timeout)}, vs.Warnings, nil
}

func varNames(flags []string) []string {
	var out []string
	for _, f := range flags {
		if name, _, ok := strings.Cut(f, "="); ok && !slices.Contains(out, name) {
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
```

`credential` is already allowed in `app`'s import table. A missing branch comes back from `ResolvePlan` as a usage error, so it is collected; check `resolve.go` and, if it returns another kind, make `planRun` wrap only `ResolvePlan`'s not-found result as usage. `ValidateVars` warns about an undeclared `--var` name per run; `PlanSet` prefixes each warning with the preset name, so a name no run declares shows once per run. Collapse identical warning texts across runs into one line prefixed with every preset name, so a global override prints once.

- [ ] **Step 4: Run** `go test ./internal/app/ -run PlanSet -v` then `go test ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: resolve every run of a set before anything triggers"`

### Task 5: `RunSet` runs the stages and records them

**Files:**
- Modify: `internal/app/sets.go`
- Test: `internal/app/sets_test.go`

**Interfaces:**
- Consumes: `SetPlan`, `SetStore`, `Service.Run`, `Service.Watch`.
- Produces:
  ```go
  type SetEventType string
  const (SetStageStart SetEventType = "stage_start"; SetRunTriggered = "run_triggered"; SetRunEvent = "run_event"; SetRunDone = "run_done"; SetStageDone = "stage_done"; SetDone = "set_done"; SetError = "set_error")
  type SetEvent struct {
  	Type SetEventType; Time time.Time; Set, ID string; Stage int; Target string
  	Build provider.Build; Event *Event; State provider.State; Err error
  }
  type SetLockOptions struct{ Force bool }
  func (s *Service) RunSet(ctx context.Context, p SetPlan, lock SetLockOptions) (string, <-chan SetEvent, error) // returns the record id
  ```

The id is returned so the CLI can print "still running: bam set resume <id>" on interrupt.

- [ ] **Step 1: Failing tests**

```go
func drain(ch <-chan SetEvent) []SetEvent {
	var out []SetEvent
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func types(evs []SetEvent) []string {
	var out []string
	for _, e := range evs {
		if e.Type != SetRunEvent {
			out = append(out, fmt.Sprintf("%s:%d:%s", e.Type, e.Stage, e.Target))
		}
	}
	return out
}

func trainBamboo(results map[string]provider.State) *fake.Provider {
	p := fakeBamboo()
	p.TriggerResults = map[string]provider.Build{}
	p.Sequences = map[string][]provider.Build{}
	for plan, final := range results {
		key := plan + "-1"
		p.TriggerResults[plan] = provider.Build{Key: key, State: provider.StateQueued}
		p.Sequences[key] = []provider.Build{{Key: key, State: provider.StateRunning}, {Key: key, State: final}}
	}
	return p
}

func runSet(t *testing.T, svc *Service) (string, []SetEvent) {
	t.Helper()
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	return id, drain(ch)
}

func TestRunSetHappyPath(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	id, evs := runSet(t, svc)
	require.Equal(t, []string{
		"stage_start:0:", "run_triggered:0:publish-specs", "run_done:0:publish-specs", "stage_done:0:",
		"stage_start:1:", "run_triggered:1:provision-lab", "run_done:1:provision-lab", "stage_done:1:",
		"set_done:0:",
	}, types(evs))
	last := evs[len(evs)-1]
	require.Equal(t, provider.StateSuccess, last.State)
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetSuccessful, rec.Status)
	require.Equal(t, "PROJ-PROV12-1", rec.Stages[1].Runs[0].BuildKey)
	require.NotNil(t, rec.FinishedAt)
}

func TestRunSetFailedStageStopsTheTrainAndCancelsNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateFailed, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	id, evs := runSet(t, svc)
	require.Len(t, fp.Triggered, 1, "stage 2 never triggers")
	require.Empty(t, fp.Stopped)
	require.Equal(t, SetDone, evs[len(evs)-1].Type)
	require.Equal(t, provider.StateFailed, evs[len(evs)-1].State)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, SetFailed, rec.Status)
}

func TestRunSetParallelStageWaitsForEveryRun(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateFailed, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}, {Target: "provision-lab", Vars: config.StringMap{"cluster_name": "a"}}},
		{{Target: "build"}},
	}}
	_, evs := runSet(t, svc)
	var done []string
	for _, e := range evs {
		if e.Type == SetRunDone {
			done = append(done, e.Target)
		}
	}
	require.ElementsMatch(t, []string{"publish-specs", "provision-lab"}, done, "both runs finish; the stage ends once")
	n := 0
	for _, e := range evs {
		if e.Type == SetStageDone {
			n++
		}
	}
	require.Equal(t, 1, n)
	require.Len(t, fp.Triggered, 2)
}

func TestRunSetTriggerErrorFailsThatRunOnly(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	fp.TriggerErrs = map[string]error{"PROJ-BUILD": errs.Bamboof("queue full")}
	svc := setService(t, fp)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}, {Target: "provision-lab", Vars: config.StringMap{"cluster_name": "a"}}},
	}}
	id, evs := runSet(t, svc)
	var failed SetEvent
	for _, e := range evs {
		if e.Type == SetRunDone && e.Target == "publish-specs" {
			failed = e
		}
	}
	require.Error(t, failed.Err)
	require.Contains(t, types(evs), "run_done:0:provision-lab")
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, "queue full", rec.Stages[0].Runs[0].Error)
}

func TestRunSetRecordHoldsNoVariableValues(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "envsecret-9f2" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"compute_nodes=77731"}})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	data, err := os.ReadFile(filepath.Join(svc.Sets.Dir, id+".json"))
	require.NoError(t, err)
	for _, v := range []string{"77731", "alpha", "envsecret-9f2", "k8s"} {
		require.NotContains(t, string(data), v)
	}
	require.Contains(t, string(data), `"compute_nodes"`, "the name is kept for resume")
}

func TestRunSetRecordWriteFailureStopsBeforeTheTrigger(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	svc.Sets.Dir = filepath.Join(file, "sets") // a directory under a file cannot be made
	_, _, err = svc.RunSet(context.Background(), p, SetLockOptions{})
	require.Error(t, err)
	require.Empty(t, fp.Triggered)
}

func TestRunSetPresetTimeoutFailsTheRunAndStopsNothing(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	fp.TriggerResults["PROJ-BUILD"] = provider.Build{Key: "PROJ-BUILD-1", State: provider.StateQueued}
	fp.Sequences["PROJ-BUILD-1"] = []provider.Build{{Key: "PROJ-BUILD-1", State: provider.StateRunning}}
	svc := setService(t, fp)
	tg := svc.Cfg.Project.Targets["publish-specs"]
	tg.Timeout = config.Duration(time.Minute)
	svc.Cfg.Project.Targets["publish-specs"] = tg
	id, evs := runSet(t, svc)
	require.Empty(t, fp.Stopped)
	require.Equal(t, provider.StateFailed, evs[len(evs)-1].State)
	rec, _ := svc.Sets.Load(id)
	require.Contains(t, rec.Stages[0].Runs[0].Error, "timed out")
}

func TestRunSetTimeoutEndsWithATimeoutError(t *testing.T) {
	fp := trainBamboo(nil)
	fp.TriggerResults = map[string]provider.Build{"PROJ-BUILD": {Key: "PROJ-BUILD-1"}}
	fp.Sequences = map[string][]provider.Build{"PROJ-BUILD-1": {{Key: "PROJ-BUILD-1", State: provider.StateRunning}}}
	svc := setService(t, fp)
	s := svc.Cfg.Project.Sets["release"]
	s.Timeout = config.Duration(time.Hour)
	svc.Cfg.Project.Sets["release"] = s
	id, evs := runSet(t, svc)
	last := evs[len(evs)-1]
	require.Equal(t, SetError, last.Type)
	require.Equal(t, errs.KindTimeout, errs.KindOf(last.Err))
	require.Empty(t, fp.Stopped)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, SetTimedOut, rec.Status)
}

func TestRunSetInterruptLeavesAResumableRecord(t *testing.T) {
	fp := trainBamboo(nil)
	fp.TriggerResults = map[string]provider.Build{"PROJ-BUILD": {Key: "PROJ-BUILD-1"}}
	fp.Sequences = map[string][]provider.Build{"PROJ-BUILD-1": {{Key: "PROJ-BUILD-1", State: provider.StateRunning}}}
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	id, ch, err := svc.RunSet(ctx, p, SetLockOptions{})
	require.NoError(t, err)
	for e := range ch {
		if e.Type == SetRunTriggered {
			cancel()
		}
		require.NotEqual(t, SetDone, e.Type)
	}
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, SetInterrupted, rec.Status)
	require.Equal(t, "PROJ-BUILD-1", rec.Stages[0].Runs[0].BuildKey)
	_, err = os.Stat(filepath.Join(svc.Sets.Dir, "release.lock"))
	require.ErrorIs(t, err, fs.ErrNotExist, "the lock is released")
}
```

The fake clock fires every `After` at once, so a running build with a timeout times out on the first wait, and one without a timeout is polled until its sequence ends (the last entry repeats — a sequence that ends `Running` never finishes; only use that together with a timeout or a cancel).

- [ ] **Step 2: Run** `go test ./internal/app/ -run RunSet` — compile failure.

- [ ] **Step 3: Implement**

```go
// RunSet runs a planned set stage by stage. It returns an error, and no
// channel, only when the set cannot start: a held lock or a record that
// cannot be written. Ending ctx stops watching and triggering; no build is
// ever stopped.
func (s *Service) RunSet(ctx context.Context, p SetPlan, lock SetLockOptions) (string, <-chan SetEvent, error) {
	now := s.Clock.Now()
	rec := SetRecord{Version: 1, ID: NewSetID(p.Name, now), Set: p.Name, Origin: p.Origin,
		Repo: s.Cfg.RepoRoot, StartedAt: now.UTC(), Status: SetRunning, VarNames: nonNilStrings(p.VarNames)}
	for _, st := range p.Stages {
		var sr StageRecord
		for _, r := range st {
			sr.Runs = append(sr.Runs, RunRecord{Target: r.Target, PlanKey: r.Ref.PlanKey})
		}
		rec.Stages = append(rec.Stages, sr)
	}
	l, err := s.Sets.Lock([]string{p.Name, rec.ID}, lock.Force, now)
	if err != nil {
		return "", nil, err
	}
	if err := s.Sets.Save(rec); err != nil {
		l.Release()
		return "", nil, errs.Configf("cannot record the set run").WithWhy(err.Error()).Wrap(err)
	}
	_ = s.Sets.Prune(p.Name, 20)
	ch := make(chan SetEvent, 16)
	go func() {
		defer close(ch)
		defer l.Release()
		(&setRun{s: s, p: p, rec: rec, ch: ch}).run(ctx, 0)
	}()
	return rec.ID, ch, nil
}
```

The runner, in the same file:

```go
// setRun is one execution of a set: it owns the record while the set runs.
type setRun struct {
	s   *Service
	p   SetPlan
	rec SetRecord
	ch  chan<- SetEvent
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

// save writes the record; a failure ends the set before anything else
// triggers.
func (r *setRun) save(ctx context.Context) bool {
	if err := r.s.Sets.Save(r.rec); err != nil {
		r.send(ctx, SetEvent{Type: SetError, Err: errs.Configf("cannot record the set run").WithWhy(err.Error()).Wrap(err)})
		return false
	}
	return true
}

func (r *setRun) finish(status SetStatus) {
	t := r.s.Clock.Now().UTC()
	r.rec.Status, r.rec.FinishedAt = status, &t
	_ = r.s.Sets.Save(r.rec)
}

// run executes stages from `from` onwards. Runs of the first stage that
// already have a build key are watched instead of triggered (resume).
func (r *setRun) run(parent context.Context, from int) {
	var setDeadline <-chan time.Time
	if r.p.Timeout > 0 {
		setDeadline = r.s.Clock.After(r.p.Timeout)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	timedOut := make(chan struct{})
	if setDeadline != nil {
		go func() {
			select {
			case <-setDeadline:
				close(timedOut)
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	for i := from; i < len(r.p.Stages); i++ {
		ok := r.stage(ctx, i)
		select {
		case <-timedOut:
			r.finish(SetTimedOut)
			r.send(parent, SetEvent{Type: SetError, Stage: i,
				Err: errs.New(errs.KindTimeout, fmt.Sprintf("set %s timed out after %s", r.p.Name, r.p.Timeout)).
					WithWhy("its builds keep running").WithTry("bam set resume " + r.rec.ID)})
			return
		default:
		}
		if parent.Err() != nil {
			r.finish(SetInterrupted)
			return
		}
		if !ok {
			r.finish(SetFailed)
			r.send(ctx, SetEvent{Type: SetDone, Stage: i, State: provider.StateFailed})
			return
		}
	}
	r.finish(SetSuccessful)
	r.send(ctx, SetEvent{Type: SetDone, Stage: len(r.p.Stages) - 1, State: provider.StateSuccess})
}
```

Note the ordering problem with the fake clock: `r.s.Clock.After(r.p.Timeout)` fires immediately in tests, so the set-timeout test sees the timeout before or during stage 1, and every other test must not set a set timeout. That is intended.

`stage` triggers sequentially, then watches in parallel, and records each change:

```go
type runResult struct {
	idx   int
	build provider.Build
	err   error
}

// stage runs stage i to the end and reports whether every run succeeded.
func (r *setRun) stage(ctx context.Context, i int) bool {
	if !r.send(ctx, SetEvent{Type: SetStageStart, Stage: i}) {
		return false
	}
	runs := r.p.Stages[i]
	recs := r.rec.Stages[i].Runs
	results := make(chan runResult, len(runs))
	var wg sync.WaitGroup
	pending := 0
	for j, run := range runs {
		if recs[j].State.Finished() || recs[j].Error != "" {
			continue // resumed: already known
		}
		key := recs[j].BuildKey
		if key == "" {
			if ctx.Err() != nil {
				return false
			}
			b, err := r.s.Run(ctx, run.Ref, run.Vars)
			if err != nil {
				recs[j].Error = describeErr(err)
				recs[j].State = provider.StateFailed
				if !r.save(ctx) {
					return false
				}
				r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, State: provider.StateFailed, Err: err})
				continue
			}
			key = b.Key
			recs[j].BuildKey = key
			if !r.save(ctx) {
				return false
			}
			r.send(ctx, SetEvent{Type: SetRunTriggered, Stage: i, Target: run.Target, Build: b})
		}
		pending++
		wg.Add(1)
		go func(j int, run SetRunPlan, key string) {
			defer wg.Done()
			results <- r.watchRun(ctx, i, j, run, key)
		}(j, run, key)
	}
	for ; pending > 0; pending-- {
		res := <-results
		if ctx.Err() != nil && res.err == nil && !res.build.State.Finished() {
			continue // interrupted: the build keeps running; leave its state unknown
		}
		run := runs[res.idx]
		st := res.build.State
		if res.err != nil {
			st = provider.StateFailed
			recs[res.idx].Error = describeErr(res.err)
		}
		recs[res.idx].State = st
		if !r.save(ctx) {
			wg.Wait()
			return false
		}
		r.send(ctx, SetEvent{Type: SetRunDone, Stage: i, Target: run.Target, Build: res.build, State: st, Err: res.err})
	}
	wg.Wait()
	if ctx.Err() != nil {
		_ = r.s.Sets.Save(r.rec)
		return false
	}
	ok := true
	for _, rr := range recs {
		ok = ok && rr.State == provider.StateSuccess
	}
	state := provider.StateSuccess
	if !ok {
		state = provider.StateFailed
	}
	r.send(ctx, SetEvent{Type: SetStageDone, Stage: i, State: state})
	return ok
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
			r.send(ctx, SetEvent{Type: SetRunEvent, Stage: i, Target: run.Target, Build: e.Build, Event: &ev})
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
```

The results channel is buffered to `len(runs)`, so a watcher never blocks on it after the stage loop stopped reading. `SetEvent` sends select on `ctx`, so no goroutine blocks after an interrupt. Add `func nonNilStrings(s []string) []string` returning `[]string{}` for nil so `var_names` is `[]`, not `null`. The record keeps every `RunRecord` pointer-free: `recs` aliases `r.rec.Stages[i].Runs`, and the slice is only touched by the goroutine running `stage`, never by watchers.

- [ ] **Step 4: Run** `go test -race ./internal/app/ -run 'RunSet' -v` then `go test -race ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: run a set stage by stage and record every change"`

### Task 6: `ResumeSet`

**Files:**
- Modify: `internal/app/sets.go`
- Test: `internal/app/sets_test.go`

**Interfaces:**
- Produces: `func (s *Service) ResumeSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error)`; `func (s *Service) SetStatus(ctx context.Context, id string) (SetRecord, map[string]provider.Build, error)` (live builds keyed by build key; a build that cannot be fetched is left out).

- [ ] **Step 1: Failing tests**

```go
func interrupted(t *testing.T, svc *Service, rec SetRecord) {
	t.Helper()
	rec.Version, rec.Set, rec.Origin, rec.Repo = 1, "release", svc.Origin, svc.Cfg.RepoRoot
	rec.Status = SetInterrupted
	if rec.ID == "" {
		rec.ID = NewSetID("release", svc.Clock.Now())
	}
	if rec.VarNames == nil {
		rec.VarNames = []string{}
	}
	require.NoError(t, svc.Sets.Save(rec))
}

func TestResumeWatchesKeyedRunsAndTriggersTheRest(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	interrupted(t, svc, SetRecord{Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
	}})
	rec, _, _ := svc.Sets.Latest(svc.Cfg.RepoRoot, "release", nil)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Len(t, fp.Triggered, 1, "only the untriggered run")
	require.Equal(t, "PROJ-PROV12", fp.Triggered[0].PlanKey)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
}

func TestResumeWhenTheStageFinishedWhileAway(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-PROV12": provider.StateSuccess})
	fp.Sequences["PROJ-BUILD-1"] = []provider.Build{{Key: "PROJ-BUILD-1", State: provider.StateSuccess}}
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	interrupted(t, svc, SetRecord{Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
	}})
	rec, _, _ := svc.Sets.Latest(svc.Cfg.RepoRoot, "release", nil)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Contains(t, types(evs), "run_done:0:publish-specs")
	require.Contains(t, types(evs), "run_triggered:1:provision-lab")
}

func TestResumeRefusals(t *testing.T) {
	stages := []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD"}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
	}
	for name, tc := range map[string]struct {
		rec  SetRecord
		opts SetOptions
		kind errs.Kind
		want string
	}{
		"failed":      {SetRecord{Status: SetFailed, Stages: stages}, SetOptions{}, errs.KindUsage, "rerun --failed is not built yet"},
		"successful":  {SetRecord{Status: SetSuccessful, Stages: stages}, SetOptions{}, errs.KindUsage, "nothing to resume"},
		"missing var": {SetRecord{Status: SetInterrupted, Stages: stages, VarNames: []string{"env"}}, SetOptions{}, errs.KindUsage, "--var env"},
		"changed set": {SetRecord{Status: SetInterrupted, Stages: stages[:1]}, SetOptions{}, errs.KindConfig, "changed since"},
		"other origin": {SetRecord{Status: SetInterrupted, Stages: stages, Origin: "https://bamboo.lab.example"}, SetOptions{}, errs.KindConfig, "bamboo.lab.example"},
	} {
		t.Run(name, func(t *testing.T) {
			svc := setService(t, trainBamboo(nil))
			r := tc.rec
			r.Version, r.ID, r.Set, r.Repo = 1, NewSetID("release", svc.Clock.Now()), "release", svc.Cfg.RepoRoot
			if r.Origin == "" {
				r.Origin = svc.Origin
			}
			require.NoError(t, svc.Sets.Save(r))
			_, err := svc.ResumeSet(context.Background(), r.ID, tc.opts, SetLockOptions{})
			require.Equal(t, tc.kind, errs.KindOf(err), "%v", err)
			require.Contains(t, err.Error()+" "+errTry(err), tc.want)
		})
	}
}

func errTry(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Why + " " + e.Try
	}
	return ""
}

func TestSetStatusFetchesLiveBuilds(t *testing.T) {
	fp := trainBamboo(nil)
	fp.Sequences = map[string][]provider.Build{"PROJ-BUILD-1": {{Key: "PROJ-BUILD-1", State: provider.StateRunning}}}
	svc := setService(t, fp)
	interrupted(t, svc, SetRecord{Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
		{Runs: []RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
	}})
	rec, _, _ := svc.Sets.Latest(svc.Cfg.RepoRoot, "", nil)
	got, builds, err := svc.SetStatus(context.Background(), rec.ID)
	require.NoError(t, err)
	require.Equal(t, rec.ID, got.ID)
	require.Equal(t, provider.StateRunning, builds["PROJ-BUILD-1"].State)
	require.Empty(t, fp.Triggered)
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'Resume|SetStatus'` — compile failure.

- [ ] **Step 3: Implement**

```go
// ResumeSet continues an interrupted or timed-out set run: it watches the
// runs that were triggered and triggers the ones that were not.
func (s *Service) ResumeSet(ctx context.Context, id string, o SetOptions, lock SetLockOptions) (<-chan SetEvent, error) {
	rec, err := s.Sets.Load(id)
	if err != nil {
		return nil, err
	}
	switch rec.Status {
	case SetFailed:
		return nil, errs.Usagef("set run %s failed; rerun --failed is not built yet", id).WithTry("bam set run " + rec.Set)
	case SetSuccessful:
		return nil, errs.Usagef("set run %s succeeded; nothing to resume", id)
	}
	if rec.Origin != s.Origin {
		return nil, errs.Configf("set run %s ran on %s, not %s", id, rec.Origin, s.Origin).WithTry("pass --server for " + rec.Origin)
	}
	var missing []string
	given := varNames(o.Vars)
	for _, n := range rec.VarNames {
		if !slices.Contains(given, n) {
			missing = append(missing, "--var "+n+"=...")
		}
	}
	if len(missing) > 0 {
		return nil, errs.Usagef("resume needs the variables the set run was given").
			WithWhy("bam never stores variable values").WithTry(strings.Join(missing, " "))
	}
	p, err := s.PlanSet(ctx, rec.Set, o)
	if err != nil {
		return nil, err
	}
	if !sameShape(p, rec) {
		return nil, errs.Configf("set %s has changed since run %s", rec.Set, id).WithTry("bam set run " + rec.Set)
	}
	p.VarNames = rec.VarNames
	l, err := s.Sets.Lock([]string{rec.Set, rec.ID}, lock.Force, s.Clock.Now())
	if err != nil {
		return nil, err
	}
	rec.Status, rec.FinishedAt = SetRunning, nil
	if err := s.Sets.Save(rec); err != nil {
		l.Release()
		return nil, errs.Configf("cannot record the set run").WithWhy(err.Error()).Wrap(err)
	}
	from := 0
	for from < len(rec.Stages) && stageSucceeded(rec.Stages[from]) {
		from++
	}
	ch := make(chan SetEvent, 16)
	go func() {
		defer close(ch)
		defer l.Release()
		(&setRun{s: s, p: p, rec: rec, ch: ch}).run(ctx, from)
	}()
	return ch, nil
}

func sameShape(p SetPlan, rec SetRecord) bool {
	if len(p.Stages) != len(rec.Stages) {
		return false
	}
	for i := range p.Stages {
		if len(p.Stages[i]) != len(rec.Stages[i].Runs) {
			return false
		}
		for j := range p.Stages[i] {
			if p.Stages[i][j].Target != rec.Stages[i].Runs[j].Target {
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

// SetStatus reads a record and fetches the current state of its builds.
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
```

A resumed stage run whose record already says it finished is skipped by `stage` (Task 5's `recs[j].State.Finished()` check), and an unfinished keyed run is watched, which covers "finished while away": its first poll yields `EventDone` at once. In the `stage` loop, a run recorded `interrupted` with a state that is not finished must be watched, not skipped — the check is `State.Finished()`, and the interrupt path leaves the state empty, so this holds.

`PlanSet` on resume re-resolves runs that already triggered; that costs a few requests and keeps one code path.

- [ ] **Step 4: Run** `go test -race ./internal/app/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: resume an interrupted set run and read its status"`

### Task 7: `view` renderers for list, show, dry run and status

**Files:**
- Create: `internal/view/sets.go`, `internal/view/sets_test.go`
- Modify: `internal/view/json.go` (docs)

**Interfaces:**
- Consumes: `config.ResolvedSet` is not allowed in `view` (view may import `provider`, `app`, `errs` only). So `app` gets a describe helper first:
  ```go
  // in internal/app/sets.go
  type SetInfo struct {
  	Name      string
  	Timeout   time.Duration
  	Stages    [][]SetRunInfo
  	DefinedIn []string
  }
  type SetRunInfo struct { Target string; Vars []TargetVar } // TargetVar masks secrets and ${ENV} like DescribeTarget
  func DescribeSets(cfg *config.Config, getenv func(string) string) ([]SetInfo, error)
  func DescribeSet(cfg *config.Config, getenv func(string) string, name string) (SetInfo, error)
  ```
- Produces:
  ```go
  func Sets(o Out, ss []app.SetInfo) error
  func SetDetail(o Out, s app.SetInfo) error
  func SetDryRun(o Out, p app.SetPlan) error
  func SetStatusView(o Out, rec app.SetRecord, builds map[string]provider.Build) error
  type SetDoc, SetRunDoc, SetPlanDoc, SetRecordDoc, SetEventDoc // JSON
  func SetJSON(s app.SetInfo) SetDoc
  func SetPlanJSON(p app.SetPlan) SetPlanDoc
  func SetRecordJSON(rec app.SetRecord, builds map[string]provider.Build) SetRecordDoc
  func SetEventJSON(e app.SetEvent) SetEventDoc
  ```

- [ ] **Step 1: Failing tests** (describe helper in `internal/app/sets_test.go`, renderers in `internal/view/sets_test.go`)

```go
// app
func TestDescribeSetMasksSecrets(t *testing.T) {
	svc := setService(t, fakeBamboo())
	s := svc.Cfg.Project.Sets["release"]
	s.Stages[1][0].Vars["db_password"] = "${LAB_DB_PASSWORD}"
	info, err := DescribeSet(svc.Cfg, func(string) string { return "" }, "release")
	require.NoError(t, err)
	require.Equal(t, "provision-lab", info.Stages[1][0].Target)
	for _, v := range info.Stages[1][0].Vars {
		if v.Name == "db_password" {
			require.True(t, v.Secret)
			require.Equal(t, "LAB_DB_PASSWORD", v.EnvRef)
		}
	}
}
```

```go
// view
func setInfo() app.SetInfo {
	return app.SetInfo{Name: "release", Timeout: 2 * time.Hour, DefinedIn: []string{"/src/app/.bam.yaml"},
		Stages: [][]app.SetRunInfo{
			{{Target: "publish-specs"}},
			{{Target: "provision"}, {Target: "deploy", Vars: []app.TargetVar{
				{Name: "deploy_token", Value: "${DEPLOY_TOKEN}", EnvRef: "DEPLOY_TOKEN", Secret: true},
				{Name: "env", Value: "qa"}}}},
		}}
}

func TestSetsList(t *testing.T) {
	var b strings.Builder
	require.NoError(t, Sets(Out{W: &b, Width: 80}, []app.SetInfo{setInfo()}))
	require.Equal(t, "NAME     STAGES  RUNS  DEFINED IN\nrelease  2       3     /src/app/.bam.yaml\n", b.String())
}

func TestSetDetailMasksSecrets(t *testing.T) {
	var b strings.Builder
	require.NoError(t, SetDetail(Out{W: &b, Width: 80}, setInfo()))
	out := b.String()
	require.Contains(t, out, "stage 1  publish-specs")
	require.Contains(t, out, "stage 2  provision, deploy")
	require.Contains(t, out, "deploy_token = ${DEPLOY_TOKEN}")
	require.Contains(t, out, "env = qa")
	require.Contains(t, out, "timeout 2h0m0s")
}

func TestSetJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(SetJSON(setInfo()))
	require.NoError(t, err)
	require.JSONEq(t, `{"name":"release","timeout_ms":7200000,"defined_in":["/src/app/.bam.yaml"],
	  "stages":[[{"target":"publish-specs","vars":[]}],
	            [{"target":"provision","vars":[]},{"target":"deploy","vars":[
	               {"name":"deploy_token","value":"${DEPLOY_TOKEN}","secret":true,"env_ref":"DEPLOY_TOKEN"},
	               {"name":"env","value":"qa","secret":false}]}]]}`, string(data))
}

func TestSetEventJSONFieldNames(t *testing.T) {
	e := app.SetEvent{Type: app.SetRunDone, Time: time.Date(2026, 9, 29, 10, 16, 0, 0, time.UTC),
		Set: "release", ID: "release-20260929T101500Z", Stage: 1, Target: "deploy",
		Build: provider.Build{Key: "OPS-DEP-9", State: provider.StateFailed}, State: provider.StateFailed}
	data, err := json.Marshal(SetEventJSON(e))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	for _, k := range []string{"type", "time", "set", "id", "stage", "target", "build", "state"} {
		require.Contains(t, m, k)
	}
	require.NotContains(t, m, "event", "only run_event carries the inner event")
	require.NotContains(t, m, "error")
}

func TestSetStatusView(t *testing.T) {
	rec := app.SetRecord{ID: "release-20260929T101500Z", Set: "release", Status: app.SetInterrupted,
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-SPEC", BuildKey: "PROJ-SPEC-12", State: provider.StateSuccess}}},
			{Runs: []app.RunRecord{{Target: "provision", PlanKey: "OPS-PROV", BuildKey: "OPS-PROV-40"},
				{Target: "deploy", PlanKey: "OPS-DEP"}}},
		}}
	builds := map[string]provider.Build{"OPS-PROV-40": {Key: "OPS-PROV-40", State: provider.StateRunning}}
	var b strings.Builder
	require.NoError(t, SetStatusView(Out{W: &b, Width: 80, Now: time.Now}, rec, builds))
	out := b.String()
	require.Contains(t, out, "release  release-20260929T101500Z  interrupted")
	require.Contains(t, out, "OPS-PROV-40")
	require.Contains(t, out, "try: bam set resume release-20260929T101500Z")
}
```

Dry run: `TestSetDryRunShowsEveryStageMasked` builds an `app.SetPlan` with one run whose `VarSet` has a secret `db_password` and asserts the output has `stage 1/1`, the plan key, `db_password = ********`, and never the raw value.

- [ ] **Step 2: Run** `go test ./internal/app/ ./internal/view/ -run 'Set'` — compile failure.

- [ ] **Step 3: Implement**

`DescribeSets`/`DescribeSet` in `app/sets.go` use `cfg.Sets()` and build `TargetVar`s exactly as `describe` in `targets.go` does (factor the per-variable loop there into `func targetVars(m config.StringMap, getenv func(string) string) []TargetVar` and call it from both). Unknown set name: `errs.Usagef("unknown set %q", name).WithTry("bam set list")`.

`view/sets.go`:

```go
// Sets is bam set list.
func Sets(o Out, ss []app.SetInfo) error {
	t := Table{Headers: []string{"NAME", "STAGES", "RUNS", "DEFINED IN"}}
	for _, s := range ss {
		runs := 0
		for _, st := range s.Stages {
			runs += len(st)
		}
		t.Rows = append(t.Rows, []string{s.Name, strconv.Itoa(len(s.Stages)), strconv.Itoa(runs), s.DefinedIn[len(s.DefinedIn)-1]})
	}
	return t.Render(o)
}

// SetDetail is bam set show.
func SetDetail(o Out, s app.SetInfo) error {
	fmt.Fprintf(o.W, "%s\n", s.Name)
	if s.Timeout > 0 {
		fmt.Fprintf(o.W, "  timeout %s\n", s.Timeout)
	}
	fmt.Fprintf(o.W, "  defined in %s\n\n", strings.Join(s.DefinedIn, ", "))
	for i, st := range s.Stages {
		names := make([]string, len(st))
		for j, r := range st {
			names[j] = r.Target
		}
		fmt.Fprintf(o.W, "stage %d  %s\n", i+1, strings.Join(names, ", "))
		for _, r := range st {
			for _, v := range r.Vars {
				fmt.Fprintf(o.W, "    %s: %s = %s\n", r.Target, v.Name, v.Display())
			}
		}
	}
	return nil
}
```

`TargetVar.Display()` already masks: a secret without an env ref shows `********`, an env ref shows `${NAME}` as written. `SetDryRun` prints, per stage, `stage i/n` then per run `  <target>  <plan key>  branch <branch or default>` and each changed variable (`vs.Changed()`, masked via `ResolvedVar.Display()`), then `dry run: nothing triggered`.

`SetStatusView` prints the header `<set>  <id>  <status>`, then per stage `stage i/n` and per run a row: glyph + target, build key or `–`, the live state when the build was fetched else the recorded state, and the recorded error if any. When the status is `interrupted` or `timed_out`, it ends with `try: bam set resume <id>`.

JSON docs in `json.go`:

```go
type SetVarDoc struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	EnvRef string `json:"env_ref,omitempty"`
}
type SetRunDoc struct {
	Target string      `json:"target"`
	Vars   []SetVarDoc `json:"vars"`
}
type SetDoc struct {
	Name      string        `json:"name"`
	TimeoutMS int64         `json:"timeout_ms,omitempty"`
	DefinedIn []string      `json:"defined_in"`
	Stages    [][]SetRunDoc `json:"stages"`
}
type SetEventDoc struct {
	Type   string    `json:"type"`
	Time   string    `json:"time"`
	Set    string    `json:"set"`
	ID     string    `json:"id"`
	Stage  int       `json:"stage"`
	Target string    `json:"target,omitempty"`
	Build  *BuildDoc `json:"build,omitempty"`
	Event  *EventDoc `json:"event,omitempty"`
	State  string    `json:"state,omitempty"`
	Error  *ErrorDoc `json:"error,omitempty"`
}
```

Reuse the existing error JSON document from `errors.go`/`json.go` (look up its type name; if none exists as a value type, add `ErrorDoc{Kind, What, Why, Try}` built from `*errs.Error` the same way `PrintError` reads it). A secret `SetVarDoc.Value` is `********` unless it is an `${ENV}` reference, which is shown as written. `SetPlanDoc` holds `name`, `origin`, `stages` (each run: `target`, `plan_key`, `branch`, `variables` as the existing `RunDoc` variables list) and `warnings`. `SetRecordDoc` is the record's JSON field names from Task 3 plus, per run, `"build"` when fetched, with the live state in `state`.

- [ ] **Step 4: Run** `go test ./internal/app/ ./internal/view/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: render sets, set dry runs and set status"`

### Task 8: `view` renderers for a running set

**Files:**
- Create: `internal/view/setwatch.go`, `internal/view/setwatch_test.go`
- Create goldens: `internal/view/testdata/set_running.golden`, `set_failed.golden`, `set_success.golden`

**Interfaces:**
- Produces:
  ```go
  type SetRenderer interface { Event(e app.SetEvent); Tick() }
  func NewSetLive(o Out, p app.SetPlan) *SetLive
  func NewSetLines(o Out) *SetLines
  func NewSetNDJSON(o Out) *SetNDJSON
  ```
  `NewSetLive` takes the plan so stages that have not started show their presets as pending.

- [ ] **Step 1: Failing tests**

```go
func setPlan() app.SetPlan {
	return app.SetPlan{Name: "release", Stages: [][]app.SetRunPlan{
		{{Target: "publish-specs", Ref: app.PlanRef{PlanKey: "PROJ-SPEC"}}},
		{{Target: "provision", Ref: app.PlanRef{PlanKey: "OPS-PROV"}}, {Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}}},
	}}
}

func setEvents(final provider.State) []app.SetEvent {
	at := time.Date(2026, 9, 29, 10, 15, 0, 0, time.UTC)
	ev := func(typ app.SetEventType, stage int, target string, b provider.Build, st provider.State) app.SetEvent {
		return app.SetEvent{Type: typ, Time: at, Set: "release", ID: "release-20260929T101500Z", Stage: stage, Target: target, Build: b, State: st}
	}
	spec := provider.Build{Key: "PROJ-SPEC-12", State: provider.StateSuccess, Duration: 72 * time.Second}
	prov := provider.Build{Key: "OPS-PROV-40", State: final, Duration: 90 * time.Second}
	dep := provider.Build{Key: "OPS-DEP-9", State: provider.StateFailed, Duration: 48 * time.Second}
	return []app.SetEvent{
		ev(app.SetStageStart, 0, "", provider.Build{}, ""),
		ev(app.SetRunTriggered, 0, "publish-specs", provider.Build{Key: "PROJ-SPEC-12", State: provider.StateQueued}, ""),
		ev(app.SetRunDone, 0, "publish-specs", spec, provider.StateSuccess),
		ev(app.SetStageDone, 0, "", provider.Build{}, provider.StateSuccess),
		ev(app.SetStageStart, 1, "", provider.Build{}, ""),
		ev(app.SetRunTriggered, 1, "provision", provider.Build{Key: "OPS-PROV-40", State: provider.StateQueued}, ""),
		ev(app.SetRunTriggered, 1, "deploy", provider.Build{Key: "OPS-DEP-9", State: provider.StateQueued}, ""),
		ev(app.SetRunDone, 1, "deploy", dep, provider.StateFailed),
		ev(app.SetRunDone, 1, "provision", prov, final),
		ev(app.SetStageDone, 1, "", provider.Build{}, provider.StateFailed),
		ev(app.SetDone, 1, "", provider.Build{}, provider.StateFailed),
	}
}

func TestSetLinesOneLinePerChange(t *testing.T) {
	var b strings.Builder
	l := NewSetLines(Out{W: &b})
	for _, e := range setEvents(provider.StateSuccess) {
		l.Event(e)
	}
	out := b.String()
	require.Contains(t, out, "2026-09-29T10:15:00Z stage 2 deploy OPS-DEP-9 failed 48s\n")
	require.Contains(t, out, "2026-09-29T10:15:00Z stage 1 publish-specs PROJ-SPEC-12 triggered\n")
	require.Contains(t, out, "set failed: stage 2 (1 of 2 runs failed)\n")
	require.Contains(t, out, "try: bam logs OPS-DEP-9 --failed\n")
}

func TestSetLiveGoldens(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 9, 29, 10, 20, 0, 0, time.UTC) }
	for name, evs := range map[string][]app.SetEvent{
		"set_running": setEvents(provider.StateSuccess)[:7],
		"set_failed":  setEvents(provider.StateSuccess),
	} {
		t.Run(name, func(t *testing.T) {
			var b strings.Builder
			l := NewSetLive(Out{W: &b, TTY: true, Width: 80, Now: now}, setPlan())
			for _, e := range evs {
				l.Event(e)
			}
			golden(t, name, lastFrame(b.String()))
		})
	}
}

func TestSetNDJSONEndsWithSetDone(t *testing.T) {
	var b strings.Builder
	n := NewSetNDJSON(Out{W: &b})
	for _, e := range setEvents(provider.StateSuccess) {
		n.Event(e)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	var last map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	require.Equal(t, "set_done", last["type"])
}
```

Use the existing golden helper in `golden_test.go` (check its name and signature; `golden(t, name, got)` is assumed here — adapt to whatever it is). `lastFrame` strips everything up to and including the last `\x1b[<n>A\x1b[J`, so the golden shows the final redraw; add it to `setwatch_test.go`. Add a `set_success` golden with a success-only event list (make the deploy run succeed and `SetDone` `success`).

- [ ] **Step 2: Run** `go test ./internal/view/ -run 'SetLines|SetLive|SetNDJSON'` — compile failure.

- [ ] **Step 3: Implement** `setwatch.go`:

```go
// SetRenderer shows set events. Tick lets a live display update its clocks.
type SetRenderer interface {
	Event(e app.SetEvent)
	Tick()
}

type setRow struct {
	target string
	build  provider.Build
	prog   provider.Progress
	err    error
	done   bool
}

// setBoard is what every set renderer knows about the set so far.
type setBoard struct {
	name, id string
	stages   [][]*setRow
	final    *app.SetEvent
}

func newBoard(p app.SetPlan) *setBoard {
	b := &setBoard{name: p.Name}
	for _, st := range p.Stages {
		var rows []*setRow
		for _, r := range st {
			rows = append(rows, &setRow{target: r.Target})
		}
		b.stages = append(b.stages, rows)
	}
	return b
}

func (b *setBoard) row(stage int, target string) *setRow {
	for len(b.stages) <= stage {
		b.stages = append(b.stages, nil)
	}
	for _, r := range b.stages[stage] {
		if r.target == target {
			return r
		}
	}
	r := &setRow{target: target}
	b.stages[stage] = append(b.stages[stage], r)
	return r
}

func (b *setBoard) apply(e app.SetEvent) {
	b.id = e.ID
	if b.name == "" {
		b.name = e.Set
	}
	switch e.Type {
	case app.SetRunTriggered:
		b.row(e.Stage, e.Target).build = e.Build
	case app.SetRunEvent:
		r := b.row(e.Stage, e.Target)
		r.build = e.Build
		if e.Event != nil {
			r.prog = e.Event.Progress
		}
	case app.SetRunDone:
		r := b.row(e.Stage, e.Target)
		if e.Build.Key != "" {
			r.build = e.Build
		}
		r.build.State, r.err, r.done = e.State, e.Err, true
	case app.SetDone, app.SetError:
		ev := e
		b.final = &ev
	}
}

// summary is the closing lines: the result and a try line per failed run.
func (b *setBoard) summary(o Out) {
	if b.final == nil {
		return
	}
	if b.final.Type == app.SetError {
		PrintError(o.W, b.final.Err, false)
		return
	}
	if b.final.State == provider.StateSuccess {
		fmt.Fprintf(o.W, "%s\n", style.Colored(o.Style, provider.StateSuccess, "set succeeded"))
		return
	}
	st := b.stages[b.final.Stage]
	failed := 0
	for _, r := range st {
		if r.build.State != provider.StateSuccess {
			failed++
		}
	}
	fmt.Fprintf(o.W, "%s\n", style.Colored(o.Style, provider.StateFailed,
		fmt.Sprintf("set failed: stage %d (%d of %d runs failed)", b.final.Stage+1, failed, len(st))))
	for _, r := range st {
		switch {
		case r.err != nil && r.build.Key == "":
			fmt.Fprintf(o.W, "  %s: %s\n", r.target, describe(r.err))
		case r.build.State != provider.StateSuccess && r.build.Key != "":
			fmt.Fprintf(o.W, "  try: bam logs %s --failed\n", r.build.Key)
		}
	}
}
```

`describe(err)` returns `*errs.Error`'s `What` or `err.Error()`; name it `errWhat` if `describe` is taken in `view`.

`SetLive` keeps a `setBoard`, redraws the whole block in place on every event and `Tick` (same `\x1b[%dA\x1b[J` technique as `Live.draw`), and prints the summary once after the final event. Block layout per §7.1 of the spec: header `name  id`, then `stage i/n`, then a `Table{Indent: "  "}` row per run: `style.StateGlyph(o.Style, state) + " " + target`, build key (`–` when none), `style.Title(state)` (`pending` when not triggered), `durationIfRun`/`elapsed`, `Bar(o, prog)` for a running build.

`SetLines` keeps a `setBoard` too, and prints on each event (RFC3339 UTC time first, as `Lines` does):
- `run_triggered`: `<ts> stage <n> <target> <key> triggered`
- `run_event` of type `state`: `<ts> stage <n> <target> <key> <state>`; other inner types are skipped (stage/job detail stays in `bam watch`).
- `run_done`: `<ts> stage <n> <target> <key> <state> <duration>`, or `<ts> stage <n> <target> failed: <error>` when there is no build.
- `set_done`/`set_error`: the summary.

`SetNDJSON` writes `SetEventJSON(e)` for every event except `run_event`s whose inner event is a progress event (as `NDJSON` skips them).

- [ ] **Step 4: Run** `go test ./internal/view/ -run 'Set' -update`, review the three goldens by eye against spec §7.1, then `go test ./internal/view/` — PASS.

- [ ] **Step 5: Commit** `git commit -m "feat: show a running set live, as lines and as NDJSON"`

### Task 9: `bam set` commands

**Files:**
- Create: `internal/cli/sets.go`, `internal/cli/sets_test.go`
- Modify: `internal/cli/env.go` (`Paths.Sets`), `internal/cli/runtime.go` (`connectServer` fills `Service.Sets`), `internal/cli/harness_test.go` (sets dir in the temp home)

**Interfaces:**
- Consumes: everything above.
- Produces: commands `bam set list|show|run|status|resume`; `Paths.Sets string` = `filepath.Join(xdg.StateHome, "bam", "sets")`.

- [ ] **Step 1: Failing tests** — read `harness_test.go` first for the helper names (`run(t, h, args...)` or similar, the fake backend field, the project-file writer); the tests below assume `newHarness(t)`, `h.writeProject(yaml)`, `h.run(args...) (stdout, stderr string, code int)` and `h.backend *fake.Provider`. Rename to the real helpers.

```go
const setYAML = `version: 1
servers: {work: {url: https://bamboo.example.com}}
default_server: work
targets:
  publish-specs: {plan: PROJ-BUILD}
  provision-lab: {plan: PROJ-PROV, defaults: {cluster_name: alpha}}
sets:
  release:
    stages:
      - [publish-specs]
      - [provision-lab]
`

func TestSetRunExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		final provider.State
		code  int
	}{
		"success": {provider.StateSuccess, 0},
		"failed":  {provider.StateFailed, 1},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.writeProject(setYAML)
			for _, plan := range []string{"PROJ-BUILD", "PROJ-PROV"} {
				key := plan + "-1"
				h.backend.TriggerResults = mapSet(h.backend.TriggerResults, plan, provider.Build{Key: key})
				h.backend.Sequences = mapSet(h.backend.Sequences, key, []provider.Build{{Key: key, State: tc.final}})
			}
			_, _, code := h.run("set", "run", "release")
			require.Equal(t, tc.code, code)
		})
	}
}

func TestSetRunDryRunTriggersNothingAndWritesNoRecord(t *testing.T) {
	h := newHarness(t)
	h.writeProject(setYAML)
	out, _, code := h.run("set", "run", "release", "--dry-run")
	require.Equal(t, 0, code)
	require.Contains(t, out, "stage 2/2")
	require.Empty(t, h.backend.Triggered)
	_, err := os.Stat(h.env.Paths.Sets)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSetRunJSONIsNDJSON(t *testing.T) {
	h := newHarness(t)
	h.writeProject(setYAML)
	// success sequences as in TestSetRunExitCodes
	out, _, code := h.run("set", "run", "release", "--json")
	require.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, l := range lines {
		require.True(t, json.Valid([]byte(l)), l)
	}
	require.Contains(t, lines[len(lines)-1], `"type":"set_done"`)
}

func TestSetUsageAndConfigExitCodes(t *testing.T) {
	h := newHarness(t)
	h.writeProject(setYAML)
	_, _, code := h.run("set", "run", "nope")
	require.Equal(t, 2, code)
	h.writeProject(setYAML + "  broken:\n    stages: [[missing]]\n")
	_, _, code = h.run("set", "list")
	require.Equal(t, 3, code)
}

func TestSetRunTimeoutExits6(t *testing.T) {
	// release with timeout: 1h and a build that stays running; the harness
	// clock fires After at once. Expect code 6 and "bam set resume" on stderr.
}

func TestSetListShowStatusResume(t *testing.T) {
	// list and show print the set; after an interrupted record is saved with
	// app.SetStore directly in h.env.Paths.Sets, status prints it and
	// resume --var-less completes with code 0.
}

func TestSetCompletion(t *testing.T) {
	h := newHarness(t)
	h.writeProject(setYAML)
	out, _, _ := h.run("__complete", "set", "run", "")
	require.Contains(t, out, "release")
}
```

Write the two sketched tests in full while implementing, following the patterns of the three complete ones; each asserts the exit code and one line of output.

- [ ] **Step 2: Run** `go test ./internal/cli/ -run 'Set'` — FAIL (unknown command "set").

- [ ] **Step 3: Implement** `internal/cli/sets.go`, registered like `runloop.go` via `func init() { commandSets = append(commandSets, addSets) }`:

```go
func addSets(root *cobra.Command, r *runtime) {
	cmd := &cobra.Command{Use: "set", Short: "Run release trains: presets in ordered stages"}
	requireSubcommand(cmd)
	cmd.AddCommand(newSetListCmd(r), newSetShowCmd(r), newSetRunCmd(r), newSetStatusCmd(r), newSetResumeCmd(r))
	root.AddCommand(cmd)
}
```

- `list`, `show`: load config, `app.DescribeSets`/`DescribeSet`, render or `WriteJSON`. No network.
- `run <set>`: flags `--var` (StringArray), `--dry-run`, `--force`. Steps:
  1. `cfg.Set(name)`; unknown → usage error. Pick the server from the first run's preset (`cfg.Target(first).Server`) and `r.connect(ctx, thatServer)`; `PlanSet` checks the rest.
  2. `svc.PlanSet(ctx, name, app.SetOptions{Vars: vars, Server: r.flags.server})`; print `view.PrintWarnings(stderr, p.Warnings)`.
  3. `--dry-run`: `view.SetDryRun` or `WriteJSON(SetPlanJSON(p))`; return.
  4. `id, events, err := svc.RunSet(ctx, p, app.SetLockOptions{Force: force})`.
  5. `return r.setLoop(ctx, id, p, events)`.
- `setLoop` mirrors `watchLoop`: choose `SetNDJSON` for `--json`, `SetLive` on a TTY with a one-second ticker, else `SetLines`. Read events; on `SetDone` return `nil` for success or `&resultError{State: provider.StateFailed}`; on `SetError` return `e.Err`; when the channel closes without either and `ctx.Err() != nil`, print `still running: bam set resume <id>` to stderr and return `ctx.Err()` (exit 130).
- `status [set|id]`: resolve the argument: if it matches an id in `svc.Sets.IDs()`, use it; else treat as a set name and use `Latest(repo, name, nil)`; empty → `Latest(repo, "", nil)`. None → usage error "no set runs yet" with try `bam set run <set>`. Connect to the server whose origin equals the record's (loop over `cfg.Servers()`, as `connectLast` does), then `svc.SetStatus`.
- `resume [set|id]`: flags `--var`, `--force`; resolve like `status`, but with `match` = status is `interrupted` or `timed_out`; connect by the record's origin; `svc.ResumeSet(ctx, id, app.SetOptions{Vars: vars, Server: r.flags.server}, ...)`; `setLoop` with a plan rebuilt from the record for `SetLive` (`app.SetPlan{Name: rec.Set, Stages: ...Target names...}`).
- Completion: `run`, `show`, `status`, `resume` complete set names from `cfg.Sets()`; `status` and `resume` add ids from the store.

`env.go`: add `Sets string` to `Paths` and set it in `SystemEnv` next to `State` as `filepath.Join(xdg.StateHome, "bam", "sets")`. `runtime.connectServer`: `Sets: &app.SetStore{Dir: r.env.Paths.Sets, Host: os.Hostname, PID: os.Getpid}`. `harness_test.go`: set `Paths.Sets` under the temp home.

- [ ] **Step 4: Run** `go test -race ./internal/cli/` then `go test -race ./...` — PASS. `make docs` regenerates `docs/cli/bam_set*.md`.

- [ ] **Step 5: Commit** `git add -A internal/cli docs/cli && git commit -m "feat: add bam set list, show, run, status and resume"`

### Task 10: documentation and e2e

**Files:**
- Modify: `README.md`, `docs/configuration.md`, `docs/json.md`, `docs/prd.md`, spec status
- Modify: `e2e_test.go`

- [ ] **Step 1: e2e test** (`//go:build e2e`), next to the existing trigger test, reading plan keys and cluster variables the same way the existing provision test does:

```go
func TestE2ESetRunAndResume(t *testing.T) {
	// 1. Write a temp .bam.yaml with presets for the publish-specs and
	//    provision plans (keys from the existing e2e env) and a set
	//    "e2e-train" with stages [[publish-specs], [provision]].
	// 2. bam set run e2e-train --json: exit 0, last line set_done success.
	// 3. bam set status --json: status "successful", both build keys set.
	// 4. t.Cleanup: run the decommission plan with the same cluster
	//    variables through bam run --watch and require exit 0.
}
```

Write it using the helpers of `e2e_test.go` (read the file first; reuse its cluster-name and decommission code). The interrupt-and-resume path is checked by hand during acceptance (Step 3), not in the suite: an interrupt inside `go test` is fragile.

- [ ] **Step 2: Docs**
  - `README.md`: a "Release trains" section after the presets section, with the spec §3.1 YAML and the §7.1 block, and the five commands.
  - `docs/configuration.md`: `sets:` — schema, the bare-name shorthand, layering (replaced whole), the §3.3 rules, variable precedence.
  - `docs/json.md`: the set event NDJSON, `set status --json`, `set list/show --json`, `set run --dry-run --json`.
  - `docs/prd.md` §7 Future: "Run sets: the core (named stages, `bam set run|status|resume`) is built; matrix expansion, batch files, `rerun --failed`, plan statistics and the terminal UI panel remain."
  - Spec status: `Implemented`.
  - `make docs`.

- [ ] **Step 3: Acceptance** against the local Bamboo (`bam server` alias `home`): `make e2e ARGS='-run TestE2ESetRunAndResume'`; then by hand: `bam set run e2e-train`, Ctrl-C during stage 2, `bam set status`, `bam set resume`, then decommission and confirm it succeeded.

- [ ] **Step 4: Full verification**: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` with no diff.

- [ ] **Step 5: Commit** `git commit -m "docs: document release train sets"` (and `test: cover set run in the e2e suite` for the e2e file).
