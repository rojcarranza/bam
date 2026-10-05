# Matrix runs in sets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `matrix` on a set run expands one preset into several runs of its stage, each named `preset[k=v,…]`, recorded, watched, resumed and rerun per run.

**Architecture:** `config` parses both matrix forms and expands set runs (`SetRun.Expand`), checking names and size at load. `app` plans one run per expanded entry and carries the run name through `SetRunPlan`, `RunRecord`, `SetEvent` and `sameShape`. `view` keys and prints runs by name and shows matrices in `set show`.

**Tech Stack:** Go, yaml.v3, cobra, testify.

**Spec:** `docs/superpowers/specs/2026-10-05-bam-set-matrix-design.md`

## Global Constraints

- Placeholders only (`bamboo.example.com`, `PROJ`, `OPS`, `LAB`, `jdoe`).
- `--json` fields are added, never renamed or removed. New: `run` on set events, `name` on record runs and dry-run runs, `matrix` and `runs` on `set show`/`list` runs.
- A set without a matrix must behave and print exactly as before; a record without `name` must still resume and rerun.
- A run name never carries a secret: a secret-named key's literal value shows as `********`, an `${ENV}` reference as written.
- `app` never prints, reads stdin or imports cobra. No new dependencies. No test sleeps or touches the network.
- Conventional Commits, one per task; the owner's identity only.
- Before finishing: `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.
- `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"` in non-interactive shells.

## Review Focus

1. Old records (no `name`) must still resume and rerun — `TestResumeAnOldRecordWithoutNames` (Task 2).
2. A product that multiplies past 50 runs must be refused at load, before any network — `TestSetConfigErrors` "too many runs" (Task 1).
3. A secret-named matrix key must not leak its literal value into names, records or output — `TestExpandMasksASecretInTheName` (Task 1).
4. `--var` must still beat matrix values — `TestPlanSetMatrixPrecedence` (Task 2).
5. Only the failed matrix run is restarted by `rerun --failed` — `TestRerunRestartsOnlyTheFailedMatrixRun` (Task 2).

---

### Task 1: `config` — the matrix, its expansion and its checks

**Files:**
- Modify: `internal/config/schema.go` (`SetRun.Matrix`, `Matrix` + `UnmarshalYAML`, stage field check)
- Create: `internal/config/matrix.go` (`ExpandedRun`, `SetRun.Expand`, `maxStageRuns`)
- Modify: `internal/config/sets.go` (`validateSet` by expanded names, size limit)
- Modify: `internal/config/validate.go` (`checkSetSecrets` over expanded vars)
- Test: `internal/config/matrix_test.go`, `internal/config/sets_test.go`

**Interfaces:**
- Produces: `type Matrix struct { Product map[string][]string; Sets []StringMap }`; `SetRun.Matrix *Matrix`; `type ExpandedRun struct { Name, Target string; Vars StringMap }`; `func (r SetRun) Expand() []ExpandedRun`; `const maxStageRuns = 50`.

- [ ] **Step 1: Failing tests** — create `internal/config/matrix_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func decodeRun(t *testing.T, src string) SetRun {
	t.Helper()
	var st Stage
	require.NoError(t, yaml.Unmarshal([]byte("["+src+"]"), &st))
	require.Len(t, st, 1)
	return st[0]
}

func names(rs []ExpandedRun) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func TestExpandWithoutAMatrixIsTheRunItself(t *testing.T) {
	rs := decodeRun(t, "{target: deploy, vars: {env: qa}}").Expand()
	require.Equal(t, []ExpandedRun{{Name: "deploy", Target: "deploy", Vars: StringMap{"env": "qa"}}}, rs)
}

func TestExpandProductOrder(t *testing.T) {
	r := decodeRun(t, "{target: deploy, matrix: {region: [eu, us], env: [qa, staging]}}")
	require.Equal(t, []string{
		"deploy[env=qa,region=eu]", "deploy[env=qa,region=us]",
		"deploy[env=staging,region=eu]", "deploy[env=staging,region=us]",
	}, names(r.Expand()), "sorted keys, values as written, last key fastest")
}

func TestExpandListKeepsOrderAndOwnKeys(t *testing.T) {
	r := decodeRun(t, "{target: smoke, vars: {tier: web}, matrix: [{env: prod, region: us}, {env: qa}]}")
	rs := r.Expand()
	require.Equal(t, []string{"smoke[env=prod,region=us]", "smoke[env=qa]"}, names(rs))
	require.Equal(t, StringMap{"tier": "web", "env": "qa"}, rs[1].Vars, "vars stay, matrix values join")
}

func TestExpandMatrixBeatsVars(t *testing.T) {
	rs := decodeRun(t, "{target: deploy, vars: {env: dev}, matrix: [{env: qa}]}").Expand()
	require.Equal(t, "qa", rs[0].Vars["env"])
}

func TestExpandMasksASecretInTheName(t *testing.T) {
	rs := decodeRun(t, `{target: deploy, matrix: [{db_password: hunter2, env: qa}, {db_password: "${PW}", env: prod}]}`).Expand()
	require.Equal(t, []string{"deploy[db_password=********,env=qa]", "deploy[db_password=${PW},env=prod]"}, names(rs))
	require.Equal(t, "hunter2", rs[0].Vars["db_password"], "the value itself is still sent")
}

func TestMatrixDecodeErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"scalar":        {"{target: d, matrix: qa}", "matrix"},
		"empty map":     {"{target: d, matrix: {}}", "matrix"},
		"empty list":    {"{target: d, matrix: []}", "matrix"},
		"empty values":  {"{target: d, matrix: {env: []}}", `"env"`},
		"nested value":  {"{target: d, matrix: {env: [[qa]]}}", `"env"`},
		"list of lists": {"{target: d, matrix: [[qa]]}", "matrix"},
	} {
		t.Run(name, func(t *testing.T) {
			var st Stage
			err := yaml.Unmarshal([]byte("["+tc.src+"]"), &st)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
```

