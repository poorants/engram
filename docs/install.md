# Installing engram

There are **two installers**, and they set up two different kinds of machine.

| Script | Installs | Where | How often |
|---|---|---|---|
| [`server/setup.sh`](../server/README.md) | the **store** — Postgres 17 + the app, one compose file | one Linux/macOS host with Docker | once, for everyone |
| `install.sh` / `install.ps1` | the **client** — binary, CLI, MCP server, skill, capture hooks | Linux, macOS **and Windows** | every person, every machine |

The store is Postgres and is **not supported on Windows**. A Windows machine
installs the client and points it at a store somebody brought up elsewhere.

This page is about the client. For the store, see
[self-hosting](../server/README.md).

## The client, in one command

The client installer does four things: downloads and verifies the binary,
designates the store, proves the machine can write to it, and registers the
skill, the capture hooks and the `brain_*` MCP tools with Claude Code. That last
part is here rather than left as three lines in a README because it is the part
people skip, and a session with no `brain_*` tools looks like engram not working.

Paste what `server/setup.sh` printed when the store was brought up:

```bash
# Linux · macOS
curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh \
  | sh -s -- --store http://<host>:8081 --token <store token>
```

```powershell
# Windows — iex cannot take parameters, so pass them as environment variables
$env:ENGRAM_STORE_URL = 'http://<host>:8081'
$env:ENGRAM_TOKEN     = '<store token>'
irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex
```

With no arguments both installers still work — you get the binary, and
everything else is printed as commands to run yourself.

A store brought up with `setup.sh --tls` has an `https://` address and no port —
`https://203-0-113-10.sslip.io`, or the name its operator gave it. The client
needs nothing else; a public certificate is verified like any other.

### Options

Every option has a matching environment variable, which is how you pass it to
the PowerShell one-liner.

| Option | Environment | Meaning |
|---|---|---|
| `--store <url>` | `ENGRAM_STORE_URL` | designate the store, then run `store doctor` |
| `--token <t>` | `ENGRAM_TOKEN` | the store's one credential — reads and writes alike |
| `--author <name>` | `ENGRAM_AUTHOR` | byline stamped on revisions from this machine |
| `--version <tag>` | `ENGRAM_VERSION` | install a specific release (default: the latest) |
| `--dir <path>` | `ENGRAM_INSTALL_DIR` | install location |
| `--repo <owner/name>` | `ENGRAM_REPO` | install from a fork |
| `--no-claude` | — | binary only: skip the skill, the hooks and the MCP server |

On Windows the same names work as PowerShell parameters if you download the
script instead of piping it: `.\install.ps1 -Store http://host:8081 -Token abc`.

### Where things land

| | Linux · macOS | Windows |
|---|---|---|
| binary | `~/.local/bin/engram` | `%LOCALAPPDATA%\engram\bin\engram.exe` |
| settings | `~/.claude/engram/config.json` | `%USERPROFILE%\.claude\engram\config.json` |
| token | `~/.claude/engram/store.token` (mode `0600`) | the same path |

The Windows installer adds its directory to your user `PATH`; the POSIX one
warns if `~/.local/bin` is not already on yours.

The MCP server is registered at **user scope** (`--scope user`), not per
project. The brain is not a property of one checkout, and registering it
per-project means the tools vanish the first time someone opens a different
repo.

### Git Bash, MSYS, Cygwin

Running `install.sh` there will not work and does not try to: it detects Windows
and points you at `install.ps1`. Those shells report a POSIX `uname`, but the
machine still needs `engram.exe`, and a Linux binary installed onto `PATH` there
fails later in a way that looks like an engram bug.

## Reading the script before you run it

Piping a script into a shell is a reasonable thing to be uncomfortable with.
Both installers are short and readable, and downloading first costs one line:

```bash
curl -fsSLO https://raw.githubusercontent.com/poorants/engram/main/install.sh
less install.sh && sh install.sh
```

```powershell
irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 -OutFile install.ps1
Get-Content install.ps1 | more; .\install.ps1
```

