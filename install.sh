#!/usr/bin/env bash
#
# Drix Status installer for Linux servers (Debian/Ubuntu; other distros need
# Docker already installed).
#
# Main system (server, status page, admin panel, database):
#   curl -fsSL https://raw.githubusercontent.com/24-ghost-the-killer/Drix-Status/main/install.sh | sudo bash
#
# A worker on another machine (token from Admin → Workers → Add worker):
#   curl -fsSL https://raw.githubusercontent.com/24-ghost-the-killer/Drix-Status/main/install.sh \
#     | sudo bash -s -- worker --server https://status.example.com --token drx_...
#
# Commands:
#   install            (default) install or upgrade the main system
#   worker             install or upgrade a worker
#   update             back up the database, pull the latest code and rebuild
#   backup             back up the database now (to <dir>/backups)
#   restore FILE       replace the database with a backup (asks first)
#   status | logs [service] | uninstall [--purge]
#
# Main options (each can also be an environment variable, shown in brackets):
#   --status-domain D  [DRIX_STATUS_DOMAIN]  e.g. status.example.com
#   --admin-domain D   [DRIX_ADMIN_DOMAIN]   e.g. admin.example.com
#                      Leave both empty to serve on plain ports instead of HTTPS.
#   --email E          [DRIX_LE_EMAIL]       Let's Encrypt account email
#   --tls MODE         [DRIX_TLS]            letsencrypt (default) | self-signed
#   --staging                                Let's Encrypt staging (test) certificates
#   --admin-email E    [ADMIN_EMAIL]         first admin account
#   --admin-password P [ADMIN_PASSWORD]      generated when empty
#   --smtp-url URL     [SMTP_URL]            optional mail server
#   --yes                                    never prompt; use defaults
# Worker options:
#   --server URL       [DRIX_SERVER_URL]     address of the main system
#   --token T          [DRIX_AGENT_TOKEN]
#   --name N                                 container name (default drix-agent)
# Advanced:
#   --dir PATH         [DRIX_DIR]            install directory (default /opt/drix)
#   --repo URL         [DRIX_REPO_URL]       git repository to install from
#   --branch B         [DRIX_BRANCH]         default main
#   --project NAME     [DRIX_PROJECT]        docker compose project (default drix-prod)
#   --skip-deps                              don't install packages or Docker
#   --http-port/--https-port/--status-port/--admin-port/--api-port N

set -Eeuo pipefail
ORIG_ARGS=("$@")

REPO_URL="${DRIX_REPO_URL:-https://github.com/Drix-Service/Drix-Status.git}"
BRANCH="${DRIX_BRANCH:-main}"
DIR="${DRIX_DIR:-/opt/drix}"
PROJECT="${DRIX_PROJECT:-drix-prod}"
CMD="install"
YES=0
SKIP_DEPS=0
PURGE=0
STAGING_FLAG=""
WORKER_NAME="drix-agent"
LOG_SERVICE=""
RESTORE_FILE=""

