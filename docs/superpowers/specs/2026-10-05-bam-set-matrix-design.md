# bam: matrix runs in sets

**Status:** Implemented | **Date:** 2026-10-05
**Product requirements:** [`docs/prd.md`](../../prd.md) §2 problem 3 ("No parallelism"), §7 "Future", "Run sets"
**Builds on:** [`2026-09-29-bam-run-sets-core-design.md`](2026-09-29-bam-run-sets-core-design.md) (configuration §3, record §5.1, events §4.3, output §7), [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

Testing one plan across several variable sets (every environment, every region) means one preset per combination in `.bam.yaml`, or one `bam run --var …` per combination typed by hand and watched in separate terminals. The PRD names this as the "no parallelism" problem.

A **matrix** on a set run expands one preset into several runs of the same stage, one per variable set. They trigger together, are watched together, and the stage succeeds only if all of them do. Resume and `rerun --failed` work per expanded run.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Where a matrix lives | On a set run, next to `vars` | Sets already order, watch, record, resume and rerun runs; a matrix is more runs in a stage |
| Form | Both: a map of lists (every combination) and a list of maps (exact variable sets) | The owner's choice: the product is short to write, the list says exactly what runs |
| Identity of a run | A run name, `<preset>[k=v,…]`, unique in its stage; the preset name stays in `target` | Records, events and the live board keyed runs on the preset, which a matrix repeats |
| Size | At most 50 runs per stage, checked at load | Guards against an accidental 10×10 product; no parallel cap beyond it, Bamboo queues on its agents |
| Rejected | A matrix on `bam run` outside a set | It would need its own watch, record and failure rules, which sets already have |
| Rejected | `exclude:` for products | The list form covers a product with holes |

## 3. Configuration

### 3.1 Schema

`SetRun` gains `Matrix`:

```yaml
sets:
  regions:
    stages:
      - - target: deploy
          vars: {deploy_token: "${DEPLOY_TOKEN}"}
          matrix:                 # map of lists: every combination
            env: [qa, staging]
            region: [eu, us]
      - - target: smoke
          matrix:                 # list of maps: exactly these variable sets
            - {env: qa}
            - {env: prod, region: us}
```

```go
// Matrix expands one set run into several. Exactly one of the two forms is
// set: Product (a map of value lists, every combination) or Sets (a list of
// variable maps, each one run).
type Matrix struct {
	Product map[string][]string
	Sets    []StringMap
}

type SetRun struct {
	Target string    `yaml:"target"`
	Vars   StringMap `yaml:"vars,omitempty"`
	Matrix *Matrix   `yaml:"matrix,omitempty"` // UnmarshalYAML accepts a mapping (Product) or a sequence (Sets)
}
```

### 3.2 Expansion

```go
// ExpandedRun is one run of a stage after matrix expansion.
type ExpandedRun struct {
	Name   string    // the preset, or preset[k=v,…] for a matrix run
	Target string
	Vars   StringMap // the set run's vars with the matrix values laid over them
}

// Expand returns the runs a set run stands for: itself when it has no
// matrix, else one per matrix entry.
func (r SetRun) Expand() []ExpandedRun
```

- **Product:** keys in sorted order, values in the order written, the last key changing fastest: `env: [qa, staging]`, `region: [eu, us]` gives `qa/eu`, `qa/us`, `staging/eu`, `staging/us`.
- **List:** the entries in the order written.
- **Name:** the preset, then `[`, the entry's own matrix keys in sorted order as `k=v` joined by `,`, then `]`: `deploy[env=qa,region=eu]`. Only the matrix's keys appear, not the set run's `vars`. A value that is an `${ENV}` reference appears as written, never resolved: names are printed and recorded, and must not carry a secret.
- **Variables:** precedence, lowest first: plan values < preset `defaults` < the set run's `vars` < matrix values < `--var`. `--var` still applies to every run and still wins.
- A set run without a matrix expands to itself, named after its preset: existing sets, records and output do not change.

`ResolvedSet` keeps the configured `Set` (for `bam set show`), and `config.Set`/`Sets` callers that run a set use `Expand` per set run.

### 3.3 Checks when the configuration loads

Added to, and in one case replacing, the run sets design §3.3. Each is a config error (exit 3) naming the set, the stage and the file:

- A matrix has at least one entry. A product key has at least one value.
- Matrix values are single scalars; `${ENV}` references are allowed.
- A matrix has the map form or the list form, nothing else.
- **Replaces** "a preset appears at most once per stage": every run *name* in a stage is unique after expansion. A preset may appear twice in a stage when its matrices give different names; two runs that expand to the same name (`[a, a]`, or two matrix entries with the same values) are an error naming the run.
- A stage expands to at most 50 runs: `set "regions" stage 1 expands to 64 runs; the limit is 50`, Try `split the matrix across stages, or list fewer variable sets`.

## 4. Running

### 4.1 `app`

- `SetRunPlan` gains `Name`. `PlanSet` iterates `Expand()` of each set run and resolves each expanded run with its merged `Vars` as the set vars; `planRun` is otherwise unchanged. Errors collected by `PlanSet` name the run: `stage 1 deploy[env=qa,region=eu]: …`.
- Orchestration is unchanged: a matrix stage is a stage with more runs, triggered one after another in order and watched in parallel. The stage succeeds only if every run succeeds.

### 4.2 Record and identity

- `RunRecord` gains `Name string \`json:"name,omitempty"\``. `RunSet` writes it for every run. A record written before this change has no name; reading it, a missing name is the run's `target`.
- `sameShape` (resume and rerun) compares run names instead of presets, so a matrix that changed since the record is a config error, as a changed stage is today.
- `SetEvent` gains `Run string`: the run's name, on every event that has a `Target`. `Target` stays the preset.
- Resume and `rerun --failed` need no other change: they act per recorded run, so only the failed `deploy[env=prod,region=us]` is restarted.

## 5. Output

### 5.1 Text

- The live block, the plain lines, `bam set status` and the failure summary show the run name where they showed the preset:

```
regions  regions-20261005T120000Z
stage 1/2
  ✓ deploy[env=qa,region=eu]       PROJ-DEP-41  Success  2m10s
  ✓ deploy[env=qa,region=us]       PROJ-DEP-42  Success  2m31s
  ● deploy[env=staging,region=eu]  PROJ-DEP-43  Running  ████░░ 60%
  ✗ deploy[env=staging,region=us]  PROJ-DEP-44  Failed   58s
stage 2/2
  · smoke[env=qa]                  pending
  · smoke[env=prod,region=us]      pending
```

- `bam set show` prints each set run as configured, with its matrix on one line (`matrix env=[qa staging] region=[eu us]`, or `matrix 2 variable sets`), then the expanded run names indented under it.
- `bam set list` counts runs after expansion.
- `bam set run --dry-run` lists every expanded run with its plan, branch and changed variables, secrets masked.

### 5.2 JSON

Fields are added; none is renamed or removed.

- Set events: `run` (the run name).
- `bam set status --json`: `name` on each run.
- `bam set show --json` and `set list --json`: each configured run gains `matrix` when it has one (`{"env":["qa","staging"]}` for a product, `[{"env":"qa"}]` for a list), and each stage gains `runs`, the expanded names.
- `bam set run --dry-run --json`: `name` on each run.

`docs/json.md` lists them.

## 6. Errors and exit codes

Unchanged from the run sets design §8. New config errors (exit 3) are those of §3.3.

## 7. Testing

Test first. No test sleeps or touches the network.

- **`config`**: both forms parse, and anything else is an error; product order (sorted keys, last fastest) and list order; names (sorted keys, `${ENV}` as written); vars precedence inside `Expand` (matrix over `vars`); every §3.3 check, including the 50-run limit and a duplicate name; a set without a matrix expands to itself.
- **`app`**: `PlanSet` resolves each expanded run with plan < defaults < vars < matrix < `--var`; a matrix stage triggers every run and fails if one fails; the record carries names; resume of an interrupted matrix stage watches and triggers per run; `rerun --failed` restarts only the failed matrix run; an old record without names resumes; a changed matrix is a config error on resume.
- **`view`**: run names in the live block, plain lines, status, summary, `set show` and dry run; JSON `run` and `name`.
- **`cli`** (harness): a matrix set runs, prints its names and exits by result; `set show --json` has `matrix` and `runs`.
- **e2e** (`//go:build e2e`): `TestSetSmoke` gains a matrix stage over the first `-set-plans` plan with two variable sets of a dummy variable, and checks two runs in the record.

## 8. Documentation

- `docs/configuration.md` "Sets (release trains)": the `matrix` key, both forms, naming, precedence and the limit.
- README Release trains: one matrix example.
- `docs/json.md`: the fields of §5.2.
- `make docs` regenerates `docs/cli/` if help text changes.
- The run sets design §12 points here for matrix expansion.

## 9. Acceptance

Against the local Bamboo:

1. A set with a two-by-two product over a plan's variables runs four builds in parallel, each with its variables (`bam build show` per key), and reports one result.
2. With one of them failing, `bam set rerun --failed` restarts only that one.
3. `bam set show` and `bam set run --dry-run` list the four names; a 60-run product is refused at load with exit 3.
4. `go test -race ./...`, `make lint`, `make check-fixtures`, `make docs` clean.

## 10. Out of scope

- `exclude:` or `include:` on a product.
- A cap on parallel runs below the 50-run limit.
- A matrix on `bam run` outside a set, or on `bam set run --var`.
- Batch files, plan statistics, and the run-sets panel in the terminal UI (the panel is the next spec).
