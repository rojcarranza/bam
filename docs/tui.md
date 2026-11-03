# Terminal UI

Running `bam` alone on a terminal opens a k9s-style table of every plan, with a plan screen behind it to browse detail, watch a build live, read its logs, run a preset, and cancel. The README shows the daily use; this page has the whole key list and the details.

`bam ui` opens it explicitly. To keep the UI out of the way, use `--no-tui`, set `BAM_NO_TUI=1`, or pipe bam's output. On a pipe, on a dumb terminal, or without a terminal, bare `bam` prints help.

Terminals under 20 rows or 60 columns keep a one-line header.

## Projects

`:projects` (or `:proj`) lists the projects, `(all)` first, `●` on the one the plans are narrowed to. `enter` shows that project's plans; `esc` goes back to the list. A configured project the server does not have is marked `missing`.

To open the UI on one project, pass `bam --project BAMCI` (or `bam ui --project BAMCI`), or set `default_project: BAMCI` in `.bam.yaml`, under a server in the machine file, or under a `repos` entry. `--project all` opens on every project.

## Nothing changes by accident

No lowercase key changes anything on the server. `R` only opens the run form, `ctrl-R` inside that form is the only key that starts a build, and `C` always asks before it cancels one.

Leaving the UI never stops a build: quitting, leaving a screen and switching server all cancel *watches*, and a watch is not a build. Only `C` in the UI, and `bam build cancel` on the command line, stop a build.

## Plan screen

`enter` on a plan opens its plan screen: the plan's full name on top, its builds on the left, and on the right the stages of the selected build and the plans that build triggered. `enter` on a triggered plan opens its own plan screen; `esc` comes back.

## Run sets

`:sets` lists the run sets with their last run. `enter` opens a set: its stages and runs, live while it runs. `R` runs it (a prompt takes optional variables, `d` previews the dry run, `ctrl-R` starts it), `U` resumes an interrupted run, `F` reruns the failed runs; resume and rerun ask again for the variables the run was started with. The train keeps running while you browse, one per window; `q` asks first and leaves it interrupted, to continue with `U` or `bam set resume`. A set run by another bam is shown and re-read every few seconds.

`H` on a set, in the sets table or on the set screen, lists its past runs, newest first, as `bam set history` does. `esc` closes it.

## Plan stats

`:stats` shows the success rate, durations and failing jobs over the last 20 builds of the plan or preset under the cursor, as `bam plan stats` does; `:stats PROJ-BUILD` or `:stats provision` names one. `esc` closes it.

## Build trees

`t` on a build, or `:tree KEY` from the command bar, opens its tree: the build and every build it triggered, with their stages, live while they run. `enter` opens the selected build and `l` its logs, `esc` comes back. `F` lists the failed builds whose parents succeeded and asks before rerunning them, then keeps watching the tree.

## Keys

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
| | `:` | command bar: :plans :presets :sets :projects :tree :stats :project :server :sort :q |
| View | `l` | logs for the selection: a stage, a job, or failed jobs; a running build: its current stage, live |
| | `a` | all logs, not only failed; in the run form or set vars prompt, add a variable |
| | `f` | follow the jobs on the log screen |
| | `w` | wrap long log lines; again to cut them |
| | `E` | open the whole log in your pager |
| | `/` | filter the focused list, or search the log |
| | `n` `N` | next / previous match, or next failure with no search |
| | `r` | refresh the focused panel |
| | `t` | the build tree of the selected build: every build it triggered |
| | `e` | expand the current error |
| | `H` | past runs of the set (sets table, set screen) |
| Run | `R` | open the run form for the selection |
| | `ctrl-R` | run (in the form) |
| | `d` | dry-run: show what would be sent (in the form) |
| | `space` | cycle an options field (in the form) |
| | `f` | use the build's revision (in the form, on the revision row) |
| | `C` | cancel the selected build (asks first) |
| | `p` | save the selected build's variables as a preset (asks for a name) |
| | `U` | resume the set's interrupted run (set screen) |
| | `F` | rerun the failed runs (set screen) or builds (tree screen) |
| | `x` | remove an added variable row (run form, set vars prompt) |
| Go | `o` | open the selection's Bamboo URL in the browser |
| | `y` | copy the selection's Bamboo URL |
| | `S` | switch server |
| | `P` | filter by project |
| | `b` | switch plan branch |
| Meta | `?` | help |
| | `q` `ctrl-c` | quit |

`y` copies through the terminal (OSC 52), so it works over SSH and in WSL with Windows Terminal. `o` needs a browser opener; on WSL it falls back to the Windows browser when no Linux opener is installed (see "Open it in Bamboo" in the README).

When a build or tree you are watching finishes, the UI rings the terminal bell once. `bell: false` in the machine config turns it off.
