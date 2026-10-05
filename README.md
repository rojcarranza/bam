# bam

A terminal remote control for Atlassian Bamboo: trigger, watch and diagnose builds without the browser, and get the Bamboo link the moment you need it.

    bam run provision-lab --var cluster_name=alpha --watch
    bam logs --last --failed
    bam open --last

bam works with Bamboo Data Center 9.x and later, using a personal access token.

## Install

Download a binary for your platform from the [releases page](https://github.com/r0jjames/bam-cli/releases), or build from source:

    go install github.com/r0jjames/bam-cli/cmd/bam@latest

Check that `bam` does not shadow another command on your machine: `type bam`.

## Quick start

1. Register your server and log in. `login` asks for a personal access token, which you create in Bamboo under your profile → Personal access tokens; bam prints the link.

       bam server add work --url https://bamboo.example.com --project PROJ

2. Create `.bam.yaml` in your repository with presets generated from your plans' variables:

       bam init --server work --project PROJ --plan PROJ-BUILD --plan PROJ-PROV=provision-lab

   Review the generated file, then commit it. It holds no secrets.

3. Run, watch, and investigate:

       bam run provision-lab --var cluster_name=alpha --watch
       bam logs --last --failed
       bam open --last

A teammate who clones the repository only needs `bam login work`.

## Try it locally, without a Bamboo

You can run the whole CLI, including the terminal UI, against a fake Bamboo
built from this repository's test fixtures. No server, no token, no network.
Useful for trying bam before you point it at something real, and for working
on bam itself.

You need Go 1.26 or newer, `git`, `make`, and two terminals.

### 1. Get the code and install `bam`

    git clone https://github.com/r0jjames/bam-cli.git
    cd bam-cli
    make install

This installs `bam` into Go's bin directory (`go env GOPATH`/bin, usually
`~/go/bin`), so every command below is just `bam`. The version string comes
from `git describe`, so `bam version` prints the tag you are on, or the commit
if the clone has no tags yet. Re-run `make install` after each change.

### 2. Check that `bam` is on your PATH

    type bam            # should print <GOPATH>/bin/bam

If it prints nothing, add Go's bin directory to your shell profile:

    grep -q 'go/bin' ~/.zshrc || echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
    source ~/.zshrc

Use `~/.bashrc` instead for bash. On a non-interactive shell, Go itself may not
be on your PATH either; see [SETUP.md](SETUP.md).

If you would rather keep the binary inside the clone, `make build` writes
`bin/bam` and leaves your PATH alone; then run `./bin/bam` everywhere below.

### 3. Start the fake Bamboo (first terminal)

From the repository root:

    make stub

It prints the address it is serving and the two variables that point bam at
it, then stays in the foreground:

    fake Bamboo on http://127.0.0.1:7990, fixtures from internal/provider/bamboo/testdata
    drive it with: BAM_URL=http://127.0.0.1:7990 BAM_TOKEN=devtoken bam

Leave this terminal running. It logs every request bam makes, which is the
fastest way to see what a command actually does. Pass `ARGS` to change the
address, for example `make stub ARGS='-addr 127.0.0.1:8085'`.

### 4. Point bam at it (second terminal)

    export BAM_URL=http://127.0.0.1:7990
    export BAM_TOKEN=devtoken

`BAM_URL` defines the reserved server alias `env`, and `BAM_TOKEN` is the
token for that server only. Your configuration files are still read for things
like presets and colour, but the server and the token now come from the
environment, and nothing is written to them or to your keychain. The stub
accepts any token value; it only checks that one was sent.

Confirm the two ends agree. Pass a plan, because without one `doctor` has
nothing to probe the optional capabilities against:

    bam doctor --plan PROJ-BUILD

Everything should be a `✓` down to `failed tests`. The last line, `stop
builds`, stays `–` until you cancel a build for the first time: stopping has
side effects, so bam never probes it.

### 5. Open the terminal UI

    bam

