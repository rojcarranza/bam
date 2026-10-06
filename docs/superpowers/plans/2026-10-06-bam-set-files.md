# Run a set from a file Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bam set run -f train.yaml` runs a set defined in its own file; `status`, `resume` and `rerun --failed` work on it, re-reading the file.

**Architecture:** `config.LoadSetFile` parses and validates one set from a file with the existing set code. `app.SetOptions.File` makes `PlanSet` read from it; `SetPlan.File` goes into the record; resume and rerun pass the recorded file back through `continueSet`. `cli` adds `-f`/`--file`; `view` shows `file` in status.

**Tech Stack:** Go, yaml.v3, cobra, testify.

**Spec:** `docs/superpowers/specs/2026-10-06-bam-set-files-design.md`

## Global Constraints

- Placeholders only. JSON fields are added only: `file` on the set record document.
- A configured set must behave exactly as before.
- Literal secrets in a train file are refused, as in the project file.
- `app` never prints. No new dependencies. No test sleeps or touches the network.
- Conventional Commits, one per task; the owner's identity only.
- Gate, chained with `&&` before each commit: `go test -race ./... && make lint && make check-fixtures && make docs`.
- `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

## Review Focus

1. Resume of a file set must re-read the file, and a moved file must be a config error with a try line — `TestResumeAFileSetWhoseFileIsGone` (Task 2).
2. A file naming a different set than the record must be refused — `TestResumeAFileThatNamesAnotherSet` (Task 2).
3. `set run` with both a name and `-f` must fail before any network — `TestSetRunNameAndFileTogether` (Task 3).
4. A literal secret in a train file must be refused — `TestLoadSetFileRefusesALiteralSecret` (Task 1).
5. Configured sets unchanged — the existing set tests pass untouched.

---

### Task 1: `config.LoadSetFile`

**Files:**
- Create: `internal/config/setfile.go`, `internal/config/setfile_test.go`

**Interfaces:**
- Produces: `func (c *Config) LoadSetFile(path string) (ResolvedSet, error)`.

- [ ] **Step 1: Failing tests** — create `internal/config/setfile_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/stretchr/testify/require"
)

const setFileProject = "version: 1\nservers: {work: {url: https://bamboo.example.com}}\n" +
	"targets:\n  a: {plan: PROJ-A}\n  b: {plan: PROJ-B}\n"

// trainFile writes body as dir/name and returns the path.
func trainFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

func TestLoadSetFile(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	p := trainFile(t, "hotfix-2041.yaml", "timeout: 1h\nstages:\n  - [a]\n  - [{target: b, matrix: [{env: qa}, {env: prod}]}]\n")
	s, err := cfg.LoadSetFile(p)
	require.NoError(t, err)
	require.Equal(t, "hotfix-2041", s.Name, "named after the file")
	require.Equal(t, []string{p}, s.DefinedIn)
	require.Len(t, s.Stages, 2)
	require.Len(t, s.Stages[1][0].Expand(), 2)
}

func TestLoadSetFileTakesItsName(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	s, err := cfg.LoadSetFile(trainFile(t, "Train File.yml", "name: release-2\nstages: [[a]]\n"))
	require.NoError(t, err)
	require.Equal(t, "release-2", s.Name)
}

func TestLoadSetFileErrors(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	for name, tc := range map[string]struct{ file, body, want string }{
		"bad file name":  {"Train File.yaml", "stages: [[a]]\n", "add name:"},
		"bad name":       {"t.yaml", "name: Bad\nstages: [[a]]\n", `invalid set name "Bad"`},
		"unknown key":    {"t.yaml", "stagez: [[a]]\n", "stagez"},
		"unknown preset": {"t.yaml", "stages: [[nope]]\n", `unknown preset "nope"`},
		"duplicate run":  {"t.yaml", "stages: [[a, a]]\n", `run "a" twice in stage 1`},
		"no stages":      {"t.yaml", "stages: []\n", "has no stages"},
		"empty file":     {"t.yaml", "", "is empty"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := cfg.LoadSetFile(trainFile(t, tc.file, tc.body))
			require.Error(t, err)
			require.Equal(t, errs.KindConfig, errs.KindOf(err))
			require.Contains(t, err.Error()+" "+errWhyTry(err), tc.want)
		})
	}
	_, err = cfg.LoadSetFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
}

