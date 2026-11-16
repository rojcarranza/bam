# Helpers shared by seed.sh and record.sh; sourced after home.sh, inside the container.

# Wait until nothing of SETLIST is building or queued, so one recording never starts behind another.
wait_idle() {
  local n r
  for _ in $(seq 1 120); do
    n=$(bam queue --json 2>/dev/null | jq '[.[] | select(.key | startswith("SETLIST"))] | length' 2>/dev/null || echo 0)
    r=$(bam build list SETLIST-LOAD -n 1 --json | jq -r '.[0].state')
    [ "$n" = 0 ] && [ "$r" != "building" ] && [ "$r" != "running" ] && [ "$r" != "queued" ] && return 0
    sleep 5
  done
  echo "SETLIST builds never went idle" >&2; return 1
}

# The number of the newest SETLIST-IT build.
it_last() { bam build list SETLIST-IT -n 1 --json | jq -r '.[0].number'; }

# SETLIST-IT fails its first attempt on every 4th build. Run CI until the next IT build is
# (fail) or is not (pass) one of those, so a recording knows what its run will do.
it_align() {
  local want next
  want=$1
  for _ in 1 2 3 4 5; do
    next=$(( $(it_last) + 1 ))
    if [ "$want" = fail ] && [ $((next % 4)) -eq 0 ]; then return 0; fi
    if [ "$want" = pass ] && [ $((next % 4)) -ne 0 ] && [ $((next % 4)) -ne 3 ]; then return 0; fi
    bam run ci --watch >/dev/null 2>&1 || true
  done
}

# Install the demo clone's .bam.yaml and the secret its deploy preset reads.
use_config() {
  cp /demo/docs/demos/fixtures/setlist.bam.yaml "$HOME/setlist-service/.bam.yaml"
}
