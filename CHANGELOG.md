# Changelog

All notable changes to bam are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and bam uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0, the JSON output can gain new fields, but existing fields are never renamed or removed.

## [Unreleased]

### Fixed

- `Ctrl-C` during `bam set rerun --failed` keeps the restarts it still owes, so `bam set resume` restarts those builds instead of watching the old failed ones, and `Ctrl-C` while a run is being triggered no longer records a failed trigger. `bam set status` shows a run waiting for its restart as `Restart due`. Older set records still load; `--json` gains `restart` on a run that is due.

### Added

- A pinned pipeline shows its branches. A pin written as a plan key without a branch lists, under the plan's own line, the plan branches that built in the last 7 days, newest first, up to 10 and then `+N more branches`; Needs you raises a failed or slow branch like a pin. `PLAN@branch` pins and presets stay as they are. `bam top --once` prints the branch lines, and `--json` gains `branches` and `branches_more` on each pipeline.
- `bam top` shows your part of the queue. With `agents:`, a pinned pipeline or `projects:` set, the queue lists the builds that wait for your agents, your pins or your projects with their true place, and counts the rest on one line. Needs you raises only your builds, grouped by reason, a wait of 48 hours or more reads as days and hours (`57d 4h`) in the board, `bam top --once` and `bam queue`, and a wait bam did not read shows a dash instead of "at least". The agent summary reads `N building · N idle · N down · N disabled`, the two small charts print their lowest and highest value, and help says what each panel lists. `a` and `bam top --all` show the whole queue; `--json` gains `mine` on each queued build and `queue_others`.
- `bam top` shows the health of the agents you support. List the start of their names under `agents:` or with `bam agent mine add NAME-START`, and the board's Agents panel becomes Your agents: how many are building, idle, down or disabled, one row per pool, and the names of the ones that are down or disabled. Needs you raises your agents that are down or disabled, and an agent Bamboo no longer lists shows as down for 30 days. `a` and `bam top --all` show the whole server, `bam agent list --mine` filters the agent list, and `--json` gains `agents.mine`. With `agents:` set the board reads agent details for your agents only.
- `bam build show` and `bam build investigate` print each failed test's message under its name. `--json` gains `failed_test_details` beside `failed_tests`. Secret-looking values in a message print as `********`.
- `bam status` lists every build you started with bam in the last day, across repositories and servers, newest first, with its live state, progress and link. Inside a repository the build of the checked-out branch comes first. `bam status --short` prints one line for a shell prompt. bam keeps the last 50 runs it started in its state file: build, plan, preset, server and time, never a variable value.
- A queued build says its place in the queue, such as `queue 3 of 7`, in `bam build show`, `bam watch` and the tree. `bam queue` lists what the server has queued, first in line first, and `bam agent list` lists the build agents with online, enabled and busy. `--json` gains `queue_position` and `queue_length` on a queued build.
- `--notify` on `bam run`, `bam watch` and `bam build rerun`: when the watched build finishes, bam rings the terminal bell and raises a desktop notification where the system has one (`osascript` on macOS, `notify-send` on Linux).
- A finished `bam run --watch`, `bam watch`, `bam build tree` and `bam set run` print the failed tests and the likely cause under each failed build, up to five builds, with secret-looking values masked. `bam build investigate` on a build whose child failed investigates the failed builds of the whole tree. `--json` is unchanged.
- `bam build show` prints how long a build waited for an agent on its Queued line, and `--json` fills `queued_at` and `queue_duration_ms` and gives each job `queue_duration_ms` and `checkout_duration_ms`. `bam plan stats` and `:stats` add the wait for an agent (median, 90th percentile and its share of job time), the slowest stages and jobs by median run time, and the same figures for the window before when `-n` leaves room; `--json` gains `wait`, `wait_earlier`, `slowest_stages`, `slowest_jobs` and `unread_builds`. `plan stats` now reads every finished build in the window, not only the failed ones.
- Commands that only read (`watch`, `logs`, `build show`, `build tree`, `build investigate`, `url`, `open`) use the last build you started from this repository when you name none, and `build list` lists its plan. `bam tree`, `bam investigate` (also `bam why`) and `bam cancel` work without `build`, `ls` works wherever `list` does, `-w` stands for `--watch` and `-b` for `--branch`, and bam accepts `PROJ-BUILD 482`, a bare `482`, a preset typed with capitals, a plan after `bam preset add NAME` and a URL after `bam server add ALIAS`. `cancel` and `rerun` still need a key or `--last`.
- `bam top` prints a board: the server's version, what needs you (an enabled agent that is offline, a build waiting over 5 minutes, a pinned pipeline whose last build failed, a pinned build running half again past its usual time), agent counts and every agent that is offline or disabled, the queue with the time each build waited, and your pinned pipelines by group with their last 10 results. Pin pipelines under `monitor:` or with `bam pin add`, `bam pin rm` and `bam pin list`. When Bamboo refuses the agent list or the queue, the board says so and prints the rest. `--json` prints the same board. `bam queue` gains a WAITED column, and `--json` gains `waited_ms` and `waited_at_least`.
- On a terminal, `bam top` and `:top` in the UI open the board live: it refreshes by itself (agents, queue and running builds every 15 seconds, the rest every minute), shows the busy share of agents and a running build's time against its usual time, and rings the bell once when something new needs you. `enter` opens a pipeline or build, `l`, `t`, `i`, `o` and `y` work as elsewhere, and `P` pins or unpins. Bare `bam` opens the board once pipelines are pinned. `bam top --once`, `--json` and a pipe still print the board.
- A waiting build says why it waits: no agent can run it, agents can run it but all are busy, offline or disabled, agents are free and it is next, or agents are free and other builds come before it, naming the agents when three or fewer can run the job. It shows as a WHY column in `bam queue` and the `bam top` queue, and beside the queue place in `bam build show`, `bam watch` and the tree. Naming the agents and saying whether they are free needs a Bamboo administrator; a part your account cannot read is left out. bam reads which jobs each agent can run once an hour and keeps them in its cache directory, so the board and repeated commands do not read every agent again. `--json` gains `why`, `why_case`, `any_agent_can_run` and `agents_that_can_run`.
- The live `bam top` board uses a tall or wide terminal: from 40 rows every panel keeps an empty row above and below its lines and two empty columns inside its border, Needs you shows all its lines (with a count in its title, such as `Needs you · 13 of 25`, and scrolling once it fills a third of the rows) instead of 4, and from 160 columns Needs you, Agents and the Queue stand on the left with Pinned the full height on the right. Under 40 rows the board is as before.
- A pinned pipeline on the live `bam top` board shows the pipelines its build started as a tree under its row, the way `bam build tree` prints them: every pipeline and every stage, with its state, time and the stage a running or failed one is in (`3/5 Run integration builds`). The tree is of the build running now, else the newest finished one. `space` folds or unfolds it, `j` and `k` move over its pipelines, `enter` opens one and `t` opens its tree screen; when Pinned is too short, finished trees whose pipelines all succeeded fold first, then the longest tree is cut. A failed child pipeline adds a Needs you line. A running tree refreshes once a minute and reads only what runs. `bam top --once --tree` prints the trees, and `--json` with `--tree` gains `tree` on each pin.
- `bam pin add GROUP PIN...` takes several pins at once, and a pin can be a plan's name: an exact name (any case) wins, else the plans whose name holds the words. bam stores the plan key and prints key and name; a name that fits several plans lists up to 10 and pins nothing, as does a name that fits none, and one bad pin leaves the whole command unwritten. bam looks the name up with Bamboo's plan search, or in the plans of your configured projects when the server has none. Pinned pipelines show the plan's name on the board and in `bam top --once`, `bam pin list` prints it, and `--json` gains `name` (`names` on `bam pin list`). In the terminal UI, `m` pins the selected plan from the plans table or a plan's screen.

