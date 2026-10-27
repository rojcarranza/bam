# Bam CLI - MVP Design (v0.1)

**Status:** Implemented | **Date:** 2026-09-12
**Product requirements:** [`docs/prd.md`](../../prd.md)

This spec is the input to the v0.1 implementation plan. It refines the earlier design notes kept outside this repository; section 14 lists every change from those notes. All hosts, keys and user names are placeholders.

## 1. Summary

Bam is a single Go binary that drives Bamboo Data Center 9.x+ over its REST API with a personal access token. v0.1 covers setup, browsing, run presets and their generation, triggering, watching, logs, cancel, and links back to Bamboo, with human output on a TTY, plain output on a pipe, and a stable JSON contract.

Commands are noun-verb (`bam build run`), with three top-level shortcuts for the daily loop (`run`, `watch`, `logs`) and two cross-entity commands (`open`, `url`).

Bare `bam` on a terminal is reserved for the lazygit-style terminal UI, which ships in v0.2 with its own spec. v0.1 builds the core that the UI will call, under the constraints in section 8.5.

## 2. Command surface

### 2.1 Tree

```
Interactive
  bam                   on a terminal: terminal UI (v0.2); v0.1 prints help plus a note
                        on a pipe, or with --help: help

Setup
  bam server add <alias> --url URL [--project KEY]...
  bam server list
  bam server rm <alias>
  bam login <alias> [--with-token]
  bam logout <alias>
  bam whoami
  bam doctor
  bam init --server ALIAS [--url URL] --project KEY... [--plan KEY[=name]]... [--force]
  bam version
  bam completion bash|zsh|fish|powershell

Presets
  bam preset list
  bam preset show <name>
  bam preset add <name> --plan KEY [--from N|last] [--branch B] [--shared] [--print] [--force]
  bam preset migrate [--dry-run]

Browse
  bam project list [--all]
  bam plan list [PROJ...]
  bam plan show <plan>
  bam plan vars <plan> [--from N|last]
  bam plan branches <plan>
  bam build list <plan> [--branch B] [--limit N] [--state STATE]
  bam build show <build>

Run loop                                              shortcut
  bam build run <plan> [--var k=v]... [--from N|last]   bam run
        [--branch B] [--watch] [--timeout D] [--dry-run]
  bam build watch <build> [--timeout D]                 bam watch
  bam build logs <build> [--failed] [--job KEY]         bam logs
        [--follow] [--tail N]
  bam build cancel <build>

Links
  bam open <key|target|--last>
  bam url  <key|target|--last>

Global flags
  --server ALIAS   --json   --color auto|always|never   --debug
```

Shortcuts are cobra aliases of the `build` subcommands: same flags, same output, same JSON. Help lists them in a "Shortcuts" group.

### 2.2 Argument resolution

Each command accepts one entity type, so parsing never guesses the hierarchy level.

- **`<plan>`** - a target name or a plan key.
  - Target names are validated as `^[a-z][a-z0-9_-]*$`. Plan keys are uppercase. The two can never collide.
  - An exact target name resolves to that target's plan, server and branch.
  - Otherwise the argument must match `^[A-Z][A-Z0-9]*-[A-Z][A-Z0-9]*$`.
  - Anything else is a usage error that lists the configured target names.
- **`<build>`** - one of three forms:
  - A build key: `PROJ-PLAN-123`, or a branch build key such as `PROJ-PLAN12-5`.
  - A `<plan>`: the latest build of that plan, on `--branch` if given, else on the target's branch, else on the default branch.
  - `--last`: the last build that bam triggered from the current repository (section 3.8).
- **`--branch B`** - matched against the plan's branch names, then short names. The match resolves to the branch plan key. No match is a usage error that lists the closest names.
- **`--job KEY`** - a job key within the build, either the short form `JOB1` or the full `PROJ-PLAN-JOB1`.
- **`--from N|last`** - `N` is a build number of the resolved plan branch, or a full build key. `last` is the most recent manual build of the resolved plan branch, falling back to the most recent build. The command always prints which build it used.
- **`open` / `url`** - accept any key (project, plan, branch plan, build, job result), a target name, or `--last`. Every Bamboo entity browses at `{server}/browse/{key}`.

### 2.3 Variable resolution for `build run`

Sources, lowest to highest precedence:

1. The plan's declared variable values in Bamboo. These are what Bamboo uses anyway; bam reads them only to display and validate.
2. The target's `defaults`.
3. `--from <build>`: only variables whose names are declared plan variables or keys of the target's `defaults`. If the declared list is unavailable, all of the build's variables are taken and a warning says so.
4. `--var k=v`, repeatable. The last occurrence of a name wins.

Validation, in order, before any trigger:

1. An `${NAME}` value whose environment variable is unset is a usage error naming both the Bamboo variable and the environment variable.
2. A name listed in the target's `required` with an empty or missing value is a usage error.
3. A value outside the target's `options` for that name is a usage error listing the allowed values.
4. A `--var` name that is not a declared plan variable is a **warning** on stderr with a did-you-mean suggestion, not an error. Plans may legitimately override Bamboo global variables, which the plan variable endpoint does not list.

Only variables that differ from the plan's declared values are sent to Bamboo.

`run` prints the resolved set before triggering, each value labelled with its source (`plan`, `target`, `from #N`, `flag`, `env`). Values whose names match the masked-name pattern (section 3.6), and values resolved from `${NAME}`, print as `********`.