# ---------- output ----------
if [ -t 1 ]; then B=$'\033[1m' DIM=$'\033[2m' G=$'\033[32m' Y=$'\033[33m' R=$'\033[31m' N=$'\033[0m'; else B="" DIM="" G="" Y="" R="" N=""; fi
step() { printf '\n%s› %s%s\n' "$B" "$*" "$N"; }
ok() { printf '  %s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '  %s!%s %s\n' "$Y" "$N" "$*"; }
die() { printf '\n%s✗ %s%s\n' "$R" "$*" "$N" >&2; exit 1; }
trap 'die "Failed at line $LINENO: $BASH_COMMAND"' ERR

# ---------- arguments ----------
setopt() { printf -v "OPT_$1" '%s' "$2"; }
need_arg() { [ $# -ge 2 ] && [ -n "$2" ] || die "$1 needs a value"; }
while [ $# -gt 0 ]; do
  case "$1" in
    install | worker | update | backup | restore | status | logs | uninstall) CMD="$1" ;;
    --status-domain) need_arg "$@"; setopt DRIX_STATUS_DOMAIN "$2"; shift ;;
    --admin-domain) need_arg "$@"; setopt DRIX_ADMIN_DOMAIN "$2"; shift ;;
    --email) need_arg "$@"; setopt DRIX_LE_EMAIL "$2"; shift ;;
    --tls) need_arg "$@"; setopt DRIX_TLS "$2"; shift ;;
    --staging) STAGING_FLAG=1 ;;
    --admin-email) need_arg "$@"; setopt ADMIN_EMAIL "$2"; shift ;;
    --admin-password) need_arg "$@"; setopt ADMIN_PASSWORD "$2"; shift ;;
    --smtp-url) need_arg "$@"; setopt SMTP_URL "$2"; shift ;;
    --server) need_arg "$@"; setopt DRIX_SERVER_URL "$2"; shift ;;
    --token) need_arg "$@"; setopt DRIX_AGENT_TOKEN "$2"; shift ;;
    --name) need_arg "$@"; WORKER_NAME="$2"; shift ;;
    --dir) need_arg "$@"; DIR="$2"; shift ;;
    --repo) need_arg "$@"; REPO_URL="$2"; shift ;;
    --branch) need_arg "$@"; BRANCH="$2"; shift ;;
    --project) need_arg "$@"; PROJECT="$2"; shift ;;
    --http-port) need_arg "$@"; setopt HTTP_PORT "$2"; shift ;;
    --https-port) need_arg "$@"; setopt HTTPS_PORT "$2"; shift ;;
    --status-port) need_arg "$@"; setopt STATUS_PORT "$2"; shift ;;
    --admin-port) need_arg "$@"; setopt ADMIN_PORT "$2"; shift ;;
    --api-port) need_arg "$@"; setopt API_PORT "$2"; shift ;;
    --skip-deps) SKIP_DEPS=1 ;;
    --purge) PURGE=1 ;;
    -y | --yes) YES=1 ;;
    -h | --help) sed -n '2,42p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || echo "See https://github.com/24-ghost-the-killer/Drix-Status"; exit 0 ;;
    *)
      if [ "$CMD" = "logs" ] && [ -z "$LOG_SERVICE" ]; then LOG_SERVICE="$1"
      elif [ "$CMD" = "restore" ] && [ -z "$RESTORE_FILE" ]; then RESTORE_FILE="$1"
      else die "Unknown option: $1 (try --help)"; fi ;;
  esac
  shift
done

ENV_FILE="$DIR/.env"
ENVOY_CHANGED=""

# ---------- helpers ----------
have() { command -v "$1" >/dev/null 2>&1; }
tty_ok() { [ "$YES" = 0 ] && [ -r /dev/tty ] && { : </dev/tty; } 2>/dev/null; }

# ask VAR "Question" [default] [secret]
ask() {
  local var=$1 q=$2 def=${3:-} secret=${4:-} ans=""
  local o="OPT_$var"
  if [ -n "${!o+x}" ]; then printf -v "$var" '%s' "${!o}"; return; fi
  if [ -n "${!var:-}" ]; then return; fi # already set (env or existing .env)
  if tty_ok; then
    if [ -n "$secret" ]; then
      read -r -s -p "  $q${def:+ [$def]}: " ans </dev/tty || true
      echo
    else
      read -r -p "  $q${def:+ [$def]}: " ans </dev/tty || { ans=""; echo; }
    fi
  fi
  printf -v "$var" '%s' "${ans:-$def}"
}

local_ips() { { hostname -I 2>/dev/null || true; } | tr -s ' ' ' '; }

# The first build downloads Node, Go and the service images (a few GB).
check_disk() {
  local root avail_kb need_kb=$((4 * 1024 * 1024))
  root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)"
  [ -d "$root" ] || root=/
  avail_kb="$(df -Pk "$root" 2>/dev/null | awk 'NR==2 {print $4}' || true)"
  [ -n "$avail_kb" ] || return 0
  if [ "$avail_kb" -lt "$need_kb" ]; then
    die "Only $((avail_kb / 1024)) MB free for Docker ($root); the build needs about 4 GB.
  Free space, for example:
    docker builder prune -af        # Docker's build cache
    apt-get clean                   # cached packages
    journalctl --vacuum-size=200M   # old logs
  then run this installer again."
  fi
}

