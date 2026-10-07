# bam: Kubernetes rollout status after a deploy

**Status:** Rejected | **Date:** 2026-10-07
**Product requirements:** [`docs/prd.md`](../../prd.md) §7 "Future" (Kubernetes: read-only rollout status per environment)
**Builds on:** [`2026-09-12-bam-cli-mvp-design.md`](2026-09-12-bam-cli-mvp-design.md) (§5 exit codes, §7 JSON), [`2026-09-29-bam-run-sets-core-design.md`](2026-09-29-bam-run-sets-core-design.md) (set runs), [`2026-10-05-bam-set-rerun-failed-design.md`](2026-10-05-bam-set-rerun-failed-design.md)

All hosts, keys, contexts, namespaces, user names and paths are placeholders.

## Decision: not built

The owner decided not to build this design. It stays here as the record of what was considered and why it was set aside.

- **Pipelines already wait.** Teams that deploy to Kubernetes from Bamboo usually make the deploy job wait: `helm upgrade --wait` or `--atomic`, a `kubectl rollout status` step, or an Argo CD sync that waits for healthy. Their green build already means the deploy landed, and this feature would check it a second time.
- **It narrows who can use bam.** Everything else in bam needs a Bamboo URL and a token. This needs `kubectl`, a kubeconfig and cluster access on each user's machine; many people who trigger deploys have none.
- **It serves one platform.** Teams on ECS, virtual machines, Nomad or serverless gain nothing, while bam would carry a Kubernetes package, its states and its JSON fields for good (JSON fields are never removed).
- **It is outside bam's scope.** bam is a remote control for Bamboo (`docs/prd.md`); reading a second system is another product.

If users ask for "wait until the deploy has landed", the broader answer is a generic post-build check: a command the preset names (`curl -f …/health`, `kubectl rollout status …`), run after a green build, its exit code deciding the run. Because a committed `.bam.yaml` must never run commands on whoever runs bam, that setting could only live in the machine config. It gets its own design if the need appears.

The rest of this document is the design as it was approved before the decision.

## 1. Problem

A deploy preset's Bamboo build applies manifests to a cluster (`kubectl apply`, `helm upgrade`) and goes green as soon as the apply returns. The pods have not rolled out yet. A green build says "the change was sent", not "the change landed": a bad image, a crash loop or a failed readiness probe shows up minutes later, in the cluster, where bam does not look. In a train, the next stage starts against an environment that is still half old, half new.

The question bam should answer is: **did my deploy land?**

## 2. Decisions

| Question | Decision | Why |
| --- | --- | --- |
| Purpose | Wait for a preset's Deployments to roll out after its build succeeds | The owner's choice: "did my deploy land", not an environment overview |
| Deploy flow | The build applies the change itself | The owner's flow; when the build ends the new Deployment generation already exists, so bam only waits for it to be ready |
| Cluster access | Run `kubectl`, read-only | Uses the owner's kubeconfig and auth plugins (OIDC, cloud exec) unchanged; no new Go dependency. `client-go` was rejected: a very large dependency tree outside spec §12 that would still run exec plugins |
| Ready means | The rule of `kubectl rollout status` | Users already trust it; one definition, no bam-specific notion of healthy |
| Where it waits | `bam run --watch`, `bam set run` / `resume` / `rerun`, and a standalone `bam rollout` | A train is where "did it land" matters most; the standalone command answers it again later |
| Configuration | An optional `rollout:` block on a preset, filled from the run's variables | The variables already pick the environment (`env: qa`) |
| Placeholder syntax | `{name}` | `${NAME}` already means an environment variable in preset defaults |
| Writes | None | Read-only: bam never restarts, scales or undoes a rollout |

## 3. Configuration

```yaml
targets:
  deploy:
    plan: PROJ-DEPLOY
    defaults: { env: qa }
    options: { env: [dev, qa, staging] }
    rollout:
      context: lab-{env}          # kubeconfig context; required
      namespace: app-{env}        # required
      deployments: [api, worker]  # required, at least one
      timeout: 10m                # how long to wait for the rollout; default 10m
```

