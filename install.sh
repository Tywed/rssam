#!/bin/sh
# rssam installer / updater. Also installed as /usr/local/sbin/rssam-update,
# where the root-owned rssam-ops.service runs it with --request to execute a
# restart or update asked for from the admin UI (the service itself never
# runs anything as root).
set -eu

main() {
  REPO="${GITHUB_REPO:-Tywed/rssam}"
  PREFIX="${RSSAM_PREFIX:-/opt/rssam}"
  BIN_DIR="$PREFIX/bin"
  ENV_FILE="$PREFIX/.env"
  RUN_DIR="$PREFIX/run"
  REQUEST_FILE="$RUN_DIR/ops.request"
  STAGE_ROOT="${RSSAM_STAGE_ROOT:-/var/lib/rssam-update}"
  LOG_FILE="${RSSAM_UPDATE_LOG:-/var/log/rssam-update.log}"
  RELEASE_BASE="${RSSAM_RELEASE_BASE:-https://github.com/${REPO}/releases/download}"
  HELPER=/usr/local/sbin/rssam-update
  SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
  QUIET=0
  ACTION=install
  TARGET_VER=""
  WITH_PG=0
  REMOVE_ENV=0
  WITH_BACKUP=1
  ALLOW_DOWNGRADE=0
  LOG_TEE=0
  STAGE=""
  NEW_PASSWORD=""

  while [ $# -gt 0 ]; do
    case "$1" in
      --quiet|-q) QUIET=1 ;;
      --with-postgres) WITH_PG=1 ;;
      --remove-env) REMOVE_ENV=1 ;;
      --no-backup) WITH_BACKUP=0 ;;
      --allow-downgrade) ALLOW_DOWNGRADE=1 ;;
      --help|-h) usage; exit 0 ;;
      --update) ACTION=update ;;
      --remove) ACTION=remove ;;
      --migrate-layout) ACTION=migrate ;;
      --request) ACTION=request ;;
      install|update|remove) ACTION=$1 ;;
      v*.*.*|[0-9]*.[0-9]*) TARGET_VER=$1 ;;
      *) echo "unknown arg: $1" >&2; usage; exit 1 ;;
    esac
    shift
  done

  if [ -n "$TARGET_VER" ] && ! valid_tag "$TARGET_VER"; then
    die "invalid version: $TARGET_VER"
  fi
  trap cleanup EXIT

  case "$ACTION" in
    install) do_install ;;
    update) LOG_TEE=1; do_update ;;
    migrate) do_migrate ;;
    request) do_request ;;
    remove) do_remove ;;
    *) usage; exit 1 ;;
  esac
}

usage() {
  cat <<EOF
Usage: install.sh [install|update|remove] [VERSION] [options]
  install            install or repair: binary, units, helper, backup timer (default)
  --migrate-layout   switch an existing install to the root-owned layout and
                     rssam-ops units (after a binary-only update from <= 0.3.6)
  update [vX.Y.Z]    replace binary from GitHub Release (DB backup first, no downgrade)
  remove             stop unit, remove binary and units (keep .env and DB)
  --quiet            non-interactive
  --with-postgres    create role/db rssam if missing (needs peer/sudo postgres)
  --no-backup        do not install the daily backup timer (rssam-backup.timer)
  --allow-downgrade  let update install an older version
  --help
EOF
}

log() {
  printf '%s\n' "$*"
  if [ "$LOG_TEE" -eq 1 ]; then
    printf '%s\n' "$*" >>"$LOG_FILE" 2>/dev/null || true
  fi
}
die() {
  printf 'error: %s\n' "$*" >&2
  if [ "$LOG_TEE" -eq 1 ]; then
    printf 'error: %s\n' "$*" >>"$LOG_FILE" 2>/dev/null || true
  fi
  exit 1
}
cleanup() {
  [ -n "$STAGE" ] && rm -rf "$STAGE"
  return 0
}

# Only a plain semver tag may reach the download URL: the script runs as root
# and the version is its single untrusted argument (it can come from the
# request file the service writes).
valid_tag() {
  printf '%s' "$1" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
}

need_root() {
  [ "$(id -u)" -eq 0 ] || die "run as root"
}

