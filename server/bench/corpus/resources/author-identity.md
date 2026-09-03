# Who a revision says wrote it

Every revision records an `author`. The write token is a single SHARED
credential, so that author is a **claim the client makes**, not something the
server can verify.

That distinction decides the whole design: the goal is to make the claim honest
by default, not to make it provable. Proving it would mean per-person tokens,
which means accounts, issuing and revocation — a different system, and one
nobody asked for.

## Resolution order

1. an explicit argument — the caller named someone, never overridden
2. `ENGRAM_AUTHOR`, or `author` in the config file — the deliberate offline answer
3. an injected lookup, for deployments with a forge to ask
4. `git config user.name` — the same name that appears on this person's commits
5. `$USER` / `$LOGNAME` — a machine account, but a real one
6. the literal `engram` — naming the tool, admitting we do not know the person

Step 4 is the good default because it makes brain history and git history line up
on one identity instead of two. The `$USER` it replaces is often `dev`, `root` or
`ubuntu` on a shared or containerized box, and names nobody.

## Failures below step 1 are silent

Every fallback happens without an error and without a warning. A write must never
fail — or even complain — because attribution could not be resolved. The document
is the point; the byline is not.

The lookup is memoized: a session that writes ten documents does not repeat it
ten times, and the answer cannot change between calls within one process.
