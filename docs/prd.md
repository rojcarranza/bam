# Bam CLI - Product Requirements

**Status:** Approved for MVP (v0.1) | **Date:** 2026-09-12
**Design spec:** [`docs/superpowers/specs/2026-09-12-bam-cli-mvp-design.md`](superpowers/specs/2026-09-12-bam-cli-mvp-design.md)

All hostnames, project keys, plan keys and user names in this repository are placeholders. This repository is public.

## 1. Vision

Bam is a terminal remote control for Atlassian Bamboo: trigger, watch and diagnose builds without the browser, and get the Bamboo link the moment the browser is needed.

Bam is a native developer tool, not a REST API wrapper. It is organised around what a user intends ("run my pipeline", "why did it fail"), not around Bamboo's endpoints.

Bam has two faces over one core. Typing `bam` alone opens a terminal UI (a k9s-style plans table with a plan screen for each pipeline) to navigate servers, projects, plans, builds, stages and logs, and to run presets with a keystroke. Typing `bam <command>` gives scriptable commands for speed, scripts and CI. Both call the same use cases, so they cannot disagree.

## 2. Problem

Running Bamboo plans through the web UI has four costs:

1. **Click ops.** Every build starts with navigation, not with intent.
2. **Manual variable entry.** The same plan variables are retyped for every custom build, with no memory of what was used last time and no validation of what was typed.
3. **No parallelism.** Testing several plans, or one plan across several variable sets, means several browser tabs and manual tracking.
4. **Slow failure triage.** Finding out why a build failed means opening the build, then the stage, then the job, then scrolling the log.

## 3. Users

1. **Developers** who trigger a plan on their branch and need to know quickly whether it passed and, if not, why.
2. **DevOps and platform engineers** who run infrastructure plans with variable sets and exercise many pipelines at once.
3. **Scripts and CI jobs.** They consume `--json` output and exit codes. They are treated as a user with a contract: output they depend on does not break within a major version.

Adoption path: the author first, on a personal Bamboo and a work Bamboo; then work teammates who clone a repository that carries `.bam.yaml`; then the public, as an open-source tool for any Bamboo Data Center user.

## 4. Value over the browser

- **Intent first.** `bam run build` instead of finding the plan and opening the run dialog.
- **Variables are remembered and validated.** Presets in `.bam.yaml` hold a plan's variables, allowed values and required names. `--from last` reuses the previous build's values.
- **Presets are generated.** `bam preset add` reads the plan's declared variables from Bamboo and writes the preset, so nobody transcribes variables by hand.
- **Failure in one command.** `bam logs --last --failed` prints only the failed jobs' logs.
- **Always one step from Bamboo.** Every entity carries its Bamboo URL; `bam open` opens it.
- **Safe in CI.** `bam watch` exits non-zero when the build fails, and tool errors have their own exit codes.

## 5. Product principles

- **Developer first.** Optimise for people who live in the terminal.
- **CLI-native.** Standard flags, stdout for data, stderr for messages, pipes and pagers behave as in `git` and `gh`.
- **Scriptable.** Every command works without a prompt. The only prompt in the command set is the hidden token prompt in `bam login`, and `--with-token` replaces it with stdin. The terminal UI is interactive by nature, and it opens only when `bam` is run alone on a terminal.
- **Discoverable.** `bam --help`, `bam build --help` and shell completion teach the tool. Commands are grouped by the Bamboo noun they act on.
- **Fast.** No request that the output does not need. No output that the user did not ask for.
- **Predictable.** One command returns one output shape. Exit codes mean the same thing everywhere.
- **Progressive disclosure.** The important line first; detail behind a flag or a follow-up command that the output prints.
- **Bamboo stays the source of truth.** Bam never edits plans and never treats its own cache as authoritative. It always gives the Bamboo URL.

## 6. Success metrics

- A fresh clone of a repository that carries `.bam.yaml` reaches its first triggered and watched build in under two minutes: `bam login`, then `bam run`.
- Across two weeks of daily use against a personal and a work Bamboo, the Bamboo web UI is opened only through `bam open`.
- One work teammate completes the README quickstart without help.
- No breaking change to `--json` fields or exit codes within a major version, enforced by golden tests.