# Fail early when another program (not our own containers) holds a port.
check_ports() {
  have ss || return 0
  local p used ours
  ours="$(dc ps -q 2>/dev/null || true)"
  for p in "$@"; do
    used="$(ss -Hltnp "sport = :$p" 2>/dev/null || true)"
    [ -n "$used" ] || continue
    if [ -n "$ours" ] && printf '%s' "$used" | grep -q 'docker-proxy'; then continue; fi
    local who
    who="$(printf '%s' "$used" | grep -o 'users:(("[^"]*"' | head -1 | sed 's/users:(("//; s/"$//' || true)"
    die "Port $p is already in use by ${who:-another program}. Stop it, or choose other ports (see --help)."
  done
}

rand() { openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | cut -c1-"${1:-32}"; }

# Load KEY='value' lines from the existing .env (values written by us).
load_env() {
  [ -f "$ENV_FILE" ] || return 0
  local line key val
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in '' | \#*) continue ;; esac
    key="${line%%=*}"
    val="${line#*=}"
    case "$key" in *[!A-Za-z0-9_]*) continue ;; esac
    val="${val#\'}"
    val="${val%\'}"
    if [ -z "${!key+x}" ]; then printf -v "$key" '%s' "$val"; fi
  done <"$ENV_FILE"
}

q() { printf "'%s'" "$1"; } # single-quote for .env (docker compose reads it literally)

dc() {
  local files=(-f "$DIR/deploy/docker-compose.prod.yml")
  if [ -n "${DRIX_STATUS_DOMAIN:-}" ]; then files+=(-f "$DIR/deploy/docker-compose.tls.yml"); else files+=(-f "$DIR/deploy/docker-compose.ports.yml"); fi
  docker compose -p "$PROJECT" --env-file "$ENV_FILE" "${files[@]}" "$@"
}

require_root() {
  if [ "$SKIP_DEPS" = 0 ] && [ "$(id -u)" != 0 ]; then
    die "Please run as root, e.g.: curl -fsSL …/install.sh | sudo bash   (or pass --skip-deps if Docker is already set up for your user)"
  fi
}

install_deps() {
  if [ "$SKIP_DEPS" = 1 ]; then ok "Skipping package installation (--skip-deps)"; return; fi
  if [ -n "${DRIX_REEXEC:-}" ]; then return; fi # done before the restart
  step "Installing packages"
  if have apt-get; then
    export DEBIAN_FRONTEND=noninteractive
    local missing=()
    for p in git curl openssl ca-certificates; do have "$p" || [ "$p" = ca-certificates ] || missing+=("$p"); done
    if [ ${#missing[@]} -gt 0 ]; then
      apt-get update -qq >/dev/null 2>&1 || true # a broken third-party repo shouldn't stop us
      apt-get install -y -qq git curl openssl ca-certificates >/dev/null
    fi
    ok "git, curl, openssl"
  else
    for p in git curl openssl; do have "$p" || die "'$p' is missing. Install it with your package manager and re-run."; done
  fi
  if ! have docker; then
    if have apt-get; then
      echo "  Installing Docker (official get.docker.com script)…"
      curl -fsSL https://get.docker.com | sh >/dev/null
    else
      die "Docker is not installed. Install Docker Engine + the compose plugin, then re-run."
    fi
  fi
  have systemctl && systemctl enable --now docker >/dev/null 2>&1 || true
  ensure_compose
  ok "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo ready)"
}

# Docker from the distro's own packages (docker.io) has no compose plugin.
# Ubuntu calls it docker-compose-v2, Docker's repo docker-compose-plugin;
# otherwise fall back to the official release binary.
ensure_compose() {
  docker compose version >/dev/null 2>&1 && return
  echo "  Installing the Docker compose plugin…"
  if have apt-get; then
    apt-get update -qq >/dev/null 2>&1 || true
    apt-get install -y -qq docker-compose-v2 >/dev/null 2>&1 ||
      apt-get install -y -qq docker-compose-plugin >/dev/null 2>&1 || true
    apt-get install -y -qq docker-buildx >/dev/null 2>&1 ||
      apt-get install -y -qq docker-buildx-plugin >/dev/null 2>&1 || true
  fi
  if ! docker compose version >/dev/null 2>&1; then
    local arch
    case "$(uname -m)" in
      x86_64 | amd64) arch=x86_64 ;;
      aarch64 | arm64) arch=aarch64 ;;
      armv7l) arch=armv7 ;;
      *) die "Install the Docker compose plugin for $(uname -m) yourself, then re-run." ;;
    esac
    mkdir -p /usr/local/lib/docker/cli-plugins
    curl -fsSL "https://github.com/docker/compose/releases/download/v2.32.4/docker-compose-linux-$arch" \
      -o /usr/local/lib/docker/cli-plugins/docker-compose
    chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
  fi
  docker compose version >/dev/null 2>&1 || die "Couldn't install the Docker compose plugin. Install it yourself (https://docs.docker.com/compose/install/linux/) and re-run."
  ok "$(docker compose version)"
}

