# Session Update Review — read back what this session fed the brain

The Capture loop *writes* to the brain during work; this workflow *reads back*
what landed, so at session close the user can review exactly what changed. It is
**command-triggered** (the user asks for it), not a hook — the read-back
counterpart to the [capture-loop](capture-loop.md).

Triggers: "이번 세션 브레인 업데이트 리뷰", "엔그램 세션 리뷰", "브레인 업데이트
알려줘", "세션 회고", "review brain updates", "engram session review", "what
changed in the brain this session".

## Sources — combine two; model memory is primary

1. **Session memory (primary).** Recall every document you created, patched or
   moved and every link / MOC you touched this session via the Create / Move /
   Link & Connect workflows — with their full store paths. You are the source of
   truth for *intent* — what each change means and why it landed.

2. **Revision cross-check.** For each path from step 1, `brain_revisions` (a
   small `limit` is enough) and keep the entries from today stamped with your
   id. The store stamps every revision with the token owner's id, so a revision
   under your id that you do not remember is either another of your sessions or
   something this one forgot — surface it rather than hiding it. A revision
   stamped by someone else in between means the document moved under you;
   say so.

   The store has no "everything I wrote today" query; the cross-check can only
   confirm paths you name. Say that the list is as complete as your session
   memory, not more.

Reconcile the two lists and de-duplicate.

## Closing check

Run the Integrity Check Workflow (`brain_integrity`) so the recap also confirms
the session left the brain consistent (no new broken links / orphans).

## Report format

```
## engram Session Brain Review — YYYY-MM-DD

### New notes
- acme/<repo>/projects/x/decision-y.md — one-line summary

### Updated notes
- acme/shared/areas/z.md — what changed (e.g. added "Gotcha: …" section)

### Links & MOCs
- wove [[decision-y]] into acme/shared/areas/z.md; updated acme/<repo>/README.md

### Integrity
- brain_integrity: 0 broken, 0 new orphans, weak nodes 14 → 12

### Summary
- N new · M updated · K links · MOCs: …
```

If nothing landed in the brain this session, say so in one line — do not
manufacture a report (same Brain-boundary discipline as the Capture loop).
