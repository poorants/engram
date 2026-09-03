# Which repos may write

`ENGRAM_OWNERS` on the server lists the owner groups this store admits. A write
whose path begins with anything else is refused with 403.

## Why the boundary is not a column

`owner` and `repo` are columns, and they serve relevance — what ranks first. They
cannot protect confidentiality, because reads are open ([[store-api]]): whatever
the column says, the document is readable by anyone who can reach the service.

So what must not be read is **never let in**. That is enforced by the allow-list
at write time, not by anyone remembering which repo they are standing in.

## Why groups and not repos

Enumerate repos and the list falls behind the day someone creates one — and then
either a legitimate repo is locked out, or somebody widens the list carelessly
and something wrong slips in. "This group only" keeps being correct as repos come
and go underneath it.

## Empty means closed

The default is an empty list, which admits nothing. A deployment that forgot to
configure this closes rather than opens. The opposite default would produce a
store that looks like it is working, right up until the wrong document is in it.

This design predates the public release and survived it unchanged — see
[[public-release]] for what was cut and what was not.

## Scope is derived, never chosen

The client derives `owner/repo` from the working directory's git remote. Nobody
picks a scope, which is what turns a habit into a boundary: a repo the store does
not admit cannot have its knowledge filed into the store by someone forgetting
where they were.

When the store refuses, the document is not lost — it goes to a local file brain,
which is where knowledge from an unadmitted repo belonged all along. That routing
depends on telling a refusal apart from an outage; see [[client-exit-codes]].
