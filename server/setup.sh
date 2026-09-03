#!/usr/bin/env sh
# engram STORE setup — from a clone to a running store in one command.
#
# There are two installers and they set up two different machines:
#
#   server/setup.sh            the STORE  — once, on one Linux/macOS host
#   install.sh / install.ps1   the CLIENT — every person, every machine
#
# This is the store one. It needs Docker, and it is Linux/macOS only: the store
# is Postgres and Windows machines are clients of it, not hosts for it. Run it
# once; it finishes by printing the exact client one-liner to hand out.
#
# The manual path is four steps (copy .env.example, invent two secrets, decide
# ENGRAM_OWNERS, compose up) and three of them are places to get it subtly
# wrong: a weak password, a token pasted twice, an empty ENGRAM_OWNERS that
# admits nothing and only says so on the first write. This does all four and
# then proves the result answers.
#
#   ./setup.sh                      # asks for the owner groups
#   ./setup.sh --owners acme,contoso
#   ./setup.sh --owners acme --port 9000 --tz Asia/Seoul
#
# Re-running is safe: an existing .env is never overwritten (--force to start
# over), so this doubles as "bring the store back up".
set -eu

cd "$(dirname "$0")"

OWNERS=""
PORT=""
TZ_VALUE=""
DATA=""
FORCE=0
NO_START=0
READ_AUTH=required

usage() {
  cat <<'USAGE'
usage: ./setup.sh [options]

  --owners <a,b>   owner groups this store admits (the confidentiality boundary)
  --port <n>       published port (default 8081)
  --tz <zone>      zone revision timestamps display in (default UTC)
  --data <dir>     where the database files live (default ./data)
  --force          overwrite an existing .env — THIS ROTATES BOTH SECRETS
  --open-reads     serve reads without a token — LAN only, see below
  --no-start       write .env and stop; do not run docker compose
  -h, --help       this

With no --owners and no existing .env, you are asked for the owner groups.

Reads require the token by default, and the viewer asks for it once. That is a
change from how this store behaved historically: --open-reads restores the old
behaviour, which is sound only on a network where everyone who can reach the
port is already allowed to read everything in it.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --owners) OWNERS="${2:?--owners needs a value}"; shift 2 ;;
    --port)   PORT="${2:?--port needs a value}"; shift 2 ;;
    --tz)     TZ_VALUE="${2:?--tz needs a value}"; shift 2 ;;
    --data)   DATA="${2:?--data needs a value}"; shift 2 ;;
    --force)  FORCE=1; shift ;;
    --open-reads) READ_AUTH=open; shift ;;
    --no-start) NO_START=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'setup: unknown option %s\n\n' "$1" >&2; usage >&2; exit 1 ;;
  esac
done

die() { printf 'setup: %s\n' "$1" >&2; exit 1; }

# ------------------------------------------------------------ preflight -------
# Everything this script needs is checked HERE, before it writes anything.
#
# Checking late is worse than not checking: a missing tool then surfaces as
# whatever the first command that needed it happened to print, and that message
# names the wrong problem. The health wait below is the clearest case — without
# curl or wget it would poll nothing for sixty seconds and then report "the
# store did not answer", sending you to read docker logs for a container that
# was fine. A minimal cloud image is exactly where this happens.
missing=""
for t in sed tr date; do
  command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
[ -z "$missing" ] || die "missing required tools:$missing (install coreutils)"

# The health check needs one of these. Named now, not in sixty seconds.
if command -v curl >/dev/null 2>&1; then
  probe() { curl -fsS "$1" >/dev/null 2>&1; }
elif command -v wget >/dev/null 2>&1; then
  probe() { wget -qO- "$1" >/dev/null 2>&1; }
else
  die "either curl or wget is required to check that the store came up
  (apt-get install curl, or dnf install curl)"
fi

# Randomness for the two secrets. A secret this file generates has to be
# unguessable even when openssl is absent, so there are two real sources and no
# fallback to something weaker — it refuses rather than degrade.
if command -v openssl >/dev/null 2>&1; then
  gen_secret() { openssl rand -hex 24; }
elif [ -r /dev/urandom ] && command -v od >/dev/null 2>&1; then
  gen_secret() { od -An -tx1 -N24 /dev/urandom | tr -d ' \n'; }
