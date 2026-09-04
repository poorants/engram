# Capture loop — keeping the brain fed

A brain is only as good as what reaches it. Durable thinking that stays in the
chat and never lands under the PARA base (`brain/`, or legacy `para/`) is lost.
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
contextual links into prose. **In store mode that is the whole loop** — a hub
`README.md` line is not discoverability there (search is), so `brain_integrity`
replaces the MOC step. Only a file vault, which has no index, still needs the
folder MOC updated.

Stay selective — this is the Brain boundary and no-over-structuring discipline
applied continuously:

- Skip trivia, status chatter, and one-off conversational turns.
- Skip anything the code, git history, or existing docs already capture.
- Don't force links; link only where there is real relevance.

## 2. Wrap-up trigger — `UserPromptSubmit` hook

When the user's message looks like an end-of-session sign-off, the bundled hook
injects a reflect-and-save instruction so the session's final ideas are captured
before the user leaves. This is the primary automatic trigger because it fires at
the natural closing moment.

Default wrap-up phrases (case-insensitive substring; Korean + English): 고생했 ·
수고했 · 오늘은 여기까지 · 마무리하 · 마치자 · 끝내자 · 이만 · 푹 쉬 · wrap up ·
that's all · done for today · good night · good work · … (see `engram hook`).

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

- `.claude-plugin/marketplace.json` registers the command `engram hook` on both
  `UserPromptSubmit` and `Stop`. One command for both: Claude Code puts
  `hook_event_name` in the payload, so the binary branches on it.
- **It is the binary, not a script.** The hook used to be `python3 …
  brain_reflect.py`, which meant the capture loop was silently dead on every
  Windows machine: `python3` is not a command there even where Python is
  installed — the App Execution Alias of that name opens the Microsoft Store and
  exits. A hook that needs an interpreter is a hook that does not run.
- They act only where a brain resolves (a designated store, a designated file
  brain, or a local `brain/`), and never fail a session: every path exits 0,
  including a panic, garbage on stdin and an unknown event.
- Text is UTF-8 end to end, which is now a property of the runtime rather than
  something each script has to remember — so a Korean wrap-up phrase survives a
  cp949 console.

The plugin therefore needs `engram` **v0.3.0 or newer** on `PATH`. An older
binary does not know the `hook` verb and will print an unknown-command error on
every prompt until it is updated (`install.sh` / `install.ps1`).

Env knobs:

| Variable | Default | Effect |
|---|---|---|
| `ENGRAM_CAPTURE_DISABLE` | unset | `1` disables both reflect hooks |
| `ENGRAM_CAPTURE_COOLDOWN_MIN` | `30` | minutes between `Stop`-backstop nudges |
| `ENGRAM_CAPTURE_PHRASES` | built-in list | comma-separated wrap-up phrases |

When a reflection (from either hook) decides something is worth keeping, run it
through the Create Workflow and close with the Integrity Lint Workflow.

To **read back** what a session fed the brain (a wrap-up recap of new/changed
notes), use the command-triggered Session Update Review Workflow — see
[session-review.md](session-review.md). Capture writes; that workflow reports.
