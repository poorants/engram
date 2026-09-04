<!-- Thanks for the patch. Keep this short; delete what does not apply. -->

## What and why

<!-- What changes, and the reason. The diff shows the what; the why is what
     review needs. If it fixes an issue: Fixes #123 -->

## Which part

- [ ] client — `cmd/`, `pkg/`, `internal/`
- [ ] store — `server/`
- [ ] skill — `skills/engram/`
- [ ] installers / CI / docs

<!-- Spanning two of these is fine, but say why: the layers are deliberately not
     collapsed. https://github.com/poorants/engram/blob/main/docs/design.md -->

## Checks

- [ ] `make lint test` passes
- [ ] `python -m pytest server/tests -q` passes (if `server/` changed)
- [ ] `go test ./... -race` passes (if `cmd/`, `pkg/` or the skill changed)

## Ranking

- [ ] This does not touch ranking.
- [ ] It does, and the bench numbers before and after are below.

<!-- "Touches ranking" means server/app/search.py, server/app/core.py, or the
     schema's indexes. Ranking degrades invisibly — a change that helps five
     questions and quietly breaks three looks like an improvement from the
     inside. See server/bench/README.md.

     make server-up && make bench-seed && make bench -->

```
before:
after:
```

## Decided design

- [ ] This does not change any of the decided design points (no delete, no
      fallback/queue, one ranking, lexical by default, not multi-tenant, one
      transport client).
- [ ] It changes one, and the PR says which reason no longer holds.