latest_tag() {
  tag=$(curl -fsSL -A rssam "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1) || true
  if [ -n "$tag" ]; then
    printf '%s\n' "$tag"
    return 0
  fi
  loc=$(curl -fsSL -A rssam -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest") || return 1
  printf '%s\n' "$loc" | sed -n 's!.*/tag/\([^/]*\)$!\1!p'
}

fetch() {
  curl -fsSL -A rssam -o "$2" "$1"
}

ensure_user() {
  if ! id rssam >/dev/null 2>&1; then
    useradd --system --home "$PREFIX" --shell /usr/sbin/nologin rssam
  fi
}

# Directory layout. Everything the service must not change is root-owned;
# it can write only its log, backups, the request directory and .env.
ensure_layout() {
  mkdir -p "$PREFIX" "$BIN_DIR" "$PREFIX/log" "$PREFIX/backups" "$RUN_DIR" "$STAGE_ROOT"
  chown root:root "$PREFIX" "$BIN_DIR" "$STAGE_ROOT"
  chmod 755 "$PREFIX" "$BIN_DIR"
  chmod 700 "$STAGE_ROOT"
  chown rssam:rssam "$PREFIX/log" "$PREFIX/backups" "$RUN_DIR"
  chmod 750 "$PREFIX/log" "$RUN_DIR"
  chmod 700 "$PREFIX/backups"
  if [ -f "$ENV_FILE" ]; then
    chown rssam:rssam "$ENV_FILE"
    chmod 600 "$ENV_FILE"
  fi
  if [ -f "$BIN_DIR/rssam" ]; then
    chown root:root "$BIN_DIR/rssam"
    chmod 755 "$BIN_DIR/rssam"
  fi
  touch "$LOG_FILE"
  chown root:root "$LOG_FILE"
  chmod 644 "$LOG_FILE"
  # The sudoers-based mechanism of older releases is replaced by rssam-ops.
  rm -f /etc/sudoers.d/rssam
}

# deploy_file NAME DEST MODE: the file from the release archive (STAGE),
# else from the checkout the script runs in, else from the repository at
# the given tag.
deploy_file() {
  name=$1; dest=$2; mode=$3; ver=$4
  if [ -n "$STAGE" ] && [ -f "$STAGE/deploy/$name" ]; then
    install -o root -g root -m "$mode" "$STAGE/deploy/$name" "$dest"
  elif [ -f "$SCRIPT_DIR/deploy/$name" ]; then
    install -o root -g root -m "$mode" "$SCRIPT_DIR/deploy/$name" "$dest"
  else
    tmp=$(mktemp)
    if ! fetch "https://raw.githubusercontent.com/${REPO}/${ver}/deploy/$name" "$tmp"; then
      rm -f "$tmp"
      return 1
    fi
    install -o root -g root -m "$mode" "$tmp" "$dest"
    rm -f "$tmp"
  fi
}

install_units() {
  ver=$1
  mkdir -p /etc/systemd/system
  deploy_file rssam.service /etc/systemd/system/rssam.service 644 "$ver" || die "cannot obtain rssam.service"
  deploy_file rssam-ops.path /etc/systemd/system/rssam-ops.path 644 "$ver" || die "cannot obtain rssam-ops.path"
  deploy_file rssam-ops.service /etc/systemd/system/rssam-ops.service 644 "$ver" || die "cannot obtain rssam-ops.service"
  systemctl daemon-reload
  systemctl enable --now rssam-ops.path
}

install_helper() {
  mkdir -p /usr/local/sbin
  if [ -n "$STAGE" ] && [ -f "$STAGE/install.sh" ]; then
    install -o root -g root -m 755 "$STAGE/install.sh" "$HELPER"
  elif [ -f "$SCRIPT_DIR/install.sh" ]; then
    install -o root -g root -m 755 "$SCRIPT_DIR/install.sh" "$HELPER"
  else
    install -o root -g root -m 755 "$0" "$HELPER"
  fi
}

maybe_postgres() {
  [ "$WITH_PG" -eq 1 ] || return 0
  command -v psql >/dev/null || die "psql not found"
  su -s /bin/sh postgres -c "psql -tc \"SELECT 1 FROM pg_roles WHERE rolname='rssam'\"" | grep -q 1 || \
    su -s /bin/sh postgres -c "createuser -P rssam" || true
  # Russian FTS and ILIKE need a ctype that folds Cyrillic case; a database
  # created under locale=C silently misses matches. C.UTF-8 ships with every
  # glibc since 2.35 and with PostgreSQL 17's builtin provider.
  su -s /bin/sh postgres -c "psql -tc \"SELECT 1 FROM pg_database WHERE datname='rssam'\"" | grep -q 1 || \
    su -s /bin/sh postgres -c "createdb -O rssam --template=template0 --encoding=UTF8 --locale=C.UTF-8 rssam" || \
    su -s /bin/sh postgres -c "createdb -O rssam --template=template0 --encoding=UTF8 --locale=C.utf8 rssam" || \
    die "createdb with an UTF-8 locale failed; create it by hand: createdb -O rssam --template=template0 --encoding=UTF8 --locale=<UTF-8 locale> rssam"
  if ! su -s /bin/sh postgres -c "psql -Atc \"SELECT lower('Ж') = 'ж'\" rssam" | grep -q t; then
    log "warning: database rssam does not fold Cyrillic case (locale=C?); Russian search will miss matches — recreate it with an UTF-8 locale"
  fi
  # Full-page images dominate rssam's WAL (mark-read touches scattered
  # pages): lz4 halves WAL bytes for ~1 % CPU. Cluster-wide, so only advised.
  if su -s /bin/sh postgres -c "psql -Atc 'SHOW wal_compression'" | grep -qx off; then
    log "hint: wal_compression is off; halve WAL writes with: ALTER SYSTEM SET wal_compression = 'lz4'; SELECT pg_reload_conf();"
  fi
  # A page is written to WAL in full once per checkpoint; with the default
  # 5-minute checkpoints a page that readers touch all day is copied up to
  # 288 times. Half-hour checkpoints cut that 6x (measured: 7.1 -> 2.4 kB
  # of WAL per mark-read on warm pages) for a longer crash recovery, still
  # well under a minute at rssam's write rate.
  if su -s /bin/sh postgres -c "psql -Atc 'SHOW checkpoint_timeout'" | grep -qx 5min; then
    log "hint: checkpoint_timeout is 5min; fewer full-page WAL writes with: ALTER SYSTEM SET checkpoint_timeout = '30min'; ALTER SYSTEM SET max_wal_size = '2GB'; SELECT pg_reload_conf();"
  fi
}

# 20 alphanumerics from /dev/urandom (~119 bits); printed once at the end of
# install and kept only in .env.
gen_password() {
  LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 20
}

# NEW_PASSWORD is set only when this run created .env; do_install prints it.
write_env() {
  [ -f "$ENV_FILE" ] && return 0
  NEW_PASSWORD=$(gen_password)
  example=""
  if [ -n "$STAGE" ] && [ -f "$STAGE/.env.example" ]; then
    example="$STAGE/.env.example"
  elif [ -f "$SCRIPT_DIR/.env.example" ]; then
    example="$SCRIPT_DIR/.env.example"
  fi
  if [ -n "$example" ]; then
    # The example ships placeholders the service refuses on a fresh database.
    sed -e "s/^ADMIN_PASSWORD=.*/ADMIN_PASSWORD=${NEW_PASSWORD}/" \
        -e "s/^METRICS_TOKEN=.*/METRICS_TOKEN=/" \
        "$example" >"$ENV_FILE"
  else
    cat >"$ENV_FILE" <<EOF
DATABASE_URL=postgres://rssam:rssam@127.0.0.1:5432/rssam?sslmode=disable
LISTEN_ADDR=:8080
RUN_MIGRATIONS=true
UI_ENABLED=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=${NEW_PASSWORD}
WORKER_POOL_SIZE=10
WEBHOOK_WORKER_POOL_SIZE=10
GITHUB_REPO=${REPO}
EOF
  fi
  chmod 600 "$ENV_FILE"
  chown rssam:rssam "$ENV_FILE"
}

# Backup script and a daily timer, from the release archive when possible so
# the backup tooling always matches the release.
install_backup() {
  [ "$WITH_BACKUP" -eq 1 ] || return 0
  ver=$1
  deploy_file rssam-backup.sh /usr/local/sbin/rssam-backup 755 "$ver" || { log "warning: could not obtain rssam-backup.sh; backup timer not installed"; return 0; }
  deploy_file rssam-backup.service /etc/systemd/system/rssam-backup.service 644 "$ver" || { log "warning: could not obtain rssam-backup.service"; return 0; }
  deploy_file rssam-backup.timer /etc/systemd/system/rssam-backup.timer 644 "$ver" || { log "warning: could not obtain rssam-backup.timer"; return 0; }
  systemctl daemon-reload
  # Let rssam watch the dumps (system alert "backup stale").
  if [ -f "$ENV_FILE" ] && ! grep -q '^BACKUP_DIR=' "$ENV_FILE"; then
    printf 'BACKUP_DIR=%s/backups\n' "$PREFIX" >>"$ENV_FILE"
  fi
  if command -v pg_dump >/dev/null 2>&1; then
    systemctl enable --now rssam-backup.timer
  else
    log "warning: pg_dump not found; install postgresql-client and run: systemctl enable --now rssam-backup.timer"
  fi
}

# Map uname -m to the GOARCH used in release asset names.
release_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m) (releases ship linux/amd64 and linux/arm64)" ;;
  esac
}

