# Changelog

All notable changes to bam are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and bam uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0, the JSON output can gain new fields, but existing fields are never renamed or removed.

## [Unreleased]

### Added

- The terminal UI rings the terminal bell once when a build or tree you are watching finishes; set `bell: false` in the machine config to turn it off.
- The terminal UI log screen wraps long lines with `w` and opens the whole log in your pager with `E`.
- In the terminal UI, `:stats` shows a plan's success rate, durations and failing jobs like `bam plan stats`, and `H` on a set lists its past runs like `bam set history`.
- In `bam ui`, `enter` on a preset shows the variables it would send and the file that holds it, `E` opens that file in your editor, and `D` removes the preset from its files after asking. `enter` in that view still opens the plan; `1` `2` `3` open the plan screen straight from the table.
- The terminal UI takes the mouse: a click selects a row and the wheel scrolls the panel under the pointer.

### Fixed

- In the terminal UI, an error leaves with the screen it happened on and a successful run clears it, a started build shows running in the Builds panel once Bamboo runs it, and `l` on a running build in the build tree opens its live stage.
- On WSL, `bam open` and `o` in the terminal UI fall back to the Windows browser when no Linux opener such as `xdg-open` is installed.


## [0.3.0] - 2026-11-02

### Added

- `bam run --repo-branch REPO=BRANCH` (repeatable, or `PLAN:REPO=BRANCH` for one plan) builds a repository branch on the plan and the child plans it triggers, through plan branches bam creates and reuses; presets hold the same under `repo_branches`, `--dry-run` shows the plan branches, and `--json` lists them under `repo_branches`.
- Personal presets can live in `~/bam/presets/`, one file per Bamboo project: every `*.yaml` there is read alongside `~/bam/presets.yaml`, `bam preset list` shows each preset's file, and `bam preset add --file NAME` writes `~/bam/presets/NAME.yaml`.
- `bam preset migrate` moves presets from your machine config (including ones under `repos:`) into `~/bam/presets.yaml`, so they work from any directory.
- Personal presets live in `~/bam/presets.yaml` (or `BAM_PRESETS`) and work from any directory; in WSL, bam uses the presets file in your Windows home, so Git Bash and WSL share one file. Presets can write their variables under `variables:`.
- `bam --project KEY` and `bam ui --project KEY` open the terminal UI on one project, and `default_project` in `.bam.yaml` or the machine file makes that the default.
- The terminal UI's `:projects` view lists the projects; `enter` shows one project's plans and `esc` goes back.
- `bam build tree` shows a build and every build it triggered, with their stages and failures, and `--watch` follows them to the end.
- `bam build tree --rerun-failed` and `bam build rerun` restart failed builds; only the newest run of a child plan counts toward the tree.
- The terminal UI shows a build's tree (`t`, `:tree`) and reruns its failed builds with `F`.
- The terminal UI streams a running build's stage logs live (`l` on a stage, or on a running build) and search highlights and counts matches as lines arrive.
- `bam build tree` and the terminal UI's tree views show what bam is reading while a tree loads.

- `bam build investigate` prints a failed build's error lines in context, its likely cause and what to try next; `--compare` shows the revisions and variables that changed since the last successful build.
- `bam server default ALIAS` sets the server bam uses when none is named.
- `bam set history` lists this repository's past set runs, newest first.
- `bam preset rm NAME` removes a preset from every file that defines it.
- `bam preset add --from BUILD` fills a preset with that build's variable values; `p` on a build in the terminal UI does the same.
- `bam build cancel --dry-run` and `bam build rerun --dry-run` show which build they would stop or restart.
- `bam open --job` and `bam url --job` point at one job of a build.
- `bam plan list --match TEXT`, and a text filter on `bam plan branches`, `plan vars`, `project list`, `preset list` and `set list`.
- Build and plan arguments accept a link copied from a Bamboo page.
- Plans and branches that Bamboo has disabled are marked `(disabled)` and their JSON gains `enabled`; `bam run --dry-run` warns before running one, and `bam run --json` lists warnings under `warnings`.
- The terminal UI's run form adds a variable the plan does not declare with `a`.
- The terminal UI keeps finished build trees for 7 days, so a tree it has seen draws at once.
- Tab completion offers plan branches, recent builds, job keys and variable names.
- `bam --version` prints the version, like `bam version`.

### Changed

- `bam run --watch` and `bam watch` follow the build and every build it triggers, as a live tree with a progress bar on each running build, and finish only when all of them have; each build that did not succeed is listed with its Bamboo link. `--json` still writes the one build's events.
- `bam preset add` writes to the plan's project file in `~/bam/presets/` (for example `proj.yaml` for `PROJ-DEPLOY`) by default; `--shared` writes `.bam.yaml`. `--machine` is removed.
- `bam target` is now `bam preset` (`bam preset add`, `list`, `show`, `sync`); `bam target` is removed. Config files keep the `targets:` key.
- The terminal UI draws the selected row as a full-width bar and opens with a header that names the server, the bam and Bamboo versions and the user, lists the keys, and titles the table, such as `plans(PROJ)[12]`.
- The terminal UI's three-panel screen is replaced by a plan screen: the plan's full name on top, its builds, the selected build's stages, and the plans that build triggered. The run form and log screen name the plan too.

- `bam preset add` writes only the variables that differ from the plan default; `--full` writes all of them.
- `bam build rerun --watch` follows the build and every build it triggers, like `bam watch`.
- A mistyped command, flag, preset, set or server alias gets a "did you mean" suggestion, and aliases, states and keys work in any letter case.
- A mistake in a config file names its line and the closest real field, and a project config saved as `.bam.yml` is reported instead of ignored.

### Fixed

- `bam plan list` and the terminal UI list every plan of a project, not only the first 25.
- A set run that cannot save its record now always ends with `set_error`; a `run_event` from a build still running could arrive after it.
- `bam build list`, `bam plan show`, `bam plan stats` and the terminal UI's build lists include queued and running builds, so `bam build list` shows the `ETA` of a running build; Bamboo leaves them out unless asked.
- `bam build show`, the build tree and the terminal UI list a failed build's failed tests on Bamboo 12, which reports them per job; `bam doctor` no longer calls failed tests unsupported there.
- `bam logs`, `--follow` and the terminal UI's log screens show a job's whole log on Bamboo 12 instead of its last 25 lines.
- Watching a build shows its progress bar again on Bamboo 9.6: Bamboo's 404 "not building" for a finished or queued build, which `bam doctor` asks about, no longer makes bam decide the server reports no progress.

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

[Unreleased]: https://github.com/rojcarranza/bam/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/rojcarranza/bam/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/r0jjames/bam-cli/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/r0jjames/bam-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/r0jjames/bam-cli/releases/tag/v0.1.0