## [0.4.0] - 2026-11-10

### Added

- `bam watch @` waits for Bamboo to start a build of the commit you have checked out, then follows it and the builds it triggers: `git push && bam watch @`. The plan is the one you last ran in this repository; `bam watch PLAN --branch @` names another. A commit that is not pushed has no build, so bam says so and follows the newest build of the branch; with no build of it within `--timeout` (10 minutes by default) bam exits with the timeout code.
- `@` stands for the branch checked out in the current directory: `--branch @`, `--repository NAME=@` and `@` in a preset's `branch:` or `repositories:`. When the plan has no plan branch for it, bam says so, names the closest one and prints Bamboo's create link; it creates nothing.
- `bam preset add`, `bam init --plan` and the terminal UI's save-as-preset write the server, the plan branch and `watch: true` as active lines, and list the plan branch's repositories under `repositories:`. A plan branch key given as `--plan` is written as its main plan plus the branch. `--full` no longer does anything.
- `repositories:` in a preset and `--repository NAME=BRANCH` on `bam run`: bam checks which branch each repository built in the plan branch's newest finished job, opens the Repositories tab link when one differs, waits for Enter, then triggers. `bam preset add` writes the current branches. `--json` gains `repositories`.
- The terminal UI rings the terminal bell once when a build or tree you are watching finishes; set `bell: false` in the machine config to turn it off.
- The terminal UI log screen wraps long lines with `w` and opens the whole log in your pager with `E`.
- In the terminal UI, `:stats` shows a plan's success rate, durations and failing jobs like `bam plan stats`, and `H` on a set lists its past runs like `bam set history`.
- In `bam ui`, `enter` on a preset shows the variables it would send and the file that holds it, `E` opens that file in your editor, and `D` removes the preset from its files after asking. `enter` in that view still opens the plan; `1` `2` `3` open the plan screen straight from the table.
- The terminal UI takes the mouse: a click selects a row and the wheel scrolls the panel under the pointer.
- In the terminal UI plan screen, Stages lists the failed test names, `i` shows each failed job's error lines and the likely cause like `bam build investigate`, and `F` on a failed build asks, then reruns its failed jobs.

