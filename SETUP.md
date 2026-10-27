# Development setup

This guide prepares a macOS or Linux machine to build, test and release bam. Follow it once per machine, top to bottom. Every step ends with a check you can run.

Windows is not covered here.

## What you need

| Tool | Why | Required |
| --- | --- | --- |
| Git | source control | yes |
| Go (latest stable, 1.26 or newer) | build and test | yes |
| GNU make (3.81 or newer) | `make install`, `make test`, `make lint` … | yes |
| golangci-lint v2 | the lint gate (`make lint`) | yes |
| curl | downloads in this guide | yes |
| gopls | Go language server for your editor | recommended |
| goreleaser v2 | release builds (`goreleaser build --snapshot`) | only for release work |
| GitHub CLI (`gh`) | pull requests | optional |
| no-mistakes | the push gate (see Before you push) | yes |

The project uses no Docker, no database and no cgo.

---

## macOS

### 1. Command line tools and Homebrew

```bash
xcode-select --install          # git, make, clang; skip if already installed
```

If Homebrew is missing, install it from https://brew.sh, then open a new terminal.

Check:

```bash
git --version && make --version | head -1 && brew --version | head -1
```

### 2. Go and the tools

```bash
brew install go golangci-lint
brew install goreleaser gh      # optional
go install golang.org/x/tools/gopls@latest
```

`golangci-lint` depends on the Homebrew `go` formula, so Homebrew installs Go
even if you leave `go` out of the command.

If Go is already installed from a go.dev tarball in `/usr/local/go`, the
Homebrew copy wins after this step, because `/opt/homebrew/bin` comes earlier on
PATH. Run `command -v go && go version` to see which one you get. Keep one Go
only: either remove the tarball with `sudo rm -rf /usr/local/go`, or skip
`brew install go` and install golangci-lint with
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`.

### 3. PATH

Homebrew already puts `go` and `golangci-lint` on your PATH. Tools installed with `go install` land in `$(go env GOPATH)/bin`, usually `~/go/bin`. Add it to your shell profile (`~/.zshrc` for the default zsh):

```bash
grep -q 'go/bin' ~/.zshrc || echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

The `grep` guard keeps the line unique if you run this guide again.

### 4. Check

```bash
command -v go              # which Go you actually run
go version                 # go1.26 or newer
golangci-lint version      # must say "version 2."
gopls version
make --version | head -1   # GNU Make 3.81 or newer
```

### Keychain

bam stores tokens in the macOS Keychain. Nothing to install. The first time bam saves a token, macOS may ask you to allow access.

---

## Linux (Ubuntu or Debian)

Do not use `apt install golang`. Distribution packages usually lag behind the Go version this project needs. Install the official release instead.

### 1. Base packages

```bash
sudo apt update
sudo apt install -y git make curl tar
```

### 2. Go from go.dev

This installs Go into `~/.local/go`, which needs no sudo and is easy to upgrade:

```bash
GO_VERSION="$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)"   # e.g. go1.27.1
case "$(uname -m)" in
  x86_64)  GO_ARCH=amd64 ;;
  aarch64|arm64) GO_ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac
curl -fLO "https://go.dev/dl/${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
rm -rf "$HOME/.local/go"
mkdir -p "$HOME/.local"
tar -C "$HOME/.local" -xzf "${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
rm "${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
```

To upgrade Go later, run the same block again.

### 3. PATH

Add Go and the Go tool directory to your shell profile (`~/.bashrc` for bash, `~/.zshrc` for zsh):

