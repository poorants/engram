# Capture loop — keeping the brain fed

A brain is only as good as what reaches it. Durable thinking that stays in the
chat and never lands in the store is lost.
This reference details the three-layer capture loop summarized in SKILL.md.

The hooks are **triggers and backstops, not the engine**. A shell hook cannot
judge what is worth keeping or write it well — that is the model's job. The hooks
only fire a reflection at the right moment.

## 1. Capture-as-you-go (primary, model-driven)

While working, when a durable piece of knowledge crystallizes, record it *then* —
do not wait for session end. Worth capturing:

- a newly defined concept or piece of terminology,
- a design / architecture decision (and the why),
- a good idea to apply to this project,
- a research conclusion or comparison outcome,
- an important gotcha, constraint, or non-obvious failure mode.

Follow the Create Workflow: choose the PARA category, place the note, weave
contextual links into prose, then `brain_integrity`. That is the whole loop — a
hub MOC line is navigation, not discoverability (search is), so a capture in the
middle of other work does not stop to curate MOCs; the next Link & Connect pass
does that.

Stay selective — this is the Brain boundary and no-over-structuring discipline
applied continuously:

- Skip trivia, status chatter, and one-off conversational turns.
- Skip anything the code, git history, or existing docs already capture.
- Don't force links; link only where there is real relevance.

## 1.5 Recall trigger — `SessionStart` hook

The loop has two halves, and the read half has a moment too: the opening one.
"Where did we get to", "what did we decide and why", "how is this usually done
here" cluster in a session's first turns — exactly when the working tree is the
tempting place to look. So when a session opens, the hook injects one rule:
**a knowledge question goes to `brain_search` before grep.**

The injection is a **condition, not a command**. It performs no search and does
not call the store (no listing, no count: a network call on the path every
session walks is paid by every session, and on a machine that cannot reach the
store it is paid as a timeout). The time to search is when there is a question.

## 2. Wrap-up trigger — `UserPromptSubmit` hook

When the user's message looks like an end-of-session sign-off, the bundled hook
injects a reflect-and-save instruction so the session's final ideas are captured
before the user leaves. This is the primary automatic trigger because it fires at
the natural closing moment.

Default wrap-up phrases (case-insensitive substring; Korean + English): 고생했 ·
수고했 · 오늘은 여기까지 · 마무리하 · 마치자 · 끝내자 · 이만 · 푹 쉬 · wrap up ·
that's all · done for today · good night · good work · … (the list lives in `cmd/engram/hook.go`).

Override the list with `ENGRAM_CAPTURE_PHRASES` (comma-separated). When the
injected instruction appears, do the reflection as part of that turn's reply.

## 3. Backstop — `Stop` hook

`Stop` fires after every assistant turn, so the backstop is heavily gated:

- loop guard via `stop_hook_active` (never re-fires inside a continuation),
- per-session cooldown (default 30 min) via a temp marker file; the first
  encounter only starts the clock, so the first nudge lands later in the session.

It exists for long sessions where the user never types a wrap-up phrase. If
nothing is worth keeping, acknowledge in one line and finish — never create
filler.

## Distribution & configuration

These hooks ship with the plugin and need no per-machine setup:

- `.claude-plugin/marketplace.json` registers the command `engram hook` on
  `SessionStart`, `UserPromptSubmit` and `Stop`. One command for all three:
  Claude Code puts `hook_event_name` in the payload, so the binary branches on it.
- **It is the binary, not a script.** The hook used to be `python3 …
  brain_reflect.py`, which meant the capture loop was silently dead on every
  Windows machine: `python3` is not a command there even where Python is
  installed — the App Execution Alias of that name opens the Microsoft Store and
  exits. A hook that needs an interpreter is a hook that does not run.
- They speak only in a repo with a git `origin` (optionally narrowed to some
  owners with `ENGRAM_CAPTURE_OWNERS`) and are silent everywhere else — a
  directory that is not a repo, an owner outside the list. They read only the
  git origin: no settings file, no token, no network. They never fail a session:
  every path exits 0, including a panic, garbage on stdin and an unknown event.
- Text is UTF-8 end to end, which is now a property of the runtime rather than
  something each script has to remember — so a Korean wrap-up phrase survives a
  cp949 console.

The plugin therefore needs the `engram` binary on `PATH` —
`/mcp__engram__setup` installs it (`install.sh` / `install.ps1`; re-running is
the upgrade). A 0.11-or-earlier binary still answers, but decides from the old
local store settings and points at CLI verbs that no longer exist; one older
than v0.4.0 does not know the `hook` verb at all.

Env knobs:

| Variable | Default | Effect |
|---|---|---|
| `ENGRAM_CAPTURE_DISABLE` | unset | `1` disables all three hooks |
| `ENGRAM_CAPTURE_COOLDOWN_MIN` | `30` | minutes between `Stop`-backstop nudges |
| `ENGRAM_CAPTURE_PHRASES` | built-in list | comma-separated wrap-up phrases |
| `ENGRAM_CAPTURE_OWNERS` | unset (every owner) | comma-separated origin owners the hooks speak for, case-insensitive |

When a reflection (from either hook) decides something is worth keeping, run it
through the Create Workflow and close with the Integrity Check Workflow.

To **read back** what a session fed the brain (a wrap-up recap of new/changed
notes), use the command-triggered Session Update Review Workflow — see
[session-review.md](session-review.md). Capture writes; that workflow reports.