fetch_code() {
  if [ -n "${DRIX_REEXEC:-}" ]; then return; fi # already fetched; now running the new installer
  step "Getting Drix Status ($BRANCH)"
  if [ -d "$DIR/.git" ]; then
    git -C "$DIR" fetch --quiet origin "$BRANCH"
    git -C "$DIR" checkout --quiet "$BRANCH"
    git -C "$DIR" reset --quiet --hard "origin/$BRANCH"
    ok "Updated $DIR to $(git -C "$DIR" rev-parse --short HEAD)"
  else
    mkdir -p "$(dirname "$DIR")"
    git clone --quiet --depth 1 --branch "$BRANCH" "$REPO_URL" "$DIR" || die "Could not clone $REPO_URL (branch $BRANCH)."
    ok "Cloned into $DIR ($(git -C "$DIR" rev-parse --short HEAD))"
  fi
  # Bash has already read this (possibly older) script: continue with the
  # installer that was just downloaded, so its fixes apply to this run.
  if [ -f "$DIR/install.sh" ]; then
    exec env DRIX_REEXEC=1 bash "$DIR/install.sh" ${ORIG_ARGS[@]+"${ORIG_ARGS[@]}"} \
      --dir "$DIR" --branch "$BRANCH" --repo "$REPO_URL" --project "$PROJECT"
  fi
}

# ---------- main system ----------
configure() {
  step "Configuration"
  load_env
  if [ -n "$STAGING_FLAG" ]; then DRIX_LE_STAGING=1; fi
  echo "  Use your own domains for HTTPS (leave empty to use plain ports)."
  ask DRIX_STATUS_DOMAIN "Status page domain (e.g. status.example.com)" ""
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then
    ask DRIX_ADMIN_DOMAIN "Admin panel domain (e.g. admin.example.com)" ""
    [ -n "$DRIX_ADMIN_DOMAIN" ] || die "An admin domain is needed too (both sites run on their own domain)."
    [ "$DRIX_STATUS_DOMAIN" != "$DRIX_ADMIN_DOMAIN" ] || die "Use two different domains for the status page and the admin panel."
    ask DRIX_TLS "Certificates: letsencrypt or self-signed" "letsencrypt"
    case "$DRIX_TLS" in letsencrypt | self-signed) ;; *) die "--tls must be letsencrypt or self-signed" ;; esac
    if [ "$DRIX_TLS" = letsencrypt ]; then
      ask DRIX_LE_EMAIL "Email for Let's Encrypt (expiry warnings)" ""
      [ -n "$DRIX_LE_EMAIL" ] || die "Let's Encrypt needs an email address (or use --tls self-signed)."
    fi
  else
    DRIX_ADMIN_DOMAIN=""
  fi
  ask ADMIN_EMAIL "Admin email (your login)" "admin@example.com"
  ask ADMIN_PASSWORD "Admin password (empty = generate)" "" secret
  GENERATED_PASSWORD=""
  if [ -z "$ADMIN_PASSWORD" ]; then ADMIN_PASSWORD="$(rand 20)"; GENERATED_PASSWORD=1; fi
  [ ${#ADMIN_PASSWORD} -ge 10 ] || die "The admin password must be at least 10 characters."
  ask SMTP_URL "SMTP URL for email (optional, e.g. smtps://user:pass@smtp.example.com?from=Status%20<status@example.com>)" ""

  : "${SECRET_KEY:=$(rand 48)}"
  : "${POSTGRES_PASSWORD:=$(rand 32)}"
  : "${HTTP_PORT:=${OPT_HTTP_PORT:-80}}" "${HTTPS_PORT:=${OPT_HTTPS_PORT:-443}}"
  : "${STATUS_PORT:=${OPT_STATUS_PORT:-3000}}" "${ADMIN_PORT:=${OPT_ADMIN_PORT:-3001}}" "${API_PORT:=${OPT_API_PORT:-8080}}"
  local k o
  for k in HTTP_PORT HTTPS_PORT STATUS_PORT ADMIN_PORT API_PORT; do
    o="OPT_$k"
    if [ -n "${!o+x}" ]; then printf -v "$k" '%s' "${!o}"; fi
  done

  local host
  host="$(local_ips | awk '{print $1}')"
  host="${host:-localhost}"
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then
    local hp=""
    if [ "$HTTPS_PORT" != 443 ]; then hp=":$HTTPS_PORT"; fi
    STATUS_URL="https://$DRIX_STATUS_DOMAIN$hp"
    ADMIN_URL="https://$DRIX_ADMIN_DOMAIN$hp"
    PUBLIC_URL="$STATUS_URL"
    COOKIE_SECURE=true
  else
    STATUS_URL="http://$host:$STATUS_PORT"
    ADMIN_URL="http://$host:$ADMIN_PORT"
    PUBLIC_URL="http://$host:$API_PORT"
    COOKIE_SECURE=false
  fi

  ENV_KEYS="DRIX_STATUS_DOMAIN DRIX_ADMIN_DOMAIN DRIX_TLS DRIX_LE_EMAIL DRIX_LE_STAGING ADMIN_EMAIL ADMIN_PASSWORD
    SECRET_KEY POSTGRES_PASSWORD SMTP_URL PUBLIC_URL STATUS_URL ADMIN_URL COOKIE_SECURE
    HTTP_PORT HTTPS_PORT STATUS_PORT ADMIN_PORT API_PORT"
  for k in $ENV_KEYS; do
    case "${!k:-}" in *"'"*) die "$k can't contain a single quote (')." ;; esac
  done

  umask 077
  {
    echo "# Drix Status configuration, written by install.sh. Keep it private."
    echo "# Re-running install.sh keeps these values; edit and re-run to change them."
    for k in $ENV_KEYS; do
      echo "$k=$(q "${!k:-}")"
    done
  } >"$ENV_FILE.new"
  mv "$ENV_FILE.new" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  umask 022
  ok "Saved settings to $ENV_FILE"
}

