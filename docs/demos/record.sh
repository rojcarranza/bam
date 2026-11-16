#!/bin/bash
# Inside the container: render tapes with live build keys and record them.
#   record.sh NAME...   record those tapes (default: every tape in tapes/)
set -euo pipefail
# The jdoe token, for tapes that type it into the login prompt; the rendered tape is deleted after recording.
DEMO_TOKEN=$(cat /run/secrets/jdoe-token); export DEMO_TOKEN
D=/demo/docs/demos
cd "$D"
. "$D/home.sh"
. "$D/lib.sh"
[ -f "$D/build/keys.env" ] && { set -a; . "$D/build/keys.env"; set +a; }
names=("$@"); [ ${#names[@]} -eq 0 ] && mapfile -t names < <(cd "$D/tapes" && ls *.tape | grep -v '^_' | sed 's/\.tape$//')
mkdir -p "$D/build" "$D/gifs" "$D/text"
for n in "${names[@]}"; do
  cd "$D"
  src=$D/tapes/$n.tape
  [ -f "$src" ] || { echo "no tape $src" >&2; exit 1; }
  # {{NAME}} in a tape is replaced by the build key or value of that name from build/keys.env.
  out=$D/build/$n.tape
  cp "$src" "$out"
  # vhs writes the screen text a moment behind the GIF: a hidden clear at the end flushes the last screen into it.
  printf 'Sleep 1s\nHide\nType "clear"\nEnter\nSleep 1s\n' >> "$out"
  for v in $(grep -o '{{[A-Z0-9_]*}}' "$src" | sort -u | tr -d '{}'); do
    [ -n "${!v:-}" ] || { echo "$src needs $v, which build/keys.env does not set (run make demos-seed)" >&2; exit 1; }
    export V="$v"
    awk 'BEGIN{k="{{" ENVIRON["V"] "}}"; r=ENVIRON[ENVIRON["V"]]} {while((i=index($0,k))>0) $0=substr($0,1,i-1) r substr($0,i+length(k)); print}' "$out" > "$out.new" && mv "$out.new" "$out"
  done
  # A "# demo: no-login" line starts the tape from a home that has not met the server yet.
  if grep -q '^# demo: no-login' "$src"; then export DEMO_NO_LOGIN=1; else unset DEMO_NO_LOGIN; fi
  fresh_home
  # "# demo: in-repo" clones the sample service; "# demo: config" also installs its .bam.yaml;
  # "# demo: branch" checks out the red feature branch; "# demo: it-next=fail|pass" sets what the next
  # SETLIST-IT build does; "# demo: idle" waits until no SETLIST build is running or queued.
  if grep -q '^# demo: \(in-repo\|config\|branch\)' "$src"; then clone_service; fi
  grep -q '^# demo: config' "$src" && use_config
  grep -q '^# demo: branch' "$src" && git switch -q -c capo-transpose origin/feat/capo-transpose
  if grep -q '^# demo: idle' "$src"; then wait_idle; fi
  if grep -q '^# demo: it-next=fail' "$src"; then wait_idle; it_align fail; fi
  if grep -q '^# demo: it-next=pass' "$src"; then wait_idle; it_align pass; fi
  cd "$HOME"
  echo "recording $n"
  (cd "$D" && vhs -q "$out"); rm -f "$out"
  # vhs appends every screen it saw; keep each distinct line once, in order, as the screen text.
  awk 'NF && !seen[$0]++' "$D/text/$n.txt" > "$D/text/$n.txt.tmp" && mv "$D/text/$n.txt.tmp" "$D/text/$n.txt"
done
