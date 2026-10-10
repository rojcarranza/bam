# Changelog

All notable changes to bam are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and bam uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0, the JSON output can gain new fields, but existing fields are never renamed or removed.

## [Unreleased]

### Added

- The terminal UI's `:projects` view lists the projects; `enter` shows one project's plans and `esc` goes back.
- `bam build tree` shows a build and every build it triggered, with their stages and failures, and `--watch` follows them to the end.
- `bam build tree --rerun-failed` and `bam build rerun` restart failed builds; only the newest run of a child plan counts toward the tree.
- The terminal UI shows a build's tree (`t`, `:tree`) and reruns its failed builds with `F`.
- The terminal UI streams a running build's stage logs live (`l` on a stage, or on a running build) and search highlights and counts matches as lines arrive.

### Changed

- The terminal UI draws the selected row as a full-width bar and opens with a header that names the server, version and user, lists the keys, and titles the table, such as `plans(PROJ)[12]`.

## [0.2.1] - 2026-10-08

### Changed

- The terminal UI names plans, not just their keys: in the Plans panel when it fits, in the Builds and build titles, and in the main panel while you move through plans.

### Fixed

- `bam version` prints the real version for `go install` builds instead of `dev`.

## [0.2.0] - 2026-10-07

### Added

- Matrix runs in sets: one preset runs over several variable sets in the same stage.
- `bam set rerun --failed` retries a train's failed builds in Bamboo, then finishes the train.
- Train files: `bam set run -f FILE` runs a one-off train without adding it to `.bam.yaml`.
- A run sets screen in the terminal UI (`:sets`) to run, resume and rerun trains and watch them live.
- `bam plan stats` shows a plan's success rate, typical duration and the jobs that fail most.
- `bam run --revision from` rebuilds the commit an earlier build used.
- Servers that don't report build progress now get an estimate from recent builds.

## [0.1.0] - 2026-10-05

### Added

- Servers, login and `bam doctor`.
- Browsing projects, plans, branches, variables and builds.
- Presets with default, allowed and required variables, generated from a plan by `bam init` and `bam target add`, and kept current by `bam target sync`.
- `bam run` with variables, `--from`, `--branch`, `--revision`, `--edit`, `--watch` and `--dry-run`.
- `bam watch`, `bam logs`, `bam build cancel`, `bam open` and `bam url`.
- Progress bars from Bamboo's own build estimate.
- Run sets: presets in ordered stages, with `bam set run`, `status` and `resume`.
- A terminal UI with a plans table, panels for builds, stages and logs, and a run form.
- `--json` on every read command, and stable exit codes.

[Unreleased]: https://github.com/r0jjames/bam-cli/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/r0jjames/bam-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/r0jjames/bam-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/r0jjames/bam-cli/releases/tag/v0.1.0