else
  # Only fatal if we are actually going to generate secrets — an existing .env
  # means this run is just bringing the store back up.
  gen_secret() { die "no source of randomness: install openssl, or run on a
  system with /dev/urandom and od"; }
fi

# Docker is checked before anything is written, so a machine without it fails
# having changed nothing rather than leaving a half-configured directory.
if [ "$NO_START" -eq 0 ]; then
  command -v docker >/dev/null 2>&1 \
    || die "docker is required — https://docs.docker.com/get-docker/
  (--no-start writes .env and skips this, if you are configuring only)"
  docker compose version >/dev/null 2>&1 \
    || die "docker compose v2 is required — this is 'docker compose' (a plugin),
  not the older standalone 'docker-compose' binary"
  docker info >/dev/null 2>&1 \
    || die "the docker daemon is not reachable — is it running, and is this user
  in the docker group? (sudo usermod -aG docker \$USER, then log back in)"
fi

# ---------------------------------------------------------------- .env --------
if [ -f .env ] && [ "$FORCE" -eq 0 ]; then
  printf 'setup: .env exists — keeping it (--force rewrites it, rotating both secrets)\n'
else
  if [ -z "$OWNERS" ]; then
    cat <<'ASK'

ENGRAM_OWNERS is the confidentiality boundary. It is a list of GROUPS (GitHub
orgs or users), not repos: a document whose path does not start with one of them
is refused with 403, so knowledge from a repo that should not be in this store
cannot get in because somebody forgot where they were.

ASK
    printf 'owner groups, comma separated (e.g. acme,contoso): '
    read -r OWNERS </dev/tty || die "no terminal to ask on — pass --owners instead"
  fi
  [ -n "$OWNERS" ] || die "ENGRAM_OWNERS cannot be empty — an empty list admits nothing"

  INGEST_TOKEN=$(gen_secret)
  umask 077
  cat > .env <<ENV
# Written by setup.sh on $(date -u '+%Y-%m-%dT%H:%M:%SZ'). Never commit this file.
POSTGRES_PASSWORD=$(gen_secret)
ENGRAM_INGEST_TOKEN=$INGEST_TOKEN
ENGRAM_OWNERS=$OWNERS
# Reads carry the token too. New stores close by default: the alternative is a
# store that is readable by anyone who can reach the port, which is fine on a
# LAN and is not fine the first time this lands on a public IP -- and which of
# the two it is is not something this script can know.
ENGRAM_READ_AUTH=$READ_AUTH
ENGRAM_DATA=$DATA
ENGRAM_PORT=${PORT:-8081}
ENGRAM_TZ=${TZ_VALUE:-UTC}
ENV
  umask 022
  printf 'setup: wrote .env (mode 600) — owners: %s\n' "$OWNERS"
fi

# Read back what is actually in the file, so the values printed at the end are
# the running store's and not the ones this run happened to generate.
INGEST_TOKEN=$(sed -n 's/^ENGRAM_INGEST_TOKEN=//p' .env | head -1)
PORT=$(sed -n 's/^ENGRAM_PORT=//p' .env | head -1)
PORT="${PORT:-8081}"

if [ "$NO_START" -eq 1 ]; then
  # The backticks are prose quoting a command, not a substitution.
  # shellcheck disable=SC2016
  printf 'setup: --no-start — .env is ready, run `docker compose up -d` when you are\n'
  exit 0
fi

# ------------------------------------------------------------- compose --------
printf 'setup: building and starting...\n'
docker compose up -d --build

# Up is not the same fact as answering: the schema is applied on boot and
# Postgres takes a moment, so a setup that stops at `up -d` hands back a store
# that fails on the first real call.
printf 'setup: waiting for the store to answer'
i=0
while [ "$i" -lt 60 ]; do
  if probe "http://127.0.0.1:$PORT/healthz"; then
    printf ' ok\n'
    break
  fi
  i=$((i + 1))
  printf '.'
  sleep 1
done
if [ "$i" -ge 60 ]; then
  printf '\n'
  die "the store did not answer on port $PORT within 60s — check: docker compose logs app"
fi

HOST=$(hostname 2>/dev/null || echo '<this host>')
RAW="https://raw.githubusercontent.com/poorants/engram/main"

cat <<NEXT

The store is up: http://localhost:$PORT   (open it in a browser for the viewer)

Now set up the people. This is the whole client install — binary, MCP server,
skill and hooks — on Linux and macOS:

  curl -fsSL $RAW/install.sh \\
    | sh -s -- --store http://$HOST:$PORT --token $INGEST_TOKEN

and on Windows, in PowerShell:

  \$env:ENGRAM_STORE_URL = 'http://$HOST:$PORT'
  \$env:ENGRAM_TOKEN     = '$INGEST_TOKEN'
  irm $RAW/install.ps1 | iex

Check that '$HOST' is a name other machines can actually resolve — the value
above is this host's own idea of its name, which is often not the one on the
network. Substitute an IP or a DNS name if it is not.

The token is the shared write token. It is in .env, it is not recoverable from
the server if you lose that file, and anyone who has it can write.

  docker compose logs -f app     what it is doing
  docker compose down            stop it (the data in ${DATA:-./data} stays)
NEXT
