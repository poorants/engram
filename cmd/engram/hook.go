package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/poorants/engram/pkg/workspace"
)

// The capture-loop hook: wrap-up detection on UserPromptSubmit, plus a throttled
// Stop backstop. It nudges the model to look back over the session and capture
// durable new concepts, decisions and traps into the brain.
//
// **The hooks are not the engine.** Three layers, and only the first one does
// the judging:
//
//   - Engine (primary): the model saves things as they crystallize, during the
//     work. A hook cannot judge what is worth keeping; it can only fire the
//     reflection at the right moment.
//   - Primary trigger — UserPromptSubmit: when the user's message looks like the
//     end of a session, inject a reflect-and-save instruction. It fires at the
//     natural closing moment, before the last ideas are gone.
//   - Backstop — Stop: a time-throttled nudge for long sessions that never got a
//     sign-off. Stop fires every turn, so it is heavily gated (loop guard plus a
//     cooldown).
//
// This lives in the binary rather than in a script because a hook that needs an
// interpreter is a hook that does not run. On Windows `python3` is not a command
// even where Python is installed — the App Execution Alias of that name opens
// the Microsoft Store and exits — so the capture loop was silently dead on every
// Windows machine. The binary is already this plugin's declared dependency.
//
// Knobs:
//
//	ENGRAM_CAPTURE_DISABLE=1        turn the hooks off entirely
//	ENGRAM_CAPTURE_COOLDOWN_MIN=30  minutes between Stop-backstop nudges
//	ENGRAM_CAPTURE_PHRASES="a,b,c"  override the wrap-up phrases (comma-separated,
//	                                case-insensitive substring match)

// wrapUpPhrases are matched as case-insensitive substrings, so short forms are
// deliberately avoided — "done" would fire on "I'm done reading that file".
var wrapUpPhrases = []string{
	// English
	"wrap up", "wrap it up", "that's all", "thats all", "that's it", "thats it",
	"done for today", "call it a day", "good night", "goodnight", "see you",
	"good work", "well done", "great work", "nice work", "thanks for the help",
	"let's stop", "lets stop", "stop here", "that will do",
	// Korean
	"고생했", "수고했", "수고하", "오늘은 여기까지", "여기까지 하", "여기까지만",
	"마무리하", "마무리 하", "마치자", "끝내자", "오늘 그만", "그만하자",
	"푹 쉬", "내일 보", "다음에 보", "이만",
}

