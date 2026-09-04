package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/poorants/engram/pkg/vault"
	"github.com/poorants/engram/pkg/workspace"
)

// The file-brain surface: the integrity lint and the weave finder.
//
// Both apply to FILE brains only. In store mode the link graph is a table, so
// `engram integrity` measures it server-side and these two have nothing to say —
// the only file base left is the fallback vault of a repo the store refuses,
// which really is that repo's brain and is linted normally.

// lintBase resolves the PARA base to scan.
// Order: --base > the workspace resolution > local brain/para/flat > brain/.
//
// In absorb mode this may target a brain OUTSIDE the current repo (the
// designated file brain); its links all live inside that brain, so they still
// resolve.
func lintBase(explicit, cwd string) (base, label string) {
	if strings.TrimSpace(explicit) != "" {
		return workspace.Abs(filepath.Join(cwd, explicit)), explicit
	}
	if r := workspace.Resolve(cwd); r.Base != "" {
		label := r.Label
		if label == "" {
			label = "brain"
		}
		return r.Base, label
	}
	if b, l := workspace.LocalBase(cwd); b != "" {
		return b, l
	}
	// A fresh repo: default to brain/, and say nothing when it is not there yet.
	return filepath.Join(cwd, "brain"), "brain"
}

// storeIsCanonical reports the resolution when the store — not a file vault — is
// this repo's brain. A repo the store refuses (Admitted false) is excluded: there
// the local vault really is the brain.
func storeIsCanonical(cwd string) *workspace.Resolution {
	r := workspace.Resolve(cwd)
	if r.Source == workspace.SourceStore && r.Admitted() {
		return &r
	}
	return nil
}

