#!/bin/bash
# Host side: build the recording image and run one command inside it, with the lab Bamboo
# (port-forwarded to localhost:8085) behind a proxy that answers as bamboo.example.com.
#   run.sh bash                      a shell inside the recording container
#   run.sh /demo/docs/demos/seed.sh  build the history the GIFs need
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
token=${BAM_DEMO_TOKEN_FILE:-$HOME/.forgelab/lab-jdoe-token}
[ -r "$token" ] || { echo "no jdoe token file at $token (set BAM_DEMO_TOKEN_FILE)" >&2; exit 1; }
curl -fsS -o /dev/null -m 5 -H "Authorization: Bearer $(cat "$token")" http://localhost:8085/rest/api/latest/info \
  || { echo "the lab Bamboo does not answer on localhost:8085" >&2; exit 1; }
docker build -q -t bam-demos "$here" >/dev/null
mkdir -p "$root/bin"
(cd "$root" && CGO_ENABLED=0 GOOS=linux go build -o bin/linux/bam ./cmd/bam)
tty=-i; [ -t 0 ] && tty=-it
exec docker run --rm $tty --network host --shm-size 1g \
  -v "$root:/demo" -v "$root/bin/linux:/demo/bin:ro" -v "$token:/run/secrets/jdoe-token:ro" \
  -w /demo bam-demos "$@"
