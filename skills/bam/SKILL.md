---
name: bam
description: "Drive Atlassian Bamboo from the terminal with bam: find plans, run a plan or preset with variables, watch a build, read why it failed, see the queue and agents, and cancel. Use when the user mentions Bamboo, a build key such as PROJ-BUILD-482, a plan key, a failing Bamboo build, or asks to run, watch or investigate a Bamboo build."
---

# bam: Bamboo from the terminal

`bam` talks to Bamboo Data Center over REST with the user's own token. Everything below
works without a terminal UI. Run `bam --help` or `bam <command> --help` for the full flags.

## Ground rules for an agent

1. **Start read-only.** Unless the user asked you to run, stop or rerun something, add
   `--read-only` (or run with `BAM_READ_ONLY=1`). bam then refuses every command that
   changes Bamboo, prints `read-only mode: refused to ...` on stderr and exits 2. Never
   work around a refusal: tell the user and let them decide to lift it.
2. **Always pass `--json` and `--no-tui`** when you read output. JSON field names are stable;
   text output is not. A bare `bam` opens an interactive UI, which hangs an agent.
3. **Read the exit code**, not the text:

   | Code | Meaning |
   | --- | --- |
   | 0 | success (for `run` without `--watch`: queued) |
   | 1 | a watched build did not succeed |
   | 2 | usage error, a bad argument, or a read-only refusal |
   | 3 | configuration problem |
   | 4 | token missing, rejected or expired |
   | 5 | Bamboo or the network failed |
   | 6 | `--timeout` ran out while the build was still running |
   | 130 | interrupted |

4. **Confirm before writing.** `run`, `cancel`, `build rerun`, `build tree --rerun-failed`,
   `set run`, `set resume`, `set rerun`, `agent disable` and `agent enable` change Bamboo, and a run can deploy. Say what you are about to run, with its
   variables, and wait for a yes. Use `--dry-run` first: it resolves and validates and
   triggers nothing.
5. **Never print or ask for a token.** bam keeps it in the keychain or an env var. Secret
   variables appear as `********`; do not try to recover them.
6. **Interrupting `watch` does not stop the build.** Only `bam cancel` stops one.

## Read: what is going on

```bash
bam status --json --no-tui                    # builds the user started in the last day
bam top --json --no-tui                       # what needs attention: agents, queue, pinned pipelines
bam queue --json --no-tui                     # builds waiting for an agent
bam plan list PROJ --json --no-tui            # plans of a project
bam plan show PROJ-BUILD --json --no-tui      # one plan, with its last build
bam plan vars PROJ-BUILD --json --no-tui      # variables the plan declares
bam build list PROJ-BUILD --json --no-tui     # recent builds of a plan
bam build show PROJ-BUILD-482 --json --no-tui # one build: stages, jobs, failed tests
bam tree PROJ-BUILD-482 --json --no-tui       # a build and every build it triggered
bam preset list --json --no-tui               # the user's named runs (presets)
bam agent list --json --no-tui                # build agents
```

A build key is `PLAN-NUMBER` (`PROJ-BUILD-482`). `--last` means the last build bam triggered
from this repository. A preset name works wherever a plan is accepted.

## Diagnose a failed build

```bash
bam investigate PROJ-BUILD-482 --no-tui       # error lines and the likely cause
bam logs PROJ-BUILD-482 --failed -n 80 --no-tui   # failed jobs only, last 80 lines each
bam logs PROJ-BUILD-482 --job JOB1 --no-tui   # one job
```

For a queued build, `bam build show KEY --json` carries `why`: `no_agent`, `busy`, `next`
or `ahead`. Start from the first error line of the failing job, not the last.

## Write: run, watch, cancel (ask first)

```bash
bam run deploy-qa --dry-run                                  # check, trigger nothing
bam run PROJ-BUILD --branch develop --var env=qa --watch     # run, follow, exit 1 if it fails
bam run deploy-qa --from last --var env=staging              # reuse the last build's variables
bam watch PROJ-BUILD-483 --timeout 30m --json                # follow a build; NDJSON events
bam build rerun PROJ-BUILD-482                               # rerun the failed jobs of a build
bam cancel PROJ-BUILD-483                                    # stop a queued or running build
```

- Only `--var` names the plan or preset declares are certain to apply; bam warns on an
  undeclared one.
- Put long-running work behind `--watch --timeout`, or run without `--watch` and poll
  `bam build show KEY --json` for `state`: `queued`, `running`, `success`, `failed`,
  `stopped`, `skipped`, `not_built`, `unknown`.
- `bam set run NAME` runs a release train of presets in stages. It deploys several things
  in order: describe the stages (`bam set show NAME`) and get an explicit yes.

## When something is wrong

- Exit 4: tell the user to run `bam login` themselves. Do not look for a token.
- Exit 3 or "no server": `bam doctor` explains what is missing.
- A refusal that starts `read-only mode`: that is the switch working. Report it.

The same facts, with every flag, are in the bam documentation under `docs/`.
