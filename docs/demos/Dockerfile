# Recording image: VHS 0.10 (ttyd, ffmpeg, Chromium) plus Caddy and git. VHS 0.12 runs the
# tape but writes no GIF in this setup, so the recordings pin 0.10.
FROM caddy:2.10.2 AS caddy

FROM ghcr.io/charmbracelet/vhs:v0.10.0
COPY --from=caddy /usr/bin/caddy /usr/bin/caddy
RUN apt-get update --allow-releaseinfo-change \
 && apt-get install -y --no-install-recommends git ca-certificates jq bash-completion \
 && rm -rf /var/lib/apt/lists/*
COPY xdg-open /usr/local/bin/xdg-open
COPY Caddyfile /etc/caddy/Caddyfile
COPY entry.sh /usr/local/bin/demo-entry
ENTRYPOINT ["/usr/local/bin/demo-entry"]