### Changed

- `bam preset add`, `bam init --plan`, `p` in the terminal UI and `bam preset sync` write presets without per-line comments, except `# not declared by the plan`, and no longer write `required` or commented example keys; `bam preset add` then prints a box of up to three values the plan's last 10 builds ran with, for each variable that varied.
- **Breaking:** `repo_branches` and `--repo-branch` are removed, with plan-branch creation and the TUI repository-branch rows. A preset that still sets `repo_branches` fails with `unknown field`. `--json` keeps `repo_branches` as an empty list.
- On macOS and Windows, bam moves `config.yaml`, `credentials.yaml` and the fixture denylist from the old config directory (`~/Library/Application Support/bam`, `%LOCALAPPDATA%\bam`) into `~/.config/bam` on first run and prints one line per file, instead of reading the old directory for good; a file already in `~/.config/bam` is never replaced.

### Fixed

- `go install github.com/rojcarranza/bam/cmd/bam@latest` works: the module path now matches the project's address.
- `bam preset add`, `p` in the terminal UI and `bam preset sync` write every variable the plan declares, plan defaults filled in, so the preset is ready to fill; a secret the plan stores is a commented line.
- In the terminal UI, an error leaves with the screen it happened on and a successful run clears it, a started build shows running in the Builds panel once Bamboo runs it, and `l` on a running build in the build tree opens its live stage.
- On WSL, `bam open` and `o` in the terminal UI fall back to the Windows browser when no Linux opener such as `xdg-open` is installed.
- With `HOME` unset, bam no longer reads or writes a `.config/bam` directory under the current directory.


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

[Unreleased]: https://github.com/rojcarranza/bam/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/rojcarranza/bam/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/rojcarranza/bam/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/rojcarranza/bam/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/rojcarranza/bam/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/rojcarranza/bam/releases/tag/v0.1.0