- `config.Target` gains `Rollout *Rollout`; `Rollout{Context, Namespace string; Deployments []string; Timeout Duration}`.
- Load-time validation (config error, exit 3): `context` and `namespace` non-empty, `deployments` non-empty, no empty deployment name, `timeout` not negative. A `{` without a matching `}`, or `{}`, is a config error.
- `{name}` is replaced with the value of the run's resolved variable `name` (`app.VarSet`, after all precedence rules). `name` uses the characters a Bamboo variable name may use: `[A-Za-z0-9_.-]+`. There is no escape; no Kubernetes name contains `{`.
- Resolution errors are usage errors (exit 2), raised with the variable checks, before anything triggers:
  - a placeholder names a variable that has no value: `rollout.context uses {env}, which has no value`, Try `--var env=...`;
  - a placeholder names a secret variable (a name matching `password|secret|passphrase|sshkey`, or a value resolved from `${ENV}`): rejected, so a secret never reaches a command line or the output;
  - after substitution, `namespace` or a deployment name is not a DNS-1123 label (`[a-z0-9]([-a-z0-9]*[a-z0-9])?`, at most 63 characters).
- The context is passed as `--context=VALUE` (one argument), so a value starting with `-` cannot become a flag.
- `bam target show` prints the block; `bam target sync` leaves it untouched (comment-preserving edits keep it); `bam init` / `target add` do not generate it.
- `docs/configuration.md` documents the block.

## 4. Reading the cluster: package `internal/kube`

The only package that speaks to Kubernetes. Imports `errs` only.

```go
// Runner runs a command and returns its stdout; injected (cli.Env) so tests never exec.
type Runner func(ctx context.Context, name string, args ...string) (stdout []byte, stderr []byte, err error)

type Client struct{ Run Runner; Kubectl string /* "kubectl" */ }

// Target is one resolved rollout: no placeholders left.
type Target struct{ Context, Namespace string; Deployments []string }

type Deployment struct {
    Name                                  string
    Desired, Updated, Ready, Available    int
    Old                                   int    // replicas not yet updated (status.replicas − updated)
    State                                 State  // progressing | complete | failed
    Reason                                string // failed: the Progressing condition's reason and message; "paused" for a paused Deployment
}

type State string // "progressing", "complete", "failed"

func (c *Client) Status(ctx context.Context, t Target) ([]Deployment, error)
```

`Status` runs one command per poll:

```
kubectl --context=CTX --namespace=NS --request-timeout=20s get deployment NAME... -o json
```

For one name kubectl prints a Deployment; for several, a `List`. Both are decoded into the same minimal DTO (`metadata.name`, `metadata.generation`, `spec.replicas` (default 1), `status.observedGeneration`, `status.replicas`, `status.updatedReplicas`, `status.readyReplicas`, `status.availableReplicas`, `status.conditions`). The result keeps the configured order.

A Deployment's state, as `kubectl rollout status` decides it:

1. **failed** — the `Progressing` condition has reason `ProgressDeadlineExceeded`.
2. **progressing** — `observedGeneration < generation`, or `updated < desired`, or `status.replicas > updated` (old replicas remain), or `available < updated`.
3. **complete** — otherwise.

A paused Deployment (`spec.paused`) is progressing with reason `paused`; it is not an error, and the wait times out if nobody resumes it.

kubectl failures map to `errs` kinds:

| Condition | Kind (exit) | Try |
| --- | --- | --- |
| `kubectl` not found on PATH | config (3) | install kubectl, or check PATH |
| stderr says the context does not exist | config (3) | `kubectl config get-contexts` |
| stderr has `NotFound` for a deployment | config (3) | check `rollout.deployments` and `namespace` |
| stderr has `Unauthorized` or `Forbidden` | auth (4) | refresh cluster credentials |
| anything else, or output that does not decode | bamboo/network (5) | — |

`errs` gains no new kind; KindBamboo already means "the remote side or the network", exit 5. The Why carries kubectl's first stderr line. `--debug` prints the kubectl command line (it holds no secrets, §3).

## 5. Waiting: `app`

