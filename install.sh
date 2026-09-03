#!/usr/bin/env sh
# engram installer — downloads one prebuilt binary from GitHub Releases.
#
# There is nothing to build and nothing to clone. engram is a single static
# binary with no runtime, so installing it is: pick the right file, check its
# hash, put it on PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh
#
# Knobs:
#   ENGRAM_VERSION=v0.1.0   install a specific release (default: the latest)
#   ENGRAM_INSTALL_DIR=...  where to put it (default: ~/.local/bin)
#   ENGRAM_REPO=owner/name  install from a fork
set -eu

REPO="${ENGRAM_REPO:-poorants/engram}"
INSTALL_DIR="${ENGRAM_INSTALL_DIR:-$HOME/.local/bin}"

die() { printf 'engram install: %s\n' "$1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }

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
  mingw*|msys*|cygwin*) die "on Windows, download the .zip from https://github.com/$REPO/releases and put engram.exe on your PATH" ;;
  *) die "unsupported operating system: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch (releases carry amd64 and arm64)" ;;
esac

version="${ENGRAM_VERSION:-}"
if [ -z "$version" ]; then
  version=$(latest_tag)
  [ -n "$version" ] || die "could not determine the latest release; set ENGRAM_VERSION=vX.Y.Z"
fi

asset="engram_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'engram %s (%s/%s)\n' "$version" "$os" "$arch"
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

printf 'installed: %s\n' "$INSTALL_DIR/engram"
"$INSTALL_DIR/engram" version

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) printf '\nwarning: %s is not on your PATH. Add this to your shell profile:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" "$INSTALL_DIR" ;;
esac

cat <<'NEXT'

Next:
  engram store set http://<host>:8081 --token <write token>
  engram store doctor

No store yet? Bring one up on any machine with Docker:
  git clone https://github.com/poorants/engram && cd engram/server
  cp .env.example .env   # fill in the two secrets and ENGRAM_OWNERS
  docker compose up -d
NEXT