# run_unprivileged CMD...: execute as nobody. A downloaded binary is never
# executed as root, even just for --version.
run_unprivileged() {
  if command -v setpriv >/dev/null 2>&1; then
    setpriv --reuid=nobody --regid=nogroup --clear-groups --inh-caps=-all -- "$@"
  elif command -v runuser >/dev/null 2>&1; then
    runuser -u nobody -- "$@"
  else
    cmd=""
    for a in "$@"; do cmd="$cmd '$a'"; done
    su -s /bin/sh nobody -c "$cmd"
  fi
}

# version_of BINARY: "0.3.7" from "rssam v0.3.7 (...)"; empty for dev builds.
version_of() {
  out=$(run_unprivileged "$1" --version 2>/dev/null) || return 1
  printf '%s\n' "$out" | awk '{print $2}' | sed -n 's/^v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p'
}

# semver_lt A B: numeric compare of x.y.z (prerelease suffixes ignored).
semver_lt() {
  printf '%s\n%s\n' "$1" "$2" | awk -F. 'NR==1{a1=$1;a2=$2;a3=$3} NR==2{ if (a1!=$1) exit !(a1<$1); if (a2!=$2) exit !(a2<$2); exit !(a3<$3) }'
}

# Fetch, verify and unpack the release into STAGE (root-owned staging area,
# world-readable so `nobody` can execute the binary for --version). Leaves
# $STAGE/rssam and, for releases that ship them, $STAGE/deploy/* and
# $STAGE/install.sh.
stage_release() {
  ver=$1
  arch=$(release_arch)
  asset="rssam-linux-${arch}.tar.gz"
  mkdir -p "$STAGE_ROOT"
  chmod 700 "$STAGE_ROOT"
  STAGE=$(mktemp -d "$STAGE_ROOT/stage.XXXXXX")
  base="$RELEASE_BASE/${ver}"
  log "fetch $base/$asset"
  fetch "$base/$asset" "$STAGE/$asset"
  # Prefer the release-wide SHA256SUMS (one attested file); fall back to the
  # per-asset .sha256 that older releases ship. Refuse to install when neither
  # is available: an unverifiable download is not worth a root-owned binary.
  expect=""
  if fetch "$base/SHA256SUMS" "$STAGE/SHA256SUMS" 2>/dev/null && [ -s "$STAGE/SHA256SUMS" ]; then
    expect=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$STAGE/SHA256SUMS" | head -1)
  fi
  if [ -z "$expect" ] && fetch "$base/$asset.sha256" "$STAGE/$asset.sha256" 2>/dev/null && [ -s "$STAGE/$asset.sha256" ]; then
    expect=$(tr -d ' \n\t' <"$STAGE/$asset.sha256")
  fi
  case "$expect" in
    [a-fA-F0-9]*) [ ${#expect} -eq 64 ] || expect="" ;;
    *) expect="" ;;
  esac
  [ -n "$expect" ] || die "no checksum available for $asset (SHA256SUMS or .sha256)"
  got=$(sha256sum "$STAGE/$asset" | awk '{print $1}')
  [ "$expect" = "$got" ] || die "sha256 mismatch for $asset"
  tar -xzf "$STAGE/$asset" -C "$STAGE"
  [ -f "$STAGE/rssam" ] || die "binary missing in archive"
  chmod 755 "$STAGE_ROOT" "$STAGE"
  chmod 755 "$STAGE/rssam"
  new_ver=$(version_of "$STAGE/rssam") || die "new binary failed --version"
  if [ -x "$BIN_DIR/rssam" ] && [ "$ALLOW_DOWNGRADE" -eq 0 ]; then
    cur_ver=$(version_of "$BIN_DIR/rssam" || true)
    if [ -n "$cur_ver" ] && [ -n "$new_ver" ] && semver_lt "$new_ver" "$cur_ver"; then
      die "refusing to downgrade $cur_ver -> $new_ver (use --allow-downgrade from a root shell)"
    fi
  fi
  chmod 700 "$STAGE_ROOT"
}

