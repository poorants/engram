# Changelog

Notable changes per release. Versions follow [semantic versioning](https://semver.org/);
until 1.0 the wire format and the CLI surface may change between minor versions,
and anything that does will say so here.

Releases are cut by tagging `vX.Y.Z`, which builds and publishes the binaries.

## [Unreleased]

## [0.2.0] — 2026-09-04

### Added
- `install.ps1` — the Windows client installer. The client (binary, CLI, MCP
  server, skill) now supports Linux, macOS and Windows; the store remains
  Linux/macOS only, because it is Postgres in Docker.
- `windows/arm64` release target.
- `server/setup.sh` — brings a store up in one command: generates both secrets,
  writes `.env`, starts the compose stack, waits until it answers, and prints
  the client install one-liner with the address and token filled in.
- Both client installers now finish the whole client setup, not just the binary:
  `--store` / `--token` designate the store and run `store doctor`, and the
  skill, its capture hooks and the `brain_*` MCP tools are registered with
  Claude Code at user scope. `--no-claude` installs the binary alone.
- Reads require the token. The store has one credential, `ENGRAM_TOKEN`, and it
  authorises reads and writes alike; the viewer asks for it once and keeps a
  session cookie. `ENGRAM_PUBLIC_READS=true` (`setup.sh --public-reads`) opens
  reads deliberately, for a network where everyone who can reach the port may
  already read everything.
- `setup.sh --tls [name]` — HTTPS for a store on a host with a public IP: Caddy
  with a Let's Encrypt certificate, `<public-ip>.sslip.io` as the name when
  none is given, the plaintext port re-bound to loopback. The installer checks
  that the host actually has the public address and that 80/443 are free
  before writing anything. `ENGRAM_BIND` for hosts that already run a reverse
  proxy; a guide for hosts with no public IP (Tailscale, DNS-01).
- The viewer's session is renewed on every visit, so a browser that comes back
  within thirty days is never asked for the token again. Rotating the token
  ends every session.
- `docs/` — install, concepts, CLI and MCP reference, design decisions,
  troubleshooting.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue and pull request
  templates.

### Changed
- `ENGRAM_INGEST_TOKEN` is `ENGRAM_TOKEN`, and `ENGRAM_READ_AUTH` is gone: there
  is one credential and reads are closed unless `ENGRAM_PUBLIC_READS` says
  otherwise. A `.env` with the old names does not start.

### Fixed
- `engram store set <url>` without `--token` no longer reports a machine that
  already has a token as having none. It reached the store unauthenticated,
  took the 401, dropped the cached owner list, and printed `token: not set —
  Re-run with --token` — so re-running `store set` to change only the address
  or the byline read as if it had just discarded the credential. It never had;
  the token file is not written unless `--token` is given. The command now
  probes with the token already configured (`--token`, else `ENGRAM_TOKEN`,
  else `store.token`) and reports `token: kept` and where it came from.
- `make lint` no longer fails on every run under `dash` (Debian/Ubuntu `/bin/sh`).
  The gofmt check ended in `| (! read)`, and `read` with no variable is a
  bashism — the recipe died with `read: arg count` whatever gofmt found, so the
  target reported nothing useful in either direction.

## [0.1.1] — 2026-09-03

### Added
- The release workflow installs what it just built, on every OS the install
  instructions claim, so a broken asset name or archive layout fails the release
  rather than the first person to follow the README.

## [0.1.0] — 2026-09-03

First public release. Extracted from three private repositories, made general,
and measured against its own bench.

- One static binary: MCP server + CLI + store setup, `CGO_ENABLED=0`, no runtime.
- Six tools — `brain_search`, `brain_get`, `brain_revisions`, `brain_integrity`,
  `brain_put`, `brain_move` — over the CLI and over MCP, one transport client
  underneath.
- The store: FastAPI + Postgres 17 in one compose file, with the viewer served
  from the same ranking the tools get.
- PARA addressing derived from the git remote, with an owner allow-list as the
  confidentiality boundary.
- Revision history per document, aliases that keep links alive across moves, and
  no delete.
- The Claude Code skill, its references and the capture hooks.

[Unreleased]: https://github.com/poorants/engram/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/poorants/engram/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/poorants/engram/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/poorants/engram/releases/tag/v0.1.0
