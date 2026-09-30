package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runHook feeds a payload through cmdHook and returns (stdout, exit code).
func runHook(t *testing.T, payload string) (string, int) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = inW.WriteString(payload)
		_ = inW.Close()
	}()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	code := cmdHook(nil)
	os.Stdin, os.Stdout = oldIn, oldOut
	_ = outW.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := outR.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String(), code
}

// repoWithOrigin makes a scratch repo whose origin is github.com/<owner>/widgets
// and clears every capture knob, so neither the developer's own checkout nor
// their shell profile decides a test.
func repoWithOrigin(t *testing.T, owner string) string {
	t.Helper()
	clearKnobs(t)
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", "https://github.com/" + owner + "/widgets.git"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func clearKnobs(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DISABLE", "PHRASES", "OWNERS", "COOLDOWN_MIN"} {
		t.Setenv("ENGRAM_CAPTURE_"+k, "")
	}
}

// storeSettings is the ordinary case: a repo with an origin, no owner filter.
func storeSettings(t *testing.T) string {
	t.Helper()
	return repoWithOrigin(t, "acme")
}

func payload(t *testing.T, v map[string]any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The primary trigger. A sign-off is the last moment the session's ideas are
// still in reach, so the reflection has to fire before the reply.
func TestWrapUpPhraseInjectsTheInstruction(t *testing.T) {
	repo := storeSettings(t)
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "오늘 고생했어, 이만 마무리하자",
		"cwd":             repo,
	}))
	if code != exitOK {
		t.Fatalf("exit = %d — a hook must never fail a session", code)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not the hook protocol: %v (%q)", err, stdout)
	}
	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Errorf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "wrap-up detected") {
		t.Errorf("context = %q", out.HookSpecificOutput.AdditionalContext)
	}
	// A store-backed repo must be told to write THROUGH the store. Handing it
	// the file-vault instruction is how a session ends up editing a MOC nobody
	// reads while the store never hears about the work.
	if !strings.Contains(out.HookSpecificOutput.AdditionalContext, "via the store") {
		t.Errorf("a store-backed repo needs the store instruction: %q", out.HookSpecificOutput.AdditionalContext)
	}
}

// Non-ASCII on stdin and stdout is the case that broke the Python hook on a
// Korean Windows console, so it is the case worth pinning: the payload decodes
// and the Korean phrase list still matches.
func TestOrdinaryPromptStaysSilent(t *testing.T) {
	repo := storeSettings(t)
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "이 파일 읽고 버그 찾아줘",
		"cwd":             repo,
	}))
	if code != exitOK || stdout != "" {
		t.Fatalf("an ordinary prompt must produce nothing; got %q (exit %d)", stdout, code)
	}
}

// Stop fires every single turn. The first encounter starts the clock and stays
// quiet, or a short session is interrupted for nothing.
func TestStopBackstopIsSilentUntilTheCooldownPasses(t *testing.T) {
	repo := storeSettings(t)
	t.Setenv("ENGRAM_CAPTURE_COOLDOWN_MIN", "30")
	p := payload(t, map[string]any{
		"hook_event_name": "Stop", "cwd": repo, "session_id": "hooktest-cooldown",
	})
	marker := filepath.Join(os.TempDir(), "engram-capture-hooktest-cooldown.marker")
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	if stdout, _ := runHook(t, p); stdout != "" {
		t.Fatalf("the first Stop starts the clock and says nothing; got %q", stdout)
	}
	if stdout, _ := runHook(t, p); stdout != "" {
		t.Fatalf("inside the cooldown it stays quiet; got %q", stdout)
	}

	// Zero cooldown: the backstop is due, and it blocks with the reflection.
	t.Setenv("ENGRAM_CAPTURE_COOLDOWN_MIN", "0")
	stdout, code := runHook(t, p)
	if code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	var out struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not the Stop protocol: %v (%q)", err, stdout)
	}
	if out.Decision != "block" || !strings.Contains(out.Reason, "brain reflection") {
		t.Errorf("decision=%q reason=%q", out.Decision, out.Reason)
	}
}

// stop_hook_active means we are already inside a continuation this hook caused.
// Firing again is an infinite loop, not a backstop.
func TestStopHookActiveNeverRefires(t *testing.T) {
	repo := storeSettings(t)
	t.Setenv("ENGRAM_CAPTURE_COOLDOWN_MIN", "0")
	stdout, _ := runHook(t, payload(t, map[string]any{
		"hook_event_name": "Stop", "cwd": repo,
		"session_id": "hooktest-loop", "stop_hook_active": true,
	}))
	if stdout != "" {
		t.Fatalf("the loop guard must win; got %q", stdout)
	}
}

// A directory that is no repo with an origin has no coordinate to save under.
func TestNoBrainMeansNoNudge(t *testing.T) {
	clearKnobs(t)
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"prompt":          "thanks, that's it for today",
		"cwd":             t.TempDir(),
	}))
	if stdout != "" || code != exitOK {
		t.Fatalf("no brain, no nudge; got %q (exit %d)", stdout, code)
	}
}

// Garbage on stdin, a truncated payload, an unknown event: all of it exits 0.
// A hook that exits non-zero puts an error in front of the user on every single
// prompt, which is worse than a missed reflection.
func TestMalformedInputNeverFailsTheSession(t *testing.T) {
	storeSettings(t)
	for _, in := range []string{"", "not json", "{", `{"hook_event_name":"SomethingElse"}`, "null"} {
		if _, code := runHook(t, in); code != exitOK {
			t.Errorf("input %q exited %d, want 0", in, code)
		}
	}
}