# Replace the binary, keeping the last three previous ones next to it.
install_binary() {
  if [ -f "$BIN_DIR/rssam" ]; then
    ts=$(date +%Y%m%d_%H%M%S)
    cp -p "$BIN_DIR/rssam" "$BIN_DIR/rssam.backup.$ts"
    # Names carry a sortable timestamp, so lexical order is chronological.
    for old in "$BIN_DIR"/rssam.backup.*; do printf '%s\n' "$old"; done | sort -r | tail -n +4 | while IFS= read -r old; do rm -f -- "$old"; done
  fi
  install -o root -g root -m 755 "$STAGE/rssam" "$BIN_DIR/rssam.new"
  mv -f "$BIN_DIR/rssam.new" "$BIN_DIR/rssam"
}

# listen_port: from LISTEN_ADDR in .env (":8080" or "host:port"), default 8080.
listen_port() {
  p=$(sed -n 's/^LISTEN_ADDR=.*:\([0-9][0-9]*\)[" ]*$/\1/p' "$ENV_FILE" 2>/dev/null | head -1)
  printf '%s\n' "${p:-8080}"
}

wait_healthy() {
  port=$(listen_port)
  # Migrations run before the listener opens, so /healthz stays down for as
  # long as they take (a data migration on a large table can run minutes).
  # Keep waiting while the unit is active; only a unit that died is a failure.
  n=0
  while [ "$n" -lt 600 ]; do
    if curl -sf --retry 0 "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    if [ "$n" -ge 5 ] && ! systemctl is-active --quiet rssam; then
      die "service exited during start (journalctl -u rssam -n 50)"
    fi
    [ "$n" -eq 30 ] && log "still starting after 30 s (migrations?), waiting up to 10 min"
    n=$((n + 1))
    sleep 1
  done
  die "service did not become healthy within 10 min"
}

