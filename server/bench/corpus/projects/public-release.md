# Extracting engram for public release

engram began as three pieces inside a private group: a Postgres-backed store, a
transport client bolted onto a larger internal MCP server, and a skill holding
the judgement. The functionality has nothing to do with the company that grew it
— it solves the general problem of a knowledge store an agent can use — so it was
pulled out.

## What the work actually was

Almost no new code. The bulk of it was relocation, renaming, cutting couplings,
and scrubbing internal references.

Four couplings had to be cut:

1. **The module path** — mechanical.
2. **The transport depending on a credential loader.** Inverted: the client now
   takes a plain config struct, so it knows nothing about where settings are
   stored and can be reused by anything.
3. **Author resolution depending on a specific forge.** Made pluggable. The
   public default is `git config user.name`, and a deployment with a forge to ask
   injects a lookup — see [[author-identity]].
4. **A hardcoded default store address.** Removed entirely. A built-in address is
   a machine that exists on one network and nowhere else, and a client silently
   pointing at it fails in a way that looks like an outage rather than a missing
   setting. Unset now fails in place with the remedy.

## What deliberately did not change

The scope boundary was already general — an environment variable listing admitted
groups, plus a rule deriving the coordinates from the git remote
([[scope-boundary]]). Knowledge cannot leak into the wrong store because of the
remote address, not because anyone remembered. That is a selling point, not a
liability.

## What was left out on purpose

- **Authentication and accounts.** The write token is one shared credential and
  the author is a claim. Making it provable brings accounts, issuing and
  revocation, which is a different system.
- **Fallbacks and offline queues.** An unreachable store fails in place
  ([[client-exit-codes]]).
- **Vector search by default.** Measured as no better than lexical on this
  corpus, and it would cost the single-compose-file property.
- **Multi-tenancy.** This is one service on a LAN or a personal server.