// The off switch has to work without touching settings — it is what someone
// reaches for when a hook is misbehaving mid-session.
func TestCaptureDisableSilencesEverything(t *testing.T) {
	repo := storeSettings(t)
	t.Setenv("ENGRAM_CAPTURE_DISABLE", "1")
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit", "prompt": "wrap up", "cwd": repo,
	}))
	if stdout != "" || code != exitOK {
		t.Fatalf("disabled means silent; got %q (exit %d)", stdout, code)
	}
}

// A session id lands in a temp-file name. It is opaque, so nothing is lost by
// scrubbing it — and a slash in one would otherwise write outside the temp dir.
func TestSessionIDCannotEscapeTheTempDirectory(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abc-123_XY", "abc-123_XY"},
		{"../../etc/passwd", "......etc.passwd"},
		{"", "nosession"},
	} {
		if got := sanitizeID(tc.in); got != tc.want {
			t.Errorf("sanitizeID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// SessionStart is the read half of the loop. It must fire unconditionally —
// there is no prompt to match yet, and that is the point — and it must stay
// offline: the session-start path is walked by every session, so a store call
// here is paid by every session and, on a machine that cannot reach the store,
// paid as a timeout. The test pins the shape and the wording that carries the
// condition, because an injection that reads as "search now" would spend a call
// on every session that had no question.
func TestSessionStartInjectsTheRecallRule(t *testing.T) {
	repo := storeSettings(t)
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "SessionStart",
		"cwd":             repo,
	}))
	if code != exitOK {
		t.Fatalf("exit = %d — a hook must never fail a session", code)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("stdout is not the hook protocol: %v (%q)", err, stdout)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q", out.HookSpecificOutput.HookEventName)
	}
	ctx := out.HookSpecificOutput.AdditionalContext
	for _, want := range []string{"brain available", "brain_search", "BEFORE grepping"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("recall rule is missing %q: %q", want, ctx)
		}
	}
	// The rule is a condition, not an order. If this sentence goes, a session
	// with nothing to ask still opens with a search.
	if !strings.Contains(ctx, "background, not an instruction to act on now") {
		t.Errorf("the injection must frame itself as background: %q", ctx)
	}
}

// Outside a repo SessionStart says nothing either: "this repo is connected"
// would name a repo that does not exist, next to a scope invented from the
// folder's name.
func TestSessionStartIsSilentWithoutABrain(t *testing.T) {
	clearKnobs(t)
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "SessionStart",
		"cwd":             t.TempDir(),
	}))
	if code != exitOK || stdout != "" {
		t.Fatalf("no brain must mean no injection; got %q (exit %d)", stdout, code)
	}
}

// ENGRAM_CAPTURE_DISABLE=1 is documented as turning the hooks off entirely.
// SessionStart is a hook, so "entirely" has to include it.
func TestCaptureDisableAlsoSilencesSessionStart(t *testing.T) {
	repo := storeSettings(t)
	t.Setenv("ENGRAM_CAPTURE_DISABLE", "1")
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "SessionStart",
		"cwd":             repo,
	}))
	if code != exitOK || stdout != "" {
		t.Fatalf("disable must silence SessionStart too; got %q (exit %d)", stdout, code)
	}
}

// ENGRAM_CAPTURE_OWNERS narrows the hooks to the owners a store admits. A
// store refuses a save for any other owner, so a reflection there would only
// produce a refusal.
func TestOwnerFilterSilencesOtherOwners(t *testing.T) {
	repo := repoWithOrigin(t, "Acme")
	t.Setenv("ENGRAM_CAPTURE_OWNERS", "other, someone")
	stdout, code := runHook(t, payload(t, map[string]any{
		"hook_event_name": "SessionStart", "cwd": repo,
	}))
	if code != exitOK || stdout != "" {
		t.Fatalf("an owner outside the filter must be silent; got %q (exit %d)", stdout, code)
	}

	// Case-insensitive: the origin says Acme, the filter says acme.
	t.Setenv("ENGRAM_CAPTURE_OWNERS", "other, acme")
	stdout, _ = runHook(t, payload(t, map[string]any{
		"hook_event_name": "SessionStart", "cwd": repo,
	}))
	if !strings.Contains(stdout, "Acme/widgets/") {
		t.Fatalf("an owner on the filter must be told its coordinate; got %q", stdout)
	}
}

// The coordinate comes from the origin in its common spellings.
func TestParseOrigin(t *testing.T) {
	for _, tc := range []struct{ in, owner, repo string }{
		{"https://github.com/acme/widgets.git", "acme", "widgets"},
		{"https://github.com/acme/widgets", "acme", "widgets"},
		{"git@github.com:acme/widgets.git", "acme", "widgets"},
		{"ssh://git@host:2222/acme/widgets.git/", "acme", "widgets"},
		{"not a url", "", ""},
	} {
		owner, repo := parseOrigin(tc.in)
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("parseOrigin(%q) = %q, %q; want %q, %q", tc.in, owner, repo, tc.owner, tc.repo)
		}
	}
}

// The client no longer knows where the store is, so the injection must not
// pretend to: it names the MCP registration, never an address or a CLI verb.
func TestInjectionNamesTheMCPServerNotAnAddress(t *testing.T) {
	repo := storeSettings(t)
	stdout, _ := runHook(t, payload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit", "prompt": "wrap up", "cwd": repo,
	}))
	if !strings.Contains(stdout, "engram MCP") {
		t.Errorf("the injection must name the engram MCP server: %q", stdout)
	}
	for _, bad := range []string{"http://", "https://", "engram put", "engram search", "engram lint"} {
		if strings.Contains(stdout, bad) {
			t.Errorf("the injection mentions %q, which the client no longer has: %q", bad, stdout)
		}
	}
}
