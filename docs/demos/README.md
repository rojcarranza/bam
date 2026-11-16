# README demo GIFs

Every GIF in the main README is a recording of real bam commands against a real Bamboo. Nothing
is staged or edited: [VHS](https://github.com/charmbracelet/vhs) types the commands from a tape,
waits for the prompt to come back, and writes the GIF and its screen text.

| Path | What |
| --- | --- |
| `tapes/NAME.tape` | what is typed, one tape per GIF; `{{KEY}}` is replaced by a live build key |
| `gifs/NAME.gif` | the recording the README shows |
| `text/NAME.txt` | every distinct line the screen showed, so a reviewer can read a GIF and `make check-demos` can scan it |
| `fixtures/setlist.bam.yaml` | the `.bam.yaml` the demo clone uses: presets for each plan, a `check` set and the `release` train |
| `Dockerfile`, `Caddyfile`, `entry.sh`, `run.sh` | the recording container |
| `home.sh`, `lib.sh`, `seed.sh`, `record.sh` | a clean home for jdoe, helpers, the history the GIFs need, and the recorder |

## What the GIFs run against

The sample service: [setlist-service](https://github.com/rojcarranza/setlist-service), a small Go web
service, built by six plans in the lab Bamboo (project `SETLIST`, defined in forge-lab under `lab/setlist`):
`SETLIST-CI` fans out to `SETLIST-IT` and `SETLIST-PACKAGE`; `SETLIST-DEPLOY` takes `environment`,
`version`, `region`, `replicas` and `db_password`; `SETLIST-SMOKE` checks the deployed service;
`SETLIST-LOAD` runs for about three minutes. `SETLIST-IT` fails the first attempt of every fourth build,
and the branch `feat/capo-transpose` is red with 12 failing tests.

The recordings run in a container as the Bamboo user `jdoe`, from an empty home. A Caddy proxy inside the
container answers as `https://bamboo.example.com` with its own certificate authority, and the container
reaches the lab Bamboo on `localhost:8085` (for example through `kubectl port-forward`). bam builds every
link from its configured address, so the lab's own address never shows.

## Record

You need Docker, Go, a lab Bamboo with the SETLIST plans and an agent, and a file holding jdoe's personal
access token (default `~/.config/bam-demos/token`, or set `BAM_DEMO_TOKEN_FILE`). The token is mounted into the
container, typed into the login prompt by a hidden step, and never written to a tape that is kept.

    make demos-seed       # run CI as jdoe a few times and write build/keys.env (live build keys)
    make demos            # seed, then record every tape
    make demo NAME=tree   # record one tape
    make check-demos      # README and tapes match one to one, GIFs are small, no address or name on screen

Start one container at a time: each one binds port 443 for its proxy. A tape starts with comments that
prepare the run: `# demo: in-repo` clones the service, `config` also installs the fixture, `branch` checks
out `feat/capo-transpose`, `it-next=fail` or `it-next=pass` runs CI until the next `SETLIST-IT` build will
fail or pass, `idle` waits until no SETLIST build is running or queued, and `no-login` starts from a home
that has not met the server.

## Rules

- Placeholders only: `bamboo.example.com`, `SETLIST`, `jdoe`. `make check-demos` scans the screen text for the
  lab address and for personal names, and fails on a hit.
- This directory has its own `go.mod`, so the GIFs stay out of what `go install` downloads.
- VHS 0.10 is pinned: 0.12 runs a tape but writes no GIF in this setup.