func TestLoadSetFileRefusesALiteralSecret(t *testing.T) {
	cfg, err := loadYAML(t, setFileProject, "")
	require.NoError(t, err)
	_, err = cfg.LoadSetFile(trainFile(t, "t.yaml", "stages: [[{target: a, vars: {db_password: hunter2}}]]\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "holds a literal secret")
	_, err = cfg.LoadSetFile(trainFile(t, "t.yaml", "stages: [[{target: a, vars: {db_password: \"${PW}\"}}]]\n"))
	require.NoError(t, err)
}

func errWhyTry(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Why + " " + e.Try
	}
	return ""
}
```

(Add `"errors"` to the imports. If `sets_test.go` already defines a helper equivalent to `errWhyTry`, use it instead of redeclaring.)

- [ ] **Step 2: Run** `go test ./internal/config/ -run LoadSetFile 2>&1 | tail -2` — expected: compile error `cfg.LoadSetFile undefined`.

- [ ] **Step 3: Implement** — create `internal/config/setfile.go`:

```go
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/r0jjames/bam-cli/internal/errs"
)

// setFile is a train file: one set, with an optional name.
type setFile struct {
	Name    string   `yaml:"name,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty"`
	Stages  []Stage  `yaml:"stages"`
}

// LoadSetFile reads one set from a train file and checks it as a configured
// set is checked, against the merged presets. Literal secrets are refused,
// as in the project file: a train file is shared like .bam.yaml.
func (c *Config) LoadSetFile(path string) (ResolvedSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ResolvedSet{}, errs.Configf("cannot read the train file %s", path).WithWhy(err.Error()).Wrap(err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return ResolvedSet{}, errs.Configf("%s is empty", path).WithTry("add stages: [[<preset>]]")
	}
	var f setFile
	if err := decodeStrict(data, path, &f); err != nil {
		return ResolvedSet{}, err
	}
	name := f.Name
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		if !ValidTargetName(name) {
			return ResolvedSet{}, errs.Configf("%s: the file name %q is not a set name", path, name).
				WithTry(fmt.Sprintf("add name: <name> to %s; names match ^[a-z][a-z0-9_-]*$", path))
		}
	}
	if !ValidTargetName(name) {
		return ResolvedSet{}, errs.Configf("%s: invalid set name %q", path, name).
			WithTry("names match ^[a-z][a-z0-9_-]*$")
	}
	set := Set{Stages: f.Stages, Timeout: f.Timeout}
	if err := checkSetSecrets(path, map[string]Set{name: set}); err != nil {
		return ResolvedSet{}, err
	}
	targets, err := c.Targets()
	if err != nil {
		return ResolvedSet{}, err
	}
	rs := ResolvedSet{Name: name, Set: set, DefinedIn: []string{path}}
	if err := validateSet(rs, targets); err != nil {
		return ResolvedSet{}, err
	}
	return rs, nil
}
```

- [ ] **Step 4: Run** `go test ./internal/config/` — PASS.

- [ ] **Step 5: Gate and commit** — `go test -race ./... && make lint && git add internal/config && git commit -m "feat: read a set from a train file"`

---

### Task 2: `app` — plan, record and resume a file set

**Files:**
- Modify: `internal/app/sets.go` (`SetOptions.File`, `SetPlan.File`, `PlanSet`, `RunSet`, `continueSet`), `internal/app/setstore.go` (`SetRecord.File`)
- Test: `internal/app/setfile_test.go`

**Interfaces:**
- Consumes: `config.LoadSetFile` (Task 1).
- Produces: `SetOptions.File string`; `SetPlan.File string`; `SetRecord.File string` (`json:"file,omitempty"`).

- [ ] **Step 1: Failing tests** — create `internal/app/setfile_test.go`:

```go
package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/r0jjames/bam-cli/internal/provider"
	"github.com/stretchr/testify/require"
)

// writeTrain writes a train file "hotfix" with publish-specs then
// provision-lab, and returns its path.
func writeTrain(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "hotfix.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

const hotfixBody = "stages:\n  - [publish-specs]\n  - [{target: provision-lab, vars: {cluster_name: alpha}}]\n"

func TestPlanSetFromAFile(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	p, err := svc.PlanSet(context.Background(), "ignored", SetOptions{File: path})
	require.NoError(t, err)
	require.Equal(t, "hotfix", p.Name)
	require.Equal(t, path, p.File)
	require.Len(t, p.Stages, 2)
}

func TestRunSetRecordsTheFile(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	p, err := svc.PlanSet(context.Background(), "", SetOptions{File: path})
	require.NoError(t, err)
	id, ch, err := svc.RunSet(context.Background(), p, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	rec, err := svc.Sets.Load(id)
	require.NoError(t, err)
	require.Equal(t, path, rec.File)
	require.Equal(t, "hotfix", rec.Set)
	require.Equal(t, SetSuccessful, rec.Status)
}

// fileRecord saves an interrupted run of the train file at path.
func fileRecord(t *testing.T, svc *Service, path string) SetRecord {
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	rec.Set, rec.File = "hotfix", path
	rec.ID = NewSetID("hotfix", svc.Clock.Now())
	require.NoError(t, svc.Sets.Save(rec))
	return rec
}

func TestResumeAFileSetRereadsTheFile(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), hotfixBody)
	rec := fileRecord(t, svc, path)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
	require.Len(t, fp.Triggered, 1, "only the run that never triggered")
}

