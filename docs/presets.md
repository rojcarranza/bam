# Presets and re-running with other variables

Two jobs this page covers, each from the command line and from the terminal UI:

1. Save a plan's variable values once as a **preset**, then run it by name.
2. Re-run a plan with the variables of its **last build** (or a build you pick), change some of them, and run.

The examples use the plan `PROJ-DEPLOY` and the variables `target_env`, `region` and `version`. Substitute your own plan key.

## 1. Create a personal preset

A preset is a named entry in a **presets file** in your presets directory, `~/bam/presets/`, one file per Bamboo project. Generate it from the plan instead of typing the variable names:

    bam preset add deploy-qa --plan PROJ-DEPLOY --branch main

On a terminal, bam then opens a small form under the prompt, with one row per variable the preset will write, so you set the values before anything is saved and never open the file afterwards:

    new preset deploy-qa · PROJ-DEPLOY · branch main → ~/bam/presets/proj.yaml
    > region        eu  plan default
      target_env    staging  changed
      version       ____  no plan default
      deploy_token  plan's stored value  secret · written as a commented line
      1 of 4 differ from the plan: target_env
      ↑↓ move · enter edit · ←→ choices · ctrl-s save · esc cancel

- `↑` `↓` (or `tab`) move between rows. `enter` types a value; `enter` again accepts it and moves on, `esc` keeps the old one.
- `←` `→` step through the row's choices: the value the row starts on (the plan default, or the `--from` build's value), the plan default, then up to three values the last 10 builds ran with, newest first. The recent values load while the form is open.
- A secret row never shows or takes a value. `←` `→` switch between the plan's stored value (a commented line) and `${ENV_NAME}`; `enter` edits only the variable name.
- `ctrl-s` writes the file with your values. `esc` or `ctrl-c` writes nothing and exits 130. Every variable the plan declares is written either way.
- A name that is already taken fails before the form opens.

Leave out the name or the plan and bam asks for it first on a terminal: `bam preset add` alone shows a list of the plans in the `projects:` of `.bam.yaml` (without one it says so, as `bam plan list` does), then the plan's branches, then asks for the name (it starts as the plan's own), then opens the form. `bam preset add --plan PROJ-DEPLOY` asks only for the name.

The form and these questions never open with a pipe, `TERM` empty or `dumb`, `--no-tui`, `BAM_NO_TUI`, `--json` or `--print`, or for a plan with no variables. bam then writes the plan's values at once, as before, and a missing name is a usage error.

- `deploy-qa` is a name you choose. It is what you type later in `bam run deploy-qa`, and what the terminal UI lists under presets. It is not a Bamboo name: lowercase letters, digits, `-` and `_`, starting with a letter.
- `--plan` is the Bamboo plan key. `--branch` is optional and sets the plan branch the preset runs.
- By default bam writes the plan's project file, `~/bam/presets/proj.yaml` for `PROJ-DEPLOY`, and creates the directory and file the first time. Its presets work from any directory. `bam preset list` shows which file each preset is in.
- `--file NAME` writes `~/bam/presets/NAME.yaml` instead, for presets that belong to no one project: `bam preset add deploy-package --plan PROJ-PKG --file custom-presets`. `NAME` is a plain file name (letters, digits, `-`, `_`, `.`); a path or `..` is refused.
- `--shared` writes `.bam.yaml` at the repository root instead, which is meant to be committed and shared with the team. Keep personal presets, and any plan key or host that should not enter the repository, in your presets directory.
- `--print` shows the generated YAML and writes nothing. Use it to look first.