- `app` imports `kube`. `Service` gains `Kube Rollouts`, an interface `Status(ctx, kube.Target) ([]kube.Deployment, error)` that `*kube.Client` satisfies; `cli` wires it. A nil `Kube` with a preset that has a rollout is a config error naming kubectl.
- `app.ResolveRollout(t *config.Target, vs VarSet) (*kube.Target, error)` applies §3. `ResolveVars` callers that trigger (`run`, set planning) call it so its errors come before any trigger. A preset without `rollout` returns nil.
- `Service.WaitRollout(ctx, t kube.Target, timeout time.Duration) <-chan RolloutEvent` polls `Status` every 2s on the service clock:
  - emits `RolloutUpdate` on the first poll and whenever any Deployment's counts or state change;
  - ends with one `RolloutDone` with state `success` (all complete), `failed` (any failed; the others are not waited for), or `timeout` (the timeout passed first);
  - a `Status` error of kind config or auth ends the wait at once with `RolloutError` (retrying cannot fix it); any other error is retried, and three in a row end the wait with `RolloutError` carrying the last error;
  - ending ctx stops watching; nothing in the cluster changes.
- `RolloutEvent{Type, Time, Target kube.Target, Deployments []kube.Deployment, State RolloutState, Err error}`; `RolloutState` is `success | failed | timeout` (and empty while progressing).
- `app` still never prints.

## 6. Where it waits

### 6.1 `bam run --watch`

After the build's `done` event:

- build not successful: no rollout; exit as today.
- build successful and the preset has a rollout, without `--no-rollout`: `WaitRollout`, rendered live (TTY) or line by line:
  ```
  ✓ PROJ-DEPLOY-45 successful in 3m12s
  rollout lab-qa/app-qa
    api      2/3 ready  progressing
    worker   1/1 ready  complete
  ✓ rolled out in 48s
  ```
- Exit: `success` → 0; `failed` → 1 (`rollout failed: api: ProgressDeadlineExceeded …`); `timeout` → 6 (`rollout timed out after 10m`, Try `bam rollout deploy --var env=qa`); error → its kind.
- The preset `timeout` (and `--timeout`) bounds the build watch only; `rollout.timeout` bounds the rollout.
- Ctrl-C during the rollout prints `still rolling out: bam rollout <preset> --wait …` and exits 130.
- `--no-rollout` skips the wait. `bam run` without watching never waits.
- `bam watch KEY` does not wait: it has a build, not a preset and its variables.

### 6.2 Sets

- `SetRunPlan` gains `Rollout *kube.Target` (resolved per run, so each matrix run checks its own environment) and `RolloutTimeout`.
- `watchRun`: after a successful build, it waits for the rollout and forwards each `RolloutEvent` as a `SetEvent{Type: SetRunRollout, Rollout: &ev}`. The run's state is the build's state if not successful, otherwise `success` / `failed` / `timeout` from the rollout. The stage — and the next stage — waits for it, which is the point.
- `RunRecord` gains `Rollout *RolloutRecord \`json:"rollout,omitempty"\``: `{context, namespace, deployments, state}`, written with the run's result, for `bam set status`. `resume` and `rerun` resolve the rollout again from the configuration and the `--var` values they are given, as they already do for every other part of a run.
- An interrupted rollout leaves the run unfinished in the record (no state), even though its build succeeded.
- `bam set resume`: such a run watches its build (already finished) and waits for the rollout again.
- `bam set rerun --failed`: a run whose build succeeded and whose rollout failed or timed out is not restarted in Bamboo (a successful build cannot be); its rollout is checked again. Failed builds are restarted as today, then their rollout waits.
- `bam set run --no-rollout` (and on resume/rerun) skips every rollout; such runs record no rollout.
- `bam set status` shows a run whose build succeeded but whose rollout did not with its recorded state, not the live build's, and says `rolled out` after a successful run's state.
- The terminal UI's sets screen shows `rolling out` as a run's state during the wait and the final rollout state after; no other UI change.

### 6.3 `bam rollout`

```
bam rollout <preset> [--var k=v]... [--from N|last] [--wait] [--timeout D] [--json]
```