## 7. Scope

### MVP (v0.1)

- Server registry, `login` / `logout` / `whoami`, `doctor` with the capability probe.
- Browse: projects, plans, plan branches, plan variables, builds, build detail.
- Run presets (targets) with branch, default variables, allowed values, required variables, environment references, watch and timeout; in the project file and in the machine file.
- Preset generation: `bam init` and `bam preset add` read declared plan variables from Bamboo and write presets.
- Run: `--var`, `--from`, `--branch`, `--watch`, `--timeout`, `--dry-run`.
- Watch with live stage and job state; logs with `--failed`, `--job`, `--follow`, `--tail`; cancel.
- `open` and `url` for every entity.
- `--json` on every read command, NDJSON for watch, stable exit codes, colors with glyphs, non-TTY output.
- Bamboo Data Center 9.x and later, personal access token authentication only.
- Bare `bam` on a terminal is reserved for the terminal UI. Until v0.2 it prints help with a note that interactive mode is coming.

### Next (v0.2): terminal UI

- `bam` with no arguments on a terminal opens on a k9s-style table of the configured projects' plans (v0.3); behind it, a plan screen per plan: its full name on top, its builds, the stages and jobs of the selected build, and the plans that build triggered; logs and the run form open from there.
- From the UI: browse, run a preset or plan with a variable form prefilled from the preset or a previous build, switch branch, watch live, read failed logs, cancel, open the Bamboo URL, copy the URL.
- It is a view over the same use cases as the commands. It adds no behaviour the commands lack.

### Then (v0.3)

- Duration estimate and progress bar from Bamboo's own progress endpoint, in both the commands and the UI.
- `run --edit`, and `run --revision` to build a chosen commit (Bamboo's REST queue takes `customRevision`; confirmed on Bamboo 12.1.8).
- `bam preset sync` to refresh a preset when the plan's variables change.

### Future

- **Local duration estimator:** a median of recent builds, for servers whose REST API does not report progress.
- **Run sets:** the core is built: named sets of presets in ordered stages, `bam set run`, `status` and `resume`, set-level results (see the [run sets core design](superpowers/specs/2026-09-29-bam-run-sets-core-design.md)). Still to come: matrix expansion, batch files, `rerun --failed`, plan statistics, and a run-sets panel in the UI.
- **Kubernetes:** not planned. Deploy pipelines already wait for the rollout (`helm --wait`, `kubectl rollout status`), and reading clusters would need `kubectl` access on every machine; see the [rejected rollout status design](superpowers/specs/2026-10-07-bam-rollout-status-design.md).

### Explicitly out of scope

- Replacing the Bamboo web UI, or exposing every REST endpoint.
- Bamboo administration, plan creation or plan editing. Bamboo Specs stay in each project's own repository.
- Bamboo deployment projects (environments and releases).
- `run --verbose` (verbose build logs). Bamboo's REST build queue has no verbose-logs parameter; only the deployment queue has `verboseLogging` (confirmed on Bamboo 12.1.8), and deployments are out of scope.
- Running builds locally, or running Terraform, Ansible, Helm or kubectl directly.
- Bamboo Server, and Bamboo Data Center before 9.x.
- HTTP basic authentication.
- A second CI provider.
- Storing a credential anywhere except the OS keychain, a named environment variable, or a 0600 credentials file.

## 8. Schedule

Phases are ordered by value, not dated. v0.1 ships when every item in the MVP list works against the personal Bamboo and the success criteria in the design spec's section 13 pass.

## 9. Risks and open questions

1. **Work Bamboo token policy.** Creating a personal access token on the work instance must be permitted, and single sign-on must not block token-based REST access. If either fails, bam works against the personal Bamboo only until resolved.
2. **Bamboo REST gaps by version.** Plan variable listing, build variable read-back, log retrieval, failed-test detail and build stop vary by version. The capability probe and fallbacks absorb this; fixture recording against the personal server confirms response shapes before command work depends on them.
3. **Network reachability** of the work server from a laptop, including VPN.
4. **Name collision.** Verify on each machine that `bam` does not shadow an existing binary or alias.
