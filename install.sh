#!/usr/bin/env sh
# engram CLIENT installer — the binary, the MCP server, the skill and its hooks.
#
# There are two installers and they set up two different machines:
#
#   install.sh / install.ps1   the CLIENT — every person, every machine
#   server/setup.sh            the STORE  — once, on one Linux/macOS host
#
# This is the client one. engram is a single static binary with no runtime, so
# installing it is: pick the right file, check its hash, put it on PATH — and
# then wire it into Claude Code, which is the part people forget and which is
# why it is done here rather than left as three lines in a README.
#
#   curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh
#
# Give it the store and the whole client setup finishes in one command:
#
#   curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh \
#     | sh -s -- --store http://host:8081 --token <write token>
#
# Options (or the matching environment variables, set before the pipe):
#   --store <url>    designate the store          ENGRAM_STORE_URL
#   --token <t>      the write token              ENGRAM_TOKEN
#   --author <name>  byline for revisions         ENGRAM_AUTHOR
#   --version <tag>  a specific release           ENGRAM_VERSION
#   --dir <path>     install location             ENGRAM_INSTALL_DIR
#   --repo <o/n>     install from a fork          ENGRAM_REPO
#   --no-claude      binary only: skip the skill, the hooks and the MCP server
#   --binary-only    alias for --no-claude
set -eu

REPO="${ENGRAM_REPO:-poorants/engram}"
INSTALL_DIR="${ENGRAM_INSTALL_DIR:-$HOME/.local/bin}"
STORE_URL="${ENGRAM_STORE_URL:-}"
STORE_TOKEN="${ENGRAM_TOKEN:-}"
STORE_AUTHOR="${ENGRAM_AUTHOR:-}"
version="${ENGRAM_VERSION:-}"
WIRE_CLAUDE=1

die() { printf 'engram install: %s\n' "$1" >&2; exit 1; }
say() { printf '%s\n' "$1"; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --store)   STORE_URL="${2:?--store needs a URL}"; shift 2 ;;
    --token)   STORE_TOKEN="${2:?--token needs a value}"; shift 2 ;;
    --author)  STORE_AUTHOR="${2:?--author needs a name}"; shift 2 ;;
    --version) version="${2:?--version needs a tag}"; shift 2 ;;
    --dir)     INSTALL_DIR="${2:?--dir needs a path}"; shift 2 ;;
    --repo)    REPO="${2:?--repo needs owner/name}"; shift 2 ;;
    --no-claude|--binary-only) WIRE_CLAUDE=0; shift ;;
    -h|--help)
      cat <<'HELP'
engram client installer — the binary, the MCP server, the skill and its hooks.

  curl -fsSL .../install.sh | sh
  curl -fsSL .../install.sh | sh -s -- --store http://host:8081 --token <t>

  --store <url>    designate the store          ENGRAM_STORE_URL
  --token <t>      the write token              ENGRAM_TOKEN
  --author <name>  byline for revisions         ENGRAM_AUTHOR
  --version <tag>  a specific release           ENGRAM_VERSION
  --dir <path>     install location             ENGRAM_INSTALL_DIR
  --repo <o/n>     install from a fork          ENGRAM_REPO
  --no-claude      binary only: skip the skill, the hooks and the MCP server

The STORE is installed by a different script — server/setup.sh, once, on one
Linux or macOS host. This one runs on every machine that talks to it.
HELP
      exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

# ------------------------------------------------------- 1. the binary --------
need uname
need mktemp
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
  # latest_tag follows the /releases/latest redirect and reads the tag out of
  # where it lands. That is deliberate: the obvious alternative, the GitHub API,
  # is rate-limited per IP for unauthenticated callers, which fails on exactly
  # the shared office network where several people install on the same day.
  latest_tag() {
    curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" \
      | sed -n 's|.*/tag/\(.*\)$|\1|p'
  }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  latest_tag() {
    wget -qS --spider "https://github.com/$REPO/releases/latest" 2>&1 \
      | sed -n 's|.*[Ll]ocation:.*/tag/\([^ ]*\).*|\1|p' | tail -1
  }
else
  die "either curl or wget is required"
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  # Git Bash, MSYS and Cygwin are Windows. There is a real installer for it —
  # send people there rather than installing a linux binary they cannot run.
  mingw*|msys*|cygwin*) die "on Windows, run this in PowerShell instead:
  irm https://raw.githubusercontent.com/$REPO/main/install.ps1 | iex" ;;
  *) die "unsupported operating system: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch (releases carry amd64 and arm64)" ;;
esac

if [ -z "$version" ]; then
  version=$(latest_tag)
  [ -n "$version" ] || die "could not determine the latest release; pass --version vX.Y.Z"
fi

asset="engram_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "engram $version ($os/$arch)"
fetch "$base/$asset" "$tmp/$asset" || die "could not download $base/$asset"