check_dns() {
  local d ips local_ips
  local_ips="$(local_ips)"
  for d in "$DRIX_STATUS_DOMAIN" "$DRIX_ADMIN_DOMAIN"; do
    ips="$({ getent ahostsv4 "$d" 2>/dev/null || true; } | awk '{print $1}' | sort -u | tr '\n' ' ')"
    if [ -z "$ips" ]; then
      warn "$d doesn't resolve yet. Point an A record at this server; certificates will be retried every 10 minutes."
    elif printf '%s' " $ips" | grep -qE ' (104\.(1[6-9]|2[0-9]|3[01])\.|172\.(6[4-9]|7[01])\.|188\.114\.9[6-9]\.|162\.15[89]\.)'; then
      warn "$d points to Cloudflare's proxy (orange cloud). Set it to \"DNS only\" in Cloudflare until the certificate is issued; the proxy can block Let's Encrypt and workers."
    elif [ -n "$local_ips" ] && ! printf '%s' " $local_ips " | grep -qE " ($(echo "$ips" | sed 's/ $//; s/ /|/g')) "; then
      warn "$d points to $ips, which isn't one of this machine's addresses ($local_ips). Fine behind NAT; otherwise fix DNS."
    else
      ok "$d → $ips"
    fi
  done
}

setup_tls() {
  step "Setting up Envoy and HTTPS"
  mkdir -p "$DIR/data/tls"
  local pr=""
  if [ "$HTTPS_PORT" != 443 ]; then pr=", port_redirect: $HTTPS_PORT"; fi
  sed -e "s/__STATUS_DOMAIN__/$DRIX_STATUS_DOMAIN/g" -e "s/__ADMIN_DOMAIN__/$DRIX_ADMIN_DOMAIN/g" -e "s/__PORT_REDIRECT__/$pr/" \
    "$DIR/deploy/envoy/envoy.yaml.tmpl" >"$DIR/data/envoy.yaml.new"
  # Envoy reads its config at start; restart it after `up` when it changed.
  if ! cmp -s "$DIR/data/envoy.yaml.new" "$DIR/data/envoy.yaml" 2>/dev/null; then ENVOY_CHANGED=1; fi
  cat "$DIR/data/envoy.yaml.new" >"$DIR/data/envoy.yaml" # keep the inode: it's bind-mounted
  rm -f "$DIR/data/envoy.yaml.new"
  cat >"$DIR/data/tls/sds.yaml" <<'EOF'
resources:
  - "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.Secret
    name: drix_cert
    tls_certificate:
      certificate_chain: { filename: /etc/envoy/tls/fullchain.pem }
      private_key: { filename: /etc/envoy/tls/privkey.pem }
EOF
  # A self-signed certificate lets Envoy start; Let's Encrypt replaces it.
  # Made again only when missing or the domains changed (and not from Let's Encrypt).
  local tls="$DIR/data/tls" want="$DRIX_STATUS_DOMAIN $DRIX_ADMIN_DOMAIN"
  if [ ! -s "$tls/fullchain.pem" ] || { [ "$(cat "$tls/.domains" 2>/dev/null || true)" != "$want" ] &&
    ! openssl x509 -in "$tls/fullchain.pem" -noout -issuer 2>/dev/null | grep -q "Let's Encrypt"; }; then
    openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=$DRIX_STATUS_DOMAIN" \
      -addext "subjectAltName=DNS:$DRIX_STATUS_DOMAIN,DNS:$DRIX_ADMIN_DOMAIN" \
      -keyout "$tls/privkey.pem.new" -out "$tls/fullchain.pem.new" >/dev/null 2>&1
    mv -f "$tls/privkey.pem.new" "$tls/privkey.pem"
    mv -f "$tls/fullchain.pem.new" "$tls/fullchain.pem"
    echo "$want" >"$tls/.domains"
    ok "Created a self-signed certificate for $DRIX_STATUS_DOMAIN and $DRIX_ADMIN_DOMAIN"
  fi
  # Envoy runs as a non-root user inside its container and must read the key.
  # Envoy runs as uid 101 inside its container (ENVOY_UID) and must read the key.
  chmod 755 "$DIR/data/tls"
  chmod 644 "$DIR/data/tls/sds.yaml"
  if chown 101:101 "$DIR/data/tls/privkey.pem" 2>/dev/null; then
    chmod 600 "$DIR/data/tls/privkey.pem"
  else
    chmod 644 "$DIR/data/tls/privkey.pem" # not root (e.g. --skip-deps): fall back to readable
  fi
  if [ "$DRIX_TLS" = letsencrypt ]; then
    check_dns
    echo "  ${DIM}Let's Encrypt is used under its Subscriber Agreement: https://letsencrypt.org/repository/${N}"
    if [ -n "${DRIX_LE_STAGING:-}" ]; then warn "Using Let's Encrypt STAGING certificates (browsers won't trust them)."; fi
  else
    warn "Using a self-signed certificate; browsers will warn until you switch to --tls letsencrypt."
  fi
}