func here() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// cmdLint reports broken links, orphans, weak nodes and the density metrics of a
// file brain. Exit is always 0 — a wikilink may point at a note not written yet,
// so problems are reported and never block work.
func cmdLint(args []string) int {
	fs := flag.NewFlagSet("lint", flag.ContinueOnError)
	baseArg := fs.String("base", "", "force the PARA base (relative to the current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	showAll := fs.Bool("all", false, "print the summary even when the graph is clean")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cwd := here()

	if *baseArg == "" {
		if st := storeIsCanonical(cwd); st != nil {
			if *asJSON {
				return emit(map[string]any{
					"base": nil, "store": st.Store, "scanned": 0,
					"note": "store mode — the integrity check is `engram integrity`",
				})
			}
			if *showAll {
				msg := fmt.Sprintf("[engram] the store (%s) is canonical — integrity is "+
					"`engram integrity`, not this file lint.", st.Store)
				if st.Base != "" {
					msg += fmt.Sprintf(" (the local `%s/` is a fallback vault, not the brain, "+
						"so it is not linted)", st.Label)
				}
				out("%s\n", msg)
			}
			return exitOK
		}
	}

	base, label := lintBase(*baseArg, cwd)
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		if *asJSON {
			return emit(vault.LintResult{
				Base: label, Scanned: 0, BrokenMDLinks: []vault.BrokenLink{},
				DanglingWiki: []vault.DanglingLink{}, Orphans: []string{}, WeakNodes: []string{},
				Note: "base directory not found",
			})
		}
		if *showAll {
			out("[engram] PARA base ('%s') not found — skipping check.\n", label)
		}
		return exitOK
	}

	res, err := vault.Lint(base, label, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	if *asJSON {
		return emit(res)
	}
	if !res.HasIssues() && !*showAll {
		return exitOK
	}

	where := label + "/"
	if label == "." {
		where = "root"
	}
	out("[engram] integrity check (%d docs / %s)\n", res.Scanned, where)
	if n := len(res.BrokenMDLinks); n > 0 {
		out("  [X] %d broken link(s):\n", n)
		for _, b := range res.BrokenMDLinks {
			out("     - %s -> %s\n", b.Source, b.Target)
		}
	}
	if n := len(res.Orphans); n > 0 {
		out("  [orphan] %d orphan doc(s) (no inbound link):\n", n)
		for _, o := range res.Orphans {
			out("     - %s\n", o)
		}
	}
	if n := len(res.DanglingWiki); n > 0 {
		out("  [!] %d unresolved wikilink(s) (may be a note not created yet):\n", n)
		for _, d := range res.DanglingWiki {
			out("     - %s -> [[%s]]\n", d.Source, d.Name)
		}
	}
	if m := res.Metrics; m != nil && (*showAll || len(res.WeakNodes) > 0) {
		out("  [density] woven %d/%d (%.0f%%) | weak/spoke %d | cross-folder links %.0f%%\n",
			m.Woven, m.ContentDocs, m.WovenRatio*100, m.Weak, m.CrossFolderLinkRatio*100)
	}
	if *showAll && len(res.WeakNodes) > 0 {
		out("  [weak] %d lonely spoke(s) (only MOC-inbound - weave a contextual link):\n", len(res.WeakNodes))
		for _, w := range res.WeakNodes {
			out("     - %s\n", w)
		}
	}
	if !res.HasIssues() {
		out("  [ok] no broken links / orphans.\n")
	}
	return exitOK
}

// cmdWeave finds the concrete moves that raise the network's density. Advisory
// only — it never touches a file. The skill judges which candidates are real;
// applying all of them blindly is over-structuring.
func cmdWeave(args []string) int {
	fs := flag.NewFlagSet("weave", flag.ContinueOnError)
	baseArg := fs.String("base", "", "force the PARA base (relative to the current directory)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cwd := here()

	if *baseArg == "" {
		if st := storeIsCanonical(cwd); st != nil {
			note := "store mode — weave candidates are a file-brain measure"
			if *asJSON {
				return emit(map[string]any{"base": nil, "store": st.Store, "scanned": 0, "note": note})
			}
			out("[engram] %s. The store's graph is measured by `engram integrity`.\n", note)
			return exitOK
		}
	}

	base, label := lintBase(*baseArg, cwd)
	if st, err := os.Stat(base); err != nil || !st.IsDir() {
		if *asJSON {
			return emit(vault.WeaveResult{
				Base: label, MissingLinks: []vault.MissingLink{},
				ConceptCandidates: []vault.ConceptCandidate{}, Note: "base directory not found",
			})
		}
		out("[engram] base '%s' not found.\n", label)
		return exitOK
	}

	res, err := vault.Weave(base, label, cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	if *asJSON {
		return emit(res)
	}

	where := label + "/"
	if label == "." {
		where = "root"
	}
	out("[engram] weave candidates (%d docs / %s)\n", res.Scanned, where)
	out("  missing links: %d (%d dissolve a spoke) | concept candidates: %d\n",
		res.Summary.MissingLinks, res.Summary.MissingLinksDissolveSpoke, res.Summary.ConceptCandidates)
	if len(res.MissingLinks) > 0 {
		out("  -- top missing links (add a contextual link target<-source) --\n")
		for i, m := range res.MissingLinks {
			if i >= 15 {
				break
			}
			tag := "[      ]"
			if m.TargetIsSpoke {
				tag = "[spoke]"
			}
			eg := ""
			if len(m.MentionedIn) > 0 {
				eg = fmt.Sprintf(" (e.g. %s)", m.MentionedIn[0])
			}
			out("    %s %s  <- mentioned in %d doc(s)%s\n", tag, m.Target, m.Mentions, eg)
		}
	}
	if len(res.ConceptCandidates) > 0 {
		out("  -- top shared-concept candidates (promote to a node) --\n")
		for i, c := range res.ConceptCandidates {
			if i >= 10 {
				break
			}
			out("    \"%s\" - %d docs across %d folders (%s)\n",
				c.Phrase, c.DocCount, len(c.Folders), strings.Join(c.Folders, ", "))
		}
	}
	if len(res.MissingLinks) == 0 && len(res.ConceptCandidates) == 0 {
		out("  [ok] no obvious weave candidates found.\n")
	}
	return exitOK
}
