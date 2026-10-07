# Changelog

## v0.2.0 — 2026-10-07

Release trains grow up: matrices, retries, one-off train files and a panel in the terminal UI, plus plan statistics and progress on servers that report none.

### Run sets

- **Matrices.** A `matrix` on a set run expands one preset into several named runs of its stage (`deploy[env=qa]`), as every combination (`{env: [qa, prod]}`) or as exact variable sets (`[{env: qa}, {env: prod}]`). Each run is recorded, resumed and retried on its own; at most 50 runs per stage.
- **`bam set rerun --failed`.** Bamboo restarts each failed build of the failed stage under the same key, rerunning only its failed jobs with the same variables and commit; a run that never triggered is triggered; then the train finishes. `bam doctor` reports whether the server can restart builds.
- **Train files.** `bam set run -f hotfix.yaml` runs a one-off train from its own file, in the shape of a `sets:` entry. `status`, `resume` and `rerun --failed` work as for any set; resume reads the file again, and `--file` follows it when it moves.
- Every failed set ends with `try: bam set rerun --failed <id>`, and `resume` shows the runs that already have a build instead of pending ones.

### Terminal UI

- `:sets` lists the sets with their last run. A set screen shows its stages and runs live; `R` runs it through a prompt for variables with a dry-run preview; `U` resumes and `F` reruns the failed runs, asking again only for the variables the run was started with, and acting at once when there are none. The train keeps running while you browse; quitting asks first and leaves it resumable. A set run by another bam is shown and followed.

### Commands

- **`bam plan stats <plan|preset>`** — over the last N builds (default 20): the success rate, median, p90 and slowest durations of successful builds, the current streak, and which jobs the failures came from. `--json` included.
- **`bam run --revision from`** reuses the commit of the `--from` build; the run form in the UI offers it with `f`.

### Progress

- On a server that does not support build progress, a running build's progress is estimated from the median of its plan branch's last 10 successful builds (at least 3). A server that supports progress but has no estimate yet still shows none. `progress` in JSON says which `source` it came from.

### JSON

Fields added, none renamed or removed: `revision_from` on run documents; `rerun`, `run` on set events; `reruns`, `name`, `file` on set records; `matrix` and `runs` on `set show`/`list`; `source` on `progress`; and the new `plan stats` document.

## v0.1.0 — 2026-10-05

First public release: the command set, the terminal UI, build progress, run editing and revisions, preset sync, and the run sets core.

### Commands

- Server registry (`server add|list|rm`), `login` / `logout` / `whoami`, and `doctor` with a capability probe. Tokens are kept in the OS keychain, a named environment variable or a 0600 file, keyed by server origin.
- Browse projects, plans, plan branches, plan variables, builds and build detail.
- Run presets (targets) in `.bam.yaml` or the machine file, with branch, default variables, allowed values, required names, `${ENV}` references, watch and timeout. `bam init` and `bam target add` generate presets from the plan's declared variables.
- `run` with `--var`, `--from N|last`, `--branch`, `--watch`, `--timeout` and `--dry-run`; `watch` with live stage and job state; `logs` with `--failed`, `--job`, `--follow` and `--tail`; `build cancel`.
- `open` and `url` for every entity, a target name or `--last`.
- `--json` on every read command, NDJSON for watch, stable exit codes, colors with glyphs and plain output on a pipe.

### Terminal UI

- `bam` alone on a terminal opens a k9s-style table of the configured projects' plans, with the last build's state, sortable and filterable, and a `:` command bar.
- `enter` opens lazygit-style panels for servers, projects, plans, builds and presets, with build detail, stages, jobs, live watch and logs.
- Run a preset or a plan through a variable form prefilled from the preset or a previous build, preview it as a dry run, cancel a build, switch server, project and branch, and open or copy any Bamboo URL.

### Running and presets

- A progress bar, percentage and remaining time from Bamboo's own build progress, on watch, `build show`, `build list`, the terminal UI and the JSON documents.
- `bam run --edit` opens the resolved variables in `BAM_EDITOR` or `EDITOR` and re-validates them before triggering.
- `bam run --revision REV` builds a chosen commit, also from the terminal UI's run form.
- `bam target sync` brings a preset back in line with its plan's variables without touching what a person wrote.

### Run sets

- Named release trains under `sets:` in the configuration: ordered stages of presets, with the runs inside a stage triggered in parallel.
- `bam set list`, `show`, `run`, `status` and `resume`. A stage starts only when the previous one succeeded, every run is resolved and validated before anything triggers, and each set run is recorded so an interrupted train can be resumed.
