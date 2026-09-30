# Installing engram

engram has three parts, set up on two kinds of machine.

| Part | What | Where | How often |
|---|---|---|---|
| the **store** | Postgres 17 + the app, one compose file — [`server/setup.sh`](../server/README.md) | one Linux/macOS host with Docker | once, for everyone |
| the **remote MCP server** | `engram serve` — the `brain_*` tools over HTTP, with its own OAuth | the same host, beside the store | once, for everyone |
| a **person's machine** | an MCP registration, the plugin (skill + hooks), and the `engram` binary the hooks run | Linux, macOS **and Windows** | every person, every machine |

A session reaches the brain **only** through the remote MCP server. A person's
machine holds no store address, no token and no settings file; the binary on it
runs the capture-loop hooks and nothing else.

## A person's machine

Three steps, and the last one does the rest.

**1. Register the server** — once per machine, at user scope:

```bash
claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
```

**2. Log in** — in Claude Code, `/mcp` → engram → **Authenticate**. The server
is its own OAuth authorization server: Claude Code registers itself, opens a
browser for a Google login, and keeps and refreshes the token from then on.
Only people on the server's allow-list get in.

On a machine reached over SSH the browser runs on your laptop but the callback
lands on the remote machine. That is what `--callback-port 33418` is for: fix
the port, and forward it before you authenticate:

```bash
ssh -L 33418:localhost:33418 <remote machine>
```

**3. Finish the machine** — in a session, run:

```
/mcp__engram__setup
```

The server serves this prompt with its own address written in. It checks the
registration (and tells you how to replace a leftover stdio one), installs the
plugin, installs or upgrades the `engram` binary, and ends with a table of
what was ok, what it fixed and what needs you. Restart Claude Code afterwards so
the skill and the hooks load.

Why these pieces are separate: the **MCP server** carries the `brain_*` tools;
the **skill** is the instructions a model reads — when to save, what to link,
how to classify; the **capture hooks** (`SessionStart`, `UserPromptSubmit`,
`Stop`) keep the brain fed without anyone remembering to. The skill and the
hooks ship in the plugin; the hooks run `engram hook`.

**Register at user scope.** The brain is not a property of one checkout, and a
per-project registration makes the tools vanish the first time someone opens a
different repo.

### The binary alone

What step 3 runs, if you want it by hand:

```bash
# Linux · macOS — into ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh
```

```powershell
# Windows — into %LOCALAPPDATA%\engram\bin, added to your user PATH
irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
```

Both download the release for your OS and architecture, verify it against the
release's `SHA256SUMS` (a mismatch or a missing entry refuses to install), and
swap it in by rename so a running hook is never overwritten in place.
**Re-running is the upgrade**, and the same version is a no-op.

| Option | Environment | Meaning |
|---|---|---|
| `--version <tag>` | `ENGRAM_VERSION` | install a specific release (default: the latest) |
| `--dir <path>` | `ENGRAM_INSTALL_DIR` | install location |
| `--repo <owner/name>` | `ENGRAM_REPO` | install from a fork |
| `--force` | — | download again even at the same version |

On Windows the same names work as PowerShell parameters if you download the
script instead of piping it: `.\install.ps1 -Version vX.Y.Z -Force`.

Running `install.sh` in Git Bash, MSYS or Cygwin does not try: it detects
Windows and points you at `install.ps1`, because a Linux binary on `PATH` there
fails later in a way that looks like an engram bug.

### Coming from 0.11 or earlier

The client used to be a CLI and a local stdio MCP server with its own settings.
The installers point out what is left of it, and delete nothing:

- `~/.claude/engram/config.json` and `~/.claude/engram/store.token` are no
  longer read. Remove them once nothing else uses them — `store.token` is the
  store's credential.
- An `engram` MCP registration that is a local command (`engram mcp`) no longer
  starts. Replace it:

  ```bash
  claude mcp remove engram -s user
  claude mcp add --transport http --scope user --callback-port 33418 engram https://<host>/mcp
  ```

