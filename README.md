# bam

A terminal remote control for Atlassian Bamboo: trigger, watch and diagnose builds without the browser, and get the Bamboo link the moment you need it.

    bam run provision-lab --var cluster_name=alpha --watch
    bam logs --last --failed
    bam open --last

bam works with Bamboo Data Center 9.x and later, using a personal access token.

## Install

bam is one binary for Linux, macOS and Windows. It needs nothing else at run time.

**With Go 1.26 or newer:**

    go install github.com/r0jjames/bam-cli/cmd/bam@latest

This installs the latest release into `$(go env GOPATH)/bin`, usually `~/go/bin`. Add that directory to your PATH if `bam` is not found. Run the same command to upgrade. `@main` installs the newest unreleased changes.

**Without Go:** download the archive for your platform from the [releases page](https://github.com/r0jjames/bam-cli/releases), unpack it, and put `bam` (`bam.exe` on Windows) on your PATH. For example, on Linux x86-64 with the version you want:

    v=0.2.1
    curl -fsSL "https://github.com/r0jjames/bam-cli/releases/download/v${v}/bam_${v}_linux_amd64.tar.gz" | tar -xz -C ~/.local/bin bam

In WSL, install the Linux build: WSL runs Linux, so the Windows archive is not the one you need.

Check the install:

    bam version     # prints the installed version
    type bam        # make sure no other command named bam comes first

## Quick start

1. Register your server with the Bamboo projects you work in, then log in. `login` asks for a personal access token, which you create in Bamboo under your profile → Personal access tokens; bam prints the link.

       bam server add work --url https://bamboo.example.com --project PROJ --project OPS
       bam login work
       bam doctor

   `--project` is repeatable. It sets the projects that the terminal UI and plan lists show. To change them later, run `server add` again with `--force`, or edit `projects` under the server in the machine file (`~/.config/bam/config.yaml` on Linux and WSL; see [Configuration](docs/configuration.md)). The token stays: bam stores it by server URL, not by alias.

2. Open the terminal UI, or create `.bam.yaml` in your repository with presets generated from your plans' variables:

       bam
       bam init --server work --project PROJ --plan PROJ-BUILD --plan PROJ-PROV=provision-lab

   Review the generated file, then commit it. It holds no secrets.

3. Save your own presets. They go in `~/bam/presets/`, one file per Bamboo project, and work from any directory:

       bam preset add deploy-qa --plan PROJ-DEPLOY      # reads the plan's variables from Bamboo
       bam preset list                                  # shows the file each preset is in

   Open `~/bam/presets/proj.yaml`, set the values under `variables`, and delete the ones you never change. Git Bash, WSL and macOS can share one directory; see [Presets](docs/presets.md#one-presets-directory-for-git-bash-wsl-and-macos).

4. Run, watch, and investigate:

       bam run deploy-qa --dry-run                      # check the variables, trigger nothing
       bam run provision-lab --var cluster_name=alpha --watch
       bam logs --last --failed
       bam open --last

A teammate who clones the repository only needs `bam login work`.

To try bam without a Bamboo server, run it against the fake Bamboo built from this repository's test fixtures: see [Try bam locally](docs/try-locally.md).

## Terminal UI

Running `bam` alone on a terminal opens a k9s-style table of every plan, with
a plan screen behind it to browse detail, watch a build live, read its
logs, run a preset, and cancel.

It opens on the plans table:

```
 Server:  lab [bamboo.lab.example]   <enter> drill in          <P> filter by project     <y> copy the URL
 Version: 9.6.4                      <R> open the run form     </> filter or search      <:> command bar
 User:    jdoe                       <o> open in the browser   <s> next sort column      <?> help
──────────────────────────────────────────────────── plans(all)[4] ───────────────────────────────────────── sort key↑ ─
  PROJECT  KEY          NAME                                                    STATE             #  AGE    BY
▸ OPS      OPS-NIGHTLY  Nightly                                                 ✓ success         9  now    commit
  OPS      OPS-OLD      Archived                                                –                 –  –      –
  PROJ     PROJ-BUILD   Build and test                                          ✗ failed          4  12m    jdoe
  PROJ     PROJ-PROV    Provision lab                                           ✓ success         8  3h     sched

 enter open  / filter  s sort  : cmd  R run  ?help  q quit
```

Terminals under 20 rows or 60 columns keep the one-line header.

`:projects` (or `:proj`) lists the projects, `(all)` first, `●` on the one the plans are narrowed to. `enter` shows that project's plans; `esc` goes back to the list. A configured project the server does not have is marked `missing`.

To open the UI on one project, pass `bam --project PROJ` (or `bam ui --project PROJ`), or set `default_project: PROJ` in `.bam.yaml`, under a server in the machine file, or under a `repos` entry. `--project all` opens on every project.

No lowercase key changes anything on the server. `R` only opens the run form,
`ctrl-R` inside that form is the only key that starts a build, and `C` always
asks before it cancels one.

`enter` on a plan opens its plan screen: the plan's full name on top, its builds on the left, and on the right the stages of the selected build and the plans that build triggered. `enter` on a triggered plan opens its own plan screen; `esc` comes back.

```
 Build and test
 PROJ › PROJ-BUILD                               ✗ failed last build #44 3m ago
────────────────────────────────── builds[2] ───────────────────────────────────
┏━Builds━━━━━━━━━━━━━━━━━━━━━━━┓╭─Stages · #44─────────────────────────────────╮
┃▸ ✗ #44    main  3m  –        ┃│Manual run by jdoe                            │
┃  ✓ #43    main  1h  –        ┃│                                              │
┃                              ┃│▸ ✓ Build                      42s            │
┃                              ┃│  ✗ Test                       2m30s          │
┃                              ┃│    ✗ Integration              2m30s          │
┃                              ┃│  – Deploy                                    │
┃                              ┃│                                              │
┃                              ┃│3 failed tests                                │
┃                              ┃╰──────────────────────────────────────────────╯
┃                              ┃╭─Tree · what #44 triggered────────────────────╮
┃                              ┃│▸ ✗ PROJ-PROV  #7    failed                   │
┃                              ┃│  – Nightly  not run                          │
┃                              ┃│                                              │
┃                              ┃│                                              │
┃                              ┃│                                              │
┃                              ┃│                                              │
┃                              ┃│                                              │
┃                              ┃│                                              │
┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛╰──────────────────────────────────────────────╯
lab · 9.6.4 · jdoe       enter stages  R run  l logs  t tree  / filter  esc back
```

`bam ui` opens it explicitly. To keep the UI out of the way, use `--no-tui`,
set `BAM_NO_TUI=1`, or pipe bam's output — on a pipe, on a dumb terminal, or
without a terminal, bare `bam` prints help exactly as before.

### Run sets

`:sets` lists the run sets with their last run. `enter` opens a set: its stages and runs, live while it runs. `R` runs it (a prompt takes optional variables, `d` previews the dry run, `ctrl-R` starts it), `U` resumes an interrupted run, `F` reruns the failed runs; resume and rerun ask again for the variables the run was started with. The train keeps running while you browse, one per window; `q` asks first and leaves it interrupted, to continue with `U` or `bam set resume`. A set run by another bam is shown and re-read every few seconds.

### Build trees

`t` on a build, or `:tree KEY` from the command bar, opens its tree: the build and every build it triggered, with their stages, live while they run. `enter` opens the selected build and `l` its logs, `esc` comes back. `F` lists the failed builds whose parents succeeded and asks before rerunning them, then keeps watching the tree.

### Keys

| | Keys | Does |
| --- | --- | --- |
| Move | `j` `k` `↑` `↓` | up / down in the focused panel |
| | `g` `G` | first / last row |
| | `tab` | next panel: Builds, Stages, Tree |
| | `shift-tab` | previous panel |
| | `1` `2` `3` | focus Builds, Stages, Tree (on Home: open the plan screen there) |
| | `enter` | drill in: a build's stages, expand a stage, open a triggered plan |
| | `esc` | back one level, close an overlay; from Stages or Tree to Builds; from a triggered plan to its parent; from Builds to Home; from a project's plans to `:projects` |
| Home | `enter` | open the plan screen on the row's plan |
| | `s` | next sort column |
| | `:` | command bar: :plans :presets :sets :projects :tree :project :server :sort :q |
| View | `l` | logs for the selection: a stage, a job, or failed jobs; a running build: its current stage, live |
| | `a` | all logs, not only failed; in the set vars prompt, add a variable |
| | `f` | follow the jobs on the log screen |
| | `/` | filter the focused list, or search the log |
| | `n` `N` | next / previous match, or next failure with no search |
| | `r` | refresh the focused panel |
| | `t` | the build tree of the selected build: every build it triggered |
| | `e` | expand the current error |
| Run | `R` | open the run form for the selection |
| | `ctrl-R` | run (in the form) |
| | `d` | dry-run: show what would be sent (in the form) |
| | `space` | cycle an options field (in the form) |
| | `f` | use the build's revision (in the form, on the revision row) |
| | `C` | cancel the selected build (asks first) |
| | `p` | save the selected build's variables as a preset (asks for a name) |
| | `U` | resume the set's interrupted run (set screen) |
| | `F` | rerun the failed runs (set screen) or builds (tree screen) |
| | `x` | remove a variable row (set vars prompt) |
| Go | `o` | open the selection's Bamboo URL in the browser |
| | `y` | copy the selection's Bamboo URL |
| | `S` | switch server |
| | `P` | filter by project |
| | `b` | switch plan branch |
| Meta | `?` | help |
| | `q` `ctrl-c` | quit |

Leaving the UI never stops a build: quitting, leaving a screen and switching
server all cancel *watches*, and a watch is not a build. Only `C` in the UI,
and `bam build cancel` on the command line, stop a build.

## Everyday commands

| I want to… | Command |
| --- | --- |
| see my plans | `bam plan list` |
| see a plan's variables and what the last build used | `bam plan vars provision-lab` |
| run with the previous build's variables | `bam run provision-lab --from last` |
| check variables without running | `bam run provision-lab --dry-run` |
| review and change variables in your editor, then run | `bam run provision-lab --edit` |
| save my own variable values as a preset | `bam preset add NAME --plan KEY` (see [presets](docs/presets.md)) |
| change the last (or build #41's) variables, then run | `bam run provision-lab --from 41 --edit` |
| build a chosen commit instead of the newest | `bam run provision-lab --revision abc1234` |
| rebuild the commit an earlier build used | `bam run provision-lab --from 41 --revision from` |
| see recent builds | `bam build list provision-lab` |
| is this plan flaky? | `bam plan stats provision-lab` |
| follow a running build | `bam watch --last` |
| see every build a build triggered, and where it failed | `bam build tree --last` |
| follow a build and all its sub-pipelines until they finish | `bam build tree PROJ-API-142 --watch` |
| retry the failed parts of a run | `bam build tree --last --rerun-failed` |
| read only the failed jobs' logs | `bam logs --last --failed` |
| stop a build | `bam build cancel --last` |
| open anything in Bamboo | `bam open PROJ-PLAN-123` |
| run a release train of presets, stage by stage | `bam set run release` |
| continue a train after Ctrl-C | `bam set resume` |
| run a one-off train from its own file | `bam set run -f hotfix.yaml` |
| retry a failed train's failed runs, then finish it | `bam set rerun --failed` |

`--revision` takes a full or short SHA and passes it to Bamboo as given; Bamboo applies it to the plan's default repository. A revision Bamboo cannot build ends as "not built", so add `--watch` to see it. If the server ignores the revision, bam stops the build it queued and exits 5. `--revision from` reuses the commit of the `--from` build, together with its variables.

Every command has `--help`, and every read command has `--json`. The full reference is in [docs/cli](docs/cli/bam.md).

## Release trains

A set names several presets and runs them in ordered stages: every run of a stage at once, the next stage only when all of them succeeded.

```yaml
sets:
  release:
    stages:
      - [publish-specs]
      - [provision, {target: deploy, vars: {env: qa}}]
```

```
$ bam set run release
release  release-20260929T101500Z
stage 1/2
  ✓ publish-specs  PROJ-SPEC-12  Success  1m12s
stage 2/2
  ✓ provision  OPS-PROV-40  Success  1m30s
  ✗ deploy     OPS-DEP-9    Failed   48s

set failed: stage 2 (1 of 2 runs failed)
  try: bam logs OPS-DEP-9 --failed
```

A `matrix` runs one preset over several variable sets in the same stage: `matrix: {env: [qa, staging]}` runs every combination, `matrix: [{env: qa}, {env: prod, region: us}]` exactly those. Each run is named, for example `deploy[env=qa]`, and is resumed and rerun on its own.

A failure never cancels a build. Ctrl-C stops watching; `bam set status` and `bam set resume` pick the train up again. After a failure, `bam set rerun --failed` has Bamboo restart the failed builds (only their failed jobs run again, with the same variables and commit) and then finishes the train. See [Sets](docs/configuration.md#sets-release-trains) for the rules.

## Build trees

When a plan triggers child plans, each child is a separate build in Bamboo. `bam build tree` shows a build and every build it caused, to any depth, with their stages and what failed:

```
$ bam build tree PROJ-API-142
PROJ-API-142              ✗ failed   12m03s
├─ stage Build            ✓ success  2m10s
├─ stage Test             ✓ success  4m55s
├─ OPS-DEPLOY-88          ✓ success  3m12s
│  ├─ stage Deploy        ✓ success  3m12s
│  └─ OPS-SMOKE-51        ✗ failed   1m40s   3 tests failed
│     └─ stage Smoke      ✗ failed   1m40s   Smoke tests
└─ PROJ-DOCS-30           ▸ running  45s
next: bam logs OPS-SMOKE-51 --failed
```

To watch a running build and its sub-pipelines live:

```bash
bam build tree PROJ-API-142 --watch
```

- The argument works as in `bam build show`: a build key, a plan or preset (its latest build, with `--branch`), or `--last`.
- `--watch` follows the tree until every build has finished, including children that start after their parent ends. On a terminal the tree redraws in place as builds start, run and finish; Ctrl-C stops watching only. To follow a single build without its children, use `bam watch`.
- `--failed` shows only the branches that lead to a failure; `--depth N` limits the levels; `--json` prints the tree as one document.
- It exits 0 when every build succeeded and 1 when any did not. Without `--watch`, a tree that is still running exits 0 and suggests `--watch`.

To retry the failures, `bam build tree PROJ-API-142 --rerun-failed` lists the failed builds whose parents succeeded and asks before restarting them. Bamboo reruns only their failed jobs, under the same build keys, and triggers their children again when they succeed; bam then follows the tree until it has finished. `--yes` skips the question (required without a terminal), `--no-watch` restarts and exits. `bam build rerun OPS-SMOKE-51` restarts one build. Only the newest run of a child plan counts: an older one shows as `earlier run`.

Bamboo's API does not list a plan's child plans, so bam finds a child by its reason, "Child of PROJ-API-142", in the last 25 builds of each plan in your projects and in the build's own project. A `children:` map in the configuration narrows that and shows a child that never ran; see [Child plans](docs/configuration.md#child-plans-children).

## More

- [Configuration and presets](docs/configuration.md)
- [Presets and re-running with other variables](docs/presets.md)
- [Authentication](docs/authentication.md)
- [Using bam in CI](docs/ci.md)
- [JSON output](docs/json.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Try bam locally, without a Bamboo](docs/try-locally.md)
- [Development setup](SETUP.md) — Go, make and linter on macOS and Linux

## License

MIT
