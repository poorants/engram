// Package workspace answers one question for every other part of engram: given
// the directory I am standing in, is the brain the STORE, a local file brain,
// or nothing yet?
//
// # Resolution
//
// The store is asked FIRST and the answer stops there:
//
//  1. a store is designated -> SourceStore. The brain is the store. Base is only
//     the fallback vault: where a document goes when the store answers 403 for a
//     repo whose owner group it does not admit. Shared and repo-only knowledge
//     are not two places — they are the repo coordinate inside the one store,
//     decided per write.
//  2. no store, and cwd is inside the designated shared file brain -> SourceAbsorb:
//     that brain IS the base.
//  3. no store, a shared file brain is designated -> SourceShared: use it from anywhere.
//  4. no store, no designation, a local brain/ (or legacy para/, or PARA folders
//     at the root) -> SourceLocal.
//  5. nothing -> SourceNone. Ask; never invent a path.
//
// Getting order 1 wrong is the classic failure: a repo the store admits takes
// the file-vault branch merely because a not-yet-migrated para/ is still sitting
// there, and the session then hand-edits files nobody reads.
//
// Nothing here touches the network. Resolve is on the hook path, which runs on
// every prompt and every turn, so a designated store is read from settings and
// the admitted-owner list from the cache `engram store set` wrote. No cache
// means "unknown", which is not the same as "refused".
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/poorants/engram/pkg/config"
)

// Categories are the four PARA folders.
var Categories = [4]string{"projects", "areas", "resources", "archives"}

// Source is which of the five resolutions applied.
type Source string

const (
	SourceStore  Source = "store"
	SourceAbsorb Source = "absorb"
	SourceShared Source = "shared"
	SourceLocal  Source = "local"
	SourceNone   Source = "none"
)

// Resolution is the answer. The JSON names match what the skill's helpers
// printed before they were this package, so a `--json` consumer is unchanged.
type Resolution struct {
	Source   Source `json:"source"`
	Base     string `json:"base,omitempty"`
	Label    string `json:"label,omitempty"`
	Brain    string `json:"brain,omitempty"`
	Store    string `json:"store,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Repo     string `json:"repo,omitempty"`
	InScope  *bool  `json:"in_scope"`
	RepoRoot string `json:"repo_root,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

// OriginRe pulls owner/repo out of a git remote URL in either spelling
// (git@host:group/repo.git, https://host/group/repo.git).
var OriginRe = regexp.MustCompile(`[:/]([^/:]+)/([^/]+?)(?:\.git)?/*$`)

// GitRoot is the repo root owning dir: git first, then a walk up for a .git
// entry, then dir itself. The walk matters — a worktree or a submodule has a
// .git FILE rather than a directory, and a machine with no git at all still has
// to resolve rather than crash a hook.
func GitRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if out, err := exec.Command("git", "-C", abs, "rev-parse", "--show-toplevel").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			if p, err := filepath.Abs(s); err == nil {
				return p
			}
		}
	}
	if root := OwningGitDir(abs); root != "" {
		return root
	}
	return abs
}

