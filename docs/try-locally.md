# Try bam locally, without a Bamboo

You can run the whole CLI, including the terminal UI, against a fake Bamboo
built from this repository's test fixtures. No server, no token, no network.
Useful for trying bam before you point it at something real, and for working
on bam itself.

You need Go 1.26 or newer, `git`, `make`, and two terminals.

## 1. Get the code and install `bam`

    git clone https://github.com/r0jjames/bam-cli.git
    cd bam-cli
    make install

This installs `bam` into Go's bin directory (`go env GOPATH`/bin, usually
`~/go/bin`), so every command below is just `bam`. The version string comes
from `git describe`, so `bam version` prints the tag you are on, or the commit
if the clone has no tags yet. Re-run `make install` after each change.

## 2. Check that `bam` is on your PATH

    type bam            # should print <GOPATH>/bin/bam

If it prints nothing, add Go's bin directory to your shell profile:

    grep -q 'go/bin' ~/.zshrc || echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
    source ~/.zshrc

Use `~/.bashrc` instead for bash. On a non-interactive shell, Go itself may not
be on your PATH either; see [SETUP.md](../SETUP.md).

If you would rather keep the binary inside the clone, `make build` writes
`bin/bam` and leaves your PATH alone; then run `./bin/bam` everywhere below.

## 3. Start the fake Bamboo (first terminal)

From the repository root:

    make stub

It prints the address it is serving and the two variables that point bam at
it, then stays in the foreground:

    fake Bamboo on http://127.0.0.1:7990, fixtures from internal/provider/bamboo/testdata
    drive it with: BAM_URL=http://127.0.0.1:7990 BAM_TOKEN=devtoken bam

Leave this terminal running. It logs every request bam makes, which is the
fastest way to see what a command actually does. Pass `ARGS` to change the
address, for example `make stub ARGS='-addr 127.0.0.1:8085'`.

## 4. Point bam at it (second terminal)

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

## 5. Open the terminal UI

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

## 6. Or drive it from the command line

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

## 7. Optional: try presets

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

## 8. Stop and clean up

Press `ctrl-c` in the first terminal, or `pkill -f bamboostub`. Then, if you
want the environment back as it was:

    unset BAM_URL BAM_TOKEN BAM_CONFIG
    rm "$(go env GOPATH)/bin/bam"                    # only if you ran make install
    rm /tmp/bam-local.yaml

## If something does not work

| Symptom | Cause and fix |
| --- | --- |
| `bam: command not found` | `make install` was skipped, or Go's bin directory is not on your PATH; run `type bam` and step 2 again |
| `bam version` prints an older commit | the binary is stale; run `make install` again after every change |
| `connection refused` | the stub is not running, or is on another port; check the first terminal |
| `no fixtures in internal/provider/bamboo/testdata` | `make stub` was run from somewhere other than the repository root; `cd` there or pass `-fixtures` |
| every command says the token is invalid | `BAM_TOKEN` is unset; the stub requires some value, not a valid one |
| `bam` prints help instead of opening the UI | output is piped, the terminal is dumb, or `BAM_NO_TUI` is set; use `bam ui` |
| plans list on the stub but not on your own server | the stub answers for any key; a real Bamboo does not. Check `bam doctor` against that server |

The stub is `tools/bamboostub`; [SETUP.md](../SETUP.md) covers it from the
contributor's side, along with the rest of the development workflow.
