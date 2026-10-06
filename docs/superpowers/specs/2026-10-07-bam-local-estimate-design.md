# bam: a local duration estimate when Bamboo has none

**Status:** Draft | **Date:** 2026-10-07
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future" (local duration estimator)
**Builds on:** [`2026-09-20-bam-v0.3-estimator-design.md`](2026-09-20-bam-v0.3-estimator-design.md) (§2 rejected alternative, §5 progress on every poll, §8 JSON)

All hosts, keys, user names and paths are placeholders.

## 1. Problem

The progress bar comes from Bamboo's `result/status` endpoint. A server without it — older versions, or one where the endpoint is blocked — shows no bar, no percentage and no remaining time: a running build reads only as "running for 4m".

The estimator design (§2) rejected a local estimate for v0.3 and said a server lacking the endpoint would make it a real proposal again, with its own spec. This is that spec.

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| When | Only when Bamboo reports progress as unsupported | Bamboo's own number always wins; the two never disagree on screen |
| Not when | Bamboo answers with no valid estimate (a new plan) | Bamboo has no history then, and neither does bam |
| Sample | The last 10 successful builds of the same plan branch | Recent enough to reflect the plan today; successes only, as failures often stop early |
| Statistic | Their median duration | One slow outlier does not move it |
| Minimum | 3 successful builds | Fewer is a guess, and no bar is better than a wrong one |
| Cache | In memory, per plan branch, for the life of the process | One history read per plan; no staleness rules to keep (the reason §2 rejected a disk cache) |
| Display | The same as Bamboo's; JSON says which source | The number only appears when there is no other |

## 3. The estimate

For a running build `b` of plan branch `P`, when `BuildProgress` returns an error wrapping `errs.ErrUnsupported`:

1. **Median.** `ListBuilds(P, State: success, Limit: 10)`; the median of their durations (the lower middle value for an even count, a real build's duration, as `bam plan stats` computes it). Fewer than 3 successful builds: no estimate.
2. **Elapsed.** `now − b.StartedAt`, with the service's clock. A build without a start time (queued) gets no estimate.
3. **Progress.** `Average = median`, `Elapsed`, `Remaining = max(0, median − elapsed)`, `Percent = min(1, elapsed / median)`, `Valid = true`, `Source = "history"`. `Stage` is the stage that is running in `b`, when one is.

A build running past the median reads 100% with nothing remaining, as Bamboo's does.

Bamboo's estimate keeps `Source = "bamboo"`. A zero `Progress` has no source.

## 4. Architecture

### 4.1 `provider`

`Progress` gains `Source string // "bamboo" or "history"; empty without an estimate`. `provider/bamboo` sets `"bamboo"` on a valid answer.

### 4.2 `app`

- `Service.Progress(ctx context.Context, b provider.Build) (provider.Progress, error)` — it now takes the build, not its key, because the fallback needs its plan and start time. It asks Bamboo first; on `errs.ErrUnsupported` it returns the history estimate (§3), or a zero `Progress`; other errors are returned as today. Callers: `cli` (`build show`, `build list`, the run and watch loops through `Watch`) and the terminal UI's detail load.
- `Watch` keeps its loop-local "unsupported" flag. Once set, each poll uses the history estimate instead of calling Bamboo again; the median is read once per watch.
- `Service` gains an in-memory cache `planKey → median (or "none")`, guarded by a mutex, so one process reads a plan's history at most once.
- `app` still never prints.

### 4.3 `view`

`ProgressDoc` gains `Source string \`json:"source,omitempty"\``. Text output is unchanged: the same bar, percentage and remaining time.

## 5. JSON

`progress` objects, wherever they appear (`build show`, `build list`, watch and set events), gain `source`: `"bamboo"` or `"history"`. Fields are added only. `docs/json.md` documents it.

## 6. Testing

Test first. No test sleeps or touches the network; the clock is the fake clock.

- **`app`**:
  - With Bamboo's progress supported, its estimate is used and history is never read.
  - With it unsupported: the median of the successful builds only; elapsed, remaining and percent from the fake clock; past the median → 100% and 0 remaining; fewer than 3 successful builds → zero; a build without a start time → zero.
  - One history read per plan per process across several `Progress` calls; one per `Watch`.
  - `Source` is `"history"` and `"bamboo"` as appropriate.
- **`provider/bamboo`**: a valid answer carries `Source: "bamboo"`.
- **`view`**: `source` in the JSON progress document, omitted on a zero progress.
- **`cli`** (harness): `build show --json` on a running build against a fake that reports progress unsupported has a `progress` with `"source":"history"`.

## 7. Documentation

- `docs/json.md`: `source` on progress.
- The estimator design §2 points here.

## 8. Out of scope

- Per-stage estimates.
- A disk cache of medians.
- History when Bamboo answers with no valid estimate.
- A setting to prefer history over Bamboo.