`bam` opens on a table of every plan in the configured projects: `OPS-BUILD`,
`OPS-OLD`, `PROJ-BUILD` and `PROJ-OLD`, each with its last build's state,
number, age and trigger. The header reads `bam · env · 9.6.2 · jdoe`. From there:

- `j` / `k` to move, `/fail` to keep the failed plans, `s` to change the sort.
- `enter` on a plan to open its builds in the panels, `enter` again to open
  one. `#481` is a failed build with stages and failed tests. `esc` from the
  Plans panel returns to the table.
- `l` on a failed build to read the failing job's log.
- `R` to open the run form, then `d` to dry-run it or `ctrl-R` to start a
  build. The build is simulated: queued for 5 seconds, running for 10, then
  successful, so the watch view has something to show.
- `:presets` for the presets table, `:plans` to come back, `?` for help, `q`
  to quit.

### 6. Or drive it from the command line

    bam project list
    bam plan list PROJ
    bam plan vars PROJ-BUILD
    bam build list PROJ-BUILD
    bam build show PROJ-BUILD-481
    bam logs PROJ-BUILD-481 --failed
    bam run PROJ-BUILD --var cluster_type=k8s --watch --no-tui
    bam build cancel PROJ-BUILD-902      # use the key the previous command printed

Add `--json` to any read command to see the machine-readable form, and
`--debug` to log the HTTP requests bam sends.

### 7. Optional: try presets

Presets live in configuration rather than on the server, so they need a file.
Point `BAM_CONFIG` at a throwaway one instead of your real machine file:

    cat > /tmp/bam-local.yaml <<'YAML'
    version: 1
    targets:
      smoke:
        plan: PROJ-BUILD
        defaults:
          cluster_type: k8s
        options:
          cluster_type: [k8s, dcos]
        required: [cluster_name]
    YAML
    export BAM_CONFIG=/tmp/bam-local.yaml

    bam target list
    bam run smoke --dry-run                          # refused: cluster_name is required
    bam run smoke --var cluster_name=beta --watch --no-tui

### 8. Stop and clean up

Press `ctrl-c` in the first terminal, or `pkill -f bamboostub`. Then, if you
want the environment back as it was:

    unset BAM_URL BAM_TOKEN BAM_CONFIG
    rm "$(go env GOPATH)/bin/bam"                    # only if you ran make install
    rm /tmp/bam-local.yaml

### If something does not work

| Symptom | Cause and fix |
| --- | --- |
| `bam: command not found` | `make install` was skipped, or Go's bin directory is not on your PATH; run `type bam` and step 2 again |
| `bam version` prints an older commit | the binary is stale; run `make install` again after every change |
| `connection refused` | the stub is not running, or is on another port; check the first terminal |
| `no fixtures in internal/provider/bamboo/testdata` | `make stub` was run from somewhere other than the repository root; `cd` there or pass `-fixtures` |
| every command says the token is invalid | `BAM_TOKEN` is unset; the stub requires some value, not a valid one |
| `bam` prints help instead of opening the UI | output is piped, the terminal is dumb, or `BAM_NO_TUI` is set; use `bam ui` |
| plans list on the stub but not on your own server | the stub answers for any key; a real Bamboo does not. Check `bam doctor` against that server |

The stub is `tools/bamboostub`; [SETUP.md](SETUP.md) covers it from the
contributor's side, along with the rest of the development workflow.

## Terminal UI

Running `bam` alone on a terminal opens a k9s-style table of every plan, with
lazygit-style panels behind it to browse detail, watch a build live, read its
logs, run a preset, and cancel.

It opens on the plans table; `enter` opens the panels for a plan:

```
bam · env · 9.6.2 · jdoe · project all · 4 plans · sort key↑
PROJECT  KEY          NAME             STATE      #    AGE    BY
▸OPS      OPS-NIGHTLY  Nightly          ● running   91   now   sched
 PROJ     PROJ-BUILD   Build and test   ✗ failed   482  12m    jdoe
 PROJ     PROJ-PROV    Provision lab    ✓ success    8   3h    sched

 enter open  / filter  s sort  : cmd  R run  ?help  q quit
```