// hookInput is the payload Claude Code sends on stdin.
type hookInput struct {
	HookEventName  string `json:"hook_event_name"`
	Prompt         string `json:"prompt"`
	CWD            string `json:"cwd"`
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// brainInfo is how the instruction names the brain being fed, and whether it is
// the store (which changes what the model is told to do).
type brainInfo struct {
	display string
	store   bool
}

// describeBrain answers what feeds this directory, or nil when nothing does.
//
// **The store is asked first.** Resolve answers SourceStore whenever one is
// designated, so a not-yet-migrated local para/ cannot hijack an admitted repo
// into the file-vault instruction — which is exactly how a session ends up
// hand-editing a hub MOC nobody reads.
func describeBrain(cwd string) *brainInfo {
	r := workspace.Resolve(cwd)
	switch r.Source {
	case workspace.SourceStore:
		if !r.Admitted() {
			// Admitted nowhere: this repo's knowledge lives in files.
			if r.Base != "" {
				return &brainInfo{display: "local file brain (" + filepath.ToSlash(r.Base) + ")"}
			}
			return nil
		}
		owner, repo := r.Owner, r.Repo
		if owner == "" {
			owner = "?"
		}
		if repo == "" {
			repo = "?"
		}
		return &brainInfo{display: "the shared store " + r.Store + " (" + owner + "/" + repo + ")", store: true}
	case workspace.SourceAbsorb, workspace.SourceShared, workspace.SourceLocal:
		if r.Base == "" {
			return nil
		}
		return &brainInfo{display: "the file brain at " + filepath.ToSlash(r.Base)}
	}
	return nil
}

func captureInstruction(info *brainInfo, wrapup bool) string {
	head := "[engram — brain reflection] "
	if wrapup {
		head = "[engram — session wrap-up detected] "
	}
	var record string
	if info.store {
		record = "If there is, record it through the engram skill into the right PARA area — " +
			"**via the store** (the brain_put MCP tool, or `engram put`; a note is required), " +
			"never by writing a file. Weave links into the prose where the idea comes up, " +
			"and check brain_integrity afterwards. "
	} else {
		// File-brain wording. MOC updating survives here because a file vault
		// has no index and no search — the folder README is its only discovery
		// mechanism. In the store, search fills that role, which is why the
		// branch above has no MOC step. That is design, not an omission.
		record = "If there is, record it through the engram skill into the right PARA folder, " +
			"weave links into the prose, update that folder's MOC (README.md), and run `engram lint` " +
			"to check integrity. "
	}
	body := "This repo is connected to an engram brain — " + info.display + ". Look back over this " +
		"session and judge whether anything worth keeping came out of it: a concept that got " +
		"pinned down, a design decision, a research conclusion, a trap or constraint someone " +
		"will hit again. " + record +
		"Be selective: skip small talk, progress checks, anything already captured by the code " +
		"or its history, and anything already documented. Over-documenting and forced links are " +
		"both failures. If there is nothing worth keeping, say so in one line and move on."
	if wrapup {
		body += " The user is closing the session, so do this before you reply."
	}
	return head + body
}

func emitHook(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = os.Stdout.Write(b)
}

// cooldownPassed is the Stop backstop's throttle. The first encounter starts the
// clock and stays silent, so a short session is never interrupted at all.
func cooldownPassed(sessionID string, cooldown time.Duration) bool {
	if sessionID == "" {
		sessionID = "nosession"
	}
	marker := filepath.Join(os.TempDir(), "engram-capture-"+sanitizeID(sessionID)+".marker")
	now := time.Now()
	st, err := os.Stat(marker)
	if err != nil {
		_ = os.WriteFile(marker, []byte(strconv.FormatInt(now.Unix(), 10)), 0o600)
		return false
	}
	if now.Sub(st.ModTime()) < cooldown {
		return false
	}
	if err := os.Chtimes(marker, now, now); err != nil {
		return false
	}
	return true
}

// sanitizeID keeps a session id from escaping the temp directory or naming a
// file the OS refuses. Ids are opaque and this never has to be reversible.
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('.')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		out = "nosession"
	}
	return out
}

// cmdHook never fails a session: every path returns exitOK. A hook that exits
// non-zero puts an error in front of the user on every single prompt, which is a
// worse outcome than a missed reflection.
func cmdHook(args []string) int {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Unknown flags are ignored on purpose: a future plugin release may pass one
	// this binary predates, and refusing it would break every prompt.
	_ = fs.Parse(args)

	defer func() { _ = recover() }()

	if os.Getenv("ENGRAM_CAPTURE_DISABLE") == "1" {
		return exitOK
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return exitOK
	}
	var in hookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return exitOK
	}

	cwd := in.CWD
	if strings.TrimSpace(cwd) == "" {
		cwd, _ = os.Getwd()
	}
	info := describeBrain(cwd)
	if info == nil {
		return exitOK
	}

	switch in.HookEventName {
	case "UserPromptSubmit":
		prompt := strings.ToLower(in.Prompt)
		phrases := wrapUpPhrases
		if raw := os.Getenv("ENGRAM_CAPTURE_PHRASES"); strings.TrimSpace(raw) != "" {
			phrases = nil
			for _, p := range strings.Split(raw, ",") {
				if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
					phrases = append(phrases, p)
				}
			}
		}
		for _, p := range phrases {
			if strings.Contains(prompt, p) {
				emitHook(map[string]any{"hookSpecificOutput": map[string]any{
					"hookEventName":     "UserPromptSubmit",
					"additionalContext": captureInstruction(info, true),
				}})
				break
			}
		}
	case "Stop":
		if in.StopHookActive { // never re-fire inside a continuation
			return exitOK
		}
		cooldown := 30 * time.Minute
		if v := strings.TrimSpace(os.Getenv("ENGRAM_CAPTURE_COOLDOWN_MIN")); v != "" {
			if m, err := strconv.ParseFloat(v, 64); err == nil && m >= 0 {
				cooldown = time.Duration(m * float64(time.Minute))
			}
		}
		if !cooldownPassed(in.SessionID, cooldown) {
			return exitOK
		}
		emitHook(map[string]any{"decision": "block", "reason": captureInstruction(info, false)})
	}
	return exitOK
}
