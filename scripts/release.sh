#!/bin/sh
# Publish bam vX.Y.Z from the owner's machine: tag, push the tag, and have
# goreleaser build .goreleaser.yaml and publish the GitHub release with this
# version's CHANGELOG.md section as the notes. Run through `make release VERSION=x.y.z`.
set -eu

v="${1:-}"
die() { echo "release: $*" >&2; exit 1; }

echo "$v" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || die "usage: make release VERSION=x.y.z (got '$v')"
[ -z "$(git status --porcelain)" ] || die "working tree is not clean"
git fetch -q origin main --tags
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || die "HEAD is not origin/main; check out and pull main first"
! git rev-parse -q --verify "refs/tags/v$v" >/dev/null || die "tag v$v already exists"

# The notes are this version's section of CHANGELOG.md, written outside the
# checkout, which goreleaser requires to be clean.
notes=$(mktemp)
trap 'rm -f "$notes"' EXIT
awk -v v="$v" 'index($0, "## [" v "]") == 1 {on=1; next} on && /^## \[/ {exit} on && /^\[/ {exit} on {print}' CHANGELOG.md > "$notes"
grep -q '[^[:space:]]' "$notes" || die "CHANGELOG.md has no section for $v; rename [Unreleased] first"

token=$(gh auth token) || die "gh is not logged in; run gh auth login"
command -v goreleaser >/dev/null || die "goreleaser v2 is not installed"
go test ./...
git tag -a "v$v" -m "v$v"
# The pre-push hook only lets pushes through the no-mistakes gate; a release tag
# of an already-validated main commit goes straight to origin.
git push --no-verify origin "v$v" || { git tag -d "v$v"; die "push of v$v failed"; }
GITHUB_TOKEN="$token" goreleaser release --clean --release-notes="$notes"