do_install() {
  need_root
  ensure_user
  ensure_layout
  maybe_postgres
  ver=$TARGET_VER
  [ -n "$ver" ] || ver=$(latest_tag)
  [ -n "$ver" ] || die "no GitHub release; pass a version or publish a tag"
  stage_release "$ver"
  write_env
  install_binary
  install_units "$ver"
  install_helper
  install_backup "$ver"
  systemctl enable rssam
  systemctl restart rssam
  sleep 1
  wait_healthy
  log "installed $ver  UI http://<host>:$(listen_port)/ui/login"
  if [ -n "$NEW_PASSWORD" ]; then
    log "admin login: admin / ${NEW_PASSWORD}  (kept in $ENV_FILE; change it in Settings after the first login)"
  fi
}

# Existing install, binary already current: bring units, helper and
# ownership to this release's layout. The deploy files come from the release
# archive of the installed version, so this also works when the script is
# piped from curl.
do_migrate() {
  need_root
  ensure_user
  [ -x "$BIN_DIR/rssam" ] || die "no $BIN_DIR/rssam; run install instead"
  ensure_layout
  cur=$(version_of "$BIN_DIR/rssam") || die "installed binary failed --version"
  ver="v$cur"
  stage_release "$ver"
  install_units "$ver"
  install_helper
  if [ -x /usr/local/sbin/rssam-backup ]; then
    install_backup "$ver"
  fi
  systemctl restart rssam
  wait_healthy
  log "layout migrated; rssam $ver running under the hardened unit"
}

