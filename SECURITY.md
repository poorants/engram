# Security policy

## Reporting a vulnerability

**Do not open a public issue.**

Use GitHub's private reporting:
[Report a vulnerability](https://github.com/poorants/engram/security/advisories/new).

Please include what an attacker gets, the steps to reproduce it, and which
component is affected — client, store, or skill. You will get an
acknowledgement within a few days; expect a real answer rather than a fast one.

## Supported versions

The latest release. There are no long-term support branches — this is a small
project and a fix ships as a new release.

## What engram's threat model actually is

Read this before deciding whether something is a vulnerability, because several
properties that look like weaknesses are documented design decisions with
reasons attached ([docs/design.md](docs/design.md)).

**Not multi-tenant.** One store serves one group on a LAN or a personal server.
There is no per-tenant isolation and no plan for any. Cross-tenant reasoning does
not apply.

**Reads need no token.** Anything admitted into the store is readable by anyone
who can reach the port. That is not an oversight — it is why the owner allow-list
is the control that matters. "Unauthenticated read of a document" is the design.

**The write token is one shared credential.** Everyone who can write shares it.
It is not per-user, it cannot be scoped, and revoking it means rotating it for
everyone.

**The byline is a claim, not a proof.** The recorded author is whatever the
client said. Forging a byline is possible by design; proving identity would mean
accounts, issuing and revocation, which is a different system. Reports that
amount to "the author field can be spoofed" are known.

**Deployment is expected to be on a trusted network.** The compose file publishes
one port over plain HTTP. Exposing it to the internet without a reverse proxy
doing TLS and access control is a deployment mistake, not a vulnerability in this
code.

## What is in scope

- The **owner allow-list being bypassed** — a document written under an owner
  group the store does not admit. This is the confidentiality boundary and the
  single most important property in the system.
- **Injection** in the store: SQL injection, or crafted markdown that escapes
  chunking, linking or the viewer's rendering.
- **Path traversal** in a document address, in `bin/import_tree.py`, or in
  `engram get --out`.
- **The installers**: anything that makes `install.sh` or `install.ps1` execute
  or install something other than the verified release asset — checksum
  verification that can be skipped, or an archive that can write outside the
  install directory.
- **Token disclosure**: the write token appearing in `config.json`, in logs, in
  process arguments visible to other users, or in a file that is not `0600`.
- **Remote code execution** anywhere, including through the skill's hooks.
- Anything that lets a **read** reach content the store never admitted.

## What is not

- The design decisions listed above.
- Denial of service by volume against a store on your own network.
- Vulnerabilities in Postgres, Docker, or Claude Code — report those upstream.
- A store you deliberately exposed to the internet unproxied.
- Missing hardening headers on the viewer, absent a concrete attack.

## Handling of secrets, for reference

The write token is written to `~/.claude/engram/store.token` at mode `0600`,
deliberately not into `config.json` — that is a file people open and paste from.
On the server both secrets live in `server/.env`, which is gitignored and never
committed. `server/setup.sh` generates them with `openssl rand -hex 24`, or
`/dev/urandom` if openssl is absent, and refuses to run if it has neither rather
than falling back to something weaker.