In `internal/config/sets_test.go`, `TestSetConfigErrors`: change the `"duplicate"` row's expectation to `` `run "a" twice in stage 1` `` and add rows:

```go
		"duplicate matrix": {"sets: {rel: {stages: [[{target: a, matrix: [{env: qa}, {env: qa}]}]]}}", `run "a[env=qa]" twice in stage 1`},
		"too many runs":    {"sets: {rel: {stages: [[{target: a, matrix: {x: [1,2,3,4,5,6,7,8], y: [1,2,3,4,5,6,7,8]}}]]}}", "stage 1 expands to 64 runs; the limit is 50"},
		"matrix secret":    {"sets: {rel: {stages: [[{target: a, matrix: [{db_password: hunter2}]}]]}}", "holds a literal secret"},
```

and append:

```go
func TestSetMatrixAllowsAPresetTwiceInAStage(t *testing.T) {
	cfg, err := loadYAML(t, "version: 1\nservers: {work: {url: https://bamboo.example.com}}\ntargets:\n  a: {plan: PROJ-A}\n"+
		"sets: {rel: {stages: [[{target: a, matrix: [{env: qa}]}, {target: a, matrix: [{env: prod}]}]]}}\n", "")
	require.NoError(t, err)
	_, err = cfg.Sets()
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run** `go test ./internal/config/ 2>&1 | tail -3` — expected: compile error `r.Expand undefined`.

- [ ] **Step 3: Implement**

`internal/config/schema.go` — `SetRun` gains `Matrix *Matrix \`yaml:"matrix,omitempty"\``. In `Stage.UnmarshalYAML`, the field check accepts `matrix`:

```go
				if k := item.Content[i]; k.Value != "target" && k.Value != "vars" && k.Value != "matrix" {
```

and the default error reads `a stage run is a preset name or {target, vars, matrix}`. Add after `SetRun`:

```go
// Matrix expands one set run into several. Exactly one form is set:
// Product, a map of value lists (every combination), or Sets, a list of
// variable maps (each one run).
type Matrix struct {
	Product map[string][]string
	Sets    []StringMap
}

func (m *Matrix) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.MappingNode:
		var p StringListMap
		if err := n.Decode(&p); err != nil {
			return err
		}
		if len(p) == 0 {
			return fmt.Errorf("line %d: a matrix needs at least one variable", n.Line)
		}
		for k, vs := range p {
			if len(vs) == 0 {
				return fmt.Errorf("line %d: matrix variable %q has no values", n.Line, k)
			}
		}
		m.Product = p
	case yaml.SequenceNode:
		if len(n.Content) == 0 {
			return fmt.Errorf("line %d: a matrix needs at least one variable set", n.Line)
		}
		for _, item := range n.Content {
			if item.Kind != yaml.MappingNode {
				return fmt.Errorf("line %d: a matrix entry is a map of variables", item.Line)
			}
			var vs StringMap
			if err := item.Decode(&vs); err != nil {
				return err
			}
			m.Sets = append(m.Sets, vs)
		}
	default:
		return fmt.Errorf("line %d: a matrix is a map of value lists or a list of variable maps", n.Line)
	}
	return nil
}
```