func TestResumeAFileSetWhoseFileIsGone(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	dir := t.TempDir()
	path := writeTrain(t, dir, hotfixBody)
	rec := fileRecord(t, svc, path)
	require.NoError(t, os.Remove(path))
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "the train file of set run "+rec.ID+" is gone")
	require.Contains(t, errTry(err), "--file")

	moved := writeTrain(t, t.TempDir(), hotfixBody)
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{File: moved}, SetLockOptions{})
	require.NoError(t, err, "--file points at the moved file")
	drain(ch)
}

func TestResumeAFileThatNamesAnotherSet(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), "name: other\n"+hotfixBody)
	rec := fileRecord(t, svc, path)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "defines set other, not hotfix")
}

func TestResumeAFileSetWhoseStagesChanged(t *testing.T) {
	svc := setService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	path := writeTrain(t, t.TempDir(), "stages:\n  - [publish-specs]\n")
	rec := fileRecord(t, svc, path)
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "changed since")
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ -run 'FromAFile|RecordsTheFile|FileSet|AnotherSet' 2>&1 | tail -2` — expected: compile errors (`File` unknown).

- [ ] **Step 3: Implement**

`internal/app/setstore.go`, `SetRecord`, after `Set`:

```go
	File       string        `json:"file,omitempty"` // the train file's absolute path; empty for a configured set
```

`internal/app/sets.go`:

- `SetOptions` gains `File string // a train file to read the set from instead of the configuration`.
- `SetPlan` gains `File string // the train file the set came from; empty for a configured set`.
- In `PlanSet`, replace the lookup:

```go
	set, ok, err := s.Cfg.Set(name)
	if err != nil {
		return SetPlan{}, err
	}
	if !ok {
		return SetPlan{}, errs.Usagef("unknown set %q", name).WithTry("bam set list")
	}
```

  with:

```go
	var set config.ResolvedSet
	if o.File != "" {
		abs, err := filepath.Abs(o.File)
		if err != nil {
			return SetPlan{}, errs.Configf("cannot resolve %s", o.File).WithWhy(err.Error())
		}
		if set, err = s.Cfg.LoadSetFile(abs); err != nil {
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
```

  and set `File: o.File` in the `SetPlan` literal (`p := SetPlan{Name: name, …, File: o.File}`). Add `"path/filepath"` to the imports.
- In `RunSet`, the record literal gains `File: p.File`.
- In `continueSet`, before `p, err := s.PlanSet(ctx, rec.Set, o)`:

```go
	again := "bam set " + verb + " " + rec.ID
	if verb == "rerun" {
		again = "bam set rerun --failed " + rec.ID
	}
	if rec.File != "" && o.File == "" {
		o.File = rec.File
		if _, err := os.Stat(rec.File); err != nil {
			return SetPlan{}, nil, errs.Configf("the train file of set run %s is gone: %s", rec.ID, rec.File).
				WithTry(again + " --file <its new path>")
		}
	}
```

  and after `PlanSet` returns, before the shape check:

```go
	if rec.File != "" && p.Name != rec.Set {
		return SetPlan{}, nil, errs.Configf("%s defines set %s, not %s", p.File, p.Name, rec.Set).
			WithTry(again + " --file <the train file of " + rec.Set + ">")
	}
```

  Add `"os"` to the imports. The `rec.File` from the record is kept when saving the continued run (it is part of `rec`, which is saved as is).

- [ ] **Step 4: Run** `go test -race ./internal/app/` — PASS, with every existing set test unchanged.

- [ ] **Step 5: Gate and commit** — `go test -race ./... && make lint && git add internal/app && git commit -m "feat: run, record and resume a set from a train file"`

---

### Task 3: `cli` and `view`, docs

**Files:**
- Modify: `internal/cli/sets.go` (`set run -f`, `--file` on resume and rerun, connect), `internal/view/sets.go` (`SetRecordDoc.File`, status text)
- Test: `internal/cli/sets_test.go`, `internal/view/sets_test.go`
- Docs: `docs/configuration.md`, `README.md`, `docs/json.md`, `docs/cli/`, run sets design §12, spec status

- [ ] **Step 1: Failing tests** — append to `internal/cli/sets_test.go`:

