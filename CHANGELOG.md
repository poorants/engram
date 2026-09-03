# Changelog

Notable changes per release. Versions follow [semantic versioning](https://semver.org/);
until 1.0 the wire format and the CLI surface may change between minor versions,
and anything that does will say so here.

Releases are cut by tagging `vX.Y.Z`, which builds and publishes the binaries.

## [Unreleased]

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
- `docs/` — install, concepts, CLI and MCP reference, design decisions,
  troubleshooting.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`, issue and pull request
  templates.

## [0.1.0] — unreleased

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

[Unreleased]: https://github.com/poorants/engram/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/poorants/engram/releases/tag/v0.1.0
