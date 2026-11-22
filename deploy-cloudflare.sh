#!/usr/bin/env bash
#
# deploy-cloudflare.sh — one-run "from zero to Cloudflare" deploy for Lane Siege.
#
# What it does (idempotent, safe to re-run):
#   1. Installs prerequisites (curl, ca-certificates, tar) via the system package manager.
#   2. Ensures a Go toolchain new enough for go.mod (downloads latest stable from go.dev if missing/old).
#   3. Builds a static lanesiege binary and installs it to /usr/local/bin.
#   4. Creates a 'lanesiege' system user and runs the game on 127.0.0.1:$PORT
#      (systemd unit if available, nohup fallback otherwise).
#   5. Installs cloudflared and starts a QUICK tunnel (https://<random>.trycloudflare.com)
#      in the foreground so you can see the public URL immediately.
#
# Notes:
#   * The app is stateful (in-memory rooms) — exactly one instance is correct.
#   * A quick tunnel needs NO Cloudflare account, but the URL is random on every
#     run and is semi-public; don't treat it as private hosting.
#   * Ctrl+C stops the tunnel only; the game keeps running (systemd / nohup).
#
# Usage:
#   sudo ./deploy-cloudflare.sh            # build, install, run, open tunnel
#   PORT=9000 ./deploy-cloudflare.sh       # use a different port
#   FORCE_CLOUDFLARED=1 ./deploy-cloudflare.sh   # re-download cloudflared too
#
set -euo pipefail

# ---------- config (env overridable) ----------
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
if [ -z "${PORT:-}" ] && [ -f "$REPO_ROOT/.env" ]; then
  PORT="$(grep -E '^PORT=' "$REPO_ROOT/.env" | tail -n1 | cut -d= -f2- | tr -d "\"' \r" || true)"
fi
PORT="${PORT:-8180}"
BIND_ADDR="${BIND_ADDR:-127.0.0.1}"          # localhost only: cloudflared reaches it locally
APP_USER="${APP_USER:-lanesiege}"
BIN_PATH="/usr/local/bin/lanesiege"
GO_INSTALL_DIR="/usr/local/go"

# ---------- pretty output ----------
if [ -t 1 ]; then
  C_BLUE=$'\033[1;34m'; C_GREEN=$'\033[1;32m'; C_YELLOW=$'\033[1;33m'; C_RED=$'\033[1;31m'; C_OFF=$'\033[0m'
else
  C_BLUE=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_OFF=""
fi
log()  { printf '%s==>%s %s\n' "$C_BLUE"  "$C_OFF" "$*"; }
ok()   { printf '%s ok%s %s\n' "$C_GREEN" "$C_OFF" "$*"; }
warn() { printf '%swarn%s %s\n' "$C_YELLOW" "$C_OFF" "$*" >&2; }
die()  { printf '%serr %s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }

# ---------- privileges ----------
if [ "$(id -u)" -eq 0 ]; then
  SUDO=""
else
  command -v sudo >/dev/null 2>&1 || die "run as root or install sudo"
  SUDO="sudo"
fi

# ---------- platform ----------
[ "$(uname -s)" = "Linux" ] || die "this script targets Linux"
case "$(uname -m)" in
  x86_64|amd64)   ARCH=amd64 ;;
  aarch64|arm64)  ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (need amd64 or arm64)" ;;
esac

USE_SYSTEMD=0
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  USE_SYSTEMD=1
fi

install_packages() {
  log "Installing base packages (curl, ca-certificates, tar)"
  if command -v apt-get >/dev/null 2>&1; then
    $SUDO apt-get update -qq
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates tar gzip >/dev/null
  elif command -v dnf >/dev/null 2>&1; then
    $SUDO dnf install -y -q curl ca-certificates tar gzip >/dev/null
  elif command -v yum >/dev/null 2>&1; then
    $SUDO yum install -y -q curl ca-certificates tar gzip >/dev/null
  elif command -v pacman >/dev/null 2>&1; then
    $SUDO pacman -Sy --noconfirm --needed curl ca-certificates tar gzip >/dev/null
  elif command -v zypper >/dev/null 2>&1; then
    $SUDO zypper install -y -n curl ca-certificates tar gzip >/dev/null
  elif command -v apk >/dev/null 2>&1; then
    $SUDO apk add --no-cache curl ca-certificates tar gzip >/dev/null
  else
    warn "no known package manager found; assuming curl/tar already exist"
  fi
}

