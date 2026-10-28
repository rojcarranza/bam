# Configuration

bam reads four layers. Only the last is secret.

| Layer | File | Commit it? | Holds |
| --- | --- | --- | --- |
| Project | `.bam.yaml` at the repository root | yes | servers, projects, team presets (`targets:`) |
| Machine | `~/.config/bam/config.yaml` (`BAM_CONFIG` overrides) | no | your own servers, overrides, preferences |
| Presets | `~/bam/presets/*.yaml` and `~/bam/presets.yaml` (`BAM_PRESETS` overrides) | no | your personal presets, usable in every directory |
| Credentials | OS keychain, an env variable, or `~/.config/bam/credentials.yaml` (0600) | never | tokens only |

bam looks for `.bam.yaml` from the current directory up to the git root (or your home directory outside a repository).

## Project file

    version: 1
    servers:
      work:
        url: https://bamboo.example.com
    default_server: work
    projects: [PROJ, OPS]
    targets:
      provision-lab:
        plan: PROJ-PROV
        branch: develop
        defaults:
          cluster_type: k8s
          db_password: "${LAB_DB_PASSWORD}"
        options:
          cluster_type: [k8s, dcos]
        required: [cluster_name]
        watch: true
        timeout: 45m

A token, or a literal value for a variable named like `password`, `secret`, `passphrase` or `sshkey`, is an error in this file. Use `"${ENV_NAME}"` for secret values; bam reads the variable when it runs.

The server alias `env` is reserved for the server defined by `BAM_URL` and is rejected if a config file defines a server by that name.

## Presets

A preset is a plan plus the variable values you run it with. Personal presets live in your presets files; team presets live under `targets:` in `.bam.yaml`.

    # ~/bam/presets/proj.yaml
    presets:
      deploy-qa:
        plan: PROJ-DEPLOY
        branch: main
        variables:
          target_env: qa
          api_token: "${DEPLOY_TOKEN}"
        required: [version]

| Field | Meaning |
| --- | --- |
| `plan` | plan key (required) |
| `server` | server alias, if not the default |
| `branch` | default plan branch; `--branch` overrides |
| `variables` | variable values; `--from` and `--var` override. `defaults` is its older name, accepted everywhere; use one or the other in a preset |
| `options` | allowed values per variable; anything else is refused before triggering |
| `required` | variables that must have a value |
| `watch` | watch by default; `--watch=false` overrides |
| `timeout` | default `--timeout` |

Generate a preset from a plan's declared variables instead of typing it:

    bam preset add mine --plan PROJ-PROV                        # personal: ~/bam/presets/proj.yaml
    bam preset add pkg --plan PROJ-PKG --file custom-presets    # personal: ~/bam/presets/custom-presets.yaml
    bam preset add provision-lab --plan PROJ-PROV --branch develop --shared   # .bam.yaml, committed
    bam preset add x --plan PROJ-PROV --print                   # show only

Presets from an older bam that still live in the machine file (`targets:`, or under a `repos:` entry) move into the presets file with `bam preset migrate` (`--dry-run` first to see what would move).

Use them:

    bam preset list                    # every preset and the file it is in
    bam preset show deploy-qa          # merged values and the file each came from
    bam run deploy-qa --dry-run        # resolve and check, trigger nothing
    bam run deploy-qa --var version=2.4.1 --watch

**Where presets come from**, lowest to highest: `.bam.yaml` `targets`, machine file `targets`, the presets file and presets directory, machine file `repos.<root>.targets`. A preset with the same name in several of these layers is merged field by field. Within the presets layer no file outranks another: the same name in `~/bam/presets.yaml` and a file of `~/bam/presets/`, or in two files of the directory, is a config error naming both.

