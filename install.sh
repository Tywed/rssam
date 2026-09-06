#!/bin/sh
set -eu

REPO="${GITHUB_REPO:-Tywed/rssam}"
PREFIX="${RSSAM_PREFIX:-/opt/rssam}"
BIN_DIR="$PREFIX/bin"
ENV_FILE="$PREFIX/.env"
UNIT_SRC=""
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
QUIET=0
ACTION=install
TARGET_VER=""
WITH_PG=0
REMOVE_ENV=0

usage() {
  cat <<EOF
Usage: install.sh [install|update|remove] [VERSION] [--quiet] [--with-postgres] [--remove-env]
  install           install binary, unit, sudoers (default)
  update [vX.Y.Z]   replace binary from GitHub Release
  remove            stop unit, remove binary (keep .env and DB)
  --quiet           non-interactive
  --with-postgres   create role/db rssam if missing (needs peer/sudo postgres)
  --help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --quiet|-q) QUIET=1 ;;
    --with-postgres) WITH_PG=1 ;;
    --remove-env) REMOVE_ENV=1 ;;
    --help|-h) usage; exit 0 ;;
    --update) ACTION=update ;;
    --remove) ACTION=remove ;;
    install|update|remove) ACTION=$1 ;;
    v*.*.*|[0-9]*.[0-9]*) TARGET_VER=$1 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 1 ;;
  esac
  shift
done

log() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# Only a plain semver tag may reach the download URL: the script runs as root
# (sudo from the UI) and the version is its single untrusted argument.
valid_tag() {
  printf '%s' "$1" | grep -Eq '^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'
}
if [ -n "$TARGET_VER" ] && ! valid_tag "$TARGET_VER"; then
  die "invalid version: $TARGET_VER"
fi

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

install_sudoers() {
  cat >/etc/sudoers.d/rssam <<'EOF'
rssam ALL=(root) NOPASSWD: /usr/bin/systemctl restart rssam, /usr/bin/systemctl restart --no-block rssam, /bin/systemctl restart rssam, /bin/systemctl restart --no-block rssam
rssam ALL=(root) NOPASSWD: /usr/local/sbin/rssam-update
EOF
  chmod 440 /etc/sudoers.d/rssam
}

install_unit() {
  mkdir -p /etc/systemd/system
  if [ -f "$SCRIPT_DIR/deploy/rssam.service" ]; then
    cp "$SCRIPT_DIR/deploy/rssam.service" /etc/systemd/system/rssam.service
  elif [ -f "$PREFIX/deploy/rssam.service" ]; then
    cp "$PREFIX/deploy/rssam.service" /etc/systemd/system/rssam.service
  else
    cat >/etc/systemd/system/rssam.service <<'EOF'
[Unit]
Description=rssam RSS aggregator
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=rssam
Group=rssam
WorkingDirectory=/opt/rssam
EnvironmentFile=/opt/rssam/.env
ExecStart=/opt/rssam/bin/rssam
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

# --- Sandboxing (compatible with restart/update from the admin UI) ---------
# The admin UI restarts and updates the service through `sudo systemctl` and
# `sudo rssam-update` (deploy/sudoers.rssam). sudo needs to gain privileges,
# so only options that do NOT imply NoNewPrivileges= are used here. If you do
# not need restart/update from the UI, install deploy/rssam-hardened.service
# instead and drop /etc/sudoers.d/rssam.
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ProtectControlGroups=true
ProtectProc=invisible
ProcSubset=pid
RemoveIPC=true
UMask=0077

[Install]
WantedBy=multi-user.target
EOF
  fi
  systemctl daemon-reload
}

maybe_postgres() {
  [ "$WITH_PG" -eq 1 ] || return 0
  command -v psql >/dev/null || die "psql not found"
  su -s /bin/sh postgres -c "psql -tc \"SELECT 1 FROM pg_roles WHERE rolname='rssam'\"" | grep -q 1 || \
    su -s /bin/sh postgres -c "createuser -P rssam" || true
  su -s /bin/sh postgres -c "psql -tc \"SELECT 1 FROM pg_database WHERE datname='rssam'\"" | grep -q 1 || \
    su -s /bin/sh postgres -c "createdb -O rssam rssam"
}

write_env() {
  [ -f "$ENV_FILE" ] && return 0
  mkdir -p "$PREFIX"
  if [ -f "$SCRIPT_DIR/.env.example" ]; then
    cp "$SCRIPT_DIR/.env.example" "$ENV_FILE"
  else
    cat >"$ENV_FILE" <<EOF
DATABASE_URL=postgres://rssam:rssam@127.0.0.1:5432/rssam?sslmode=disable
LISTEN_ADDR=:8080
RUN_MIGRATIONS=true
UI_ENABLED=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=changeme
WORKER_POOL_SIZE=10
WEBHOOK_WORKER_POOL_SIZE=10
GITHUB_REPO=${REPO}
EOF
  fi
  chmod 600 "$ENV_FILE"
  chown rssam:rssam "$ENV_FILE"
}