The server is chosen the usual way (`--server`, or your only configured server; see [Configuration](configuration.md#which-server-is-used)).

### Complete preset names with Tab

Press Tab after `bam run`, `bam watch`, `bam pin add GROUP` and the other commands that take a plan or preset, and bam lists your presets with their plan and branch: `deploy-qa  PROJ-DEPLOY · main`. A preset that fails validation (a bad plan key, say) is left out and the others still show, and a name defined in two presets files still shows, so Tab keeps working while you fix the preset; `bam preset list` and `bam run` name the problem. A presets file that cannot be read at all (an unknown key, broken YAML) empties the list until you fix it.

Completion needs a one-time setup per shell, with `bam completion SHELL`:

- bash: install the `bash-completion` package, then add `source <(bam completion bash)` to `~/.bashrc`. Without the package Tab lists files.
- zsh: enable `compinit` first (`autoload -U compinit && compinit`), then add `source <(bam completion zsh)` to `~/.zshrc`.
- fish: `bam completion fish > ~/.config/fish/completions/bam.fish`.

### Pick a preset from a list

Type `bam run` with no name, on a terminal, and bam opens a small picker under the prompt instead of a full screen. Each row shows the preset name, its plan and branch, and the file that defines it. Type to filter: any part of a row matches, ignoring case, so `develop` finds every preset on that branch and `lab.yaml` every preset in that file. The line under the list shows what the highlighted preset sets, with secrets as `********`.

- Up and Down (or ctrl-P and ctrl-N) move, Enter runs the highlighted preset, and Esc or ctrl-C runs nothing and exits 130.
- bam prints `bam run NAME` first, so you can type it next time, and then runs the preset with the flags you gave: `bam run --watch` picks, then runs and follows.
- The picker does not edit variables; add `--var` or `--edit` to the command.
- With a pipe, `--json` or `--no-tui`, bam never opens it: it exits 2 and points to `bam preset list`.

### Edit a preset

`bam preset edit [NAME]` changes a saved preset without opening its YAML. On a terminal it opens the form of `bam preset add`, filled with the saved preset (bare `bam preset edit` picks the preset first, worded "edit which preset?"). The form lists the variables the preset sets, then the plan variables it leaves out, then its settings: name, branch, each repository's branch, `required` and `timeout`.

- `enter` edits a row, `←` `→` step through the choices (the preset's `options` when it has them), `x` removes a variable (the plan's own value then applies) or a repository, `o` edits a variable's options as a comma separated list, `a` adds a repository typed as `NAME=BRANCH`, `ctrl-s` saves and Esc writes nothing.
- When you change the branch, the form reads the repositories Bamboo lists for that plan branch (the branch each built in its newest finished job) and offers them in place of the old ones: a repository the new branch lists takes its branch, one the preset had that it does not list is marked removed, and one you added by hand that it does not list is dropped. `x` and `a` then adjust the list. When Bamboo answers with an error the form keeps the repositories as they were and says so; when it could not be reached as the form opened, the form never reads and keeps them as they were.
- A variable the preset leaves out is written only when you change it; emptying it writes an empty value, as `--var NAME=` does, and `x` is how a row goes back to the plan's own value. A secret row takes an environment variable name, never a value.
- bam edits the file whose copy of the preset bam uses and keeps its comments, key order, `watch` and the other presets. The file is written the way `bam preset sync` writes it, so the spacing before a comment can change. When the plan cannot be read, the form still opens with the preset's own variables.

For scripts and agents, `--var NAME=VALUE`, `--unset NAME`, `--branch`, `--repository NAME=BRANCH` (`NAME=` removes it), `--required A,B` (`""` removes it), `--option NAME=a,b` (`NAME=` removes them), `--timeout` and `--rename` apply the change without a form and never ask. They read nothing from Bamboo, so `--branch` leaves the repositories as they are; list the ones you want with `--repository`. `--dry-run` shows the changes and writes nothing, and `--json` prints `{preset, renamed_to, file, dry_run, changed, changes: [{field, old, new, removed, file}]}` (`removed` is true when the edit took the line out, which a variable set to empty is not; `file` is there for a change outside the preset's own file: a set, a pin or a train file). When a preset is defined in more than one file, bam writes the file it uses and checks the result as merged with the others: an edit that would break the merge is refused, and so is removing a variable, branch, timeout, `required`, option or repository that another file holds, naming that file. A literal value for a secret variable is refused (`--var 'db_password=${DB_PASSWORD}'` is the way), and so are `--option` values for a secret variable (an option a file already holds for one prints as `********`), a preset that uses YAML anchors or merge keys, a value outside the preset's `options`, a rename to a name another preset has, and, for a preset every repository shares, a rename to a name another repository's own preset has. A rename also renames the preset in the stages of the sets and in the `bam top` pins of your project and machine files, in place and with their comments, and `--dry-run` lists each one; a set or pin that reaches the name through a YAML anchor or alias stops the rename before any file is written. It also renames the preset in each train file that a past `bam set run --file` used and that still exists (for a preset only this repository defines, the runs started in this repository; for one a presets file or the top of your machine file also holds, every recorded run, and the sets under each repository entry of your machine file that does not define a preset of that name). A set in another repository's own `.bam.yaml` keeps the old name. `--dry-run` lists those files too; a train file that puts a YAML anchor on the name stops the rename the same way. A secret saved as a literal value shows as `********` in the form, and typing an environment variable name replaces it.

### Remove a preset

Type `bam preset rm` with no name, on a terminal, and bam opens the same picker as `bam run`, worded "remove which preset?", where Enter only picks. Enter picks a preset and bam asks `Remove preset NAME (PLAN · branch) from FILE? [y/N]`, naming every file that defines it. `y` or `yes` removes it; any other answer removes nothing, prints `nothing removed` and exits 0. `bam preset rm NAME` asks the same question on a terminal, and `--yes` skips it. Esc in the picker removes nothing and exits 130.

With a pipe, `TERM` empty or `dumb`, `--no-tui`, `BAM_NO_TUI` or `--json`, bam never asks: `bam preset rm NAME` removes the preset at once and a bare `bam preset rm` is a usage error. A preset that a set uses is kept until you remove it from the set. A preset that a `bam top` group still pins is removed, and bam names the group so you can drop the pin.

### What bam writes

The preset lists every variable the plan declares, with the plan default filled in, so you only edit the ones you want to change. It carries no per-line comments:

    presets:
      deploy-qa:
        plan: PROJ-DEPLOY
        server: work
        branch: "main"
        repositories:
          repo-a: "feature/PROJ-101"
        variables:
          region: ""
          target_env: "qa"
          version: ""
          # deploy_token: "${DEPLOY_TOKEN}"
        watch: true

bam writes only keys it has a value for: `plan`, `server` (the server you added it on, left out for a `BAM_URL` server), the variables and `watch: true`. `branch` is written when the plan is a plan branch or you pass `--branch`, and `repositories` then lists every repository of the plan branch with the branch its newest finished job built (read from `--branch`'s plan branch, or from the `--from` build); when bam cannot read a repository's branch, the repositories are written commented. bam never writes `required` or `options`; add them with `bam preset edit` (see below). `bam init --plan` and `p` in `bam ui` write the same preset. `--full` is still accepted but does nothing. Two comments can appear: `# not declared by the plan` on a variable the plan does not declare (only with `--from` and `--include-globals`), and a note when bam cannot read the plan's variables.

Without the form (see above), when it finishes, `preset add` also prints a box, in the terminal only, of values to choose from: for each variable that ran with more than one value in the last 10 builds of the plan (or plan branch), up to three values, newest first. Secrets are never listed. Nothing is printed when no variable varied or the server cannot read build variables.

- A variable with no plan default gets an empty value, which bam sends as empty. Add `required` with `bam preset edit` to refuse a run until it has a value.
- `watch: true` makes `bam run deploy-qa` follow the build. Run `bam run deploy-qa --watch=false` to skip it for one run.
- A secret the plan already stores is a commented line: Bamboo keeps sending the stored value. Uncomment it to override the value, and export the `${ENV_NAME}` environment variable; the value never lives in a file. A secret with no stored value is an active `${ENV_NAME}` reference.
- `bam preset show deploy-qa` lists the variables written in the preset file. A run also sends the plan's own values for variables the preset does not set.

To start from the values a build already ran with, name it with `--from` (a number, a full build key, or `last`):

    bam preset add deploy-qa --plan PROJ-DEPLOY --from 41

Each variable then takes build #41's value. Only the plan's declared variables are written: Bamboo lists every global a build inherited, so bam cannot tell an override from a plain global. Add `--include-globals` to also write the build's other variables, commented `# not declared by the plan`; delete the ones you do not need. A secret the plan stores stays a commented line, since bam cannot read the value a build used; one with no stored value becomes a `${ENV_NAME}` reference.

A full build key names the plan too, so `--plan` can go; a branch build also sets the preset's `branch:` unless `--branch` says otherwise:

    bam preset add deploy-eu --from PROJ-DEPLOY-41

In `bam ui`, press `p` on a build in the plan screen to do the same: it asks for the name, writes the preset to the plan's project file and shows the path.

### Set the values

Run `bam preset edit deploy-qa` (or `bam preset edit` to pick) and fill in the variables; `x` removes a variable you never change, so it keeps the plan's value, and the `required` row and `o` on a variable add `required` and `options`, so bam checks values before it triggers. The file holds:

    variables:
      target_env: "qa"
      region: "eu"
      version: "2.4.0"
    options:
      target_env: ["qa", "staging"]
    required: [region, version]

Check it, then run:

    bam preset show deploy-qa          # what bam loaded, and from which file
    bam run deploy-qa --dry-run        # resolves and validates everything, triggers nothing
    bam run deploy-qa --watch          # run and follow the build

When the plan later gains variables, `bam preset sync deploy-qa` appends the ones you need to fill (no plan default, or an empty secret) without touching your values; a new variable with a plan default is left to the plan, as in `preset add`.

### One file per project

Every `*.yaml` and `*.yml` in `~/bam/presets/` is read, and so is `~/bam/presets.yaml` if you have one:

    ~/bam/presets.yaml               # still read; bam no longer writes it
    ~/bam/presets/proj.yaml          # PROJ-* presets, written by preset add
    ~/bam/presets/ops.yaml           # OPS-* presets
    ~/bam/presets/custom-presets.yaml   # written with --file custom-presets

- File names are free; bam only picks `proj.yaml` when it writes. Other files, hidden files and subdirectories are ignored, so a `README.md` next to them is fine.
- Preset names stay global: `bam run deploy-qa` needs no project prefix. The same name in two presets files is an error that names both files, and `preset add` refuses a name another presets file already has, even with `--force`.
- `bam preset sync` edits a preset in the file it is in and keeps its comments.
- To share one project's presets, copy or sync just that file.

### Several variable sets for one plan

Make one preset per set, each pointing at the same plan:

    bam preset add deploy-qa      --plan PROJ-DEPLOY
    bam preset add deploy-staging --plan PROJ-DEPLOY

### Build other repository branches

`repositories:` names the branch each repository of the preset's plan branch must build. Bamboo does not let bam change that setting, so you change it in your browser and bam checks it. It needs `branch:` in the same preset; without it the preset fails to load.

    presets:
      feature-101:
        plan: PROJ-PARENT
        branch: "feature-PROJ-101"
        repositories:
          repo-a: "feature/PROJ-101"

`bam preset add feature-101 --plan PROJ-PARENT --branch feature-PROJ-101` writes this block for you, with the branch each repository built in the plan branch's newest finished job. Without `--branch` the lines are commented. With no finished job it lists the names from the newest build's revisions, commented and without a branch.

**Run it.** `bam run feature-101` reads the newest finished job of the plan branch and compares:

- Every repository already builds the wanted branch: bam triggers with no question.
- One differs, or no job has finished: bam prints the plan branch's Repositories tab link (`<server>/branch/admin/config/configureChainBranchRepositories.action?planKey=...`), opens it, and waits. Change the branches there, save, and press Enter. bam then triggers.
- With `--json` or no terminal, bam stops before triggering and prints the link. `--dry-run` opens and triggers nothing.
- A repository name the plan does not build fails before anything is triggered.

While following the build (`--watch`), bam compares again after the first job finishes and says whether the build used the wanted branches. It never stops the build; `--watch=false` skips it.

**One run.** `--repository NAME=BRANCH` applies to one run and beats the preset's entry:

    bam run feature-101 --repository repo-a=feature/PROJ-102

bam never saves the Repositories tab, edits the linked repository or restores the old branch; the branch stays as you left it. `bam set run` checks every preset before its first stage. In `bam ui`, the run form shows the repositories as read-only rows and asks in a dialog when a branch differs.

### Move presets you already have

Presets made with an older bam live in your machine config (`~/.config/bam/config.yaml`), and the ones under a `repos:` entry only work inside that repository. Move them all into your presets file, `~/bam/presets.yaml`:

    bam preset migrate --dry-run     # what would move
    bam preset migrate

Each preset keeps its values and comments; `defaults:` becomes `variables:`. A name that is already in your presets file or presets directory, or that appears in more than one place in the machine config, or that uses YAML anchors (`&name`, `*name`, `<<:`), stays where it is and the output says why. If your presets file is not valid, migrate stops and changes nothing. `.bam.yaml` is never touched. Afterwards `bam preset show <name>` says `Defined in ~/bam/presets.yaml`.

### One presets directory for Git Bash, WSL and macOS

The paths are `~/bam/presets.yaml` and `~/bam/presets/` on every system, so one setup works everywhere:

- **Git Bash** runs `bam.exe`, whose `~` is `C:\Users\<you>`. That file is the one to keep.
- **WSL** has its own home. When neither `~/bam/presets.yaml` nor `~/bam/presets/` exists there, bam in WSL uses `/mnt/c/Users/<you>/bam/presets.yaml` and `/mnt/c/Users/<you>/bam/presets/`, the same ones Git Bash uses. If `preset add` creates the first presets file from WSL, it creates it in the Windows home. If several Windows users have presets, bam uses none of them and `bam preset list` asks you to set `BAM_PRESETS`.
- **Another machine, such as a Mac,** cannot reach that file directly. Keep them in a synced folder (OneDrive, iCloud, a dotfiles repository) and point bam at the presets file on each machine; the `presets/` directory beside it comes along, and `~/bam/presets/` is then not read:

      export BAM_PRESETS="$HOME/OneDrive/bam/presets.yaml"   # also reads $HOME/OneDrive/bam/presets/

Logins stay per environment: run `bam login` in each one, and give the server the same alias everywhere (for example `work`) so presets that name a `server` resolve. Files under `/mnt/c` cannot be made private from WSL, so keep secrets out of the presets files and use `${ENV}` references.

## 2. Re-run with the last build's variables, changed

You do not need a preset for this. Name the plan, or a preset, and say which build the values come from.

### From the command line

Use the last build, or a build number, as the starting point:

    bam run PROJ-DEPLOY --from last --dry-run     # see what would be sent
    bam run PROJ-DEPLOY --from 41 --dry-run       # build #41 of the plan
    bam run PROJ-DEPLOY --from last --branch main

Change values in one of three ways. Later ones win:

| How | Command |
| --- | --- |
| a flag per variable | `bam run PROJ-DEPLOY --from last --var region=us --var version=2.5.0` |
| your editor, all variables at once | `bam run PROJ-DEPLOY --from last --edit` |
| both: flags first, then the editor opens on the result | `bam run PROJ-DEPLOY --from 41 --var region=us --edit` |

`--edit` opens your editor (`BAM_EDITOR`, the machine file's `editor`, `VISUAL`, `EDITOR`, then `vi`) on one `name=value` line per variable:

    # bam run PROJ-DEPLOY · default branch · server work
    # One variable per line, name=value. Save and quit to run.
    # ******** keeps a secret's current value. A value of exactly ${NAME} reads the environment.
    # Delete a line to keep its value. Delete every line, or quit with an error (:cq), to abort.

    region=eu
    target_env=qa
    version=2.4.0

Change a value, save and quit, and the build starts. A rule you broke (a missing required value, a value outside `options`) reopens the editor with the problem on top instead of failing. Add `--dry-run` to preview what would be sent. A GUI editor needs its wait flag (`code --wait`).

With a preset the same flags apply, and the build's values are laid over the preset's defaults:

    bam run deploy-qa --from 41 --edit --watch

Precedence, lowest to highest: plan values, preset defaults, `--from` build, `--var`, then what you change in the editor. `--revision from` also rebuilds the commit that build used:

    bam run PROJ-DEPLOY --from 41 --revision from --edit

Only variables that differ from the plan are sent, and secrets print as `********`.

To see what a build used before choosing:

    bam plan vars PROJ-DEPLOY --from 41
    bam build list PROJ-DEPLOY

### From the terminal UI

    bam ui

1. **From the last build.** Press `:` and type `presets` (or `plans`) and `enter`, move to your preset or plan, and press `R`. The run form opens filled from the last build.
2. **From a specific build.** Open the plan with `enter`, move to the build in the Builds panel, and press `R`. The form opens filled from the selected build, and the header says which one.
3. In the form:

   | Key | Does |
   | --- | --- |
   | `tab` / `↓` / `↑` | move between variables |
   | `enter` | on a free-text variable: edit the value, then `enter` again to accept; on a variable with `options`: cycle to the next allowed value |
   | `space` | cycle a variable that has `options` (never opens a text field) |
   | `a` | add a variable the plan does not declare: type its name, `enter`, then its value. A name the plan declares moves to that row instead |
   | `x` | remove a row added with `a`; declared variables stay |
   | `f` | on the revision row: build the source build's commit |
   | `d` | dry-run: show what would be sent |
   | `ctrl-R` | run the build; a second `ctrl-R` while the run is on its way does nothing |
   | `esc` | cancel; nothing is sent. Once the run request has gone to Bamboo, `esc` only leaves the form: the run still goes, and bam reports the build it started |

   Each row shows where its value came from (`plan`, `target`, `from #41`). A row you changed is marked, and the footer counts the changes.

`R` only opens the form. `ctrl-R` is the one key that starts a build. The form lists variables the plan declares. A row added with `a` shows `not declared`, the same warning `bam run --var` prints, and is still sent.

Interrupting a watch, here or on the command line, never stops the build. `bam build cancel` (or `C` in the UI) does.
