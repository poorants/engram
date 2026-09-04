package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The designation lives only in the user-scope settings file, because a
// machine-specific absolute path must never be committed. The side effect: a
// plain (non-engram) session opening this repo has no signal a brain exists at
// all, and answers from the code alone. Link drops a small PORTABLE pointer into
// the repo's CLAUDE.md, which every session loads. The block is
// marker-delimited, so re-running replaces it in place and Unlink strips it.

const (
	pointerBegin = "<!-- BEGIN engram:brain-pointer (managed by engram — regenerate with " +
		"`engram link`; do not hand-edit) -->"
	pointerEnd = "<!-- END engram:brain-pointer -->"
)

var pointerRe = regexp.MustCompile(`(?s)\n*<!-- BEGIN engram:brain-pointer.*?<!-- END engram:brain-pointer -->[ \t]*\n?`)

func pointerStore(url, owner, repo string) string {
	scope := owner + "/" + repo
	if owner == "" {
		scope = "<owner>/" + repo
	}
	ownerOr := owner
	if ownerOr == "" {
		ownerOr = "<owner>"
	}
	return "## This repo's knowledge lives in an engram store\n\n" +
		"Durable knowledge about this repo — design decisions, verified traps, runbooks,\n" +
		"past investigations — is **not in these files**. It is in the shared store:\n\n" +
		"- Store: `" + url + "` (people browse it here; agents hit the same ranking)\n" +
		"- This repo's scope: `" + scope + "/<area>/…` (area = projects · areas · resources · archives)\n" +
		"- Knowledge that outlives any one repo — contracts, manuals, conventions — goes to\n" +
		"  `" + ownerOr + "/shared/<area>/…` instead\n\n" +
		"**Search the store before grepping this repo.** It returns chunks with their\n" +
		"heading path, so an answer costs a fraction of what a file sweep does. Use the\n" +
		"`brain_search` MCP tool, or `engram search \"<your question>\"`.\n\n" +
		"Scope is **derived from `origin`, never chosen**: a repo the store admits writes to\n" +
		"it, one it does not is refused with 403 and keeps a local `brain/` instead. If the\n" +
		"store is unreachable the client **fails loudly** — there is no local copy to fall\n" +
		"back to, and a stale answer would be worse than none.\n"
}

func pointerFiles(brain, remote, subpath string) string {
	remoteLine := "- Brain: a designated engram file brain (see the engram settings)"
	if remote != "" {
		remoteLine = "- Brain (git remote): `" + remote + "`"
	}
	return "## This repo's knowledge lives in an engram brain\n\n" +
		"This repo's docs live in the engram **" + brain + "** brain — this environment's\n" +
		"single shared brain. Its durable design knowledge, decisions and history live\n" +
		"there, not in this repo's code.\n\n" +
		remoteLine + "\n" +
		"- This repo's notes within the brain: `" + subpath + "`\n\n" +
		"Before answering architecture, history, or \"why is it built this way\" questions,\n" +
		"read those brain docs first. The brain's local path is machine-specific, so it is\n" +
		"deliberately not hard-coded here — resolve it with `engram resolve` and read\n" +
		"the `base` field.\n"
}

// RemoteURL is the origin remote of the repo owning dir, verbatim, or "".
func RemoteURL(dir string) string {
	root := OwningGitDir(dir)
	if root == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BuildPointer renders the marker-delimited block for a resolution.
func BuildPointer(repoRoot string, r Resolution) string {
	name := filepath.Base(repoRoot)
	var body string
	if r.Source == SourceStore {
		repo := r.Repo
		if repo == "" {
			repo = name
		}
		body = pointerStore(r.Store, r.Owner, repo)
	} else {
		brain := r.Brain
		if brain == "" {
			brain = r.Label
		}
		if brain == "" {
			brain = "shared"
		}
		var rurl string
		if r.Base != "" {
			rurl = RemoteURL(r.Base)
		}
		body = pointerFiles(brain, rurl, "projects/"+name+"/")
	}
	return pointerBegin + "\n" + body + pointerEnd
}

// WritePointer creates or refreshes the block in <repo>/CLAUDE.md. Idempotent —
// an unchanged block is reported as such and the file is not rewritten, so this
// never shows up as a spurious diff.
func WritePointer(repoRoot string, r Resolution) (state, file string, err error) {
	f := filepath.Join(repoRoot, "CLAUDE.md")
	original, readErr := os.ReadFile(f)
	existed := readErr == nil

	stripped := ""
	if existed {
		stripped = strings.TrimRight(pointerRe.ReplaceAllString(string(original), "\n"), " \t\r\n")
	}
	block := BuildPointer(repoRoot, r)
	next := block + "\n"
	if stripped != "" {
		next = stripped + "\n\n" + block + "\n"
	}
	if existed && next == string(original) {
		return "unchanged", f, nil
	}
	if err := os.WriteFile(f, []byte(next), 0o644); err != nil {
		return "", f, err
	}
	if existed {
		return "updated", f, nil
	}
	return "created", f, nil
}

// RemovePointer strips the block. CLAUDE.md is deleted only when the block was
// the whole of it — anything a person wrote there stays.
func RemovePointer(repoRoot string) (state, file string, err error) {
	f := filepath.Join(repoRoot, "CLAUDE.md")
	b, readErr := os.ReadFile(f)
	if readErr != nil {
		return "absent", f, nil
	}
	original := string(b)
	stripped := pointerRe.ReplaceAllString(original, "\n")
	if stripped == original {
		return "absent", f, nil
	}
	if rest := strings.TrimSpace(stripped); rest != "" {
		if err := os.WriteFile(f, []byte(rest+"\n"), 0o644); err != nil {
			return "", f, err
		}
		return "removed", f, nil
	}
	if err := os.Remove(f); err != nil {
		return "", f, err
	}
	return "removed-empty", f, nil
}