restart_envoy() {
  if [ -n "$ENVOY_CHANGED" ] && [ -n "${DRIX_STATUS_DOMAIN:-}" ]; then dc restart envoy >/dev/null 2>&1; fi
}

wait_healthy() {
  step "Starting up"
  local url="http://127.0.0.1:$API_PORT/healthz" curl_opts=(-fsS -o /dev/null --max-time 3)
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then
    url="https://$DRIX_STATUS_DOMAIN:$HTTPS_PORT/healthz"
    curl_opts+=(-k --resolve "$DRIX_STATUS_DOMAIN:$HTTPS_PORT:127.0.0.1")
  fi
  local i bad
  for i in $(seq 1 90); do
    if curl "${curl_opts[@]}" "$url" 2>/dev/null; then ok "Server is up"; return; fi
    if [ "$i" -ge 8 ]; then
      bad="$({ dc ps --status restarting --format '{{.Service}}' 2>/dev/null || true; } | head -1)"
      if [ -n "$bad" ]; then
        dc logs --tail 20 "$bad" 2>&1 | sed 's/^/    /'
        die "The $bad service keeps restarting (log above)."
      fi
    fi
    sleep 2
  done
  dc ps
  die "The server didn't come up in 3 minutes. See: $0 logs server"
}

# ---------- backups ----------
# The server (user 10001 in its container) writes scheduled backups here too.
prepare_backups() {
  mkdir -p "$DIR/backups"
  chown 10001:10001 "$DIR/backups" 2>/dev/null || true
  chmod 750 "$DIR/backups"
}

# dump_db SUFFIX: pg_dump through the postgres container; prints the file name.
dump_db() {
  local name file
  name="drix-$(date -u +%Y%m%d-%H%M%S)${1:+-$1}.dump"
  file="$DIR/backups/$name"
  prepare_backups
  if dc exec -T postgres pg_dump -U drix -Fc --no-owner --no-privileges drix >"$file.partial" 2>"$file.err"; then
    mv "$file.partial" "$file"
    rm -f "$file.err"
    chmod 640 "$file"
    chown 10001:10001 "$file" 2>/dev/null || true
    echo "$file"
    return 0
  fi
  cat "$file.err" >&2 || true
  rm -f "$file.partial" "$file.err"
  return 1
}

postgres_running() { [ -n "$(dc ps --status running -q postgres 2>/dev/null || true)" ]; }

do_backup() {
  [ -f "$ENV_FILE" ] || die "Not installed in $DIR yet."
  require_root
  load_env
  postgres_running || die "The database isn't running. Start it with: $0 update"
  step "Backing up the database"
  local f
  f="$(dump_db manual)" || die "The backup failed (error above)."
  ok "Saved $f ($(du -h "$f" | cut -f1))"
}

