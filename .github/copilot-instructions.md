# Copilot review instructions for bam-cli

`bam` is a Go CLI that drives Atlassian Bamboo Data Center over REST with a
personal access token. Module `github.com/r0jjames/bam-cli`, binary `bam` from
`cmd/bam`. The design lives in `docs/superpowers/specs/` and the scope in
`docs/prd.md`; if code and spec disagree, the spec wins.

Review Go diffs for correctness first, then for the repository rules below.
These rules are not visible from the diff alone, so check them explicitly.

## 1. Public repository: placeholders only

This repository is published publicly, so treat every file and every commit
message as world-readable whatever the current visibility setting says. Code,
tests, fixtures, docs and commit messages may only use placeholder identifiers:

- hosts: `bamboo.example.com`, `bamboo.lab.example`
- project keys: `PROJ`, `OPS`, `LAB`
- user names: `jdoe`

Flag any hostname, project key, plan key, build key, user name or URL that looks
like a real corporate system, including inside `testdata/`, golden files, test
names and doc examples. `make check-fixtures` only guards the host part of
`testdata/`, so everything else needs review.

## 2. Package layering

Each package under `internal/` may import only the listed packages from this
module, and every package under `internal/` has a row. An import that crosses
this table is a defect, even when it compiles.

| Package | May import |
| --- | --- |
| `errs` | nothing |
| `config` | `errs` |
| `provider` | `errs` |
| `credential` | `config`, `errs` |
| `provider/bamboo` | `provider`, `errs` |
| `provider/fake` | `provider`, `errs` |
| `app` | `provider`, `config`, `credential`, `errs` |
| `view`, `view/style` | `provider`, `app`, `errs` |
| `toolcfg` | `config`, `credential`, `errs` |
| `cli` | everything |

Related rules:

- `provider/bamboo` is the only package that speaks Bamboo REST. A new HTTP call
  anywhere else is a defect.
- `bamboo.New` is called only from `internal/cli`.
- `app` must stay UI-ready: it never prints, never reads stdin, never imports
  cobra. Output belongs in `view`.
- `view/style` is the single definition of state colours and glyphs. A colour or
  glyph literal in another package is a defect.

## 3. Errors and exit codes

Every layer returns `*errs.Error` with a `Kind` (usage/config/auth/bamboo/timeout)
and What/Why/Try text. Only `cli.exitCode` maps kinds to process exit codes
(0 ok, 1 watched build not successful, 2 usage, 3 config, 4 auth, 5 Bamboo or
network, 6 timeout, 130 interrupted). Flag any `os.Exit` or exit-code literal
outside that mapping, and any error returned from a non-`cli` package that is
not an `*errs.Error`.

## 4. Secrets

Values must print as `********` everywhere, including `--json` and `--debug`,
when any of these hold:

- the variable name matches `password|secret|passphrase|sshkey`
- Bamboo returned the value as `********`
- the value came from resolving a `${ENV}` reference

Trigger variables go in the form body, never in the URL or a query string. Flag
any new log line, error message, debug dump or JSON field that could carry a raw
token or secret value.

## 5. Credentials are keyed by server origin, never by alias

`BAM_TOKEN` applies only to the `BAM_URL` server. Flag any code path that could
send a stored token to a host other than the origin it was stored for.

## 6. `--json` output is a public contract

Field names in `--json` and NDJSON output are specified in the design spec §7.
Adding a field is fine. Renaming or removing one is a breaking change and should
be flagged.

## 7. Variable precedence

`app.ResolveVars` order: plan values < target `defaults` < `--from` build <
`--var`. Then unset `${ENV}` references, `required` and `options` violations are
usage errors raised before any build is triggered. Undeclared `--var` names warn
rather than fail. Only changed variables are sent to Bamboo.

## 8. Tests

- Tests never sleep and never touch the network. Clock, HTTP client, keychain,
  browser, pager and filesystem paths are injected; tests use `fake.Provider`,
  an instant fake clock, and `httptest` with fixtures. The one exception is the
  `//go:build e2e` suite under `e2e/`, which runs against a real Bamboo on
  demand and is excluded from the default build; it may use the network and may
  wait on a real build.
- Interrupting `bam watch` must never stop the build. Only `bam build cancel`
  stops builds.
- Golden files in `internal/view/` change only when a renderer changed on
  purpose.
- New behaviour needs a test in the same change.

## 9. Dependencies

New module dependencies are limited to cobra, `gopkg.in/yaml.v3`, `adrg/xdg`,
`go-keyring`, `golang.org/x/term`, `pkg/browser` and testify. Anything else
needs a stated reason.

## 10. Commit messages and PR bodies

Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `chore:`). Use the
repository owner's git identity only: no `Co-Authored-By`, no `Claude-Session`,
no "Generated with Claude Code" line, in either commit messages or PR bodies.

## What not to comment on

- Formatting: `gofmt` and `golangci-lint` already gate it in the no-mistakes pipeline.
- Style preferences that match the surrounding code.
- Missing features that `docs/prd.md` lists as out of scope or as a later
  version.