# ver_ge HAVE WANT -> true if HAVE >= WANT (dotted versions)
ver_ge() {
  [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

go_mod_version() {
  grep -m1 -E '^go[[:space:]]+' "$REPO_ROOT/go.mod" | awk '{print $2}'
}

install_go() {
  local required have
  required="$(go_mod_version)"; required="${required:-1.22}"

  if command -v go >/dev/null 2>&1; then
    have="$(go version | awk '{print $3}' | sed 's/^go//')"
    if ver_ge "$have" "$required"; then
      ok "Go $have found (>= $required), keeping it"
      return 0
    fi
    warn "Go $have found but go.mod requires $required; installing latest stable"
  else
    log "No Go toolchain found; installing latest stable from go.dev"
  fi

  local json ver fname sha url tmp
  json="$(curl -fsSL 'https://go.dev/dl/?mode=json')" || die "could not reach go.dev (network?)"
  # The JSON is pretty-printed, so parse it line-by-line with awk.
  ver="$(printf '%s\n' "$json" | awk -F'"' '/"version"/ { print $4; exit }' | sed 's/^go//')"
  [ -n "$ver" ] || die "could not parse latest Go version from go.dev response"
  fname="go${ver}.linux-${ARCH}.tar.gz"
  sha="$(printf '%s\n' "$json" | awk -v want="$fname" '
    match($0, /"filename"[ \t]*:[ \t]*"[^"]+"/) {
      cur = substr($0, RSTART, RLENGTH); gsub(/.*:[ \t]*"/, "", cur); sub(/"$/, "", cur)
    }
    /"sha256"/ {
      line = $0; gsub(/.*"sha256"[ \t]*:[ \t]*"/, "", line); gsub(/".*/, "", line)
      if (cur == want) { print line; exit }
    }')"
  url="https://go.dev/dl/${fname}"

  log "Downloading Go $ver (linux/$ARCH)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -fSL --retry 3 -o "$tmp/go.tar.gz" "$url" || die "Go download failed"

  if [ -n "$sha" ]; then
    printf '%s  %s\n' "$sha" "$tmp/go.tar.gz" | sha256sum -c - >/dev/null \
      || die "Go tarball checksum mismatch"
    ok "Go tarball checksum verified"
  else
    warn "could not find sha256 for $fname; skipping checksum verification"
  fi

  $SUDO rm -rf "$GO_INSTALL_DIR"
  $SUDO tar -C /usr/local -xzf "$tmp/go.tar.gz"
  $SUDO ln -sf "$GO_INSTALL_DIR/bin/go" /usr/local/bin/go
  $SUDO ln -sf "$GO_INSTALL_DIR/bin/gofmt" /usr/local/bin/gofmt
  rm -rf "$tmp"; trap - EXIT
  export PATH="$GO_INSTALL_DIR/bin:$PATH"
  ok "Go $(go version | awk '{print $3}') installed to $GO_INSTALL_DIR"
}

build_and_install() {
  cd "$REPO_ROOT"
  [ -f go.mod ] || die "go.mod not found in $REPO_ROOT (run this script from the repo)"
  command -v go >/dev/null 2>&1 || export PATH="$GO_INSTALL_DIR/bin:$PATH"

  log "Building static lanesiege binary"
  local tmpdir
  tmpdir="$(mktemp -d)"
  CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$tmpdir/lanesiege" ./cmd/lanesiege
  $SUDO install -m 0755 "$tmpdir/lanesiege" "$BIN_PATH"
  rm -rf "$tmpdir"
  ok "Installed $BIN_PATH"
}

create_service_user() {
  if ! id -u "$APP_USER" >/dev/null 2>&1; then
    log "Creating system user '$APP_USER'"
    $SUDO useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
  fi
}

write_systemd_unit() {
  log "Writing systemd unit lanesiege.service"
  $SUDO tee /etc/systemd/system/lanesiege.service >/dev/null <<EOF
[Unit]
Description=Lane Siege game server
After=network.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
EnvironmentFile=-/etc/lanesiege.env
Environment=LANESIEGE_COUPONS_FILE=/var/lib/lanesiege/coupons.json
ReadWritePaths=/var/lib/lanesiege
ExecStart=${BIN_PATH} -addr ${BIND_ADDR}:${PORT}
Restart=always
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable lanesiege.service >/dev/null
  $SUDO systemctl restart lanesiege.service
  ok "systemd service 'lanesiege' running on ${BIND_ADDR}:${PORT}"
}

start_nohup() {
  warn "systemd not detected; falling back to nohup"
  $SUDO pkill -x lanesiege 2>/dev/null || true
  sleep 1
  $SUDO nohup "$BIN_PATH" -addr "${BIND_ADDR}:${PORT}" >/var/log/lanesiege.log 2>&1 &
  ok "lanesiege started in background, log: /var/log/lanesiege.log (stop with: sudo pkill -x lanesiege)"
}

wait_for_health() {
  log "Waiting for /healthz on ${BIND_ADDR}:${PORT}"
  local i
  for i in $(seq 1 30); do
    if curl -fsS "http://${BIND_ADDR}:${PORT}/healthz" >/dev/null 2>&1; then
      ok "health check passed"
      return 0
    fi
    sleep 1
  done
  die "service did not become healthy on ${BIND_ADDR}:${PORT} (logs: journalctl -u lanesiege -e, or /var/log/lanesiege.log)"
}

install_cloudflared() {
  if command -v cloudflared >/dev/null 2>&1 && [ "${FORCE_CLOUDFLARED:-0}" != "1" ]; then
    ok "cloudflared already installed ($(cloudflared --version 2>/dev/null | head -n1))"
    return 0
  fi
  local url tmp
  url="https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${ARCH}"
  log "Downloading cloudflared (linux/$ARCH)"
  tmp="$(mktemp)"
  curl -fSL --retry 3 -o "$tmp" "$url" || die "cloudflared download failed"
  $SUDO install -m 0755 "$tmp" /usr/local/bin/cloudflared
  rm -f "$tmp"
  cloudflared --version >/dev/null || die "cloudflared installed but failed to run"
  ok "cloudflared installed ($(cloudflared --version | head -n1))"
}

main() {
  log "Repo: $REPO_ROOT"
  install_packages
  install_go
  build_and_install
  create_service_user

  $SUDO install -d -m 0750 -o "$APP_USER" -g "$APP_USER" /var/lib/lanesiege
  if [ -f "$REPO_ROOT/.env" ]; then
    $SUDO install -m 0600 "$REPO_ROOT/.env" /etc/lanesiege.env
  fi

  if [ "$USE_SYSTEMD" -eq 1 ]; then write_systemd_unit; else start_nohup; fi
  wait_for_health
  install_cloudflared

  cat <<EOF

${C_GREEN}Lane Siege is live on this box:${C_OFF} http://${BIND_ADDR}:${PORT}
${C_YELLOW}Starting a Cloudflare QUICK tunnel (no account needed).${C_OFF}
  - The public https://<random>.trycloudflare.com URL will print below.
  - The URL is random on every run and semi-public: share it only with your opponent.
  - Ctrl+C stops ONLY the tunnel; the game keeps running (systemd/nohup).
  - Watch game logs:  journalctl -u lanesiege -f     (or tail -f /var/log/lanesiege.log)
  - Stop the game:     sudo systemctl stop lanesiege
  - Full removal:      sudo systemctl disable --now lanesiege && sudo rm -f $BIN_PATH /usr/local/bin/cloudflared && sudo userdel $APP_USER

EOF

  exec cloudflared tunnel --url "http://${BIND_ADDR}:${PORT}"
}

main "$@"