`--dry-run` performs every resolution and validation step, prints the result, and exits 0 without triggering.

A target with `watch: true` makes watching the default for that target; `--watch=false` turns it off for one run.

## 3. Configuration

### 3.1 Layers

| Layer | Location | Committed | Holds |
| --- | --- | --- | --- |
| Project | `.bam.yaml` at the repository root | yes | servers, projects, targets |
| Machine | `$XDG_CONFIG_HOME/bam/config.yaml`, overridable by `BAM_CONFIG` | no | server overrides and additions, repository mappings, personal targets, preferences |
| Presets | `~/bam/presets/*.yaml` and `~/bam/presets.yaml`, overridable by `BAM_PRESETS` (§3.4, Presets file) | no | personal presets, usable in every directory |
| Credentials | OS keychain, a named environment variable, or `$XDG_CONFIG_HOME/bam/credentials.yaml` (0600) | never | tokens only |

Generated data is kept out of files that people edit:

- Capability results: `$XDG_CACHE_HOME/bam/capabilities.json`, keyed by server origin.
- Last-build record: `$XDG_STATE_HOME/bam/state.json`, keyed by repository root.

Paths come from `github.com/adrg/xdg`, so macOS and Windows get their platform directories.

### 3.2 Project file discovery

Bam walks up from the working directory looking for `.bam.yaml`. The walk stops at the git repository root when inside a repository, else at `$HOME`. A `.bam.yaml` above that boundary is never used.

The "repository root" used as a key in the machine file and state file is the git root, or the working directory when not inside a repository.

### 3.3 Project file schema

```yaml
version: 1
servers:
  work:
    url: https://bamboo.example.com
default_server: work
projects: [PROJ, OPS]          # first entry is the default where one project is needed
default_project: PROJ          # optional; the project the terminal UI opens on
targets:
  build:
    plan: PROJ-BUILD
  provision-lab:
    plan: PROJ-PROV
    server: work               # optional; defaults to the selected server
    branch: develop            # optional; --branch overrides
    defaults:
      cluster_type: k8s
      compute_nodes: 2
      db_password: "${LAB_DB_PASSWORD}"
    options:
      cluster_type: [k8s, dcos]
      compute_nodes: [2, 3, 5]
    required: [cluster_name]
    watch: true
    timeout: 45m
```

### 3.4 Machine file schema

```yaml
version: 1
default_server: home
servers:
  home:
    url: http://bamboo.lab.example:8085
    projects: [LAB]
    default_project: LAB                  # the project the terminal UI opens on
  work:
    url: https://bamboo-eu.example.com    # overrides the project file's url on this machine
    auth_env: BAM_WORK_TOKEN              # read the token from this variable first
targets:                                   # personal targets available in every repository
  lab-smoke:
    server: home
    plan: LAB-SMOKE
repos:
  /home/jdoe/src/some-repo:                # for repositories without a project file, or to add personal presets
    server: work
    projects: [PROJ, OPS]
    targets:
      provision-mine:
        plan: PROJ-PROV
        defaults: { cluster_name: jdoe-lab }
color: auto
pager: less -FRX
```


#### Presets file

`~/bam/presets.yaml` is the home-relative path on Linux, macOS and Windows alike, unlike the XDG paths above, so the same instructions work everywhere. Lookup: `BAM_PRESETS`; else `~/bam/presets.yaml` if it exists; else, in WSL (`WSL_DISTRO_NAME` set), the single match of `/mnt/c/Users/*/bam/presets.yaml`, so bam in WSL and `bam.exe` in Git Bash share one file (several matches: none is used, and `preset list` says so); else `~/bam/presets.yaml`. When `preset add` creates the file from WSL and none exists, it asks Windows for `%USERPROFILE%` once and creates it there.

The **presets directory** is the `presets/` directory beside the presets file (`~/bam/presets/`, or beside `BAM_PRESETS`). The presets file's location counts as existing when the file or the directory exists, and the WSL lookup also matches `/mnt/c/Users/*/bam/presets`, so the directory follows the file everywhere. Every `*.yaml` and `*.yml` in it, except hidden files, is read with the presets file's schema; other files and subdirectories are ignored, and a file that does not parse is a config error naming it. One file per Bamboo project is the convention. Preset names are global across the presets file and the directory: the same name in two of them is a config error naming both.

```yaml
presets:
  provision-mine:
    plan: PROJ-PROV
    variables:
      cluster_name: jdoe-lab
```

`version` is optional; a missing or empty file has no presets. Literal secrets are allowed, as in the machine file, but files under `/mnt/c` cannot be 0600 from WSL, so `${ENV}` references are recommended.

### 3.5 Resolution rules

**Server selection**, first match wins:

1. `--server` flag
2. `BAM_SERVER`
3. `BAM_URL` (defines an ad-hoc server named `env`; section 4.3)
4. the target's `server`, when the command's argument is a target
5. machine `repos.<root>.server`
6. project `default_server`
7. machine `default_server`
8. the only configured server, if exactly one exists
9. otherwise a configuration error that lists the aliases

Repository-scoped settings (4–6) rank above the machine file's global default (7). A global `default_server: home` therefore does not redirect a work repository that declares `default_server: work`.

**Server fields.** A server alias defined in both layers is merged field by field; machine values win. Overriding `url` keeps the other fields.

