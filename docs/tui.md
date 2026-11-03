# Terminal UI

Running `bam` alone on a terminal opens a k9s-style table of every plan, with a plan screen behind it to browse detail, watch a build live, read its logs, run a preset, and cancel. The README shows the daily use; this page has the whole key list and the details.

`bam ui` opens it explicitly. To keep the UI out of the way, use `--no-tui`, set `BAM_NO_TUI=1`, or pipe bam's output. On a pipe, on a dumb terminal, or without a terminal, bare `bam` prints help.

Terminals under 20 rows or 60 columns keep a one-line header.

## Projects

`:projects` (or `:proj`) lists the projects, `(all)` first, `●` on the one the plans are narrowed to. `enter` shows that project's plans; `esc` goes back to the list. A configured project the server does not have is marked `missing`.

To open the UI on one project, pass `bam --project BAMCI` (or `bam ui --project BAMCI`), or set `default_project: BAMCI` in `.bam.yaml`, under a server in the machine file, or under a `repos` entry. `--project all` opens on every project.

## Nothing changes by accident

No lowercase key changes anything on the server. `R` only opens the run form, `ctrl-R` inside that form is the only key that starts a build (and, with repository branches, creates the plan branches it needs), and `C` always asks before it cancels one. The UI never deletes a plan branch; `d` shows how to.

Leaving the UI never stops a build: quitting, leaving a screen and switching server all cancel *watches*, and a watch is not a build. Only `C` in the UI, and `bam build cancel` on the command line, stop a build.

## Plan screen

`enter` on a plan opens its plan screen: the plan's full name on top, its builds on the left, and on the right the stages of the selected build and the plans that build triggered. `enter` on a triggered plan opens its own plan screen; `esc` comes back.

## Run sets

`:sets` lists the run sets with their last run. `enter` opens a set: its stages and runs, live while it runs. `R` runs it (a prompt takes optional variables, `d` previews the dry run, `ctrl-R` starts it), `U` resumes an interrupted run, `F` reruns the failed runs; resume and rerun ask again for the variables the run was started with. The train keeps running while you browse, one per window; `q` asks first and leaves it interrupted, to continue with `U` or `bam set resume`. A set run by another bam is shown and re-read every few seconds.

`H` on a set, in the sets table or on the set screen, lists its past runs, newest first, as `bam set history` does. `esc` closes it.

## Feature branches in the run form

A preset with `repo_branches` opens its rows in the run form, one per entry, between the variables and the revision: `repo-a` or `PROJ-CHILD:repo-a` on the left, the branch to build in the middle. `enter` edits a branch.

`b` adds a row to any plan's form. Type `REPO` or `PLAN:REPO` and `enter`, then pick the branch with `↑` `↓` from the repository's branches that Bamboo lists (`/plan/<plan>/vcsBranches`), or type it. The plan's existing plan branches follow as extra suggestions, and a server without the list leaves only those, or typing; `REPO=BRANCH` in one go works too. `x` removes a row you added; a preset's rows stay.

`d` reads Bamboo and lists each plan branch the run creates or reuses, with the warning bam run prints when it reuses one. `ctrl-R` creates the missing plan branches without asking, starts the root plan's branch and follows the child plans' branch builds in the plan screen and `t`. The same plans and rules apply as `bam run --repo-branch`: a repository that is not a plan's default repository, or a plan with no build, is refused with the same message.

The UI never deletes plan branches, so the next run with the same name reuses them. To remove one, delete it on its plan's Branches page in Bamboo, or with `DELETE /rest/api/latest/plan/<branch key>`; `d` prints this too.

The presets table shows the repository branch in its BRANCH column, such as `repo-a=feature/PROJ-101 +1`.

## Plan stats

`:stats` shows the success rate, durations and failing jobs over the last 20 builds of the plan or preset under the cursor, as `bam plan stats` does; `:stats PROJ-BUILD` or `:stats provision` names one. `esc` closes it.

## Build trees

`t` on a build, or `:tree KEY` from the command bar, opens its tree: the build and every build it triggered, with their stages, live while they run. `enter` opens the selected build and `l` its logs, `esc` comes back. `F` lists the failed builds whose parents succeeded and asks before rerunning them, then keeps watching the tree.

## Mouse

A left click selects the row under the pointer and focuses its panel, on Home's tables and the plan screen's Builds, Stages and Tree. It only selects: `enter` still opens. The wheel scrolls the panel under the pointer, three rows a notch, and scrolls the log screen. The wheel also scrolls the help and stats overlays while the pointer is over them. Prompts, other overlays and the other screens ignore the mouse. The UI takes the mouse from your terminal, so select text with `shift` held (`option` in some macOS terminals).

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
| Home | `enter` | open the plan screen; on a preset, show what it sends |
| | `E` | edit the selected preset's file |
| | `D` | remove the selected preset (asks first) |
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
| | `ctrl-R` | run (in the form); creates the plan branches of repository branch rows |
| | `d` | dry-run: show what would be sent, and the plan branches it creates or reuses (in the form) |
| | `space` | cycle an options field (in the form) |
| | `f` | use the build's revision (in the form, on the revision row) |
| | `C` | cancel the selected build (asks first) |
| | `p` | save the selected build's variables as a preset (asks for a name) |
| | `U` | resume the set's interrupted run (set screen) |
| | `F` | rerun the failed runs (set screen) or builds (tree screen) |
| | `x` | remove an added variable or repository branch (run form, set vars prompt) |
| Go | `o` | open the selection's Bamboo URL in the browser |
| | `y` | copy the selection's Bamboo URL |
| | `S` | switch server |
| | `P` | filter by project |
| | `b` | switch plan branch; in the run form, add a repository branch |
| Meta | `?` | help |
| | `q` `ctrl-c` | quit |

`y` copies through the terminal (OSC 52), so it works over SSH and in WSL with Windows Terminal. `o` needs a browser opener; on WSL it falls back to the Windows browser when no Linux opener is installed (see "Open it in Bamboo" in the README).

When a build or tree you are watching finishes, the UI rings the terminal bell once. `bell: false` in the machine config turns it off.
