# bam: plan statistics (`bam plan stats`)

**Status:** Implemented | **Date:** 2026-10-06
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future", "Run sets" (plan statistics)
**Builds on:** [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) §2.2 (argument resolution), §7 (JSON contract)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

When a build fails, the first question is whether the plan is flaky or the change broke it. Today that means reading `bam build list` and counting by eye, then opening failed builds one by one to see which jobs failed.

`bam plan stats` answers it in one command for one plan: how often its recent builds succeeded, how long a good build takes, whether the latest builds are a run of successes or failures, and which jobs cause the failures.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Scope | One plan in depth | The owner's choice: where a plan fails, not which plan fails |
| Window | The last N builds, default 20, at most 100 | Recent enough to reflect the plan today; bounded cost |
| Success rate | Successful ÷ (successful + failed) | Stopped, not built and skipped are not verdicts on the code |
| Durations | Over successful builds only | Failed builds often stop early and would pull the median down |
| Failing jobs | One `GetBuild` per failed build, up to 4 at a time | The build list carries no stages or jobs; passed builds need none |
| Rejected | A cross-plan overview | Answers which plan, not where; later work |
| Rejected | A nested expand on the build list for jobs | Unrecorded on the lab server; one read per failed build is cheap and already works |

## 3. Command

```
bam plan stats <plan|preset> [-n|--last N] [--branch B] [--json]
```

- `<plan|preset>` and `--branch` resolve the plan branch exactly as `bam build list` does: a preset brings its branch; `--branch` overrides it.
- `--last N`: how many of the newest builds to read, default 20. Outside 1–100 is a usage error (exit 2), `--last takes 1 to 100, not 0`, checked before any configuration load or network call.
- Shell completion offers presets, as `bam build list` does.

## 4. What is counted

From `ListBuilds(planKey, Limit: N)`, newest first:

- **Finished builds** are those in a finished state. Queued and running builds are left out of every figure.
- **passed** = successful builds; **failed** = failed builds; **other** = stopped, not built and skipped builds.
- **Success rate** = passed ÷ (passed + failed). With neither, there is no rate.
- **Durations**, over passed builds only: median, p90 and the maximum with its build number. The median of an even count is the lower middle value; p90 is the value at index ⌈0.9·n⌉−1 of the sorted durations. Both are real build durations, never an average of two.
- **Streak**: the state of the newest finished build that is passed or failed, and how many consecutive finished builds before it share that state; builds counted in *other* are skipped over, not breaks.
- **Failing jobs**: for each failed build, `GetBuild` and its `FailedJobs()`, up to 4 reads at a time. Jobs are grouped by job name: how many failed builds each failed in, and the newest such build number. Sorted by count, then by newest failure. A failed build whose detail cannot be read counts once under `(unknown)` and adds a warning `could not read PROJ-DEPLOY-397: <what>`; the stats still print and the command still exits 0.
- **Range**: the oldest and newest build numbers read, and the time between the oldest build's start and the newest finished build's end.

## 5. Output

### 5.1 Text

```
PROJ-DEPLOY  last 20 builds (#381–#400, 6 days)
Success   80%  (16 ✓  4 ✗  1 stopped)   streak ✓ 3
Duration  median 4m10s  p90 6m02s  max 9m40s (#388)   of 16 successful
Failing jobs
  Integration tests   3 of 4 failures   last #397
  Deploy              1 of 4 failures   last #384
```

- The first line names the plan branch when there is one: `PROJ-DEPLOY  branch develop (PROJ-DEPLOY12)  last 20 builds …`.
- `1 stopped` lists each *other* state with its count, only when non-zero.
- With no passed builds: `Duration  no successful builds`.
- With no failed builds: `Failing jobs  none`.
- With no finished builds at all: one line, `no finished builds of PROJ-DEPLOY in the last 20`.
- Warnings print on stderr, as other commands print theirs.
- Glyphs and colours come from `view/style`.

### 5.2 JSON

A new document; fields are added later, never renamed or removed.

```json
{"plan_key":"PROJ-DEPLOY","branch":"","builds":20,"first":381,"last":400,
 "from":"2026-09-30T08:00:00Z","to":"2026-10-06T08:00:00Z",
 "passed":16,"failed":4,"other":{"stopped":1},"success_rate":0.8,
 "duration":{"median_ms":250000,"p90_ms":362000,"max_ms":580000,"max_build":388,"of":16},
 "streak":{"state":"success","count":3},
 "failing_jobs":[{"name":"Integration tests","failures":3,"last_build":397}],
 "warnings":[]}
```

- `success_rate` is `null` without passed or failed builds; `duration` is `null` without passed builds; `streak` is `null` without a passed or failed build; `from`/`to` are `null` without builds.
- `other` is an object of state name to count; `{}` when none.
- `docs/json.md` documents it.

## 6. Architecture

- **`app`**: `Service.PlanStats(ctx context.Context, ref PlanRef, n int) (PlanStats, error)` in a new `internal/app/stats.go`, with:

  ```go
  type PlanStats struct {
  	PlanKey, Branch string
  	Builds          int            // builds read
  	First, Last     int            // build numbers, 0 without builds
  	From, To        time.Time
  	Passed, Failed  int
  	Other           map[provider.State]int
  	Durations       *DurationStats // nil without passed builds
  	Streak          *Streak        // nil without a passed or failed build
  	FailingJobs     []JobFailures
  	Warnings        []string
  }
  type DurationStats struct{ Median, P90, Max time.Duration; MaxBuild, Of int }
  type Streak struct{ State provider.State; Count int }
  type JobFailures struct{ Name string; Failures, LastBuild int }
  func (p PlanStats) SuccessRate() (float64, bool)
  ```

  It reads with `P.ListBuilds` and `P.GetBuild`, computes, and never prints.
- **`view`**: `PlanStatsView(o Out, s app.PlanStats) error` and `PlanStatsJSON(s app.PlanStats) PlanStatsDoc`.
- **`cli`**: `newPlanStatsCmd` in `browse.go` under `bam plan`, resolving the plan as `build list` does.
- No provider change and no capability.

## 7. Errors and exit codes

| Case | Exit |
| --- | --- |
| `--last` outside 1–100 | 2 |
| Unknown plan, preset or branch | as `bam build list` |
| Listing builds fails | 5 (or auth 4) |
| A failed build's detail cannot be read | 0, a warning, counted under `(unknown)` |
| No finished builds | 0 |

## 8. Testing

Test first. No test sleeps or touches the network.

- **`app`** (fake provider): counts per state; queued and running left out; success rate with and without verdicts; median of odd and even counts; p90 on 1, 2, 10 and 11 durations; max and its build; streak across *other* builds; failing jobs grouped, counted and sorted, `last_build` the newest; an unreadable failed build under `(unknown)` with a warning; the range.
- **`view`**: explicit expected text for the full case, the branch header, no successful builds, no failures and no finished builds; JSON field names and nulls.
- **`cli`** (harness): text and `--json`; `--last 0` and `--last 101` exit 2 with no network; a preset's branch applies; `--branch` overrides it.

## 9. Documentation

- `make docs` adds `docs/cli/bam_plan_stats.md`.
- README everyday commands: `| is this plan flaky? | \`bam plan stats provision-lab\` |`.
- `docs/json.md`: the document of §5.2.

## 10. Out of scope

- A cross-plan overview (`bam plan stats` with no plan).
- Statistics in the terminal UI.
- Trends over time, per-stage durations, test-level statistics.
- Caching results between runs.