```go
const hotfixFile = "stages:\n  - [publish-specs]\n  - [{target: provision-lab, vars: {cluster_name: alpha}}]\n"

func trainPath(t *testing.T, h *harness) string {
	t.Helper()
	p := filepath.Join(h.root, "hotfix.yaml")
	require.NoError(t, os.WriteFile(p, []byte(hotfixFile), 0o644))
	return p
}

func TestSetRunFromAFile(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	require.Equal(t, 0, h.run("set", "run", "-f", p, "--dry-run"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "hotfix")
	require.Empty(t, h.fake.Triggered)

	require.Equal(t, 0, h.run("set", "run", "-f", p), h.stderr.String())
	require.Len(t, h.fake.Triggered, 2)
	require.Equal(t, 0, h.run("set", "status", "hotfix", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"file": "`+p+`"`)
	require.Equal(t, 0, h.run("set", "status", "hotfix"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "file "+p)
}

func TestSetRunNameAndFileTogether(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	require.Equal(t, 2, h.run("set", "run", "release", "-f", p))
	require.Contains(t, h.stderr.String(), "a set name or -f, not both")
	require.Equal(t, 2, h.run("set", "run"))
	require.Contains(t, h.stderr.String(), "a set name or -f")
	require.Empty(t, h.connectOpts, "no network before the check")
}

func TestSetResumeOfAFileSetWithFile(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	p := trainPath(t, h)
	store := &app.SetStore{Dir: h.env.Paths.Sets}
	id := app.NewSetID("hotfix", fixedNow)
	require.NoError(t, store.Save(app.SetRecord{Version: 1, ID: id, Set: "hotfix", File: filepath.Join(h.root, "gone.yaml"),
		Origin: workOrigin, Repo: h.root, StartedAt: fixedNow, Status: app.SetInterrupted, VarNames: []string{},
		Stages: []app.StageRecord{
			{Runs: []app.RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
			{Runs: []app.RunRecord{{Target: "provision-lab", PlanKey: "PROJ-PROV12"}}},
		}}))
	require.Equal(t, 3, h.run("set", "resume", id))
	require.Contains(t, h.stderr.String(), "is gone")
	require.Equal(t, 0, h.run("set", "resume", id, "--file", p), h.stderr.String())
	require.Len(t, h.fake.Triggered, 1)
}
```

- [ ] **Step 2: Run** `go test ./internal/cli/ -run 'FromAFile|NameAndFile|FileSetWithFile' 2>&1 | grep -E '^--- |^ok|^FAIL'` — expected: FAIL (unknown flag `-f`).

- [ ] **Step 3: Implement**

`internal/view/sets.go`: `SetRecordDoc` gains `File string \`json:"file,omitempty"\`` after `Set`, filled from `rec.File` in `SetRecordJSON`; `SetStatusView` prints `fmt.Fprintf(o.W, "file %s\n", rec.File)` right after its header line when `rec.File != ""`.

`internal/cli/sets.go`:

- `newSetRunCmd`: `Use: "run [set] [-f FILE]"`, `Args: cobra.MaximumNArgs(1)`, a `file string` flag `f.StringVarP(&file, "file", "f", "", "run the set defined in this train file instead of a configured set")`. At the top of `RunE`:

```go
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
```

  (the rest unchanged; `args[0]` uses become `name`).
- Add:

```go
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
```

- `newSetResumeCmd` and `newSetRerunCmd`: a `file string` flag `f.StringVar(&file, "file", "", "the train file of a file set, when it has moved since the run")`, passed as `File: file` in their `app.SetOptions`.

- [ ] **Step 4: Run** `go test ./internal/cli/ ./internal/view/` — PASS.

- [ ] **Step 5: Docs**
  - `docs/configuration.md`, after the `### Matrix` subsection: `### Train files` — the §3 example of the spec, then: "`bam set run -f FILE` runs it. The file holds one set in the shape of an entry under `sets:`, plus an optional `name:` (default: the file name). Presets come from your configuration; literal secrets are refused, as in `.bam.yaml`. Its runs are recorded under its name, so `bam set status`, `resume` and `rerun --failed` work as for any set; resume and rerun read the file again, and `--file` points at it when it has moved."
  - README Release trains: `` `bam set run -f hotfix.yaml` runs a one-off train from its own file, without adding it to `.bam.yaml`. ``
  - `docs/json.md` `set status --json` paragraph: add `` `file` (the train file, for a set run from one) `` after `var_names`.
  - Run sets design §12: change `Batch files remain later work.` to `` Batch files: done as train files, see [`2026-10-06-bam-set-files-design.md`](2026-10-06-bam-set-files-design.md). ``
  - Spec status → `Implemented`. `make docs`.

- [ ] **Step 6: Gate and commit** — `go test -race ./... && make lint && make check-fixtures && make docs && git add internal docs README.md && git commit -m "feat: add bam set run -f for train files"`
