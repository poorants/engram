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

## One token, and it is not a permission system

`ENGRAM_TOKEN` answers exactly one question: may this caller use this store at
all. Whoever holds it can read everything and write everything. There is no read
token and no write token, no roles, no accounts — it is a deployment credential
in the shape of a personal access token.

Splitting it was considered and rejected. A read/write split would mean two
secrets to distribute, rotate and lose, and a store whose entire model is *one
shared credential per deployment* does not get safer by having two of them; it
gets a second thing to be inconsistent about. The cost is real and worth naming:
anyone who may read may also write, so "let someone browse the brain" and "let
someone change the brain" are the same grant.

What separates a machine that may write from one that may not is therefore not
authorisation at all. It is whether that machine was given the token. A client
set up without one is read-only because it cannot authenticate for a write, not
because it holds a lesser credential.

**Reads are closed by default.** The original design left them open, on the
reasoning that the owner allow-list is the boundary and what must not be
readable is never let in. That holds exactly as long as the port is on a trusted
network — and it stops holding silently, because nothing about a wide-open store
looks wrong until the day it matters. A default that is only correct under a
condition the software cannot check is not a safe default.
`ENGRAM_PUBLIC_READS=true` is the deliberate opt-out.

The gate is middleware with a named list of unauthenticated paths, not a
dependency on each route. Forgetting a dependency leaves a route serving
everything to anyone — silent, and found by somebody else. Forgetting to list a
genuinely public route breaks it loudly, for whoever added it. The writes carry
a second check on top, so a mistake in that list cannot open one.

A browser cannot put a header on a navigation, so the viewer trades the token
for an HttpOnly, SameSite session cookie at `/login`. The alternative — a token
in the URL — lands in history, bookmarks, referrers and every log along the way.

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

It is not a substitute for authentication and does not become one. The
allow-list decides *what* may enter the store; the token decides *who* may talk
to it at all. Collapsing the two — letting either stand in for the other — is
what produces a store that is careful about which repos it admits and then
serves all of them to the internet.

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
