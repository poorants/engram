# Design decisions

The things worth knowing before you rely on this, and what each one cost.

## One ranking

The web viewer and `brain_search` hit the same `/api/search`. If there were two,
the order a person saw and the order an agent received would drift, and nobody
could reason about either — "it's not finding X" would have two possible
meanings and no way to tell them apart.

The cost is that the viewer cannot have a ranking tuned for browsing. That is
the right trade: the viewer exists to let a person check what the agent sees.

## No fallback and no queue

If the store is unreachable, reads and writes both fail on the spot. Exit code
`4`, nothing retried, nothing spooled.

A stale cached answer and a spool sitting somewhere both manufacture the belief
that it worked, and that belief outlives the outage. The session that read a
cached document proceeds on out-of-date knowledge and has no way to know; the
session whose write was queued reports success for something that may never
land. Failing loudly is recoverable. Believing wrongly is not.

A scope refusal is a different thing and takes the other path — the store is
alive and declined, so the document goes to the local file brain, which is where
it belonged.

## The byline is a claim, not a proof

The write token is one shared credential, so the recorded author is what the
client says it is: `ENGRAM_AUTHOR`, else `git config user.name`, else `$USER`.

The goal is to make it honest by default, not provable. Proving it means
accounts, issuing, rotation and revocation — a different system, and one whose
setup cost would be paid by every team that only needed to know roughly who
wrote what. If you need attribution you can act on, this is not it.

## Lexical search, not vectors

Measured with and without a vector channel: recall was the same and the failures
were the same questions. The production image does not carry the extension, and
the deployment stays one compose file.

That property decides the installation barrier, and the installation barrier
decides whether a store exists at all. A brain nobody stood up has recall zero.

Two channels are fused with RRF inside the lexical ranking — see
[`server/app/search.py`](../server/app/search.py) and the
[bench](../server/bench/README.md). Ranking changes are the one kind of change
that degrades invisibly, so a bench run is required in the PR.

## The three layers are not collapsed

A skill cannot be an MCP server: a skill is instructions a model reads, an MCP
server is a process. A server cannot be a client: there is one store and as many
clients as there are people. Collapsing any pair would save a directory and cost
the ability to upgrade them independently — which matters most for the skill,
which changes far more often than the wire format.

## Exactly one transport client

The MCP tools, `engram <verb>` and the skill's Python wrapper are three surfaces
over one client. Two clients would mean two places to put the token, two default
authors, and two copies of the address rules to drift apart — and address rules
that differ between the CLI and the MCP server produce documents at addresses
nothing can find.

## The owner allow-list is a list of groups

`ENGRAM_OWNERS` admits groups, not repos, and the asymmetry is the point: a new
repo under an admitted group works with no change at all, and a personal or
client repo never does. The boundary holds by default rather than by remembering
to maintain it — a per-repo list would be correct on the day it was written and
wrong by the third new service.

Reads need no token, so what must not be readable is never let in. Adding read
auth would let the allow-list be sloppy, which is the wrong direction.

## Never delete

There is no `brain_delete`. The contract is *move to archives*, and `move`
leaves the old path as an alias.

A store that can forget is a store whose absences are ambiguous: nobody can tell
"we decided against this" from "somebody tidied up", and the second reading is
the one that makes people re-investigate. Archives cost disk. Ambiguity costs
the thing the store exists for.

## Not multi-tenant

One service on a LAN or a personal server. No per-tenant isolation, no row-level
security, no plan to add either. Multi-tenancy is not a feature bolted onto this
shape; it is a different product, and pretending otherwise would put a
half-isolation in the way of everyone who does not need it.

## One port

The database publishes none. Only the app reaches it inside the compose network,
and even bulk imports arrive over the app's port (`POST /api/index`), so the
server needs neither documents on disk nor git credentials.

## What was cut

Written up in the example brain that ships with the server:
[`public-release.md`](../server/bench/corpus/projects/public-release.md).