**Where the presets files are**: the presets directory is always the `presets/` directory beside the presets file, and every `*.yaml` and `*.yml` in it is read (other and hidden files are ignored). The presets file is `BAM_PRESETS` if set; else `~/bam/presets.yaml`; in WSL with neither `~/bam/presets.yaml` nor `~/bam/presets/` in the Linux home, the ones in your Windows home, so Git Bash and WSL share them. `bam preset add` writes `presets/<project>.yaml` (or `presets/<NAME>.yaml` with `--file NAME`); `bam preset migrate` writes the presets file. See [Presets](presets.md#one-presets-directory-for-git-bash-wsl-and-macos).

When the plan's variables change later, bring the preset up to date:

    bam preset sync provision-lab     # add new plan variables, mark removed ones
    bam preset sync --all --dry-run   # show what every preset would gain, write nothing

Sync appends new variables to `variables` (or `defaults`, whichever the preset uses) with the same values and comments as `preset add`. A variable the plan no longer declares is kept, because it may override a Bamboo global, and marked `# not declared on PROJ-PROV`; the marker goes away if the plan declares it again. Existing values, `options` and `required` are never changed.

Variable precedence, lowest to highest: plan values, target `defaults`, `--from <build>`, `--var name=value`, then what you change in the editor with `bam run --edit`.

## Sets (release trains)

A set runs several presets in ordered stages. Every run of a stage triggers at once; the next stage starts only when every build of the one before succeeded.

```yaml
sets:
  release:
    timeout: 2h                 # optional; the whole train
    stages:
      - [publish-specs]
      - - provision
        - target: deploy
          vars:
            env: qa
            deploy_token: "${DEPLOY_TOKEN}"
```

A stage is a list of presets. A bare name is short for `{target: name}`; the long form adds `vars`, which override that preset's `defaults` for this set only.

`sets:` is allowed wherever `targets:` is: the project file, the machine file, and a machine `repos` entry. A set defined in a higher layer replaces the lower one whole; stages are never merged.

Rules, checked when the file loads:

- set names follow the target name rules;
- every stage lists at least one preset, and every preset exists;
- a preset appears at most once per stage (it may appear again in a later stage);
- a set runs on one server, so presets with an explicit `server` must agree;
- in the project file, a secret-looking variable in `vars` must be an `${ENV}` reference.

Variable precedence for a run of a set, lowest to highest: plan values, preset `defaults`, the set's `vars`, then `--var` on `bam set run`, which applies to every run whose plan declares that name. Every run is resolved and checked before the first trigger. A run always watches its build; the preset's `timeout` applies to that run.

    bam set run release --dry-run      # every run as it would trigger
    bam set run release                # run the train
    bam set status                     # the newest set run in this repository
    bam set history release            # past runs of release, newest first
    bam set resume                     # continue after Ctrl-C or a set timeout

When a build fails, the rest of its stage still finishes, no later stage starts, and nothing is cancelled. Interrupting or timing out stops watching only; the builds keep running, and `bam set resume` picks the train up where it stopped. Resume asks again for every `--var` the run was started with, because bam never stores variable values. Records live in `$XDG_STATE_HOME/bam/sets/`; bam keeps the newest 20 per set.

### Matrix

A `matrix` on a set run expands one preset into several runs of the same stage, one per variable set. Two forms:

```yaml
sets:
  regions:
    stages:
      - - target: deploy
          vars: {deploy_token: "${DEPLOY_TOKEN}"}
          matrix:                 # map of lists: every combination
            env: [qa, staging]
            region: [eu, us]
      - - target: smoke
          matrix:                 # list of maps: exactly these variable sets
            - {env: qa}
            - {env: prod, region: us}
```

- A product runs its variables in sorted order, values as written, the last variable changing fastest. A list runs its entries in order.
- Each run is named after its preset and its own matrix values, sorted: `deploy[env=qa,region=eu]`. Names appear in every output and in the record; a secret-looking key's literal value shows as `********`, an `${ENV}` reference as written.
- Precedence, lowest to highest: plan values, preset `defaults`, the set run's `vars`, the matrix values, then `--var`.
- Run names must be unique in their stage, so a preset may appear twice in a stage when its matrices differ. A stage expands to at most 50 runs.
- Every run of the stage is watched; the stage succeeds only if all of them do. `bam set resume` and `bam set rerun --failed` act on each run on its own.

### Train files

A one-off train does not have to live in `.bam.yaml`. Put one set in a file of its own and run it with `-f`:

```yaml
name: hotfix-2041            # optional; default: the file name, hotfix-2041.yaml → hotfix-2041
timeout: 1h
stages:
  - [publish-specs]
  - - target: deploy
      vars: {deploy_token: "${DEPLOY_TOKEN}"}
      matrix: [{env: qa}, {env: prod}]
```

    bam set run -f hotfix-2041.yaml --dry-run
    bam set run -f hotfix-2041.yaml

The file holds one set in the shape of an entry under `sets:`, plus an optional `name:`. Its presets come from your configuration, and every check of a configured set applies; a literal secret is refused, as in the project file. Its runs are recorded under its name, so `bam set status`, `bam set resume` and `bam set rerun --failed` work as for any set. Resume and rerun read the file again from where it was run; if it has moved, pass `--file` with its new path. A moved file keeps its run's name even under another file name, unless its own `name:` says otherwise.

## Child plans (`children`)

`bam build tree` finds the builds a build triggered by their reason, "Child of <build>", in the last 25 builds of each plan of your projects. A plan that ran more than 25 builds since the root started is read further back, up to 1600 builds, until it reaches the root's start. It also follows builds a job started with a script through the REST API, which name no parent in their reason: a link to a build's page in a job's log, such as `https://bamboo.example.com/browse/PROJ-API-42`, makes that build a child, drawn under the stage whose job started it. A `children` map narrows that for a plan and shows a configured child that never ran:

```yaml
children:
  PROJ-API: [OPS-DEPLOY, PROJ-DOCS]
  OPS-DEPLOY: [OPS-SMOKE]
```

- Keys and values are plan keys. A plan with an entry gets only the listed plans as children; a listed plan without a matching build shows as `not run` and makes the tree fail.
- Listed plans are searched even outside your projects.
- The project file, the machine file and a machine `repos` entry may each set it; a later one replaces a plan's list (project, then machine, then `repos`).

## Machine file

    version: 1
    default_server: home
    servers:
      home:
        url: http://bamboo.lab.example:8085
        projects: [LAB]
      work:
        url: https://bamboo-eu.example.com   # overrides the project file on this machine
        auth_env: BAM_WORK_TOKEN
    targets:
      lab-smoke:
        server: home
        plan: LAB-SMOKE
    repos:
      /home/jdoe/src/some-repo:
        server: work
        projects: [PROJ]
    color: auto
    pager: less -FRX
    editor: vim                     # for bam run --edit; BAM_EDITOR overrides it; else VISUAL, EDITOR, vi

A GUI editor needs its wait flag (`code --wait`, `gvim -f`), or it returns at once and the run starts with the values unchanged.

A server defined in both files is merged field by field, and the machine file wins.

## Which server is used

First match wins: `--server`, `BAM_SERVER`, `BAM_URL`, the target's `server`, the machine file's `repos` entry, the project's `default_server`, the machine's `default_server`, the only configured server.