// OwningGitDir is the nearest ancestor (or dir itself) holding a .git entry.
func OwningGitDir(dir string) string {
	cur := dir
	for {
		if _, err := os.Lstat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// OriginCoords is (owner, repo) per the origin remote, or ("", "").
//
// Scope is DERIVED, never chosen. That is what turns a habit into a boundary:
// knowledge from a repo the store does not admit cannot be filed into it by
// someone forgetting where they were.
func OriginCoords(dir string) (owner, repo string) {
	root := OwningGitDir(dir)
	if root == "" {
		return "", ""
	}
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", ""
	}
	m := OriginRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// LocalBase detects a repo-local PARA base: brain/ -> para/ -> flat root.
// Returns ("", "") when the directory holds none of them.
func LocalBase(root string) (base, label string) {
	if isDir(filepath.Join(root, "brain")) {
		return filepath.Join(root, "brain"), "brain"
	}
	if isDir(filepath.Join(root, "para")) {
		return filepath.Join(root, "para"), "para"
	}
	for _, c := range Categories {
		if isDir(filepath.Join(root, c)) {
			return root, "."
		}
	}
	return "", ""
}

// BrainBase is the PARA base inside a designated file brain's container.
//
// A designated brain points at a container (usually its own git repo); its PARA
// base nests under brain/ by default — exactly like any repo, so a dedicated
// brain repo is not a special-cased flat exception. Registering the brain/
// folder itself still resolves to itself.
func BrainBase(container string) string {
	root := Abs(container)
	if base, _ := LocalBase(root); base != "" {
		return base
	}
	return filepath.Join(root, "brain")
}

// Abs expands ~ and makes the path absolute, without requiring it to exist.
func Abs(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimLeft(strings.TrimPrefix(p, "~"), `/\`))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// Display is the stable storage form written into settings: absolute, with
// forward slashes, symlinks resolved when they can be.
func Display(p string) string {
	abs := Abs(p)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.ToSlash(abs)
}

// sameOrUnder reports whether p is container or sits beneath it, comparing
// case-insensitively on Windows the way the filesystem does.
func sameOrUnder(p, container string) bool {
	rel, err := filepath.Rel(norm(container), norm(p))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func norm(p string) string {
	abs := Abs(p)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if isCaseInsensitive {
		return strings.ToLower(abs)
	}
	return abs
}

// Resolve answers for a working directory. See the package comment for the
// order; the store wins before any file question is asked.
func Resolve(cwd string) Resolution {
	if strings.TrimSpace(cwd) == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	cwd = Abs(cwd)
	repoRoot := GitRoot(cwd)
	r := Resolution{Source: SourceNone, RepoRoot: repoRoot}

	cfg := config.Load()
	if cfg.StoreURL != "" {
		owner, repo := OriginCoords(repoRoot)
		if repo == "" {
			repo = filepath.Base(repoRoot)
		}
		base, label := LocalBase(repoRoot)
		r.Source, r.Store, r.Owner, r.Repo = SourceStore, cfg.StoreURL, owner, repo
		r.Base, r.Label = base, label
		if len(cfg.Owners) > 0 && owner != "" {
			admitted := false
			for _, o := range cfg.Owners {
				if o == owner {
					admitted = true
					break
				}
			}
			r.InScope = &admitted
			if !admitted {
				r.Warning = "owner '" + owner + "' is outside the store's admitted groups (" +
					strings.Join(cfg.Owners, ", ") + ") — this repo's documents stay in a local file brain"
				return r
			}
		}
		if base != "" && r.Warning == "" {
			r.Warning = "a local file vault `" + label + "/` is still here — the store is canonical, and " +
				"this is either waiting to be migrated or the fallback for a 403. Do not read it as the brain"
		}
		return r
	}

	if container := config.FileBrain(); container != "" {
		sb := BrainBase(container)
		lb, _ := LocalBase(repoRoot)
		inside := sameOrUnder(cwd, container) || (lb != "" && norm(lb) == norm(sb))
		r.Base, r.Label, r.Brain = sb, config.FileBrainName, config.FileBrainName
		if inside {
			// Working inside the shared brain itself: it IS the base.
			r.Source = SourceAbsorb
		} else {
			r.Source = SourceShared
		}
		return r
	}

	if base, label := LocalBase(repoRoot); base != "" {
		r.Source, r.Base, r.Label = SourceLocal, base, label
	}
	return r
}

// FallbackBase is where a scope-refused document is written: the resolved base
// when there is one. Empty means there is nowhere to put it, and the caller must
// say so rather than invent a directory.
func (r Resolution) FallbackBase() string { return r.Base }

// Admitted reports whether this repo writes to the store. The pointer is
// three-valued on purpose: nil is "the owner list was never cached", which is
// not the same as refused.
func (r Resolution) Admitted() bool { return r.InScope == nil || *r.InScope }
