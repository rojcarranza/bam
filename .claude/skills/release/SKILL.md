---
name: release
description: Publish a bam release - rename the CHANGELOG section, tag vX.Y.Z and publish the GitHub release with make release. Use when the owner asks to cut, tag, publish or release a bam version. A release needs the owner's go-ahead first.
---

# Releasing bam

A release is published from the maintainer's machine. There is no release
workflow. Do not tag, push a tag or run `make release` without the owner's
go-ahead for that version.

## Steps

1. Merge a pull request that renames `## [Unreleased]` in `CHANGELOG.md` to
   `## [x.y.z] - YYYY-MM-DD`, adds a fresh empty `## [Unreleased]` and the
   compare link. The section is the release notes, so it lists only what the
   release brings its users.
2. With `gh auth login` done, bring up to date:

   ```bash
   git checkout main && git pull
   ```

3. Publish:

   ```bash
   make release VERSION=x.y.z
   ```

## What `make release` does

`scripts/release.sh` refuses before it changes anything unless:

- `VERSION` is `x.y.z` (digits only),
- the working tree is clean,
- `HEAD` is `origin/main`,
- the tag `vx.y.z` does not exist yet,
- `CHANGELOG.md` has a non-empty section for `x.y.z`,
- `gh` is logged in and `goreleaser` is installed (v2 is required by `.goreleaser.yaml`).

Then it runs `go test ./...`, creates the annotated tag `vx.y.z`, and pushes
the tag to `origin` with `--no-verify`. The pre-push hook would otherwise
refuse it, because the hook only lets pushes through the no-mistakes gate, and
the tag is of a commit that main already validated. If the push fails, the
script deletes the local tag.

Finally it runs `goreleaser release --clean` with the `CHANGELOG.md` section
as the release notes. `.goreleaser.yaml` builds the archives (darwin and linux
on amd64 and arm64, windows on amd64 as a zip) and publishes the GitHub
release.

## If goreleaser fails after the tag is pushed

The tag stays on origin. Fix the cause, then publish the release on that tag
from a file that holds the section:

```bash
GITHUB_TOKEN=$(gh auth token) goreleaser release --clean --release-notes=<file with the section>
```

Do not re-run `make release` for the same version: its tag check refuses.