Or skip the installers entirely — see [manual install](#manual-install) below.

### Why the raw URL, and not a gist

The canonical source for both scripts is **the repository's raw URL on `main`**:

```
https://raw.githubusercontent.com/poorants/engram/main/install.sh
https://raw.githubusercontent.com/poorants/engram/main/install.ps1
```

A gist would be shorter to type and it is a common way to hand a script around,
but it is a *second copy* — it does not move when the repo does, it is not
reviewed in a pull request, it does not show up in the diff of the commit that
changed the installer, and CI never runs it. The failure mode is not that the
gist breaks; it is that the gist keeps working, six months behind, pointing at
an asset naming scheme the releases no longer use.

If you do want a gist — for a wiki page, a chat pin, an internal onboarding doc —
make it a **loader, not a copy**, so it cannot go stale:

```sh
#!/usr/bin/env sh
# engram installer — this is a pointer. The real script lives in the repo.
exec sh -c "$(curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh)"
```

The same rule applies to any mirror: pin to a tag if you want a version frozen
in place (`.../poorants/engram/v0.1.0/install.sh`), and never fork the body of
the script somewhere it will not be updated.

## Verifying what you downloaded

Both installers fetch `SHA256SUMS` from the same release and refuse to install
on a mismatch. To do it by hand:

```bash
version=v0.1.0
base=https://github.com/poorants/engram/releases/download/$version
curl -fsSLO $base/engram_${version}_linux_amd64.tar.gz
curl -fsSLO $base/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS
```

## Manual install

Every release carries one archive per platform. Download the one for your
machine from the [releases page](https://github.com/poorants/engram/releases),
unpack it, and put `engram` (or `engram.exe`) anywhere on your `PATH`.

| OS | Arch | Asset |
|---|---|---|
| Linux | x86-64 | `engram_<version>_linux_amd64.tar.gz` |
| Linux | ARM64 | `engram_<version>_linux_arm64.tar.gz` |
| macOS | Intel | `engram_<version>_darwin_amd64.tar.gz` |
| macOS | Apple Silicon | `engram_<version>_darwin_arm64.tar.gz` |
| Windows | x86-64 | `engram_<version>_windows_amd64.zip` |
| Windows | ARM64 | `engram_<version>_windows_arm64.zip` |

### macOS Gatekeeper

The binaries are not notarized. The first run of a downloaded binary is blocked
until you clear the quarantine attribute:

```bash
xattr -d com.apple.quarantine ~/.local/bin/engram
```

## From source

Go 1.25 or newer. Nothing else — `CGO_ENABLED=0` is not optional, it is what
makes the result a static binary with no runtime.

```bash
git clone https://github.com/poorants/engram && cd engram
make build            # ./engram
make install          # ~/.local/bin/engram
```

Or, without a clone:

```bash
go install github.com/poorants/engram/cmd/engram@latest
```

`go install` builds from the module path and does not stamp the version, so
`engram version` reports the module's pseudo-version rather than a release tag.

## Connecting to a store by hand

The installer does this for you when you pass `--store`. To do it later, or to
point an existing install at a different store:

```bash
engram store set http://<host>:8081 --token <store token>
engram store doctor
```

`store set` writes two files side by side:

```
~/.claude/engram/config.json    address, cached owner list, author  - plain JSON, readable
~/.claude/engram/store.token    the store's token                   - mode 0600
```

The token is deliberately not in `config.json`: that is a file people open and
paste from, and a secret in it eventually gets copied somewhere it should not
be. The directory is `$ENGRAM_CONFIG_DIR`, else `$CLAUDE_CONFIG_DIR`, else
`~/.claude` - the same ladder the skill's Python helpers walk, so the binary and
the skill land on one file without either being told where the other put it.

Environment beats file for every value, so a CI job or a one-off shell can point
at a different store without editing anything: `ENGRAM_STORE_URL`,
`ENGRAM_TOKEN`, `ENGRAM_AUTHOR`.

`--author <name>` sets the byline stamped on revisions from this machine;
without it the byline falls back to `ENGRAM_AUTHOR`, then `git config
user.name`, then `$USER`.

`store doctor` proves two separate facts — the store answers, and this machine
can write to it. Both matter: a check that proves only the first lets someone
finish a setup read-only and find out at the end of a session, when a save
fails. If it does not pass, see [troubleshooting](troubleshooting.md).

## Claude Code by hand

Also done for you by the installer, unless `--no-claude` or the `claude` CLI was
not on `PATH` at the time:

```bash
claude plugin marketplace add poorants/engram      # the skill and its capture hooks
claude plugin install engram@engram
claude mcp add --scope user engram -- engram mcp   # the brain_* tools
```

Three pieces, and they are not interchangeable. The **MCP server** is the
process that carries the `brain_*` tools. The **skill** is the instructions a
model reads — when to save, what to link, how to classify. The **capture hooks**
(`UserPromptSubmit`, `Stop`) are what keep the brain fed without anyone
remembering to; they ship with the plugin.

The skill and the hooks need `python3` on `PATH`. The MCP server does not — it
is the same static binary.

Use `--scope project` instead of `--scope user` if you want the tools in one
checkout only. That is rarely what you want: the brain is not a property of a
checkout, and per-project registration means the tools disappear the first time
someone opens a different repo.

## Upgrading

Re-run the installer. It replaces the binary by rename, so a running process is
never overwritten in place.

```bash
curl -fsSL https://raw.githubusercontent.com/poorants/engram/main/install.sh | sh   # Linux, macOS
```

```powershell
irm https://raw.githubusercontent.com/poorants/engram/main/install.ps1 | iex        # Windows
```

The store is upgraded separately: `git pull && docker compose up -d --build` in
`server/`. The schema is idempotent and applied on every boot.

## Uninstalling

```bash
engram store unset --forget-token     # drop the designation and delete the token
rm ~/.local/bin/engram
rm -rf ~/.claude/engram               # settings, if store unset left anything
claude mcp remove engram
claude plugin uninstall engram@engram
```

On Windows the binary is at `%LOCALAPPDATA%\engram\bin\engram.exe`; the
settings are at `%USERPROFILE%\.claude\engram`, the same place as everywhere
else. Removing the client does not touch the store - documents live there, and
nothing is cached locally.