- Resolves the preset's variables exactly as `bam run --dry-run` does (it needs Bamboo for plan values), then the rollout target. A preset without `rollout` is a usage error.
- Without `--wait`: one `Status`, printed as the table in §6.1. Exit 0 when every Deployment is complete, 1 when any is progressing or failed.
- With `--wait`: `WaitRollout`, exits as in §6.1. `--timeout` overrides `rollout.timeout`.
- Completion offers presets that have a rollout.

## 7. JSON

Fields are added only.

- `run --watch --json`: after the build's `done` event, a preset with a rollout continues the stream with
  ```
  {"type":"rollout","time":"...","context":"lab-qa","namespace":"app-qa","deployments":[{"name":"api","desired":3,"updated":3,"ready":2,"available":2,"state":"progressing"}]}
  {"type":"rollout_done","time":"...","context":"lab-qa","namespace":"app-qa","state":"success","deployments":[...]}
  ```
  The stream now ends with `rollout_done` for such presets; `docs/json.md` says so. An error is not written to the stream, as for watch today: the stream stops and the error goes to stderr.
- Set NDJSON: `{"type":"run_rollout",...,"target":"deploy","run":"deploy","rollout":{<the rollout event>}}`; `run_done` gains `"rollout":{"state":...}` when the run had one.
- `bam set status --json`: each run gains `rollout` (`RolloutRecord`).
- `bam rollout --json`: `{"context","namespace","state","deployments":[...]}`, where `state` is `complete`, `progressing` or `failed` (one-shot) or the `RolloutDone` state (`--wait`).

## 8. Architecture changes

- New package row in the CLAUDE.md layering table: `kube` — reads Deployment rollout state through `kubectl`; may import `errs`.
- `app` may also import `kube`; `view` may also import `kube`.
- `cli.Env` gains the `kube.Runner` (production: `exec.CommandContext`); `cli` builds the `kube.Client`.
- No new Go dependency.

## 9. Testing

Test first. No test runs kubectl, touches a cluster, or sleeps.

- **`kube`**: a fake `Runner` returning hand-written JSON fixtures in `internal/kube/testdata/` (placeholder names): single Deployment and `List` decoding; each state rule of §4 (generation lag, updated < desired, old replicas, available < updated, `ProgressDeadlineExceeded`, paused, `spec.replicas` absent); argument list exact (`--context=`, `--namespace=`, names in order); each stderr → kind row.
- **`config`**: block decoding, each load-time validation error, untouched by target sync edits.
- **`app`**: `ResolveRollout` substitution, missing variable, secret variable, invalid label; `WaitRollout` with a fake `Rollouts` and the fake clock — update only on change, success, failed stops at the first failure, timeout, three errors in a row, one error then recovery.
- **sets**: next stage waits for a rollout; rollout failure fails the run and the set; record carries the rollout; resume waits again for an unfinished rollout; rerun re-checks a failed rollout without `RestartBuild`; matrix runs resolve their own targets; `--no-rollout`.
- **`view`**: text table and NDJSON lines (goldens).
- **`cli`** (harness, fake runner): `run --watch` exit codes 0/1/6 and `--no-rollout`; usage error before trigger for an unknown placeholder variable; `bam rollout` one-shot exit 0/1 and `--json`; kubectl missing → exit 3.

## 10. Documentation

- `docs/configuration.md`: the `rollout` block.
- `docs/json.md`: the new events and fields.
- `docs/cli/` regenerated (`make docs`).
- `docs/prd.md`: the Kubernetes item points here.
- `CHANGELOG.md` `[Unreleased]`: one line.
- CLAUDE.md: the layering table rows (§8).

## 11. Out of scope

- GitOps flows: waiting for an expected image tag before readiness.
- StatefulSets, DaemonSets, Jobs, Argo Rollouts.
- An environment overview command or a terminal UI panel beyond the sets screen state.
- Any write to the cluster: restart, undo, scale.
- Waiting after `bam watch KEY` or a run triggered from the terminal UI's run form.
- A kubeconfig path per preset (kubectl's own `KUBECONFIG` applies).