```bash
grep -q '.local/go/bin' ~/.zshrc || \
  echo 'export PATH="$HOME/.local/go/bin:$HOME/go/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

### 4. golangci-lint v2, gopls, and optional tools

```bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
go install golang.org/x/tools/gopls@latest
go install github.com/goreleaser/goreleaser/v2@latest   # optional, release work only
```

If the golangci-lint script fails, this also works:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

For the GitHub CLI, follow https://github.com/cli/cli/blob/trunk/docs/install_linux.md (optional).

### 5. Check

```bash
go version                 # go1.26 or newer
golangci-lint version      # must say "version 2."
make --version | head -1
gopls version
```

### Keyring

bam stores tokens through the Secret Service API, which GNOME Keyring provides on Ubuntu desktop. On a server, in WSL, or on a build agent without a keyring, bam falls back to `~/.config/bam/credentials.yaml` with mode 0600 and tells you so. The unit tests never touch a real keyring.

---

## Both platforms: git identity

Commits in this repository use your own identity:

```bash
git config user.name
git config user.email
```

Set them with `git config --global user.name "…"` and `git config --global user.email "…"` if either is empty.

## Editor

Any editor with gopls works. For VS Code, install the official Go extension (`golang.go`). It uses the `gopls` installed above.

## Get the code

```bash
git clone git@github.com:r0jjames/bam-cli.git
cd bam-cli
```

The product design lives in `docs/`:

- `docs/prd.md` - what bam is for
- `docs/superpowers/specs/2026-09-12-bam-cli-mvp-design.md` - the v0.1 design
- `docs/superpowers/plans/` - the implementation plan in four parts

## Build and test

```bash
make install      # installs bam into $(go env GOPATH)/bin
make test         # go test ./...
make lint         # golangci-lint run
bam version
```

`make install` is what puts `bam` on your PATH, so you type `bam …` and not
`./bin/bam …`. It installs into Go's bin directory, which the PATH step above
already added to your shell profile. Check it with:

```bash
type bam          # should print <GOPATH>/bin/bam
bam version       # the tag from git describe, or the commit
```

If `type bam` prints nothing, the PATH line is missing or the shell was not
reloaded; redo the PATH step for your platform and open a new terminal.

The binary is a snapshot, not a link: re-run `make install` after every change
you want to exercise by hand.

`make build` is the alternative. It writes `bin/bam` inside the clone and
touches nothing outside it, which is what you want for a throwaway check or
when you do not want `bam` on your PATH at all. Then run `./bin/bam`.

Also used later:

| Command | What it does |
| --- | --- |
| `make build` | builds `bin/bam` inside the clone instead of installing it |
| `gofmt -l .` | prints files that need formatting; CI fails if the list is not empty |
| `go vet ./...` | the vet gate CI runs |
| `go test -race ./...` | tests with the race detector |
| `make check-fixtures` | fails if any test fixture names a real host |
| `make docs` | regenerates the command reference in `docs/cli/`; the no-mistakes gate fails if it is stale |
| `make stub` | serves a fake Bamboo from the fixtures, so bam can be driven without a server (see below) |
| `make record ARGS='-target provision'` | records scrubbed fixtures from your own Bamboo (see below) |
| `make e2e ARGS='-target smoke'` | end-to-end test against a real Bamboo |
| `goreleaser build --snapshot --clean` | cross-platform release build into `dist/`; release work only |

### Before you push

Pushes go through the [no-mistakes](https://github.com/kunchenguid/no-mistakes)
gate, which runs review, tests, lint and the checks in `.no-mistakes.yaml`
(race tests, lint, fixture guard, fresh `docs/cli`, Linux vet), then pushes and
opens the pull request. There is no GitHub Actions CI. Once per clone:

```bash
no-mistakes init                        # adds the no-mistakes remote
git config core.hooksPath .githooks     # pre-push refuses pushes that skip the gate
```

Then push with `git push no-mistakes <branch>`, or drive it from an agent with
`/no-mistakes`. To run the same checks by hand:

```bash
GOFLAGS=-race make test
make lint check-fixtures
make docs && git status --porcelain docs/cli   # must print nothing
```

## Run bam without a Bamboo

`make stub` serves a fake Bamboo Data Center from the fixtures in
`internal/provider/bamboo/testdata`. It needs no server, no token and no
network, so it is the fastest way to drive the CLI and the terminal UI by
hand. Run it from the repository root and leave it running:

```bash
make stub                                   # http://127.0.0.1:7990
make stub ARGS='-addr 127.0.0.1:8085'       # somewhere else
```

Point bam at it from another terminal. The reserved `env` alias means no
configuration file is involved:

```bash
export BAM_URL=http://127.0.0.1:7990
export BAM_TOKEN=devtoken                   # any value; the stub accepts all
bam                                         # the terminal UI
bam doctor
bam plan list PROJ
bam build show PROJ-BUILD-482
bam logs PROJ-BUILD-481 --failed
bam run PROJ-BUILD --watch
```

Reads are the fixtures, retargeted to whatever key you ask for, so every
project and plan answers. A build you trigger is simulated rather than
replayed: it stays queued for 5 seconds, runs for 10, then succeeds, which is
what `bam watch` and the UI's watch view need. `bam build cancel` stops it.
`-queued` and `-running` change those durations.

Presets are configuration, not server state, so `bam preset list` and
`bam run <preset>` still need a presets file. Point `BAM_PRESETS` at a
throwaway one written against the stub's keys (`PROJ-BUILD`, `OPS-BUILD`), so
your real `~/bam/presets.yaml` stays untouched.

## Your Bamboo for development

The fixtures in `internal/provider/bamboo/testdata/recorded/` are real Bamboo responses, scrubbed. You only need your own Bamboo to record new ones or to run `make e2e`; the unit tests replay what is already committed. Recording needs a Bamboo Data Center 9.x or newer server that you own (the personal Forge-Lab server). Never use a work server for recordings, because this repository is public.

Prepare before you record:

1. Open your Bamboo in a browser and confirm it responds.
2. Create a personal access token: your profile → Personal access tokens → Create token.
3. Pick a plan that has builds, including at least one failed build.
4. Tell bam about the server once, and store the token in your keychain. The recorder and the e2e suite read the same configuration, so nothing is exported into the environment:

   ```bash
   bam server add lab --url http://bamboo.lab.example:8085 --project LAB
   bam login lab                      # prompts for the token; it is never echoed
   bam whoami                         # confirms the token works
   ```

   With a token in a file instead of typed by hand: `bam login lab --with-token < /path/to/token`.

5. Generate `.bam.yaml` for the repository you run plans from, so the plan key has a name too:

   ```bash
   bam init --server lab --project LAB --plan LAB-PROV=provision
   ```

6. Record:

   ```bash
   make record ARGS='-target provision'          # plan key from the target
   make record ARGS='-server lab -plan LAB-PROV' # or name the plan directly
   ```

   The recorder picks the newest failed build of the plan by itself; pass `-failed LAB-PROV-12` to choose one, or `-failed skip` to record none. Add `-trigger` to also trigger a build, stop it, and record both.

   The recorder replaces your host and user names with placeholders, and `make check-fixtures` rejects anything it missed. Still read every recorded file before committing.

   `make record` also appends your real host and user names to a private denylist at `~/.config/bam/fixture-denylist.txt` (override the location with `BAM_FIXTURE_DENYLIST`), outside the repository. `make check-fixtures` fails if any of those terms show up anywhere in `testdata/`.

## Troubleshooting

| Problem | Fix |
| --- | --- |
| `go: command not found` | the PATH line is missing or the shell was not reloaded; open a new terminal |
| `golangci-lint` complains about the config version | you have v1; install v2 as above |
| a tool installed with `go install` is not found | add `$HOME/go/bin` to PATH |
| `bam: command not found` after `make install` | `$(go env GOPATH)/bin` is not on PATH; redo the PATH step and open a new terminal |
| `bam version` shows an old commit | the installed binary is stale; run `make install` again |
| `type bam` points somewhere unexpected | another `bam` shadows it; `type -a bam` lists all of them, and PATH order decides |
| Homebrew commands not found on Apple Silicon | run `eval "$(/opt/homebrew/bin/brew shellenv)"` and add it to `~/.zprofile` |
| `go` downloads fail behind a proxy | set `HTTPS_PROXY`, or `GOPROXY=direct` if your network blocks proxy.golang.org |
| `make` reports "missing separator" | a Makefile recipe line lost its leading tab; recipe lines must start with a tab, not spaces |
| `go version` shows a different Go than you installed | two Go installs; `command -v go` tells you which one wins, and PATH order decides. Remove the one you do not want |
| CI fails on `git diff --exit-code docs/cli` | the command reference is stale; run `make docs` and commit the result |
| `gh` asks for credentials | run `gh auth login` once per machine |
| `goreleaser` leaves a `dist/` directory | expected; `dist/` is in `.gitignore`. `--clean` rebuilds it from scratch |
