# Changelog

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
