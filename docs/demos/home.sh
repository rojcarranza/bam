# Sourced by record.sh and seed.sh: make a clean home for jdoe, with bam logged in unless
# the caller sets DEMO_NO_LOGIN. Nothing from an earlier recording survives: no config,
# cache, usage tally, credentials or clone.
fresh_home() {
  export HOME=/home/jdoe
  rm -rf "$HOME" && mkdir -p "$HOME" && cd "$HOME"
  export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache" XDG_STATE_HOME="$HOME/.local/state"
  export PATH="/demo/bin:$PATH" LANG=C.UTF-8 BAM_NO_UPDATE_CHECK=1
  git config --global user.name "J Doe"
  git config --global user.email "jdoe@example.com"
  git config --global init.defaultBranch main
  if [ -z "${DEMO_NO_LOGIN:-}" ]; then
    bam server add work --url https://bamboo.example.com --project SETLIST >/dev/null
    bam login work --with-token </run/secrets/jdoe-token >/dev/null
  fi
}
# A clone of the sample service, for demos that run bam inside a repository.
clone_service() {
  git clone -q https://github.com/rojcarranza/setlist-service.git "$HOME/setlist-service"
  cd "$HOME/setlist-service"
}
# A made-up secret: the deploy preset reads it from the environment, bam prints it as ********.
export DB_PASSWORD=demo-password-1
# Long logs print straight to the screen instead of waiting in a pager.
export PAGER=cat
