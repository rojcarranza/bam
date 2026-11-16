#!/bin/bash
# Inside the container: build the history the recordings read, as jdoe, and write the
# build keys the tapes name to build/keys.env. Safe to run again; it only adds builds.
set -euo pipefail
D=/demo/docs/demos
. "$D/home.sh"; . "$D/lib.sh"
mkdir -p "$D/build"
fresh_home; clone_service; use_config
wait_idle

# Five green runs by jdoe, so the newest builds of CI show a manual run and nothing else.
it_align pass
for _ in 1 2 3 4 5; do
  it_align pass
  bam run ci --watch >/dev/null 2>&1 || true
done
CI_GOOD=$(bam build list SETLIST-CI --state success -n 1 --json | jq -r '.[0].key')

# A red build of the feature branch plan: 12 failing tests.
git switch -q -c capo-transpose origin/feat/capo-transpose
bam run ci --branch @ --watch >/dev/null 2>&1 || true
CI0_RED=$(bam build list SETLIST-CI --branch capo-transpose --state failed -n 1 --json | jq -r '.[0].key')

cat > "$D/build/keys.env" <<KEYS
CI_GOOD=$CI_GOOD
CI0_RED=$CI0_RED
KEYS
echo "seeded: CI_GOOD=$CI_GOOD CI0_RED=$CI0_RED"
