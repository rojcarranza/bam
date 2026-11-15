#!/bin/bash
# Runs inside the recording container. Starts the proxy, trusts its CA, then runs the command
# it is given. home.sh gives each recording a clean home.
set -euo pipefail

echo "127.0.0.1 bamboo.example.com" >> /etc/hosts
caddy start --config /etc/caddy/Caddyfile --adapter caddyfile >/tmp/caddy.log 2>&1
for _ in $(seq 1 50); do
  root=$(find /root/.local/share/caddy/pki/authorities/local -name root.crt 2>/dev/null | head -1)
  [ -n "$root" ] && break
  sleep 0.2
done
cp "$root" /usr/local/share/ca-certificates/demo-caddy.crt
update-ca-certificates >/dev/null 2>&1

exec "$@"
