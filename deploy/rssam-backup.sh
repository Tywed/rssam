#!/bin/sh
# Backup and restore for a systemd installation of rssam (install.sh layout).
#
#   rssam-backup.sh backup            dump the database and copy .env
#   rssam-backup.sh restore FILE      restore a dump (stops rssam while running)
#   rssam-backup.sh list              show kept backups
#
# A backup is two files with the same timestamp: <name>.dump (pg_dump custom
# format, compressed) and <name>.env. The .env is part of the backup because it
# holds CSRF_SECRET and the tokens: a database restored without it logs every
# user out and breaks nothing else, but keeping them together makes the
# restore complete. Queue (jobs) and sessions are schema-only in the dump —
# both are rebuilt on start.
set -eu

PREFIX="${RSSAM_PREFIX:-/opt/rssam}"
ENV_FILE="${RSSAM_ENV_FILE:-$PREFIX/.env}"
BACKUP_DIR="${RSSAM_BACKUP_DIR:-$PREFIX/backups}"
KEEP="${RSSAM_BACKUP_KEEP:-7}"
SERVICE="${RSSAM_SERVICE:-rssam}"

log() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: rssam-backup.sh backup|restore FILE|list
  backup        pg_dump -Fc to $BACKUP_DIR, copy $ENV_FILE, keep last $KEEP
  restore FILE  stop $SERVICE, pg_restore --clean, start $SERVICE
  list          kept backups with size and age
Environment: RSSAM_PREFIX, RSSAM_ENV_FILE, RSSAM_BACKUP_DIR, RSSAM_BACKUP_KEEP,
             RSSAM_SERVICE, DATABASE_URL (overrides the value from .env)
EOF
}

# DATABASE_URL comes from the environment or from .env; the file is not
# sourced (it is not shell syntax and may contain unquoted specials).
database_url() {
  if [ -n "${DATABASE_URL:-}" ]; then
    printf '%s\n' "$DATABASE_URL"
    return 0
  fi
  [ -r "$ENV_FILE" ] || die "cannot read $ENV_FILE and DATABASE_URL is not set"
  url=$(sed -n 's/^[[:space:]]*DATABASE_URL[[:space:]]*=[[:space:]]*//p' "$ENV_FILE" | tail -1)
  url=$(printf '%s' "$url" | sed 's/^"\(.*\)"$/\1/; s/^'"'"'\(.*\)'"'"'$/\1/')
  [ -n "$url" ] || die "DATABASE_URL not found in $ENV_FILE"
  printf '%s\n' "$url"
}

need_tools() {
  for t in "$@"; do
    command -v "$t" >/dev/null 2>&1 || die "$t not found (install postgresql-client)"
  done
}

# Newest first, .dump only.
list_dumps() {
  ls -1t "$BACKUP_DIR"/rssam-*.dump 2>/dev/null || true
}

do_backup() {
  need_tools pg_dump pg_restore
  url=$(database_url)
  umask 077
  mkdir -p "$BACKUP_DIR"
  ts=$(date -u +%Y%m%d_%H%M%S)
  name="$BACKUP_DIR/rssam-$ts"
  tmp="$name.dump.part"
  trap 'rm -f "$tmp"' EXIT INT TERM

  pg_dump --dbname="$url" --format=custom --no-owner --no-acl \
    --exclude-table-data=jobs --exclude-table-data=sessions \
    --file="$tmp"
  # A dump that pg_restore cannot read is not a backup.
  pg_restore --list "$tmp" >/dev/null
  mv -f "$tmp" "$name.dump"
  trap - EXIT INT TERM
  if [ -r "$ENV_FILE" ]; then
    cp "$ENV_FILE" "$name.env"
  else
    log "warning: $ENV_FILE not readable, backup has no .env copy"
  fi

  # Keep the newest $KEEP pairs.
  n=0
  for f in $(list_dumps); do
    n=$((n + 1))
    [ "$n" -le "$KEEP" ] && continue
    rm -f "$f" "${f%.dump}.env"
  done

  size=$(du -h "$name.dump" | awk '{print $1}')
  log "ok $name.dump ($size)"
}

do_restore() {
  file=${1:-}
  [ -n "$file" ] || { usage; exit 1; }
  [ -f "$file" ] || die "no such file: $file"
  need_tools pg_restore psql
  url=$(database_url)
  pg_restore --list "$file" >/dev/null || die "not a pg_dump custom-format file: $file"

  stopped=0
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE"; then
    log "stopping $SERVICE"
    systemctl stop "$SERVICE"
    stopped=1
  fi

  # --clean drops the objects present in the dump before recreating them, so
  # the current contents are replaced, not merged. --single-transaction makes a
  # failed restore leave the database as it was.
  log "restoring $file"
  pg_restore --dbname="$url" --clean --if-exists --no-owner --no-acl \
    --single-transaction --exit-on-error "$file"

  # The dump ships an older schema when it predates the running binary;
  # the service applies the missing migrations on start (RUN_MIGRATIONS=true)
  # or via `rssam -migrate`.
  ver=$(psql --dbname="$url" -Atc "SELECT max(version) FROM schema_migrations" 2>/dev/null || true)
  log "restored schema_migrations up to ${ver:-?}"

  envcopy="${file%.dump}.env"
  if [ -f "$envcopy" ] && [ -f "$ENV_FILE" ] && ! cmp -s "$envcopy" "$ENV_FILE"; then
    log "note: $envcopy differs from $ENV_FILE (CSRF_SECRET/tokens); review before relying on old sessions"
  fi

  if [ "$stopped" -eq 1 ]; then
    log "starting $SERVICE"
    systemctl start "$SERVICE"
  fi
  log "ok"
}

do_list() {
  now=$(date +%s)
  for f in $(list_dumps); do
    mtime=$(stat -c %Y "$f" 2>/dev/null || stat -f %m "$f")
    age=$(( (now - mtime) / 3600 ))
    size=$(du -h "$f" | awk '{print $1}')
    envmark=""
    [ -f "${f%.dump}.env" ] && envmark=" +env"
    log "$f  $size  ${age}h ago$envmark"
  done
}

case "${1:-}" in
  backup) do_backup ;;
  restore) shift; do_restore "${1:-}" ;;
  list) do_list ;;
  --help|-h|help) usage ;;
  *) usage; exit 1 ;;
esac
