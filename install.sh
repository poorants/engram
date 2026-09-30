#!/usr/bin/env sh
# engram binary installer — puts the one `engram` the plugin's capture hooks
# run into ~/.local/bin.
#
# The brain is remote: a session uses the `engram` MCP server (engram serve)
# over HTTP with its own login, and this machine holds no store address and no
# token. The binary does one thing here — `engram hook` — and never touches the
# network. Registering the MCP server and installing the plugin are not this
# script's job; the server's /mcp__engram__setup prompt does them and runs this.
#
#   curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh
#
# Re-running it is the upgrade; the same version is a no-op.
#
# Options (or the matching environment variables, set before the pipe):
#   --version <tag>  a specific release           ENGRAM_VERSION
#   --dir <path>     install location             ENGRAM_INSTALL_DIR
#   --repo <o/n>     install from a fork          ENGRAM_REPO
#   --force          download even if already at that version
#
# The STORE and its server are set up by server/setup.sh, once, on one host.
set -eu

REPO="${ENGRAM_REPO:-poorants/engram}"
INSTALL_DIR="${ENGRAM_INSTALL_DIR:-$HOME/.local/bin}"
version="${ENGRAM_VERSION:-}"
FORCE=0

die() { printf 'engram install: %s\n' "$1" >&2; exit 1; }
say() { printf '%s\n' "$1"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) version="${2:?--version needs a tag}"; shift 2 ;;
    --dir)     INSTALL_DIR="${2:?--dir needs a path}"; shift 2 ;;
    --repo)    REPO="${2:?--repo needs owner/name}"; shift 2 ;;
    --force)   FORCE=1; shift ;;
    # Accepted and ignored: the installer no longer wires Claude Code, so
    # "binary only" is all it ever does. Old one-liners keep working.
    --no-claude|--binary-only) shift ;;
    --store|--token|--author)
      die "$1 is gone: the client no longer talks to a store. Register the remote MCP server instead:
  claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
then run /mcp__engram__setup in Claude Code." ;;
    -h|--help)
      cat <<'HELP'
engram binary installer — the binary the plugin's capture hooks run.

  curl -fsSL .../install.sh | sh
  curl -fsSL .../install.sh | sh -s -- --version vX.Y.Z

  --version <tag>  a specific release           ENGRAM_VERSION
  --dir <path>     install location             ENGRAM_INSTALL_DIR
  --repo <o/n>     install from a fork          ENGRAM_REPO
  --force          download even if already at that version

Re-running it is the upgrade. The brain itself is reached through the remote
MCP server; /mcp__engram__setup in Claude Code finishes a machine.
HELP
      exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

# ------------------------------------------------------- 0. preflight ---------
# Everything needed is checked here, before anything is downloaded. Checking
# late is worse than not checking: a missing tool surfaces as whatever the
# command that needed it happened to print, and that message names the wrong
# problem — `tar: not found` after a successful download reads as a corrupt
# release rather than a minimal image missing tar.
missing=""
for t in uname mktemp tar chmod mv; do
  command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
done
[ -z "$missing" ] || die "missing required tools:$missing
  On a minimal image: apt-get install -y tar coreutils (or the dnf/apk equivalent)"

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

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "sha256sum (or shasum) is required to verify the download"
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
  # Intel Macs are not built: Apple Silicon is all that is left in use, and
  # GitHub no longer has a runner to smoke-test an Intel build on.
  x86_64|amd64) [ "$os" = darwin ] && die "Intel Macs are not supported — releases carry macOS arm64 only"
                arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch (releases carry amd64 and arm64)" ;;
esac

if [ -z "$version" ]; then
  version=$(latest_tag)
  [ -n "$version" ] || die "could not determine the latest release; pass --version vX.Y.Z"
fi

ENGRAM="$INSTALL_DIR/engram"

# ------------------------------------------------------- 1. the binary --------
# The same version is not downloaded again: install = upgrade = check has to be
# one command, so it can simply be run again whenever in doubt.
current=""
if [ -x "$ENGRAM" ]; then
  current=$("$ENGRAM" version 2>/dev/null | awk '{print $2}' || true)
fi

if [ -n "$current" ] && [ "$current" = "$version" ] && [ "$FORCE" -eq 0 ]; then
  say "engram $version is already installed: $ENGRAM (--force to download again)"
else
  asset="engram_${version}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/$version"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM

  if [ -n "$current" ]; then say "upgrading engram $current -> $version ($os/$arch)"
  else say "engram $version ($os/$arch)"; fi
  fetch "$base/$asset" "$tmp/$asset" || die "could not download $base/$asset"

  # Verify the download. A truncated or tampered binary that runs anyway is
  # worse than one that fails here, because it fails later and looks like a bug.
  fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "could not download $base/SHA256SUMS"
  expected=$(grep " $asset\$" "$tmp/SHA256SUMS" | awk '{print $1}')
  [ -n "$expected" ] || die "SHA256SUMS has no entry for $asset"
  [ "$(sha256 "$tmp/$asset")" = "$expected" ] || die "checksum mismatch for $asset — refusing to install"

  tar -xzf "$tmp/$asset" -C "$tmp" || die "could not unpack $asset"
  [ -f "$tmp/engram" ] || die "the archive did not contain an engram binary"

  mkdir -p "$INSTALL_DIR"
  # Replace by rename so a running process is never overwritten in place.
  mv "$tmp/engram" "$INSTALL_DIR/engram.new"
  chmod 0755 "$INSTALL_DIR/engram.new"
  mv "$INSTALL_DIR/engram.new" "$ENGRAM"
  say "installed: $ENGRAM"
fi
"$ENGRAM" version

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    # The $PATH in the format string is meant to reach the terminal literally —
    # it is a line for the reader to paste into their shell profile.
    # shellcheck disable=SC2016
    printf '\nwarning: %s is not on your PATH, and the hooks find engram there. Add this to your shell profile:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" "$INSTALL_DIR"
    ;;
esac

# ------------------------------------------------ 2. 0.x leftovers ------------
# Up to 0.11 the client kept a store address and a token on this machine and
# registered a local stdio MCP server. None of it is read any more. The files
# are only pointed out, never deleted: store.token is a credential, and whether
# it is still used elsewhere is the owner's call.
cfgdir="${ENGRAM_CONFIG_DIR:-${CLAUDE_CONFIG_DIR:-$HOME/.claude}}/engram"
for f in "$cfgdir/config.json" "$cfgdir/store.token"; do
  if [ -e "$f" ]; then
    say ""
    say "note: $f is left over from the old client and is no longer read."
    say "      Remove it when nothing else uses it (store.token is the store's credential)."
  fi
done

if command -v claude >/dev/null 2>&1; then
  if claude mcp get engram 2>/dev/null | grep -Eiq 'type: *stdio|command: *engram'; then
    say ""
    say "note: engram is still registered as a local (stdio) MCP server, which this"
    say "      binary no longer implements. Replace it with the remote server:"
    say "        claude mcp remove engram -s user"
    say "        claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp"
  fi
fi

say ""
say "Done. In Claude Code, /mcp__engram__setup checks the rest (the MCP"
say "registration and the plugin); restart Claude Code after it."