install_helper() {
  mkdir -p /usr/local/sbin
  if [ -f "$SCRIPT_DIR/install.sh" ]; then
    cp "$SCRIPT_DIR/install.sh" /usr/local/sbin/rssam-update
  else
    cp "$0" /usr/local/sbin/rssam-update
  fi
  chmod 755 /usr/local/sbin/rssam-update
}

# Map uname -m to the GOARCH used in release asset names.
release_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m) (releases ship linux/amd64 and linux/arm64)" ;;
  esac
}

download_release() {
  ver=$1
  arch=$(release_arch)
  asset="rssam-linux-${arch}.tar.gz"
  tmp=$(mktemp -d)
  base="https://github.com/${REPO}/releases/download/${ver}"
  log "fetch $base/$asset"
  fetch "$base/$asset" "$tmp/$asset"
  # Prefer the release-wide SHA256SUMS (one attested file); fall back to the
  # per-asset .sha256 that older releases ship. Refuse to install when neither
  # is available: an unverifiable download is not worth a root-owned binary.
  expect=""
  if fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null && [ -s "$tmp/SHA256SUMS" ]; then
    expect=$(awk -v a="$asset" '$2 == a || $2 == "*" a {print $1}' "$tmp/SHA256SUMS" | head -1)
  fi
  if [ -z "$expect" ] && fetch "$base/$asset.sha256" "$tmp/$asset.sha256" 2>/dev/null && [ -s "$tmp/$asset.sha256" ]; then
    expect=$(tr -d ' \n\t' <"$tmp/$asset.sha256")
  fi
  case "$expect" in
    [a-fA-F0-9]*) [ ${#expect} -eq 64 ] || expect="" ;;
    *) expect="" ;;
  esac
  [ -n "$expect" ] || { rm -rf "$tmp"; die "no checksum available for $asset (SHA256SUMS or .sha256)"; }
  got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
  if [ "$expect" != "$got" ]; then
    rm -rf "$tmp"
    die "sha256 mismatch for $asset"
  fi
  tar -xzf "$tmp/$asset" -C "$tmp"
  [ -f "$tmp/rssam" ] || { rm -rf "$tmp"; die "binary missing in archive"; }
  mkdir -p "$BIN_DIR"
  if [ -f "$BIN_DIR/rssam" ]; then
    ts=$(date +%Y%m%d_%H%M%S)
    cp "$BIN_DIR/rssam" "$BIN_DIR/rssam.backup.$ts"
  fi
  install -m 755 "$tmp/rssam" "$BIN_DIR/rssam.new"
  "$BIN_DIR/rssam.new" --version >/dev/null || { rm -rf "$tmp" "$BIN_DIR/rssam.new"; die "new binary failed --version"; }
  mv -f "$BIN_DIR/rssam.new" "$BIN_DIR/rssam"
  chown rssam:rssam "$BIN_DIR/rssam" 2>/dev/null || true
  rm -rf "$tmp"
}

do_install() {
  need_root
  ensure_user
  mkdir -p "$BIN_DIR" "$PREFIX/log"
  chown -R rssam:rssam "$PREFIX"
  maybe_postgres
  write_env
  ver=$TARGET_VER
  [ -n "$ver" ] || ver=$(latest_tag)
  [ -n "$ver" ] || die "no GitHub release; pass a version or publish a tag"
  download_release "$ver"
  install_unit
  install_sudoers
  install_helper
  systemctl enable rssam
  systemctl restart rssam
  sleep 1
  curl -sS --fail --retry 5 --retry-delay 1 --retry-connrefused http://127.0.0.1:8080/healthz >/dev/null
  log "installed $ver  UI http://<host>:8080/ui/login"
}

do_update() {
  need_root
  mkdir -p "$PREFIX/log"
  exec >>"$PREFIX/log/update.log" 2>&1
  log "=== update $(date -u) ==="
  ver=$TARGET_VER
  [ -n "$ver" ] || ver=$(latest_tag)
  [ -n "$ver" ] || die "no release (GitHub API/redirect failed)"
  log "target $ver"
  log "downloading $ver"
  download_release "$ver"
  log "installed binary $ver"
  log "restarting rssam"
  systemctl restart rssam
  n=0
  while [ "$n" -lt 30 ]; do
    if curl -sf --retry 0 http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
      log "ok $ver"
      exit 0
    fi
    n=$((n + 1))
    sleep 1
  done
  die "service did not become healthy"
}

do_remove() {
  need_root
  systemctl stop rssam 2>/dev/null || true
  systemctl disable rssam 2>/dev/null || true
  rm -f /etc/systemd/system/rssam.service
  systemctl daemon-reload
  rm -f "$BIN_DIR/rssam" /usr/local/sbin/rssam-update /etc/sudoers.d/rssam
  if [ "$REMOVE_ENV" -eq 1 ]; then
    rm -f "$ENV_FILE"
  elif [ "$QUIET" -eq 0 ]; then
    log "kept $ENV_FILE and PostgreSQL database"
  fi
}

case "$ACTION" in
  install) do_install ;;
  update) do_update ;;
  remove) do_remove ;;
  *) usage; exit 1 ;;
esac