- Update the plugin so the hooks and the skill match the binary — a plugin
  change is not carried by a binary update:

  ```bash
  claude plugin marketplace update engram
  claude plugin update engram@engram   # applies after a Claude Code restart
  ```

A frozen (`+noupdate`) build from the managed-endpoint instructions can simply
be replaced: there is no self-update left to freeze.

## The store and its server

### The store

See [self-hosting](../server/README.md): `server/setup.sh` brings up the compose
stack, generates the secrets and proves the result answers.

### `engram serve`

The remote MCP server runs on the store's host and is the only process that
holds the store's credential. Install the binary with `install.sh --dir
/opt/engram-serve/bin`, and run it with the systemd unit in
[`server/deploy/engram-serve.service`](../server/deploy/engram-serve.service).
Its settings are environment variables, kept in an `EnvironmentFile` (mode
600):

| Variable | Meaning |
|---|---|
| `ENGRAM_TOKEN` | the store's one credential — the same value as `server/.env` |
| `ENGRAM_SERVE_ISSUER` | this server's public origin, `https://…`, no path |
| `ENGRAM_SERVE_KEY` | base64 of ≥32 random bytes (`openssl rand -base64 32`); signs everything issued. Rotating it signs everyone out |
| `ENGRAM_SERVE_GOOGLE_CLIENT_ID` / `_SECRET` | a Google OAuth "Web application" client whose redirect URI is `<issuer>/oauth/google/callback` |
| `ENGRAM_SERVE_ALLOW` | who may log in: emails and/or Google subject ids, comma-separated |
| `ENGRAM_SERVE_AUTHOR` | optional fixed byline; default the email's local part |

It listens on plain HTTP on loopback (`--addr`, default `127.0.0.1:8682`) and
reaches the store at `--store` (default `http://127.0.0.1:8081`). TLS belongs to
the proxy in front of it — `tailscale serve`, Caddy — and the proxy must hand it
the **whole origin**, because the OAuth metadata lives at `/.well-known/`. It
refuses to start with any setting missing, rather than admit nobody or admit on
a guess.

Upgrading it is re-running the installer into the same `--dir` and restarting
the unit.

## Verifying what you downloaded

Both installers do this; by hand:

```bash
version=vX.Y.Z
base=https://github.com/poorants/engram/releases/download/$version
curl -fsSLO $base/engram_${version}_linux_amd64.tar.gz
curl -fsSLO $base/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
```

The canonical source of both installers is the repository's raw URL on `main`.
If you hand one around (a wiki page, a chat pin), hand around that URL or a
loader that fetches it — never a copy, which keeps working six months behind.

## Manual install

Every release carries one archive per platform. Download the one for your
machine from the [releases page](https://github.com/poorants/engram/releases),
unpack it, and put `engram` (or `engram.exe`) anywhere on your `PATH` — the
hooks find it there.

| OS | Arch | Asset |
|---|---|---|
| Linux | x86-64 | `engram_<version>_linux_amd64.tar.gz` |
| Linux | ARM64 | `engram_<version>_linux_arm64.tar.gz` |
| macOS | Apple Silicon | `engram_<version>_darwin_arm64.tar.gz` |
| Windows | x86-64 | `engram_<version>_windows_amd64.zip` |
| Windows | ARM64 | `engram_<version>_windows_arm64.zip` |

The binaries are not notarized; on macOS clear the quarantine attribute once:
`xattr -d com.apple.quarantine ~/.local/bin/engram`.

## From source

Go 1.25 or newer. Nothing else — `CGO_ENABLED=0` is not optional, it is what
makes the result a static binary with no runtime.

```bash
git clone https://github.com/poorants/engram && cd engram
make build            # ./engram
make install          # ~/.local/bin/engram
```

Or `go install github.com/poorants/engram/cmd/engram@latest`, which does not
stamp the version: `engram version` reports the module's pseudo-version.

## Uninstalling

```bash
claude mcp remove engram -s user
claude plugin uninstall engram@engram
rm ~/.local/bin/engram
```

On Windows the binary is `%LOCALAPPDATA%\engram\bin\engram.exe`. Removing a
machine touches nothing in the store — documents live there, and nothing is
cached locally.
