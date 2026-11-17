# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`bam` is a Go CLI that drives Atlassian Bamboo Data Center 9.x+ over REST with a personal access token: browse plans and builds, run presets ("targets") with variables, watch, read failed logs, cancel, and open Bamboo URLs. Module `github.com/rojcarranza/bam`, binary `bam` from `cmd/bam`.

Source of truth, read before changing behaviour:

- `docs/prd.md` - scope, what is out of scope, and the roadmap
- `docs/superpowers/specs/2026-09-12-bam-cli-mvp-design.md` - the v0.1 design: commands, config, exit codes, JSON contract, architecture
- `SETUP.md` - toolchain install for macOS and Linux
- `VISION.md` - what bam accepts and resists; judge a change against it before the PRD's older scope lists

## Commands

```bash
make build            # bin/bam, version from git describe via -ldflags
make install          # same binary into $(go env GOPATH)/bin, so `bam` works anywhere
make test             # go test ./...
make lint             # golangci-lint run (v2 config in .golangci.yml)
make check-fixtures   # fixture host guard (TestFixtureGuard)
make check-demos      # README GIFs match their tapes, text outputs and size cap; no local host or name in the recorded text
make demos-seed / demos / demo NAME=x   # rebuild the lab history and re-record README GIFs (docs/demos/README.md; needs Docker and a lab Bamboo)
make docs             # regenerate docs/cli/ from cobra; the no-mistakes gate fails if it is stale
make release VERSION=x.y.z   # tag and publish a GitHub release (owner's go-ahead only; see .claude/skills/release/SKILL.md)
make record ARGS='-target provision'   # record scrubbed fixtures from a personal Bamboo
make e2e ARGS='-target smoke'          # //go:build e2e suite against a real Bamboo
make gate                              # set up the no-mistakes gate in this working copy; use it instead of a plain `no-mistakes init`
git push no-mistakes <branch>          # the only way to push: no-mistakes gate (.no-mistakes.yaml), then PR

go test ./internal/app/ -run TestWatchEventsAndBackoff -v   # one test
go test -race ./...                                         # before finishing a task
go test ./internal/view/ -update                            # rewrite golden files, then review them
```