# What the new binary would do to the database, in the update log before the
# dump is taken: run as the service user with its own .env, never as root.
migrate_plan() {
  [ -f "$ENV_FILE" ] || return 0
  chmod 755 "$STAGE_ROOT"
  plan=$(su -s /bin/sh rssam -c "set -a; . '$ENV_FILE'; set +a; exec '$STAGE/rssam' -migrate-plan" 2>&1) || true
  chmod 700 "$STAGE_ROOT"
  log "migrate-plan: $plan"
}

# A database dump before the binary changes: migrations are not reversible.
backup_before_update() {
  if ! systemctl list-unit-files rssam-backup.service >/dev/null 2>&1 || \
     ! systemctl list-unit-files rssam-backup.service | grep -q '^rssam-backup.service'; then
    log "no rssam-backup.service; skipping pre-update database backup"
    return 0
  fi
  log "database backup before update"
  systemctl start rssam-backup.service || die "pre-update backup failed (journalctl -u rssam-backup -n 30); update aborted"
}

do_update() {
  need_root
  ensure_layout
  log "=== update $(date -u) ==="
  ver=$TARGET_VER
  [ -n "$ver" ] || ver=$(latest_tag)
  [ -n "$ver" ] || die "no release (GitHub API/redirect failed)"
  log "target $ver"
  stage_release "$ver"
  migrate_plan
  backup_before_update
  install_binary
  log "installed binary $ver"
  # Tooling follows the binary: units, helper and backup script of the release.
  install_units "$ver"
  install_helper
  if [ -x /usr/local/sbin/rssam-backup ]; then
    install_backup "$ver"
  fi
  log "restarting rssam"
  systemctl restart rssam
  wait_healthy
  log "ok $ver"
}

# Executed by rssam-ops.service (root) when the service has written a
# request. The file lives in a directory owned by the service user, so it
# is treated as untrusted: regular file only, first line only, strict shape.
do_request() {
  need_root
  if [ ! -e "$REQUEST_FILE" ] && [ ! -L "$REQUEST_FILE" ]; then
    exit 0
  fi
  # The request must be gone when this unit finishes, whatever it contained:
  # rssam-ops.path fires on PathExists= and would otherwise retrigger until
  # the start-rate limit kills the path unit.
  if [ -L "$REQUEST_FILE" ] || [ ! -f "$REQUEST_FILE" ]; then
    rm -rf -- "$REQUEST_FILE"
    die "request is not a regular file"
  fi
  req=$(head -c 128 "$REQUEST_FILE" | head -n 1 | tr -d '\r')
  rm -f -- "$REQUEST_FILE"
  log "=== request '$req' $(date -u) ==="
  case "$req" in
    restart)
      systemctl restart rssam
      log "ok restart"
      ;;
    update\ *)
      TARGET_VER=${req#update }
      valid_tag "$TARGET_VER" || die "invalid version in request"
      do_update
      ;;
    *)
      die "invalid request"
      ;;
  esac
}

do_remove() {
  need_root
  systemctl stop rssam 2>/dev/null || true
  systemctl disable rssam 2>/dev/null || true
  systemctl disable --now rssam-backup.timer 2>/dev/null || true
  systemctl disable --now rssam-ops.path 2>/dev/null || true
  rm -f /etc/systemd/system/rssam.service /etc/systemd/system/rssam-backup.service /etc/systemd/system/rssam-backup.timer \
        /etc/systemd/system/rssam-ops.path /etc/systemd/system/rssam-ops.service
  systemctl daemon-reload
  rm -f "$BIN_DIR/rssam" "$BIN_DIR"/rssam.backup.* "$HELPER" /usr/local/sbin/rssam-backup /etc/sudoers.d/rssam
  rm -rf "$STAGE_ROOT" "$RUN_DIR"
  if [ "$REMOVE_ENV" -eq 1 ]; then
    rm -f "$ENV_FILE"
  elif [ "$QUIET" -eq 0 ]; then
    log "kept $ENV_FILE and PostgreSQL database"
  fi
}

main "$@"