No lowercase key changes anything on the server. `R` only opens the run form,
`ctrl-R` inside that form is the only key that starts a build, and `C` always
asks before it cancels one.

```
┏━1 Plans━━━━━━━━━━┓╭─PROJ-BUILD-44────────────────────────────────────────────╮
┃▸ PROJ-BUILD      ┃│✗ failed   #44   branch main   3m12s                      │
┃  PROJ-DEPLOY     ┃│jdoe · manual                                             │
┃  OPS-NIGHTLY     ┃│                                                          │
┗━━━━━━━━━━━━━━━━━━┛│✓ Build       42s                                         │
╭─2 Builds─────────╮│  ✓ Compile        28s                                    │
│▸ ✗ #44 3m ago    ││  ✓ Unit tests     14s                                    │
│  ✓ #43 1h ago    ││✗ Test        2m30s                                       │
╰──────────────────╯│  ✗ Integration    2m30s                                  │
╭─3 Presets────────╮│– Deploy                                                  │
│  provision-lab   ││                                                          │
│  smoke           ││3 failed tests                                            │
╰──────────────────╯╰──────────────────────────────────────────────────────────╯
lab · 9.6.4 · jdoe           ?help  : cmd  tab focus  l logs  o open  q quit
```

`bam ui` opens it explicitly. To keep the UI out of the way, use `--no-tui`,
set `BAM_NO_TUI=1`, or pipe bam's output — on a pipe, on a dumb terminal, or
without a terminal, bare `bam` prints help exactly as before.

### Keys

| | Keys | Does |
| --- | --- | --- |
| Move | `j` `k` `↑` `↓` | up / down in the focused panel |
| | `g` `G` | first / last row |
| | `tab` | next panel |
| | `shift-tab` | previous panel |
| | `1` `2` `3` | focus a panel (on Home: open it) |
| | `enter` | drill in |
| | `esc` | back one level, close an overlay; from Plans to Home |
| Home | `enter` | open the panels on the row's plan |
| | `s` | next sort column |
| | `:` | command bar: :plans :presets :project :server :sort :q |
| View | `l` | logs for the selection (failed jobs by default) |
| | `a` | all logs, not only failed |
| | `f` | follow (log screen) |
| | `/` | filter the focused list, or search the log |
| | `n` `N` | next / previous match, or next failure |
| | `r` | refresh the focused panel |
| | `e` | expand the current error |
| Run | `R` | open the run form for the selection |
| | `ctrl-R` | run (in the form) |
| | `d` | dry-run: show what would be sent (in the form) |
| | `space` | cycle an options field (in the form) |
| | `f` | use the build's revision (in the form, on the revision row) |
| | `C` | cancel the selected build (asks first) |
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
| build a chosen commit instead of the newest | `bam run provision-lab --revision abc1234` |
| rebuild the commit an earlier build used | `bam run provision-lab --from 41 --revision from` |
| see recent builds | `bam build list provision-lab` |
| follow a running build | `bam watch --last` |
| read only the failed jobs' logs | `bam logs --last --failed` |
| stop a build | `bam build cancel --last` |
| open anything in Bamboo | `bam open PROJ-PLAN-123` |
| run a release train of presets, stage by stage | `bam set run release` |
| continue a train after Ctrl-C | `bam set resume` |
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

A failure never cancels a build. Ctrl-C stops watching; `bam set status` and `bam set resume` pick the train up again. After a failure, `bam set rerun --failed` has Bamboo restart the failed builds (only their failed jobs run again, with the same variables and commit) and then finishes the train. See [Sets](docs/configuration.md#sets-release-trains) for the rules.

## More

- [Configuration and presets](docs/configuration.md)
- [Authentication](docs/authentication.md)
- [Using bam in CI](docs/ci.md)
- [JSON output](docs/json.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Development setup](SETUP.md) — Go, make and linter on macOS and Linux

## License

MIT