# Verify the download. A truncated or tampered binary that runs anyway is worse
# than one that fails here, because it fails later and looks like a bug.
if fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" 2>/dev/null; then
  expected=$(grep " $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else
      actual=""
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      die "checksum mismatch for $asset — refusing to install"
    fi
  fi
fi

tar -xzf "$tmp/$asset" -C "$tmp" || die "could not unpack $asset"
[ -f "$tmp/engram" ] || die "the archive did not contain an engram binary"

mkdir -p "$INSTALL_DIR"
# Replace by rename so a running process is never overwritten in place.
mv "$tmp/engram" "$INSTALL_DIR/engram.new"
chmod 0755 "$INSTALL_DIR/engram.new"
mv "$INSTALL_DIR/engram.new" "$INSTALL_DIR/engram"

ENGRAM="$INSTALL_DIR/engram"
say "installed: $ENGRAM"
"$ENGRAM" version

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    printf '\nwarning: %s is not on your PATH. Add this to your shell profile:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" "$INSTALL_DIR"
    # Fix it for the rest of THIS script, so `claude mcp add engram -- engram mcp`
    # below records a command the shell can actually find later.
    PATH="$INSTALL_DIR:$PATH"; export PATH
    ;;
esac

# -------------------------------------------------------- 2. the store --------
store_ok=0
if [ -n "$STORE_URL" ]; then
  set -- store set "$STORE_URL"
  if [ -n "$STORE_TOKEN" ]; then set -- "$@" --token "$STORE_TOKEN"; fi
  if [ -n "$STORE_AUTHOR" ]; then set -- "$@" --author "$STORE_AUTHOR"; fi
  say ""
  say "designating the store: $STORE_URL"
  "$ENGRAM" "$@"
  # doctor proves two facts — the store answers, and this machine can write to
  # it. A setup that stops at `store set` looks finished and fails on the first
  # save, at the end of a session, which is the worst moment to find out.
  if "$ENGRAM" store doctor; then
    store_ok=1
  else
    rc=$?
    say ""
    say "warning: store doctor did not pass (exit $rc) — the binary is installed,"
    say "         but this machine cannot use the store yet."
    say "         https://github.com/$REPO/blob/main/docs/troubleshooting.md"
  fi
fi

# ------------------------------- 3. the skill, the hooks and the MCP server ---
# Best effort by design: a failure here leaves a working binary, and every step
# is a command the person can run again by hand. `set -e` is off for this block
# so one missing piece does not abort an otherwise complete install.
claude_wired=0
if [ "$WIRE_CLAUDE" -eq 1 ] && command -v claude >/dev/null 2>&1; then
  say ""
  say "wiring engram into Claude Code..."
  set +e
  claude plugin marketplace add "$REPO" >/dev/null 2>&1
  claude plugin install engram@engram >/dev/null 2>&1
  plugin_rc=$?
  # --scope user: the brain is not a property of one checkout. Registering it
  # per-project means the tools vanish the first time someone opens a different
  # repo, which reads as engram being broken.
  claude mcp add --scope user engram -- engram mcp >/dev/null 2>&1
  mcp_rc=$?
  set -e
  if [ "$plugin_rc" -eq 0 ]; then
    say "  skill + capture hooks: installed"
  else
    say "  skill: could not install — run: claude plugin install engram@engram"
  fi
  if [ "$mcp_rc" -eq 0 ]; then
    say "  brain_* MCP tools: registered (user scope)"
  else
    say "  MCP: could not register — run: claude mcp add --scope user engram -- engram mcp"
  fi
  if [ "$plugin_rc" -eq 0 ] && [ "$mcp_rc" -eq 0 ]; then claude_wired=1; fi
  command -v python3 >/dev/null 2>&1 || say "  note: the skill's helpers and hooks need python3 on PATH"
elif [ "$WIRE_CLAUDE" -eq 1 ]; then
  say ""
  say "note: the claude CLI was not found, so the skill and the MCP server were"
  say "      not registered. After installing Claude Code, run:"
  say "        claude plugin marketplace add $REPO"
  say "        claude plugin install engram@engram"
  say "        claude mcp add --scope user engram -- engram mcp"
fi

# ------------------------------------------------------- what is left ---------
say ""
if [ "$store_ok" -eq 1 ] && [ "$claude_wired" -eq 1 ]; then
  say "Done. A session can search and save now — try: engram search \"anything\""
  exit 0
fi

say "Next:"
[ "$store_ok" -eq 1 ] || cat <<NEXT
  engram store set http://<host>:8081 --token <write token>
  engram store doctor
NEXT
if [ "$claude_wired" -eq 0 ] && [ "$WIRE_CLAUDE" -eq 1 ] && command -v claude >/dev/null 2>&1; then
  say "  (see the notes above for the Claude Code steps that did not complete)"
fi
[ -n "$STORE_URL" ] || cat <<NEXT

No store yet? Bring one up on a Linux or macOS machine with Docker — once, for
everyone. The store is Postgres and does not run on Windows:
  git clone https://github.com/$REPO && cd engram/server
  ./setup.sh --owners <your-github-org>
NEXT