In non-interactive shells Go may not be on PATH; prefix commands with `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

Makefile recipe lines must start with a TAB (macOS ships GNU make 3.81; no `.RECIPEPREFIX`).

## Architecture

Layered packages under `internal/`; each imports only the module packages its rule allows. The allowed imports are defined by the depguard rules in `.golangci.yml`, so `make lint` fails when a shipped file imports a package its rule does not allow. Test files are exempt; `cli` and the packages without a rule (`provider/fake`, `provider/readonly`, `skills`, `view/tui`, `tools/`, `cmd/`) have no rule. This rule is load-bearing: it keeps a second CI provider to one new package and keeps the core usable by the terminal UI (`view/tui`).

| Package | Role |
| --- | --- |
| `atomicfile` | `Write`: temp file + rename, so readers and concurrent bam processes never see a half-written state, cache, config or token file |
| `usage` | the private tally of commands run, one file of command names and counts by day; `cli` writes it after each command and reads it for `bam usage`; it never sees arguments |
| `errs` | error `Kind` (usage/config/auth/bamboo/timeout) + What/Why/Try; sentinels `ErrNotFound`, `ErrUnsupported` |
| `config` | load/discover/merge the three config layers, targets, validation, secret guards, comment-preserving YAML edits |
| `provider` | neutral domain types, `State`, `Provider` interface; `provider/fake` for tests |
| `credential` | token lookup (auth_env → keychain → 0600 file), keyed by server origin |
| `provider/bamboo` | the only package that speaks Bamboo REST; transport, retry, DTO mapping, lazy capability cache, doctor probe |
| `app` | use cases (`Service`): resolve plan/build/branch, resolve variables, run, watch (polling), logs, cancel, generate presets |
| `view`, `view/style` | terminal tables, JSON/NDJSON docs, watch renderers, errors; `style` is the single definition of state colors and glyphs |
| `toolcfg` | server alias + token lookup for the dev tools (recorder, e2e), so they read bam's config instead of env vars |
| `cli` | cobra commands, `runtime` (lazy config, server selection, token lookup, `Connect`), exit-code mapping; the only place that calls `bamboo.New` |

Cross-cutting behaviour that spans several files:

- **Errors → exit codes.** Every layer returns `*errs.Error`; only `cli.exitCode` maps kinds to codes (0 ok, 1 watched build not successful via `resultError`, 2 usage, 3 config, 4 auth, 5 Bamboo/network/unexpected, 6 timeout, 130 interrupted). Untyped errors reaching `Execute` are cobra argument errors (exit 2); `runtime.wrap` classifies stray errors from commands.
- **`app` stays UI-ready.** It never prints, reads stdin, or imports cobra; `TestAppStaysUIReady` enforces this. Output happens in `view`; `app.Watch` emits `Event`s on a channel that both the text renderers and the terminal UI consume.
- **Server selection** (`config.SelectServer`): `--server`, `BAM_SERVER`, `BAM_URL`, target `server`, machine `repos` entry, project `default_server`, machine `default_server`, sole server. Repo-scoped settings beat the machine's global default.
- **Credentials are keyed by origin, never alias.** `BAM_TOKEN` applies only to the `BAM_URL` server. Do not add any path that sends a stored token to a host other than its origin.
- **Variable precedence** (`app.ResolveVars`): plan values < target `defaults` < `--from` build < `--var`. Then: unset `${ENV}` refs, `required`, `options` → usage errors before any trigger. Undeclared `--var` names warn, not fail (they may override Bamboo globals). Only changed variables are sent.
- **Secrets.** Names matching `password|secret|passphrase|sshkey`, values Bamboo returns as `********`, and `${ENV}`-resolved values print as `********` everywhere, including JSON and `--debug`. Trigger variables go in the form body, not the URL.
- **Capabilities.** Optional Bamboo features (plan variable path, build variable read-back, log path, failed tests, stop) are learned lazily and cached per origin in `$XDG_CACHE_HOME/bam/capabilities.json`; stale after 7 days or a server version change.
- **Interrupting `watch` never stops the build.** Only `bam build cancel` stops builds.
- **Clock, keychain, HTTP client, browser, pager and filesystem paths are injected** (`cli.Env`, `app.Clock`). Tests use `fake.Provider`, an instant fake clock, and `httptest` with fixtures; no test sleeps or touches the network.

## Testing conventions

- TDD: failing test, run it, implement, run it, commit.
- `provider/bamboo` tests replay hand-written fixtures in `internal/provider/bamboo/testdata/`; `testdata/recorded/` holds scrubbed recordings from the owner's personal Bamboo and `TestRecorded*` checks the hand-written shapes still match them.
- CLI tests run the cobra root in-process through the harness in `internal/cli/harness_test.go` (fake backend, temp home/repo, in-memory keyring).
- `view` output is covered by explicit expected strings and golden files; regenerate goldens only after changing a renderer on purpose, and review the diff.
- `--json` field names are a public contract (spec §7): add fields, never rename or remove.

## Repository rules

- **Public repository: placeholders only.** Code, tests, fixtures, docs and commit messages use `bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`. Never a real work hostname, project key, plan key or user name. `make check-fixtures` enforces the host part for `testdata/`.
- Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`).
- `CHANGELOG.md` follows Keep a Changelog and lists only what a release brings its users. Pull requests do not edit it; the release pull request writes the `[x.y.z]` section from the merged changes. To publish a release, follow `.claude/skills/release/SKILL.md`.
- **Branch names are `<type>/<short-description>`**: the Conventional Commits type of the work and a few kebab-case words, for example `feat/presets-file`, `fix/tree-search-past-window`, `docs/presets-guide`, `chore/release-v0.2.1`. Never name a branch after the tool that wrote it: no `claude/` prefix and no random suffix, even when a session or tool proposes one. If a session starts on such a branch, create a properly named branch from it before the first push.
- **No GitHub Actions.** Validation is the `no-mistakes` pipeline (`.no-mistakes.yaml`); `.githooks/pre-push` refuses pushes to any other remote once a clone runs `git config core.hooksPath .githooks` (setup in `SETUP.md`). Set the gate up with `make gate`, never a plain `no-mistakes init`: in worktrees of a bare clone, a plain init re-points the `no-mistakes` remote every other worktree pushes to. Dependabot (`.github/dependabot.yml`) updates Go modules.
- Work on a feature branch; never commit directly to `main`. Open a pull request against `main` once the `no-mistakes` gate has passed, and merge it only when the owner says "push it" or approves the merge. Tagging and releases still need the owner's go-ahead.
- **Every pull request links an issue. Never open a pull request without one.** No exceptions for small, docs-only or follow-up changes. Before starting the work, find the issue it belongs to or create one (`gh-axi issue create`, or plain `gh` only for a step `gh-axi` has no command for), written as a user story: the story ("As a bam user, I want …, so that …"), scope, and acceptance criteria. Use the label `enhancement` for features, `bug` for fixes and `documentation` for docs. The issue follows the public-repo placeholder rule. Commits that do part of the work end with `Refs #N`, and the PR body says `Closes #N` (one line per issue when a PR covers several), so GitHub links the issue in the PR's Development panel; `Refs #N` alone does not link it. When a PR delivers only part of an issue, create a sub-issue for that part and close the sub-issue instead. Before opening the PR, check that its body names the issue; if it does not, stop and add it. If a closed issue turns out unfixed, reopen it and say why instead of filing a duplicate.
- **Always address every PR comment**, as soon as it arrives and again before merging. That includes review comments, inline threads, conversation comments, and comments from bot reviewers such as Copilot, and also comments on PR bodies and titles. Read them all (`gh-axi pr view <n> --comments`, `gh-axi api repos/rojcarranza/bam/pulls/<n>/comments`). Check each one against the code. Fix the ones that are right, on the PR branch, with tests where they apply, and reply in the thread with what changed. When a comment is wrong or out of scope, reply with the reason instead of changing code. Resolve each thread once it is answered. Never ignore a comment because it is minor or from a bot. Merge only when no comment is left unanswered and no thread is left unresolved, and report to the owner which comments were fixed and which were declined.
- A new dependency needs a stated reason in the commit message; `go.mod` lists the ones already accepted.
