# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`bam` is a Go CLI that drives Atlassian Bamboo Data Center 9.x+ over REST with a personal access token: browse plans and builds, run presets ("targets") with variables, watch, read failed logs, cancel, and open Bamboo URLs. Module `github.com/r0jjames/bam-cli`, binary `bam` from `cmd/bam`.

Source of truth, read before changing behaviour:

- `docs/prd.md` - scope, what is out of scope, roadmap (v0.1 CLI → v0.2 lazygit-style TUI → v0.3 estimator → run sets)
- `docs/superpowers/specs/2026-09-12-bam-cli-mvp-design.md` - the v0.1 design: commands, config, exit codes, JSON contract, architecture
- `docs/superpowers/plans/2026-09-12-bam-v0.1-part{1..4}-*.md` - the task-by-task implementation plan (29 tasks), executed in order
- `SETUP.md` - toolchain install for macOS and Linux

If code and spec disagree, the spec wins unless the change to the spec is deliberate and made in the same branch.

## Commands

```bash
make build            # bin/bam, version from git describe via -ldflags
make install          # same binary into $(go env GOPATH)/bin, so `bam` works anywhere
make test             # go test ./...
make lint             # golangci-lint run (v2 config in .golangci.yml)
make check-fixtures   # fixture host guard (TestFixtureGuard)
make docs             # regenerate docs/cli/ from cobra; CI fails if it is stale
make record ARGS='-target provision'   # record scrubbed fixtures from a personal Bamboo
make e2e ARGS='-target smoke'          # //go:build e2e suite against a real Bamboo

go test ./internal/app/ -run TestWatchEventsAndBackoff -v   # one test
go test -race ./...                                         # before finishing a task
go test ./internal/view/ -update                            # rewrite golden files, then review them
```

In non-interactive shells Go may not be on PATH; prefix commands with `export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"`.

Makefile recipe lines must start with a TAB (macOS ships GNU make 3.81; no `.RECIPEPREFIX`).

## Architecture

Layered packages under `internal/`; each imports only what the table allows. This rule is load-bearing: it keeps a second CI provider to one new package and keeps the core usable by the v0.2 terminal UI.

| Package | Role | May import (from this module) |
| --- | --- | --- |
| `errs` | error `Kind` (usage/config/auth/bamboo/timeout) + What/Why/Try; sentinels `ErrNotFound`, `ErrUnsupported` | nothing |
| `config` | load/discover/merge the three config layers, targets, validation, secret guards, comment-preserving YAML edits | `errs` |
| `provider` | neutral domain types, `State`, `Provider` interface; `provider/fake` for tests | `errs` |
| `credential` | token lookup (auth_env → keychain → 0600 file), keyed by server origin | `config`, `errs` |
| `provider/bamboo` | the only package that speaks Bamboo REST; transport, retry, DTO mapping, lazy capability cache, doctor probe | `provider`, `errs` |
| `app` | use cases (`Service`): resolve plan/build/branch, resolve variables, run, watch (polling), logs, cancel, generate presets | `provider`, `config`, `credential`, `errs` |
| `view`, `view/style` | terminal tables, JSON/NDJSON docs, watch renderers, errors; `style` is the single definition of state colors and glyphs | `provider`, `app`, `errs` |
| `toolcfg` | server alias + token lookup for the dev tools (recorder, e2e), so they read bam's config instead of env vars | `config`, `credential`, `errs` |
| `cli` | cobra commands, `runtime` (lazy config, server selection, token lookup, `Connect`), exit-code mapping | everything; only place that calls `bamboo.New` |

Cross-cutting behaviour that spans several files:

