# Telling a refusal from an outage

The `engram` CLI's exit codes are a contract, so a caller can branch without
parsing prose:

| code | meaning |
|---|---|
| `0` | success |
| `1` | an error — a bad argument, a malformed path, a missing token |
| `3` | the store REFUSED this path's owner group (403) |
| `4` | the store could not be REACHED at all |

## Why 3 and 4 are different

They lead to opposite actions, and getting them backwards is expensive in a
specific way.

A **refusal** means the store is alive and declined. The document belongs in a
local file brain, and writing it there is correct ([[scope-boundary]]).

An **outage** means nothing is known. Writing the document somewhere local would
put it in a file nobody reads while everyone believes it was recorded — and that
belief outlives the outage by months.

If the difference lived only in the message text, a caller matching on prose
would one day file a network failure as a scope refusal and quietly stop
recording what the team writes.

## There is no fallback and no queue

When the store is unreachable, reads and writes both fail on the spot. A stale
cached answer and a spool sitting somewhere both manufacture the belief that it
worked. The honest answer to "the brain is down" is to say so, not to answer from
something older than the question.

A validation error is neither 3 nor 4. An empty note or a malformed path never
touched the network, and reporting it as an outage sends the caller to check
their VPN instead of their arguments. An unconfigured store address is likewise a
setup error, not an outage.