(`StringListMap`'s own errors mention `options for "env"`; that is acceptable and names the key.)

Create `internal/config/matrix.go`:

```go
package config

import (
	"sort"
	"strings"
)

// maxStageRuns caps how many runs one stage may expand to.
const maxStageRuns = 50

// ExpandedRun is one run of a stage after matrix expansion.
type ExpandedRun struct {
	Name   string    // the preset, or preset[k=v,…] for a matrix run
	Target string
	Vars   StringMap // the set run's vars with the matrix values laid over them
}

// Expand returns the runs a set run stands for: itself when it has no
// matrix, else one per matrix entry, in order (a product varies its last
// sorted key fastest).
func (r SetRun) Expand() []ExpandedRun {
	if r.Matrix == nil {
		return []ExpandedRun{{Name: r.Target, Target: r.Target, Vars: r.Vars}}
	}
	var entries []StringMap
	if r.Matrix.Sets != nil {
		entries = r.Matrix.Sets
	} else {
		entries = product(r.Matrix.Product)
	}
	out := make([]ExpandedRun, 0, len(entries))
	for _, e := range entries {
		vars := StringMap{}
		for k, v := range r.Vars {
			vars[k] = v
		}
		for k, v := range e {
			vars[k] = v
		}
		out = append(out, ExpandedRun{Name: runName(r.Target, e), Target: r.Target, Vars: vars})
	}
	return out
}

func product(p map[string][]string) []StringMap {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []StringMap{{}}
	for _, k := range keys {
		var next []StringMap
		for _, partial := range out {
			for _, v := range p[k] {
				e := StringMap{}
				for pk, pv := range partial {
					e[pk] = pv
				}
				e[k] = v
				next = append(next, e)
			}
		}
		out = next
	}
	return out
}

// runName is preset[k=v,…] over the entry's own keys, sorted. A secret
// named key's literal value is masked: names are printed and recorded.
func runName(target string, e StringMap) string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		v := e[k]
		if _, ref := EnvRef(v); !ref && IsMaskedName(k) {
			v = "********"
		}
		parts[i] = k + "=" + v
	}
	return target + "[" + strings.Join(parts, ",") + "]"
}
```

`internal/config/sets.go`, `validateSet`: replace the per-stage loop body so names and size are checked on the expansion:

```go
	for i, st := range s.Stages {
		if len(st) == 0 {
			return fail("stage %d has no runs", i+1)
		}
		seen := map[string]bool{}
		count := 0
		for _, r := range st {
			t, ok := targets[r.Target]
			if !ok {
				return fail("names unknown preset %q in stage %d", r.Target, i+1).WithTry("bam target list")
			}
			for _, x := range r.Expand() {
				if seen[x.Name] {
					return fail("lists run %q twice in stage %d", x.Name, i+1)
				}
				seen[x.Name] = true
				count++
			}
			if t.Server == "" {
				continue
			}
			if server != "" && t.Server != server {
				return fail("mixes servers: presets %q and %q use servers %q and %q", serverFrom, r.Target, server, t.Server).
					WithTry("a set runs on one server; make one set per server")
			}
			server, serverFrom = t.Server, r.Target
		}
		if count > maxStageRuns {
			return fail("stage %d expands to %d runs; the limit is %d", i+1, count, maxStageRuns).
				WithTry("split the matrix across stages, or list fewer variable sets")
		}
	}
```

`internal/config/validate.go`, `checkSetSecrets`: check every expanded run's vars, so matrix values are covered:

```go
		for i, st := range sets[name].Stages {
			for _, r := range st {
				for _, x := range r.Expand() {
					for _, v := range sortedKeys(x.Vars) {
						val := x.Vars[v]
						if _, ok := EnvRef(val); ok || val == "" || !IsMaskedName(v) {
							continue
						}
						return errs.Configf("%s: sets.%s stage %d %s: vars.%s holds a literal secret", path, name, i+1, r.Target, v).
							WithWhy("variables named like password, secret, passphrase or sshkey cannot be committed").
							WithTry(fmt.Sprintf(`use an environment reference: %s: "${%s}"`, v, EnvNameFor(v)))
					}
				}
			}
		}
```

- [ ] **Step 4: Run** `go test ./internal/config/` — expected PASS.

- [ ] **Step 5: Commit** `git add internal/config && git commit -m "feat: expand a matrix on a set run into named runs"`

---

### Task 2: `app` — plan, record and report runs by name

**Files:**
- Modify: `internal/app/sets.go` (`SetRunPlan.Name`, `PlanSet`, `RunSet` record, every `SetEvent` with a `Target`, `sameShape`, `SetRunInfo`, `describeSet`)
- Modify: `internal/app/setstore.go` (`RunRecord.Name`, `RunRecord.RunName()`)
- Test: `internal/app/sets_test.go`

**Interfaces:**
- Consumes: `config.SetRun.Expand`, `config.ExpandedRun`, `config.Matrix` (Task 1).
- Produces: `SetRunPlan.Name string`; `RunRecord.Name string` (`json:"name,omitempty"`) and `func (r RunRecord) RunName() string`; `SetEvent.Run string`; `SetRunInfo{Target string; Vars []TargetVar; Matrix *config.Matrix; Runs []string}`.

- [ ] **Step 1: Failing tests** — append to `internal/app/sets_test.go`:

```go
// matrixService is setService with release's second stage expanded over
// cluster_name: alpha and beta.
func matrixService(t *testing.T, p *fake.Provider) *Service {
	svc := setService(t, p)
	svc.Cfg.Project.Sets["release"] = config.Set{Stages: []config.Stage{
		{{Target: "publish-specs"}},
		{{Target: "provision-lab", Vars: config.StringMap{"compute_nodes": "4"},
			Matrix: &config.Matrix{Sets: []config.StringMap{{"cluster_name": "alpha"}, {"cluster_name": "beta"}}}}},
	}}
	return svc
}

func TestPlanSetExpandsAMatrix(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{})
	require.NoError(t, err)
	require.Len(t, p.Stages[1], 2)
	require.Equal(t, "provision-lab[cluster_name=alpha]", p.Stages[1][0].Name)
	require.Equal(t, "provision-lab", p.Stages[1][0].Target)
	require.Equal(t, "publish-specs", p.Stages[0][0].Name, "a run without a matrix is named after its preset")
	v, _ := p.Stages[1][1].Vars.Get("cluster_name")
	require.Equal(t, "beta", v.Value)
}

func TestPlanSetMatrixPrecedence(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	svc.Getenv = func(string) string { return "x" }
	p, err := svc.PlanSet(context.Background(), "release", SetOptions{Vars: []string{"cluster_name=gamma"}})
	require.NoError(t, err)
	for _, r := range p.Stages[1] {
		v, _ := r.Vars.Get("cluster_name")
		require.Equal(t, "gamma", v.Value, "--var beats the matrix")
		n, _ := r.Vars.Get("compute_nodes")
		require.Equal(t, "4", n.Value, "the set run's vars still apply")
	}
}

func TestRunSetMatrixRecordsAndReportsEveryRun(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := matrixService(t, fp)
	id, evs := runSet(t, svc)
	require.Len(t, fp.Triggered, 3)
	rec, _ := svc.Sets.Load(id)
	require.Equal(t, "provision-lab[cluster_name=alpha]", rec.Stages[1].Runs[0].Name)
	require.Equal(t, "provision-lab[cluster_name=beta]", rec.Stages[1].Runs[1].Name)
	var runs []string
	for _, e := range evs {
		if e.Type == SetRunTriggered {
			runs = append(runs, e.Run)
		}
	}
	require.Equal(t, []string{"publish-specs", "provision-lab[cluster_name=alpha]", "provision-lab[cluster_name=beta]"}, runs)
}

func TestRerunRestartsOnlyTheFailedMatrixRun(t *testing.T) {
	fp := trainBamboo(nil)
	restartable(fp, "PROJ-PROV12-2", provider.StateSuccess)
	svc := matrixService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Status: SetFailed, Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", Name: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1", State: provider.StateSuccess}}},
		{Runs: []RunRecord{
			{Target: "provision-lab", Name: "provision-lab[cluster_name=alpha]", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-1", State: provider.StateSuccess},
			{Target: "provision-lab", Name: "provision-lab[cluster_name=beta]", PlanKey: "PROJ-PROV12", BuildKey: "PROJ-PROV12-2", State: provider.StateFailed},
		}},
	}})
	ch, err := svc.RerunSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	drain(ch)
	require.Equal(t, []string{"PROJ-PROV12-2"}, fp.Restarted)
	got, _ := svc.Sets.Load(rec.ID)
	require.Equal(t, SetSuccessful, got.Status)
}

func TestResumeAnOldRecordWithoutNames(t *testing.T) {
	fp := trainBamboo(map[string]provider.State{"PROJ-BUILD": provider.StateSuccess, "PROJ-PROV12": provider.StateSuccess})
	svc := setService(t, fp)
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: twoStages("PROJ-BUILD-1")})
	require.Empty(t, rec.Stages[0].Runs[0].Name, "written before run names existed")
	ch, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.NoError(t, err)
	evs := drain(ch)
	require.Equal(t, provider.StateSuccess, evs[len(evs)-1].State)
}

func TestResumeRefusesAChangedMatrix(t *testing.T) {
	svc := matrixService(t, trainBamboo(nil))
	svc.Getenv = func(string) string { return "x" }
	rec := saveRecord(t, svc, SetRecord{Stages: []StageRecord{
		{Runs: []RunRecord{{Target: "publish-specs", PlanKey: "PROJ-BUILD", BuildKey: "PROJ-BUILD-1"}}},
		{Runs: []RunRecord{
			{Target: "provision-lab", Name: "provision-lab[cluster_name=alpha]", PlanKey: "PROJ-PROV12"},
			{Target: "provision-lab", Name: "provision-lab[cluster_name=gamma]", PlanKey: "PROJ-PROV12"},
		}},
	}})
	_, err := svc.ResumeSet(context.Background(), rec.ID, SetOptions{}, SetLockOptions{})
	require.Equal(t, errs.KindConfig, errs.KindOf(err))
	require.Contains(t, err.Error(), "changed since")
}

func TestDescribeSetShowsTheMatrixAndItsRuns(t *testing.T) {
	svc := matrixService(t, fakeBamboo())
	info, err := DescribeSet(svc.Cfg, func(string) string { return "" }, "release")
	require.NoError(t, err)
	run := info.Stages[1][0]
	require.NotNil(t, run.Matrix)
	require.Equal(t, []string{"provision-lab[cluster_name=alpha]", "provision-lab[cluster_name=beta]"}, run.Runs)
	require.Equal(t, []string{"publish-specs"}, info.Stages[0][0].Runs)
}
```

- [ ] **Step 2: Run** `go test ./internal/app/ 2>&1 | tail -3` — expected: compile errors (`Name`, `Run`, `Runs`, `Matrix` undefined).

- [ ] **Step 3: Implement**

`internal/app/setstore.go`, `RunRecord` gains after `Target`:

```go
	Name     string         `json:"name,omitempty"` // run name, preset[k=v,…] for a matrix run; empty in records written before names
```

and add:

```go
// RunName is the run's name; a record written before run names existed
// names a run after its preset.
func (r RunRecord) RunName() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Target
}
```

`internal/app/sets.go`:

- `SetRunPlan` gains `Name string // the run's name: the preset, or preset[k=v,…]` before `Target`.
- `SetEvent` gains `Run    string         // run name; empty for stage and set events` after `Target`.
- `PlanSet`, the inner loop becomes:

```go
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
```

  (keep the comment above the `KindUsage` check; `runs := make(...)` capacity may stay `len(st)`.)
- `RunSet`: `RunRecord{Target: r.Target, Name: r.Name, PlanKey: r.Ref.PlanKey}`.
- Every `SetEvent{…, Target: run.Target, …}` in `stage` and `watchRun` gains `Run: run.Name` right after `Target: run.Target` (six places).
- `sameShape` compares `p.Stages[i][j].Name != rec.Stages[i].Runs[j].RunName()`.
- `SetRunInfo`:

```go
type SetRunInfo struct {
	Target string
	Vars   []TargetVar
	Matrix *config.Matrix // nil without a matrix
	Runs   []string       // the run names it expands to
}
```

- `describeSet`'s inner loop:

```go
		for _, r := range st {
			info := SetRunInfo{Target: r.Target, Vars: targetVars(r.Vars, getenv), Matrix: r.Matrix}
			for _, x := range r.Expand() {
				info.Runs = append(info.Runs, x.Name)
			}
			runs = append(runs, info)
		}
```

- [ ] **Step 4: Run** `go test -race ./internal/app/` — expected PASS (all existing set, resume and rerun tests unchanged).

- [ ] **Step 5: Commit** `git add internal/app && git commit -m "feat: plan, record and report set runs by name"`

---

### Task 3: `view` — names everywhere, matrices in `set show`

**Files:**
- Modify: `internal/view/setwatch.go` (board keyed by run name)
- Modify: `internal/view/sets.go` (`Sets`, `SetDetail`, `SetDryRun`, `SetStatusView`, JSON docs)
- Test: `internal/view/setwatch_test.go`, `internal/view/sets_test.go`

**Interfaces:**
- Consumes: `SetRunPlan.Name`, `SetEvent.Run`, `RunRecord.RunName()`, `SetRunInfo.Matrix/Runs` (Task 2).
- Produces: JSON `run` on `SetEventDoc`, `name` on `SetRunRecordDoc` and `SetRunPlanDoc`, `matrix` (omitempty) and `runs` on `SetRunDoc`.

- [ ] **Step 1: Failing tests** — append to `internal/view/setwatch_test.go`:

```go
func matrixPlan() app.SetPlan {
	return app.SetPlan{Name: "regions", Stages: [][]app.SetRunPlan{{
		{Name: "deploy[env=qa]", Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}},
		{Name: "deploy[env=prod]", Target: "deploy", Ref: app.PlanRef{PlanKey: "OPS-DEP"}},
	}}}
}

func TestSetLiveKeysMatrixRunsByName(t *testing.T) {
	o, buf := testOut(true)
	o.Width = 100
	l := NewSetLive(o, matrixPlan())
	for _, e := range []app.SetEvent{
		{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=qa]", Build: provider.Build{Key: "OPS-DEP-1", State: provider.StateQueued}},
		{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=prod]", Build: provider.Build{Key: "OPS-DEP-2", State: provider.StateQueued}},
		{Type: app.SetRunDone, Time: setAt, Target: "deploy", Run: "deploy[env=prod]", Build: provider.Build{Key: "OPS-DEP-2"}, State: provider.StateFailed},
	} {
		l.Event(e)
	}
	out := lastFrame(buf.String())
	require.Regexp(t, `deploy\[env=qa\]\s+OPS-DEP-1`, out)
	require.Regexp(t, `deploy\[env=prod\]\s+OPS-DEP-2\s+Failed`, out)
	require.Equal(t, 2, strings.Count(out, "deploy["), "two rows, not one per preset")
}

func TestSetLinesNameTheMatrixRun(t *testing.T) {
	o, buf := testOut(false)
	l := NewSetLines(o)
	l.Event(app.SetEvent{Type: app.SetRunTriggered, Time: setAt, Target: "deploy", Run: "deploy[env=qa]", Build: provider.Build{Key: "OPS-DEP-1"}})
	require.Equal(t, "2026-09-29T10:15:00Z stage 1 deploy[env=qa] OPS-DEP-1 triggered\n", buf.String())
}
```

Append to `internal/view/sets_test.go`:

```go
func matrixInfo() app.SetInfo {
	return app.SetInfo{Name: "regions", DefinedIn: []string{"/repo/.bam.yaml"}, Stages: [][]app.SetRunInfo{{
		{Target: "deploy", Matrix: &config.Matrix{Product: map[string][]string{"env": {"qa", "prod"}, "region": {"eu"}}},
			Runs: []string{"deploy[env=prod,region=eu]", "deploy[env=qa,region=eu]"}},
		{Target: "smoke", Matrix: &config.Matrix{Sets: []config.StringMap{{"env": "qa"}}}, Runs: []string{"smoke[env=qa]"}},
	}}}
}

func TestSetsListCountsExpandedRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, Sets(o, []app.SetInfo{matrixInfo()}))
	require.Regexp(t, `regions\s+1\s+3\s`, buf.String())
}

func TestSetDetailShowsMatricesAndRuns(t *testing.T) {
	o, buf := testOut(false)
	require.NoError(t, SetDetail(o, matrixInfo()))
	out := buf.String()
	require.Contains(t, out, "deploy  matrix env=[qa prod] region=[eu]")
	require.Contains(t, out, "smoke  matrix 1 variable set")
	require.Contains(t, out, "    deploy[env=qa,region=eu]\n")
}

func TestSetJSONCarriesMatrixAndRuns(t *testing.T) {
	data, err := json.Marshal(SetJSON(matrixInfo()))
	require.NoError(t, err)
	s := string(data)
	require.Contains(t, s, `"matrix":{"env":["qa","prod"],"region":["eu"]}`)
	require.Contains(t, s, `"matrix":[{"env":"qa"}]`)
	require.Contains(t, s, `"runs":["smoke[env=qa]"]`)
}

func TestSetRecordAndEventJSONCarryNames(t *testing.T) {
	rec := app.SetRecord{ID: "r", Set: "regions", Status: app.SetFailed, Stages: []app.StageRecord{{Runs: []app.RunRecord{
		{Target: "deploy", Name: "deploy[env=qa]", BuildKey: "OPS-DEP-1", State: provider.StateFailed},
		{Target: "smoke", BuildKey: "OPS-SMK-1", State: provider.StateSuccess},
	}}}}
	data, _ := json.Marshal(SetRecordJSON(rec, nil))
	require.Contains(t, string(data), `"name":"deploy[env=qa]"`)
	require.Contains(t, string(data), `"name":"smoke"`, "an old record's run is named after its preset")
	o, buf := testOut(false)
	require.NoError(t, SetStatusView(o, rec, nil))
	require.Regexp(t, `deploy\[env=qa\]\s+OPS-DEP-1`, buf.String())

	ev, _ := json.Marshal(SetEventJSON(app.SetEvent{Type: app.SetRunDone, Time: setAt, Target: "deploy", Run: "deploy[env=qa]"}))
	require.Contains(t, string(ev), `"run":"deploy[env=qa]"`)
}
```

(`sets_test.go` imports `config`: add `"github.com/r0jjames/bam-cli/internal/config"` if missing.)

- [ ] **Step 2: Run** `go test ./internal/view/ -run 'Set' 2>&1 | tail -3` — expected: compile errors.

- [ ] **Step 3: Implement**

`internal/view/setwatch.go`:

- Add:

```go
// runOf is the run an event is about: its name, or for events from before
// run names existed, its preset.
func runOf(e app.SetEvent) string {
	if e.Run != "" {
		return e.Run
	}
	return e.Target
}

func planRunName(r app.SetRunPlan) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Target
}
```

- `newBoard`: `rows = append(rows, &setRow{target: planRunName(r)})`.
- `apply`: every `b.row(e.Stage, e.Target)` becomes `b.row(e.Stage, runOf(e))`.
- `Seed`: `r := l.b.row(i, run.RunName())`.
- `SetLines.Event`: `head := fmt.Sprintf("%s stage %d %s", ts, e.Stage+1, runOf(e))`.

(`setRow.target` now holds the run name; renaming the field is optional and not required.)

`internal/view/sets.go`:

- `Sets`: count runs as `runs += len(r.Runs)` over each `r` of each stage (falling back to 1 when `len(r.Runs) == 0`).
- `SetDetail`: replace the stage block with:

```go
	for i, st := range s.Stages {
		names := make([]string, len(st))
		for j, r := range st {
			names[j] = r.Target
		}
		fmt.Fprintf(o.W, "stage %d  %s\n", i+1, strings.Join(names, ", "))
		for _, r := range st {
			if r.Matrix == nil {
				continue
			}
			fmt.Fprintf(o.W, "  %s  matrix %s\n", r.Target, matrixLine(r.Matrix))
			for _, n := range r.Runs {
				fmt.Fprintf(o.W, "    %s\n", n)
			}
		}
		t := Table{Indent: "    ", Headers: []string{"", "", ""}}
		for _, r := range st {
			for _, v := range r.Vars {
				t.Rows = append(t.Rows, []string{r.Target, v.Name, v.Display()})
			}
		}
		if len(t.Rows) > 0 {
			if err := RenderRows(o, t); err != nil {
				return err
			}
		}
	}
```

  and add:

```go
// matrixLine is a matrix on one line: a product's lists, or how many
// variable sets a list holds.
func matrixLine(m *config.Matrix) string {
	if m.Sets != nil {
		if len(m.Sets) == 1 {
			return "1 variable set"
		}
		return fmt.Sprintf("%d variable sets", len(m.Sets))
	}
	keys := make([]string, 0, len(m.Product))
	for k := range m.Product {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=[" + strings.Join(m.Product[k], " ") + "]"
	}
	return strings.Join(parts, " ")
}
```

  (`view` already imports `app`; importing `config` for the type is allowed by the layering only through `app`'s types — if the import table forbids `config` in `view`, take `matrixLine` as a method on `app.SetRunInfo` instead: `func (r SetRunInfo) MatrixLine() string` in `internal/app/sets.go`, and `MatrixDoc() any` returning `r.Matrix.Product` or `r.Matrix.Sets`; record this as a ruling.)
- `SetDryRun`: print `planRunName(r)` instead of `r.Target`.
- `SetStatusView`: the row's first cell uses `r.RunName()`.
- JSON: `SetEventDoc` gains `Run string \`json:"run,omitempty"\`` after `Target`, filled from `e.Run`. `SetRunRecordDoc` gains `Name string \`json:"name"\`` after `Target`, filled from `r.RunName()`. `SetRunPlanDoc` gains `Name string \`json:"name"\`` filled from `planRunName(run)` where the dry-run doc is built. `SetRunDoc` gains `Matrix any \`json:"matrix,omitempty"\`` (the product map or the sets list, nil without a matrix) and `Runs []string \`json:"runs"\`` (from `r.Runs`, `[]string{r.Target}` when empty).

- [ ] **Step 4: Run** `go test ./internal/view/...` — expected PASS; no golden changes (no golden uses a matrix). If a golden changes, stop and inspect: it means a non-matrix output changed, which the Global Constraints forbid.

- [ ] **Step 5: Commit** `git add internal/view internal/app && git commit -m "feat: show matrix runs by name in set output and JSON"`

---

### Task 4: CLI wiring, docs, e2e and the gate

**Files:**
- Modify: `internal/cli/sets.go` (`planFromRecord` names)
- Test: `internal/cli/sets_test.go`
- Modify: `docs/configuration.md`, `README.md`, `docs/json.md`, `e2e/e2e_test.go`, `docs/superpowers/specs/2026-09-29-bam-run-sets-core-design.md` §12, the matrix spec status

- [ ] **Step 1: Failing test** — append to `internal/cli/sets_test.go`:

```go
const matrixYAML = projectYAML + `  publish-specs:
    plan: PROJ-BUILD
sets:
  regions:
    stages:
      - - target: provision-lab
          matrix:
            cluster_name: [alpha, beta]
`

func TestSetMatrixRunsEveryEntry(t *testing.T) {
	h := setHarness(t, provider.StateSuccess)
	require.NoError(t, os.WriteFile(filepath.Join(h.root, ".bam.yaml"), []byte(matrixYAML), 0o644))
	require.Equal(t, 0, h.run("set", "show", "regions"), h.stderr.String())
	require.Contains(t, h.stdout.String(), "provision-lab[cluster_name=beta]")
	require.Equal(t, 0, h.run("set", "run", "regions"), h.stderr.String())
	require.Len(t, h.fake.Triggered, 2)
	require.Contains(t, h.stdout.String(), "provision-lab[cluster_name=alpha]")
	require.Equal(t, 0, h.run("set", "status", "--json"), h.stderr.String())
	require.Contains(t, h.stdout.String(), `"name": "provision-lab[cluster_name=beta]"`)
}
```

- [ ] **Step 2: Run** `go test ./internal/cli/ -run TestSetMatrixRunsEveryEntry` — expected to pass already if Tasks 1–3 cover it; if it fails, the failure points at the CLI wiring below. Either way continue with Step 3 (resume's live board needs names from the record).

- [ ] **Step 3: Implement** — `planFromRecord` in `internal/cli/sets.go` sets `Name: run.RunName()` on each `app.SetRunPlan`.

- [ ] **Step 4: Docs**
  - `docs/configuration.md`, "Sets (release trains)": a `### Matrix` subsection with the two forms (the §3.1 example of the spec), the run name rule, precedence (plan < defaults < vars < matrix < `--var`), and the 50-run limit.
  - `README.md` Release trains: after the example, one paragraph: `` A `matrix` runs one preset over several variable sets in the same stage: `matrix: {env: [qa, staging]}` runs every combination, `matrix: [{env: qa}, {env: prod, region: us}]` exactly those. Each run is named, for example `deploy[env=qa]`, and is resumed and rerun on its own. ``
  - `docs/json.md` Sets: `run` on set events (the run name; the preset stays in `target`), `name` on `set status` runs and dry-run runs, `matrix` and `runs` on `set list`/`set show` runs.
  - Run sets design §12: replace `- Matrix expansion and batch files.` with `- Matrix expansion: done, see [\`2026-10-05-bam-set-matrix-design.md\`](2026-10-05-bam-set-matrix-design.md). Batch files remain later work.`
  - Matrix spec: `**Status:** Implemented`.

- [ ] **Step 5: e2e** — in `TestSetSmoke`, give the first plan's target a two-entry matrix of a dummy variable: write `run1: {plan: …}` as before, and the set's first stage as `[{target: run1, matrix: [{bam_e2e: a}, {bam_e2e: b}]}]` instead of `[run1]`. After the run, check the record lists two runs in stage 1 with names `run1[bam_e2e=a]` and `run1[bam_e2e=b]` (parse `set status --json`, as the test already does for status). `go vet -tags e2e ./e2e/` — no output.

- [ ] **Step 6: Gate** — `go test -race ./... && make lint && make check-fixtures && make docs` — all clean; commit `docs/cli` only if it changed.

- [ ] **Step 7: Commit** `git add -A internal/cli docs README.md e2e && git commit -m "feat: run matrix sets from the command line, documented and in e2e"`