do_restore() {
  [ -f "$ENV_FILE" ] || die "Not installed in $DIR yet."
  require_root
  load_env
  [ -n "$RESTORE_FILE" ] || die "Which backup? e.g. $0 restore $DIR/backups/drix-20260101-030000.dump (list: ls $DIR/backups)"
  local file="$RESTORE_FILE"
  if [ ! -f "$file" ] && [ -f "$DIR/backups/$file" ]; then file="$DIR/backups/$file"; fi
  [ -f "$file" ] || die "No such file: $RESTORE_FILE"
  postgres_running || die "The database isn't running. Start it with: $0 update"
  dc exec -T postgres pg_restore --list <"$file" >/dev/null 2>&1 || die "$file isn't a Drix backup (pg_restore can't read it)."

  printf '
  %sThis replaces ALL current data%s (monitors, history, users, settings)
  with the backup %s.
' "$B" "$N" "$(basename "$file")"
  if [ "$YES" = 0 ]; then
    tty_ok || die "Run this in a terminal, or add --yes to confirm."
    local ans=""
    read -r -p "  Type restore to continue: " ans </dev/tty || true
    [ "$ans" = "restore" ] || die "Cancelled. Nothing was changed."
  fi

  step "Saving the current data first"
  local safety
  safety="$(dump_db pre-restore)" || die "Couldn't back up the current data, so nothing was changed."
  ok "Current data saved to $safety"

  step "Restoring $(basename "$file")"
  dc stop server >/dev/null
  if ! dc exec -T postgres pg_restore -U drix -d drix --clean --if-exists --no-owner --no-privileges --single-transaction <"$file"; then
    dc start server >/dev/null
    die "The restore failed and was rolled back; your data is unchanged."
  fi
  ok "Database restored"
  dc start server >/dev/null
  wait_healthy
  ok "Done. If anything looks wrong, restore $safety the same way."
}

do_install() {
  require_root
  install_deps
  fetch_code
  configure
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then check_ports "$HTTP_PORT" "$HTTPS_PORT"; else check_ports "$STATUS_PORT" "$ADMIN_PORT" "$API_PORT"; fi
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then setup_tls; fi
  check_disk
  prepare_backups
  step "Building (first run takes a few minutes)"
  dc build --quiet
  ok "Images built"
  dc up -d --remove-orphans
  restart_envoy
  wait_healthy
  if [ -n "$DRIX_STATUS_DOMAIN" ] && [ "$DRIX_TLS" = letsencrypt ]; then
    echo "  Requesting the Let's Encrypt certificate in the background (watch: $0 logs certbot)."
  fi

  printf '\n%sDrix Status is installed%s\n' "$G$B" "$N"
  printf '  Status page   %s\n' "$STATUS_URL"
  printf '  Admin panel   %s\n' "$ADMIN_URL"
  printf '  Login         %s\n' "$ADMIN_EMAIL"
  if [ -n "$GENERATED_PASSWORD" ] && [ -z "${OPT_ADMIN_PASSWORD+x}" ]; then
    printf '  Password      %s   %s(generated; also in %s)%s\n' "$ADMIN_PASSWORD" "$DIM" "$ENV_FILE" "$N"
  fi
  printf '  Workers use   %s\n' "$PUBLIC_URL"
  echo
  echo "  Next: open the admin panel → Workers → Add worker, and run the install command it shows on another server."
  if [ -n "$DRIX_STATUS_DOMAIN" ]; then
    echo "  Firewall: allow ports $HTTP_PORT and $HTTPS_PORT (e.g. ufw allow $HTTP_PORT,$HTTPS_PORT/tcp)."
  else
    echo "  Firewall: allow ports $STATUS_PORT, $ADMIN_PORT and $API_PORT, or put a proxy with HTTPS in front."
  fi
  echo "  Update later with: sudo $DIR/install.sh update"
  echo "  The database is backed up every night to $DIR/backups (Settings → Backups)."
}

do_update() {
  [ -f "$ENV_FILE" ] || die "Not installed in $DIR yet. Run install first."
  require_root
  load_env
  fetch_code
  if [ -n "${DRIX_STATUS_DOMAIN:-}" ]; then setup_tls; fi
  check_disk
  if postgres_running; then
    step "Backing up the database before updating"
    local f
    if f="$(dump_db pre-update)"; then ok "Saved $f"; else warn "The backup failed (error above); continuing with the update."; fi
  fi
  prepare_backups
  step "Rebuilding"
  dc build --quiet
  dc up -d --remove-orphans
  restart_envoy
  wait_healthy
  ok "Updated to $(git -C "$DIR" rev-parse --short HEAD)"
}

