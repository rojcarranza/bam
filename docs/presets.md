# Presets and re-running with other variables

Two jobs this page covers, each from the command line and from the terminal UI:

1. Save a plan's variable values once as a **preset**, then run it by name.
2. Re-run a plan with the variables of its **last build** (or a build you pick), change some of them, and run.

The examples use the plan `PROJ-DEPLOY` and the variables `target_env`, `region` and `version`. Substitute your own plan key.

## 1. Create a personal preset

A preset is a named entry in your **presets file**, `~/bam/presets.yaml`. Generate it from the plan instead of typing the variable names:

    bam preset add deploy-qa --plan PROJ-DEPLOY --branch main

- `deploy-qa` is a name you choose. It is what you type later in `bam run deploy-qa`, and what the terminal UI lists under presets. It is not a Bamboo name: lowercase letters, digits, `-` and `_`, starting with a letter.
- `--plan` is the Bamboo plan key. `--branch` is optional and sets the plan branch the preset runs.
- By default bam writes your presets file, and its presets work from any directory. `bam preset list` prints its path.
- `--shared` writes `.bam.yaml` at the repository root instead, which is meant to be committed and shared with the team. Keep personal presets, and any plan key or host that should not enter the repository, in your presets file.
- `--print` shows the generated YAML and writes nothing. Use it to look first.

The server is chosen the usual way (`--server`, or your only configured server; see [Configuration](configuration.md#which-server-is-used)).

### What bam writes

The preset lists every variable the plan declares, with a comment saying where the value came from:

    presets:
      deploy-qa:
        plan: PROJ-DEPLOY
        branch: "main"
        variables:
          target_env: "qa"  # plan default
          region: ""  # no plan default; last used "eu" in PROJ-DEPLOY-41
          version: ""  # no plan default; last used "2.4.0" in PROJ-DEPLOY-41
          deploy_token: "${DEPLOY_TOKEN}"  # masked by Bamboo; set env var DEPLOY_TOKEN
        # options:
        #   target_env: ["qa"]
        # required: [region, version]
        # watch: true

- A variable with no plan default gets an empty value. The `# required:` line suggests making those required; uncomment it and bam refuses to run until they have a value.
- A secret Bamboo masks becomes a `${ENV_NAME}` reference. Export that environment variable; the value never lives in a file.
- `# options:` and `# watch: true` are commented suggestions. Uncomment what you want.

### Set the values

Edit the file and fill in `variables`. Delete variables you never change, so they keep the plan's value. Uncomment `required` and `options` if you want bam to check values before it triggers:

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

When the plan later gains variables, `bam preset sync deploy-qa` appends them without touching your values.

### Several variable sets for one plan

Make one preset per set, each pointing at the same plan:

    bam preset add deploy-qa      --plan PROJ-DEPLOY
    bam preset add deploy-staging --plan PROJ-DEPLOY

### Move presets you already have

Presets made with an older bam live in your machine config (`~/.config/bam/config.yaml` on Linux and WSL, `~/Library/Application Support/bam/config.yaml` on macOS), and the ones under a `repos:` entry only work inside that repository. Move them all into your presets file:

    bam preset migrate --dry-run     # what would move
    bam preset migrate

Each preset keeps its values and comments; `defaults:` becomes `variables:`. A name that is already in your presets file, or that appears in more than one place in the machine config, stays where it is and the output says why. `.bam.yaml` is never touched. Afterwards `bam preset show <name>` says `Defined in ~/bam/presets.yaml`.

### One presets file for Git Bash, WSL and macOS

The path is `~/bam/presets.yaml` on every system, so one setup works everywhere:

- **Git Bash** runs `bam.exe`, whose `~` is `C:\Users\<you>`. That file is the one to keep.
- **WSL** has its own home. When `~/bam/presets.yaml` does not exist there, bam in WSL uses `/mnt/c/Users/<you>/bam/presets.yaml`, the same file Git Bash uses. If `preset add` creates the file from WSL, it creates it in the Windows home. If several Windows users have a presets file, bam uses none of them and `bam preset list` asks you to set `BAM_PRESETS`.
- **Another machine, such as a Mac,** cannot reach that file directly. Keep it in a synced folder (OneDrive, iCloud, a dotfiles repository) and point bam at it on each machine:

      export BAM_PRESETS="$HOME/OneDrive/bam/presets.yaml"

Logins stay per environment: run `bam login` in each one, and give the server the same alias everywhere (for example `work`) so presets that name a `server` resolve. Files under `/mnt/c` cannot be made private from WSL, so keep secrets out of the presets file and use `${ENV}` references.

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
   | `f` | on the revision row: build the source build's commit |
   | `d` | dry-run: show what would be sent |
   | `ctrl-R` | run the build |
   | `esc` | cancel; nothing is sent |

   Each row shows where its value came from (`plan`, `target`, `from #41`). A row you changed is marked, and the footer counts the changes.

`R` only opens the form. `ctrl-R` is the one key that starts a build. The form lists variables the plan declares; for one it does not declare, use `bam run ... --var name=value` on the command line.

Interrupting a watch, here or on the command line, never stops the build. `bam build cancel` (or `C` in the UI) does.