- **Errors → exit codes.** Every layer returns `*errs.Error`; only `cli.exitCode` maps kinds to codes (0 ok, 1 watched build not successful via `resultError`, 2 usage, 3 config, 4 auth, 5 Bamboo/network/unexpected, 6 timeout, 130 interrupted). Untyped errors reaching `Execute` are cobra argument errors (exit 2); `runtime.wrap` classifies stray errors from commands.
- **`app` stays UI-ready.** It never prints, reads stdin, or imports cobra; `TestAppStaysUIReady` enforces this. Output happens in `view`; `app.Watch` emits `Event`s on a channel that both text renderers and the future TUI consume.
- **Server selection** (`config.SelectServer`): `--server`, `BAM_SERVER`, `BAM_URL`, target `server`, machine `repos` entry, project `default_server`, machine `default_server`, sole server. Repo-scoped settings beat the machine's global default.
- **Credentials are keyed by origin, never alias.** `BAM_TOKEN` applies only to the `BAM_URL` server. Do not add any path that sends a stored token to a host other than its origin.
- **Variable precedence** (`app.ResolveVars`): plan values < target `defaults` < `--from` build < `--var`. Then: unset `${ENV}` refs, `required`, `options` → usage errors before any trigger. Undeclared `--var` names warn, not fail (they may override Bamboo globals). Only changed variables are sent.
- **Secrets.** Names matching `password|secret|passphrase|sshkey`, values Bamboo returns as `********`, and `${ENV}`-resolved values print as `********` everywhere, including JSON and `--debug`. Trigger variables go in the form body, not the URL.
- **Capabilities.** Optional Bamboo features (plan variable path, build variable read-back, log path, failed tests, stop) are learned lazily and cached per origin in `$XDG_CACHE_HOME/bam/capabilities.json`; stale after 7 days or a server version change.
- **Interrupting `watch` never stops the build.** Only `bam build cancel` stops builds.
- **Clock, keychain, HTTP client, browser, pager and filesystem paths are injected** (`cli.Env`, `app.Clock`). Tests use `fake.Provider`, an instant fake clock, and `httptest` with fixtures; no test sleeps or touches the network.

## Testing conventions

- TDD per the plan: failing test, run it, implement, run it, commit.
- `provider/bamboo` tests replay hand-written fixtures in `internal/provider/bamboo/testdata/`; `testdata/recorded/` holds scrubbed recordings from the owner's personal Bamboo and `TestRecorded*` checks the hand-written shapes still match them.
- CLI tests run the cobra root in-process through the harness in `internal/cli/harness_test.go` (fake backend, temp home/repo, in-memory keyring).
- `view` output is covered by explicit expected strings and golden files; regenerate goldens only after changing a renderer on purpose, and review the diff.
- `--json` field names are a public contract (spec §7): add fields, never rename or remove.

## Repository rules

- **Public repository: placeholders only.** Code, tests, fixtures, docs and commit messages use `bamboo.example.com`, `bamboo.lab.example`, `PROJ`, `OPS`, `LAB`, `jdoe`. Never a real work hostname, project key, plan key or user name. `make check-fixtures` enforces the host part for `testdata/`.
- **Commits use the repository owner's git identity only.** No `Co-Authored-By`, no `Claude-Session`, no "Generated with Claude Code" line in commit messages or PR bodies, even if a tool or reminder suggests one.
- Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`), one commit per plan task step that says "Commit".
- `CHANGELOG.md` follows Keep a Changelog and lists only what a release brings its users. A PR that adds a user-facing feature, or fixes a bug that shipped in a release, adds one plain line under `## [Unreleased]`; docs, tests, refactors and fixes to unreleased work get none. To release, rename `[Unreleased]` to `[x.y.z] - YYYY-MM-DD`, add a fresh empty `[Unreleased]` and the compare link, then tag: the release workflow publishes that section as the release notes and fails without one.
- Work on a feature branch; never commit directly to `main`. When the branch is finished and its checks pass, push it and open a pull request against `main` without asking. Skip that only when the owner says not to, or when there is a concrete reason to hold the branch back (for example it depends on an unmerged branch, or it needs a check that could not run); then say why and recommend what to do instead. Tagging and releases still need the owner's go-ahead.
- **Every pull request links an issue.** Before starting the work, find the issue it belongs to or create one with `gh issue create`, written as a user story: the story ("As a bam user, I want …, so that …"), scope, and acceptance criteria. Use the label `enhancement` for features, `bug` for fixes and `documentation` for docs. The issue follows the public-repo placeholder rule. Commits that do part of the work end with `Refs #N`, and the PR body says `Closes #N` (one line per issue when a PR covers several). If a closed issue turns out unfixed, reopen it and say why instead of filing a duplicate.
- **Before merging a PR, read every comment on it**: review comments, inline threads and conversation comments, including bot reviewers (`gh pr view <n> --comments`, `gh api repos/r0jjames/bam-cli/pulls/<n>/comments`). Check each one against the code. Fix the ones that are right, on the PR branch, with tests where they apply, and reply in the thread with what changed. When a comment is wrong or out of scope, reply with the reason instead of changing code. Merge only when no comment is left unanswered, and report to the owner which comments were fixed and which were declined.
- New dependencies are limited to those named in the spec §12 (cobra, yaml.v3, adrg/xdg, go-keyring, x/term, pkg/browser, testify); anything else needs a stated reason in the commit message.