do_uninstall() {
  [ -f "$ENV_FILE" ] || die "Nothing installed in $DIR."
  load_env
  if [ "$PURGE" = 1 ]; then
    if tty_ok; then
      local a; read -r -p "  This deletes ALL Drix data (database, history, certificates). Type 'delete' to confirm: " a </dev/tty
      [ "$a" = delete ] || die "Cancelled."
    elif [ "$YES" != 1 ]; then
      die "--purge needs confirmation; run it in a terminal or add --yes."
    fi
    dc down -v --remove-orphans
    rm -rf "$DIR/data"
    ok "Removed containers and all data. The code is still in $DIR."
  else
    dc down --remove-orphans
    ok "Stopped and removed containers. Data volumes are kept (use --purge to delete them)."
  fi
}

# ---------- worker ----------
do_worker() {
  require_root
  : "${DRIX_SERVER_URL:=${OPT_DRIX_SERVER_URL:-}}"
  : "${DRIX_AGENT_TOKEN:=${OPT_DRIX_AGENT_TOKEN:-}}"
  if [ -n "${OPT_DRIX_SERVER_URL+x}" ]; then DRIX_SERVER_URL="${OPT_DRIX_SERVER_URL}"; fi
  if [ -n "${OPT_DRIX_AGENT_TOKEN+x}" ]; then DRIX_AGENT_TOKEN="${OPT_DRIX_AGENT_TOKEN}"; fi
  [ -n "$DRIX_SERVER_URL" ] || ask DRIX_SERVER_URL "Main system address (e.g. https://status.example.com)" ""
  [ -n "$DRIX_AGENT_TOKEN" ] || ask DRIX_AGENT_TOKEN "Worker token (Admin → Workers → Add worker)" "" secret
  [ -n "$DRIX_SERVER_URL" ] && [ -n "$DRIX_AGENT_TOKEN" ] || die "Both --server and --token are required."
  case "$DRIX_AGENT_TOKEN" in drx_*) ;; *) die "That doesn't look like a worker token (it starts with drx_)." ;; esac
  export DRIX_SERVER_URL DRIX_AGENT_TOKEN # kept when the installer restarts after downloading

  install_deps
  fetch_code
  check_disk
  step "Building the worker"
  docker build --quiet -f "$DIR/deploy/Dockerfile.agent" -t drix/agent:local "$DIR" >/dev/null
  ok "Image drix/agent:local"
  # On the same machine as the main system, talk to it over Docker's
  # internal network instead of going out through DNS, a CDN or the proxy.
  local net_args=() url="$DRIX_SERVER_URL"
  if [ -n "$(docker ps -q --filter "label=com.docker.compose.project=$PROJECT" --filter label=com.docker.compose.service=server 2>/dev/null || true)" ]; then
    net_args=(--network "${PROJECT}_default")
    url="http://server:8080"
    ok "The main system runs on this machine; connecting over the internal network"
  fi
  docker rm -f "$WORKER_NAME" >/dev/null 2>&1 || true
  docker run -d --name "$WORKER_NAME" --restart unless-stopped --cap-add NET_RAW ${net_args[@]+"${net_args[@]}"} \
    -e DRIX_SERVER_URL="$url" -e DRIX_AGENT_TOKEN="$DRIX_AGENT_TOKEN" \
    drix/agent:local >/dev/null
  for _ in $(seq 1 30); do
    if docker logs "$WORKER_NAME" 2>&1 | grep -q "msg=connected"; then
      ok "Worker connected to $url"
      printf '\n%sWorker is running%s (container %s, restarts automatically)\n' "$G$B" "$N" "$WORKER_NAME"
      echo "  Logs: docker logs -f $WORKER_NAME"
      return
    fi
    if docker logs "$WORKER_NAME" 2>&1 | grep -q "invalid or revoked agent token"; then
      die "The server rejected the token. Create a new one in Admin → Workers."
    fi
    sleep 1
  done
  docker logs --tail 15 "$WORKER_NAME" 2>&1 | sed 's/^/    /'
  warn "The worker is running but hasn't connected yet; it keeps retrying. Check the address and firewall."
}

# ---------- dispatch ----------
case "$CMD" in
  install) do_install ;;
  update) do_update ;;
  backup) do_backup ;;
  restore) do_restore ;;
  worker) do_worker ;;
  status) load_env; dc ps ;;
  logs) load_env; dc logs --tail 100 -f ${LOG_SERVICE:+"$LOG_SERVICE"} ;;
  uninstall) do_uninstall ;;
esac