**Projects list**, first match wins: machine `repos.<root>.projects`, project `projects`, machine `servers.<alias>.projects`.

**Default project**, the project the terminal UI opens on: the `--project` flag of `bam` and `bam ui` (`all` means every project), then, first match wins, machine `repos.<root>.default_project`, project `default_project`, machine `servers.<alias>.default_project`; otherwise every configured project. Keys are uppercased. The flag applies only to the server the UI opens on; switching server inside the UI applies that server's `default_project`. A default project need not be in the projects list. A default project that the server does not have opens on every configured project, and the status bar says `project OPS not found on work`.

**Targets.** Merged by name, field by field, in increasing precedence: project `targets`, machine top-level `targets`, presets file and presets directory `presets` (no file of that layer outranks another; duplicates are errors), machine `repos.<root>.targets`. In every layer a preset's variables may be written `variables` or `defaults` (its older name), not both. `defaults`, `options` merge per variable name; `required` is replaced as a whole list.

**Other settings.** Flags beat environment variables, which beat the machine file, which beats the project file, which beats built-in defaults.

### 3.6 Validation

A configuration error (exit 3) names the file, the key path, and the fix:

- `version` missing or not `1`: the binary states the version it understands and stops.
- A key that looks like a credential (`token`, `password`, `pat`, `secret` under `servers.*`) in the project file.
- In the project file, a literal `defaults` value whose variable name matches the masked-name pattern: names containing `password`, `secret`, `passphrase` or `sshkey`, case-insensitive (Bamboo's default masking list). An `${NAME}` reference is allowed.
- A target name that does not match `^[a-z][a-z0-9_-]*$`, or a target without `plan`.
- A `defaults` value not contained in that variable's `options`.
- A target `server` that names an unknown alias.
- `credentials.yaml` with permissions looser than 0600 (refused, as `ssh` refuses a readable private key).

`defaults` and `options` accept YAML scalars and convert them to strings, since Bamboo variables are strings.

### 3.7 Environment references

A `defaults` value that is exactly `${NAME}`, with `NAME` matching `^[A-Za-z_][A-Za-z0-9_]*$`, is read from the environment at run time. Partial interpolation is not supported, so a literal `$` elsewhere needs no escaping. `preset show` prints the reference unresolved and marks it `set` or `unset`.

### 3.8 State file

`state.json` records, per repository root: the last triggered build key, server origin, plan key, target name and trigger time. It records no variable values. `--last` reads it. A recorded build that returns 404 is reported as expired and the record is removed.

### 3.9 Preset generation

`bam preset add <name> --plan KEY` and `bam init --plan KEY[=name]` share one generator.

Sources for variable names, in order:

1. The plan variable endpoint (section 9.1) - declared names and their current values.
2. The `--from` build's variables (default `last`), if source 1 is unsupported.
3. Neither: the target is written with `plan` only and a comment explaining why.

Output rules:

- Values are written as quoted strings.
- Each value has a trailing comment with its origin: `# plan default`, or `# no plan default; last used "beta" in PROJ-PROV-97`.
- A value Bamboo returns masked (`********`), or a name matching the masked-name pattern, is written as `"${NAME_IN_UPPER_SNAKE}"` with a comment naming the environment variable to set.
- `branch`, `options`, `required` and `watch` are written commented out. `required` is pre-filled, commented, with the names that have no plan default.
- The file is edited through the `yaml.v3` Node API so existing comments and key order survive.
- After writing, stderr prints `review before commit: values copied from Bamboo`.

`preset migrate [--dry-run]` moves the machine file's targets (top-level and every `repos.<root>.targets`) into the presets file, renaming `defaults` to `variables` and keeping comments; a name already in the presets file or presets directory, defined more than once in the machine file, or using YAML anchors is left and reported; an invalid presets file stops it before any write; emptied `targets` maps and `repos` entries are removed; `.bam.yaml` is never touched.

`preset add` writes under `presets:` to `<presets directory>/<project>.yaml` (§3.4), the plan key's project part lowercased, creating the directory and file when needed; `--file NAME` writes `<presets directory>/NAME.yaml` instead (a trailing `.yaml`/`.yml` is kept; a path, `..`, a leading dot or `--shared` with it is a usage error). A name already in another presets file is a usage error even with `--force`. `--shared` writes to the discovered project file under `targets:`; with no project file it fails and suggests `bam init`. `--print` writes the YAML to stdout and touches no file. An existing target name fails unless `--force`.

`init` writes `.bam.yaml` at the repository root with `version`, the server alias and URL (from `--url`, or copied from the machine file's alias), `default_server`, `projects`, and one generated target per `--plan`. A target name defaults to the lowercased plan part of the key (`PROJ-PROV` → `prov`); `=name` overrides it. An existing `.bam.yaml` fails unless `--force`. `init` without `--plan` makes no network call.

## 4. Authentication and credentials

### 4.1 Mechanism

Personal access tokens only, sent as `Authorization: Bearer <token>`. Tokens are created in the Bamboo web UI under the user's profile; `bam login` prints the URL of that page for the target server.

### 4.2 Credential keying and lookup

Credentials are keyed by the server's normalized origin (`https://bamboo.example.com`), never by alias. An alias is a display name. A repository whose `.bam.yaml` points an alias at a different host therefore has no token until someone runs `bam login` for that host, and a stored token is never sent to a host it was not created for.

Lookup order for a server:

1. The environment variable named by the server's `auth_env`.
2. The OS keychain via `github.com/zalando/go-keyring`: service `bam`, account = origin. macOS Keychain, Linux Secret Service, Windows Credential Manager.
3. `credentials.yaml`, a map of origin → token, mode 0600.

`login` stores in the keychain. When the keychain is unavailable (headless Linux, build agents, WSL), it stores in `credentials.yaml` and says so.

### 4.3 CI without configuration

`BAM_URL` and `BAM_TOKEN` together define a server named `env`. `BAM_TOKEN` is used only for the `BAM_URL` host; it is never sent to a configured alias.

### 4.4 Commands

- `bam login <alias>` - prompts for the token with echo off (`golang.org/x/term`), or reads it from stdin with `--with-token`. Verifies it with `GET /rest/api/latest/currentUser` before storing, then prints the user name.
- `bam logout <alias>` - deletes the stored token for that alias's origin from the keychain and the file.
- `bam whoami` - one line per configured server: alias, origin, user name or the error class. Exits 0 when every server answers, else with the code of the first failure. `--server` limits it to one server.
- `bam server add` - writes the alias to the machine file; on a TTY, continues into `login`.
- `bam server list` - alias, URL, which layer defines it, and whether a token is present. No network call.
- `bam server rm` - removes the alias from the machine file only. An alias defined by the project file cannot be removed this way; the error says where it is defined.

Every network command resolves the credential before its first request. A missing or rejected credential is an authentication error (exit 4) naming the alias and the command that fixes it.

## 5. Terminal output

### 5.1 General rules

- **TTY:** aligned tables, colors with glyphs, relative times (`18m ago`), and free-text columns (names, reasons) truncated with `…` to the terminal width. Keys are never truncated. Width comes from the terminal, then `COLUMNS`, then 80.
- **Pipe:** the same columns, no color, no truncation, absolute RFC3339 times, no cursor movement, no spinner.
- Data goes to stdout. Hints, warnings and progress go to stderr.
- An empty result prints a one-line message and a hint on stderr and exits 0, for example `no builds for PROJ-PLAN on branch develop`.
- Bamboo pagination (`start-index`, `max-result`) is followed internally up to `--limit`. Defaults: `build list` 10, `plan show` history 5.

### 5.2 States, colors and glyphs

One vocabulary, defined once in `internal/view/style`:

| State | Color | Glyph |
| --- | --- | --- |
| success | green | `✓` |
| failed | red | `✗` |
| running | cyan | `▸` |
| queued | yellow | `◷` |
| stopped | grey | `⊘` |
| not_built | dim | `–` |
| skipped | dim | `⤼` |
| unknown | magenta | `?` |

Colors are the eight standard ANSI colors, so the terminal theme decides the shade. A state is never rendered with color alone; its glyph is always present. Color follows `--color`, then `NO_COLOR`, then TTY detection. JSON is never colored.

Terminal hyperlinks (OSC 8) wrap keys in table and detail output when stdout is a TTY. They are never emitted on a pipe.

### 5.3 Examples

`bam plan list`:

```
PROJ  Example Project
  PLAN          NAME             LAST   STATE      COMPLETED  REASON
  PROJ-BUILD    Build and test   #482   ✓ success  12m ago    Manual run by jdoe
  PROJ-PROV     Provision lab    #97    ✗ failed   2h ago     Scheduled
  PROJ-OLD      Legacy           –      – never    –          Never built
```

`bam build list PROJ-PLAN`:

```
BUILD            STATE       BRANCH     STARTED   DURATION  REASON
PROJ-PLAN-1843   ▸ running   develop    2m ago    2m03s     Manual run by jdoe
PROJ-PLAN-1842   ✓ success   develop    18m ago   4m11s     Changes by a1b2c3d
PROJ-PLAN7-12    ✗ failed    feat/foo   1h ago    3m40s     Manual run by jdoe
```

`bam run build --branch develop --var env=staging --watch` (the block below the header redraws in place):

```
Plan     PROJ-BUILD  (branch develop, PROJ-BUILD12)
Vars     env=staging (flag)  region=eu (preset)
✓ Queued   PROJ-BUILD12-44   https://bamboo.example.com/browse/PROJ-BUILD12-44
▸ Running  agent linux-3     2m03s

  ✓ Checkout        12s
  ✓ Build           1m20s
  ▸ Test            31s
      ✓ Unit        28s
      ▸ Integration 31s
  – Package
```

On completion:

```
✗ Failed   PROJ-BUILD12-44  3m40s   Test › Integration
  next: bam logs PROJ-BUILD12-44 --failed
        bam open PROJ-BUILD12-44
```

`build run` without `--watch` prints the queued line with key and URL and a `watch: bam watch <key>` hint.

### 5.4 Watch behaviour

- On a TTY the live block redraws with ANSI cursor movement; no TUI library is used in v0.1.
- On a pipe, each state change prints one line: `2026-09-12T14:03:20Z PROJ-BUILD12-44 running agent=linux-3`.
- Watching a build that has already finished prints its final summary and exits with its result code.
- **Interrupt (Ctrl-C) stops watching only.** The build keeps running; bam prints `still running: bam watch <key>` and exits 130. Stopping a build is only ever `bam build cancel`.
- **`--timeout`** behaves the same way and exits 6. A target's `timeout` is the default; otherwise there is no timeout.
- Polling starts at 2 seconds, multiplies by 1.5 after each poll with no change, caps at 10 seconds, and resets to 2 seconds on any change.

### 5.5 Logs

- `bam logs <build>` prints the logs of every job in the build; `--job` selects one; `--failed` selects the failed jobs.
- With more than one job, each log is preceded by a header: `==> PROJ-BUILD12-INT-44  Integration (failed) <==`.
- On a TTY, output goes through a pager: `BAM_PAGER`, then the machine file's `pager`, then `PAGER`, then `less -FRX`. On Windows with none set, no pager. On a pipe, raw text.
- `--tail N` prints the last N lines per job.
- `--follow` polls from the stored offset until the job finishes.
- `--failed` on a build with no failed jobs prints `no failed jobs in <key>` on stderr and exits 0.

### 5.6 Build detail

`bam build show` prints state, started, completed, queue duration, duration, flags (for example `CUSTOM BUILD`), labels, agent, per-repository revisions with short SHAs, and the stages with their jobs. The URL is in the header.

For a failed build, a **Failure** block lists the failed jobs with their `bam logs` command and, when the server supports it, up to five failed test names.

### 5.7 Doctor

`bam doctor` prints one line per check with a glyph: configuration loads, server selected, token found, token accepted (user name), Bamboo version, and each capability in section 9.3. It re-runs every capability check and rewrites the cache. It exits with the code of the first failing check's class, or 0.

### 5.8 Open

`bam open` opens the URL with `github.com/pkg/browser`. When no browser can be opened (for example over SSH), it prints the URL to stdout, a warning to stderr, and exits 0.

## 6. Errors and exit codes

### 6.1 Error format

Errors go to stderr as up to three parts - what failed, why, what to try:

```
error: could not start build of PROJ-BUILD
  branch "feat/foo" not found on server "work"
  try: bam plan branches PROJ-BUILD
```

Raw status codes, response bodies and stack traces appear only with `--debug`.

### 6.2 Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success; for `build run` without `--watch`, the build was queued |
| 1 | a watched build finished in any state other than success |
| 2 | usage error: bad flag or argument, unknown target, variable validation failure |
| 3 | configuration error |
| 4 | authentication error: missing, rejected or expired token |
| 5 | Bamboo or network error: not found, server error after retries, unreachable; also any unexpected internal error |
| 6 | `--timeout` reached while the build is still running |
| 130 | interrupted |

Idempotent cases exit 0: cancelling a build that has already finished prints its final state on stderr; `logs --failed` on a build with no failed jobs prints a note.

### 6.3 Debug

`--debug` logs each request's method, URL, status and duration to stderr through `log/slog`. The `Authorization` header is always redacted, and so is the value of every trigger variable whose name matches the masked-name pattern or came from an `${NAME}` reference.

## 7. JSON contract

- `--json` is available on every command that reads data. It writes exactly one JSON document to stdout; lists are arrays.
- Every entity has `key` and `url`. Times are RFC3339 strings. Durations are integer `duration_ms`. `state` uses the vocabulary of section 5.2.
- `build watch --json` and `build run --watch --json` write **NDJSON**, one event per line: `{"type":"state"|"stage"|"job","time":...,"build_key":...,...}`. The last line is `{"type":"done","build":{...}}` with the full build object.
- `build run --json` without `--watch` writes `{key, url, plan_key, branch, variables}`; `variables` masks the same values that the human output masks.
- Errors in `--json` mode remain plain text on stderr; the exit code carries the class.
- Within a major version, fields may be added but not renamed, retyped or removed. Golden files enforce this.
- No `--yaml`, `--quiet` or `--jq` in v0.1.

Core objects:

```
Plan    { key, url, name, project_key, last_build: { key, number, state, finished_at, reason } | null }
Branch  { key, url, name, short_name, plan_key }
Variable{ name, value, masked, source, last_used }
Target  { name, plan_key, server, branch, defaults, options, required, watch, timeout_ms, defined_in[] }
Build   { key, url, plan_key, plan_name, branch, number, state, reason, custom_build, labels[],
          queued_at, started_at, finished_at, queue_duration_ms, duration_ms, agent,
          revisions[{ repository, revision, short }],
          stages[{ name, state, duration_ms, jobs[{ key, url, name, state, duration_ms }] }],
          failed_tests[] }
```

## 8. Architecture

### 8.1 Packages

```
cmd/bam/main.go          calls cli.Execute and exits with its code
internal/
  errs/                  Kind (usage, config, auth, bamboo, timeout) and an error carrying what/why/try
  config/                load, discover, merge, validate, targets, env references, secret guards; YAML node editing
  credential/            token lookup chain and storage; Store interface
  provider/              neutral domain types and the Provider interface
    bamboo/              REST client: transport, retry, pagination, DTO mapping, capability handling
  app/                   use cases: ResolvePlan, ResolveBuild, ResolveVars, Run, Watch, Logs, Cancel,
                         GenerateTarget; last-build state
  view/                  style (colors, glyphs, OSC 8), tables, watch renderers, JSON/NDJSON, error printer
  cli/                   cobra tree, flags, shortcuts, completion functions, dependency wiring, exit codes
testdata/                recorded, scrubbed Bamboo responses
```

### 8.2 Dependency rule

| Package | May import |
| --- | --- |
| `errs` | nothing internal |
| `config`, `provider` | `errs` |
| `credential` | `config`, `errs` |
| `provider/bamboo` | `provider`, `errs` |
| `app` | `provider`, `config`, `credential`, `errs` |
| `view` | `provider` (types), `app` (events), `errs` |
| `cli` | everything; the only package that calls `bamboo.New` |

`app` never imports `provider/bamboo`. A second provider is one new package. Only `cli` maps an `errs.Kind` to an exit code.

Configuration is loaded once into a plain struct and passed down. There is no global state. Clock, browser opener, pager, keychain and HTTP client are injected, so tests need no network, no real time and no real keychain.

### 8.3 Provider interface

```go
type Provider interface {
    CurrentUser(ctx context.Context) (User, error)
    ServerInfo(ctx context.Context) (ServerInfo, error)
    ListProjects(ctx context.Context) ([]Project, error)
    ListPlans(ctx context.Context, project string) ([]Plan, error)
    GetPlan(ctx context.Context, planKey string) (Plan, error)
    ListBranches(ctx context.Context, planKey string) ([]Branch, error)
    ListVariables(ctx context.Context, planKey string) ([]Variable, error)
    ListBuilds(ctx context.Context, planKey string, o ListOptions) ([]Build, error)
    GetBuild(ctx context.Context, buildKey string) (Build, error)
    BuildVariables(ctx context.Context, buildKey string) (map[string]string, error)
    Trigger(ctx context.Context, req TriggerRequest) (Build, error)
    StopBuild(ctx context.Context, buildKey string) error
    FetchLog(ctx context.Context, jobResultKey string, o LogOptions) (LogChunk, error)
    URL(key string) string
}
```

`ListVariables` and `BuildVariables` may return `errs.ErrUnsupported`; callers fall back as sections 2.3 and 3.9 describe. `Build` carries the neutral `State`; no Bamboo-shaped field reaches `app` or `view`. `LogChunk` carries lines and the offset for the next call, which is how `--follow` resumes.

Watching is a use case, not a provider method: `app.Watch` polls `GetBuild` on the injected clock and emits events on a channel. A future provider with push notifications can add an optional interface without changing `app`'s callers.

### 8.4 Data flow: `bam run provision-lab --var cluster_name=alpha --watch`

1. `cli` parses flags; `config.Load` discovers and merges the layers.
2. `cli` selects the server, calls `credential.Lookup(origin)`, and builds `bamboo.New(url, token, capabilities)`.
3. `app.Run` resolves the target to a plan and server, resolves the branch key, and fetches declared variables.
4. `app.Run` resolves and validates variables (section 2.3), calls `Trigger`, and records the last build in the state file.
5. `app.Watch` polls and emits events; `view` renders them as a live block, plain lines, or NDJSON.
6. The final `Build` returns to `cli`, which picks the exit code.

### 8.5 Constraints that keep the core ready for the terminal UI

The v0.2 terminal UI is a second view over `app`, living in `internal/view/tui` and built on bubbletea, lipgloss and bubbles. v0.1 does not build it, but v0.1 code must not block it:

- `app` never prints, never reads stdin, and never imports cobra. Every use case returns values or emits events on a channel; rendering belongs to `view`.
- `app.Watch` and `app.Logs --follow` emit events that a bubbletea program can forward as messages without translation.
- Every use case takes a `context.Context`, so the UI can cancel a watch when focus moves.
- Colors and glyphs come only from `internal/view/style`, which the UI maps onto lipgloss styles.
- `cli` builds dependencies in one function that the UI entry point reuses.

As of v0.2 part A, running `bam` with no arguments on a terminal opens the UI. On a pipe, with `--help`, with `--no-tui`, with `BAM_NO_TUI=1`, or on a dumb terminal, it prints help only. The UI's own design is [`2026-09-18-bam-v0.2-tui-navigate-design.md`](2026-09-18-bam-v0.2-tui-navigate-design.md).

## 9. Bamboo adapter

### 9.1 REST mapping

All calls send `Accept: application/json`; Bamboo defaults to XML without it.

| Operation | Call |
| --- | --- |
| Verify token, whoami | `GET /rest/api/latest/currentUser` |
| Server version | `GET /rest/api/latest/info` |
| Projects | `GET /rest/api/latest/project` |
| Plans of a project | `GET /rest/api/latest/project/{key}?expand=plans.plan` |
| Plan | `GET /rest/api/latest/plan/{planKey}` |
| Plan variables | `GET /rest/api/latest/plan/{planKey}/variables`, falling back to `/variable`; the working path is cached |
| Plan branches | `GET /rest/api/latest/plan/{planKey}/branch` |
| Build history | `GET /rest/api/latest/result/{planKey}?expand=results.result` |
| Build state | `GET /rest/api/latest/result/{buildKey}?expand=stages.stage.results.result` |
| Build variables | `GET /rest/api/latest/result/{buildKey}?expand=variables` |
| Failed tests | `GET /rest/api/latest/result/{buildKey}?expand=testResults.failedTests` |
| Job log | `GET /rest/api/latest/result/{jobResultKey}?expand=logEntries` |
| Job log fallback | `GET /download/{jobKey}/build_logs/{jobResultKey}.log` |
| Trigger | `POST /rest/api/latest/queue/{planKey}?executeAllStages=true` with `bamboo.variable.<name>=<value>` parameters |
| Stop | `DELETE /rest/api/latest/queue/{buildKey}`, and on 404 the same call for each unfinished **job** result key of the build (Data Center answers a plan-level key with "not of type ImmutableJob") |
| Browse URL | `{server}/browse/{key}` |

A plan branch is triggered through its own branch plan key. Trigger variables are sent as form-encoded body parameters, so values do not appear in URLs; if fixture recording shows the server ignores body parameters, the adapter sends them as query parameters and the debug log still redacts them.

The first task of the implementation plan records every call above against the personal server and confirms the response shapes, before any command depends on them.

### 9.2 Transport

One shared `http.Client` with a 30-second request timeout. Retries only on 429 and 5xx, with exponential backoff and jitter, three attempts, honouring `Retry-After`. Every call takes a `context.Context`, so an interrupt cancels everything in flight. Responses map to `errs` kinds: 401 and 403 → auth; 404 → bamboo with a "not found" message naming the key; other 4xx → bamboo with Bamboo's message text when the body carries one; unreachable → bamboo naming the host.

### 9.3 Capability handling

Capabilities are discovered lazily and cached per server origin with the Bamboo version:

- plan variable listing
- build variable read-back
- log path (`logEntries` or download)
- failed-test detail
- build stop

A stopped build comes back as `lifeCycleState: NotBuilt` with `state: Unknown`; it maps to `stopped` when it has a start time and `notRunYet` is false, and to `not built` otherwise.

On each optional call the adapter uses the cached result if present; otherwise it tries the primary path, falls back on an "unsupported" response, and records the outcome. The cache entry is discarded when `ServerInfo` reports a different version, or after seven days. `bam doctor` exercises every capability except build stop against the first plan of the first configured project and its latest build, and rewrites the cache. Build stop is recorded the first time `cancel` runs.

## 10. Testing

Development is test-driven.

| Package | Covers |
| --- | --- |
| `config` | table tests: every rule in section 3.5, target merging across three sources, every validation in 3.6, env references, discovery stopping at the git root |
| `credential` | lookup order; origin keying (two aliases on two hosts never share a token; one alias on two hosts never shares a token); 0600 refusal; keychain-unavailable fallback; `go-keyring` mock |
| `provider/bamboo` | `httptest` replaying recorded fixtures; 200, 400, 401, 403, 404, 429, 500, empty body, unexpected JSON; retry only on 429 and 5xx; pagination; each capability fallback; both log paths; form-body trigger |
| `app` | fake Provider and fake clock: argument resolution, variable precedence and validation, masking, watch backoff and reset, timeout, interrupt without stopping the build, finished-build watch, failed-job selection, generator output for each variable source and masked values, state expiry on 404 |
| `view` | golden files for TTY and pipe output, color on and off, width truncation, JSON and NDJSON shapes; a test that every state renders its glyph with color off |
| `cli` | the cobra root in-process against a fake Bamboo server: `init`, `preset add`, `plan list`, `run --watch`, `logs --failed`, `open` (injected opener); one test per exit code |
| end to end | `//go:build e2e`, against the personal Bamboo, server and token read from bam's own config by `internal/toolcfg` (`make e2e ARGS='-target smoke'`), excluded from default runs |

**Fixture guard.** `make record` captures fixtures from the personal server and scrubs hosts to `bamboo.example.com` and user names to `jdoe`. `make check-fixtures` fails if any URL host in `testdata/` is outside the allowlist (`bamboo.example.com`, `localhost`, `127.0.0.1`). CI runs it on every change.

## 11. Documentation

- `README.md`: what bam is, install, and a quickstart - `server add`, `login`, `init`, `run --watch`, `logs --failed`, `open`.
- `docs/configuration.md`: the three layers, resolution rules, presets and generation.
- `docs/authentication.md`: creating a personal access token, keychain behaviour per platform, CI environment variables.
- `docs/ci.md`: using bam in a Bamboo script step, exit codes.
- `docs/json.md`: the JSON contract and its stability promise.
- `docs/troubleshooting.md`: `doctor` output, 401, single sign-on, headless keychain.
- `docs/cli/`: the command reference, generated from cobra by `make docs`, never edited by hand.
- `LICENSE` (MIT) and `CHANGELOG.md`.

## 12. Delivery

- Module `github.com/r0jjames/bam-cli`; binary `bam` built from `cmd/bam`; Go current stable, minimum 1.22.
- Dependencies are limited to those named in this spec: cobra, yaml.v3, adrg/xdg, go-keyring, x/term, pkg/browser, testify, plus the standard library. v0.2 adds bubbletea, lipgloss and bubbles for the terminal UI, under [`2026-09-18-bam-v0.2-tui-navigate-design.md`](2026-09-18-bam-v0.2-tui-navigate-design.md) §10. Any other dependency needs a stated reason in its pull request.
- `Makefile` targets: `build`, `test`, `lint`, `e2e`, `record`, `check-fixtures`, `docs`.
- `golangci-lint` is the lint gate.
- `goreleaser` builds darwin and linux on amd64 and arm64, plus windows/amd64, attached to a git tag.
- The `no-mistakes` pipeline (`.no-mistakes.yaml`) runs lint, test and the fixture guard before every push; there is no GitHub Actions CI, and the release path is pending. A Forge-Lab Bamboo plan runs the e2e suite and uses bam itself.
- Versions follow semver. The project stays on `v0.x` until the JSON contract settles; `v1.0` freezes it.

## 13. v0.1 acceptance criteria

- [ ] `bam server add home --url …` then `bam login home` stores a verified token; `bam whoami` shows the user.
- [ ] `bam doctor` reports every check and capability against the personal Bamboo.
- [ ] `bam init --server home --project LAB --plan LAB-PROV=provision-lab` writes a `.bam.yaml` whose target lists the plan's declared variables.
- [ ] `bam plan list`, `bam plan show`, `bam plan vars`, `bam plan branches`, `bam build list` and `bam build show` work against the personal Bamboo, with and without `--json`.
- [ ] `bam run provision-lab --var cluster_name=alpha --watch` validates, triggers, shows live stage and job state, and exits 0 or 1 by the build result.
- [ ] A value outside `options`, or a missing `required` variable, stops the run with exit 2 before any trigger.
- [ ] `bam run provision-lab --from last` reuses the previous build's variables and prints which build it used.
- [ ] `bam logs --last --failed` prints only the failed jobs' logs.
- [ ] `bam build cancel --last` stops a running build.
- [ ] Ctrl-C during `watch` leaves the build running and exits 130.
- [ ] `bam open` and `bam url` work for a project, plan, branch, build, job, target and `--last`.
- [ ] Every exit code in section 6.2 is covered by a test.
- [ ] Piped output carries no color or escape sequences; `NO_COLOR` disables color on a TTY.
- [ ] Bare `bam` prints help plus the interactive-mode note on a terminal, and help only on a pipe.
- [ ] `app` imports no cobra, writes nothing to stdout or stderr, and reads nothing from stdin (checked by a test over its imports and by review).
- [ ] A second server alias pointing at a different host works side by side with `home`, and neither token is ever sent to the other host.
- [ ] `make check-fixtures` passes, and no work hostname or key exists anywhere in the repository.

## 14. Changes from the earlier design notes

| Change | Reason |
| --- | --- |
| Noun-verb commands with `run`/`watch`/`logs` shortcuts and top-level `open`/`url`, replacing flat verbs that resolved depth from key shape | One command, one output shape, which the public JSON contract needs; `--help` teaches the hierarchy; the daily loop stays short |
| `build cancel` added | Stopping a build is part of the run loop and Bamboo supports it |
| A `<plan>` argument means "latest build" wherever a build is expected | `bam logs build --failed` works after a CI-triggered build with no key lookup |
| Unknown `--var` names warn instead of failing | Plans can override Bamboo global variables that the plan variable endpoint does not list |
| Targets grew into presets: `server`, `branch`, `options`, `required`, `timeout`, env references; allowed in the machine file | Presets are the core productivity feature, and Bamboo has no allowed-value or required-variable concept |
| `bam preset add` and `bam init` generate presets from declared plan variables | Nobody transcribes a plan's variables by hand |
| Personal presets in `~/bam/presets.yaml`, the default for `preset add`; `--machine` replaced by `--shared` | One home-relative path for Git Bash, WSL and macOS; most presets are personal, and repos-scoped ones did not work outside their repository |
| Personal presets split into `~/bam/presets/<project>.yaml`, written by `preset add`; `--file` for presets of no one project; `~/bam/presets.yaml` still read | One growing file was hard to scan and to share piecemeal; global names keep `bam run <name>` unchanged |
| `bam target` renamed `bam preset`, with no alias; error messages say "preset". Config keys stay `targets`/`defaults` | "Target" read as a deploy target; the docs already called them presets |
| `--dry-run` on `run` | Resolution and validation can be checked without spending a build |
| Exit codes split by class (2 usage, 3 config, 4 auth, 5 Bamboo, 6 timeout) | CI must tell a failed build (1) from a failed tool |
| NDJSON for watch | A stream of events cannot be one JSON document |
| Credentials keyed by server origin, not alias | An alias in a committed file can point anywhere; keying by alias could send a token to the wrong host |
| Explicit environment token beats the keychain; `BAM_URL` + `BAM_TOKEN` for CI | Matches `GH_TOKEN` convention; `BAM_TOKEN` is bound to its own host |
| Repository-scoped server selection beats the machine's global `default_server` | A global personal default must not redirect a work repository |
| PAT only, Bamboo Data Center 9.x+; `auth:` field removed | Server is end of life; one mechanism means a smaller adapter and test matrix |
| Capabilities and last-build state moved to cache and state directories | Generated data does not belong in files people edit |
| Capabilities discovered lazily, with `doctor` exercising them proactively | Stop and trigger cannot be probed without side effects |
| `runner` renamed `app`; `credential` and `errs` split out | v0.1 logic is use cases, not batches; keychain access stays out of config tests; one place maps errors to exit codes |
| `WatchRun` moved from the provider to `app` | Polling policy and the clock belong to the use case, where they are testable |
| Terminal UI committed and moved to v0.2, before the estimator and run sets; bare `bam` reserved for it | The lazygit-style UI is the original idea's core experience; building it on top of v0.1's tested use cases adds only a view |
| Estimator, progress bar, `run --edit`, `--revision`, `--verbose` moved to v0.3 | Keeps v0.1 lean and puts the UI first; the last two depend on unconfirmed REST support |
| GitHub Actions for CI alongside the Forge-Lab Bamboo plan | Public contributors need visible status; Bamboo still dogfoods bam |
| windows/amd64 added to releases | The keychain library supports it and the build cost is one target line |
