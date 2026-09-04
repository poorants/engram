// Package vault holds everything that operates on a FILE brain: creating the
// PARA folders, linting the link graph, finding weave candidates, and writing a
// document the store refused.
//
// It exists because a file brain has no index and no search. In the store those
// jobs belong to the server — `engram integrity` measures a link TABLE, and a
// query answers "what links here". A directory of markdown answers neither
// without being read, so the reading lives here.
package vault

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Categories are the four PARA folders.
var Categories = [4]string{"projects", "areas", "resources", "archives"}

// HubNames are the structural files. A hub gives links but the orphan concept
// does not apply to it, and an inbound link FROM one only makes its target a
// folder spoke — it replicates the directory tree rather than weaving anything.
var HubNames = map[string]bool{
	"README.md": true, "_index.md": true, "index.md": true,
	"CLAUDE.md": true, "MEMORY.md": true,
}

// ExemptPrefixes are paths (relative to the base) left out of the orphan, weak
// and density accounting.
//
//   - areas/blog/ — a blog kept isolated for external publishing.
//   - archives/ — archived items are read-only storage, deliberately removed
//     from the active thinking network. Orphan-ness does not apply (archived
//     knowledge is set aside on purpose, not lost), and dead documents must not
//     dilute the density metrics of the live graph.
var ExemptPrefixes = []string{"areas/blog/", "archives/"}

// Go's RE2 has no lookbehind, so the Python originals' `(?<!!)` guard becomes an
// optional leading "!" that the caller checks and skips. Same language, one more
// branch — and unlike an `(^|[^!])` prefix it cannot swallow the character that
// starts an immediately adjacent second link.
var (
	wikiLinkRe   = regexp.MustCompile(`!?\[\[([^\]\n]+?)\]\]`)
	mdLinkRe     = regexp.MustCompile(`!?\[[^\]\n]*\]\(([^)\s]+)\)`)
	fenceRe      = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRe = regexp.MustCompile("`[^`\n]*`")
	h1Re         = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)
	boldRe       = regexp.MustCompile(`\*\*([^*\n]{2,40})\*\*`)
	dquoteRe     = regexp.MustCompile("[\"“]([^\"“”\n]{2,40})[\"”]")
	formatStrip  = regexp.MustCompile(`[` + "`" + `*_\[\]]`)
)

// StripCode removes fenced blocks and inline code before any link or phrase is
// read out of a document — a link shown as an example is not an edge.
func StripCode(s string) string {
	return inlineCodeRe.ReplaceAllString(fenceRe.ReplaceAllString(s, ""), "")
}

// Doc is one markdown file in the vault.
type Doc struct {
	Abs     string // absolute, cleaned
	Rel     string // relative to the reporting root, forward slashes
	RelBase string // relative to the PARA base, forward slashes
	Name    string // file name
	Stem    string // file name without its extension
	Folder  string // immediate parent, relative to the base — the "topic folder"
	Text    string // body with code stripped
	IsHub   bool
}

// Graph is the scanned vault plus its edges.
type Graph struct {
	Base  string
	Label string
	Docs  []*Doc

	byStem map[string][]*Doc
	byAbs  map[string]*Doc

	// Occurrence counts — one per link written, which is what the indegree
	// histogram is about.
	InHub     map[*Doc]int
	InContent map[*Doc]int

	// Deduplicated edges — one per (source, target) pair, which is what "does
	// anything at all point here" is about.
	Out          map[*Doc]map[*Doc]bool
	InContentSet map[*Doc]int

	TotalEdges  int
	HubEdges    int
	CrossFolder int

	BrokenMD     []BrokenLink
	DanglingWiki []DanglingLink
}

// BrokenLink is a relative markdown link to a file that is not there.
type BrokenLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// DanglingLink is a wikilink matching no file. It is a warning, never an error:
// by Obsidian convention a wikilink may point at a note not written yet.
type DanglingLink struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

func canon(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	return filepath.Clean(abs)
}

func stemOf(name string) string {
	b := path.Base(filepath.ToSlash(name))
	return strings.TrimSuffix(b, path.Ext(b))
}

func excluded(rel string) bool {
	for _, p := range strings.Split(filepath.ToSlash(rel), "/") {
		if p == "node_modules" || (len(p) > 1 && strings.HasPrefix(p, ".")) {
			return true
		}
	}
	return false
}

// Scan reads every markdown document under base and builds the link graph.
// reportRoot is what relative display paths are shown against — the repo root,
// so a report a person reads matches what they would type.
func Scan(base, label, reportRoot string) (*Graph, error) {
	base = canon(base)
	g := &Graph{
		Base: base, Label: label,
		byStem: map[string][]*Doc{}, byAbs: map[string]*Doc{},
		InHub: map[*Doc]int{}, InContent: map[*Doc]int{},
		Out: map[*Doc]map[*Doc]bool{}, InContentSet: map[*Doc]int{},
	}

	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not a reason to report zero documents;
			// skip it and keep the count honest about what was seen.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != base && excluded(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") || excluded(rel) {
			return nil
		}
		body, readErr := os.ReadFile(p)
		if readErr != nil || !utf8.Valid(body) {
			return nil
		}
		abs := canon(p)
		doc := &Doc{
			Abs: abs, Name: filepath.Base(p), Stem: stemOf(p),
			RelBase: filepath.ToSlash(rel),
			Folder:  filepath.ToSlash(filepath.Dir(rel)),
			Text:    StripCode(string(body)),
		}
		if doc.Folder == "." {
			doc.Folder = ""
		}
		doc.IsHub = HubNames[doc.Name]
		doc.Rel = displayRel(reportRoot, abs)
		g.Docs = append(g.Docs, doc)
		g.byStem[doc.Stem] = append(g.byStem[doc.Stem], doc)
		g.byAbs[abs] = doc
		return nil
	})
	if err != nil {
		return nil, err
	}

	for _, d := range g.Docs {
		g.InHub[d], g.InContent[d], g.InContentSet[d] = 0, 0, 0
		g.Out[d] = map[*Doc]bool{}
	}
	g.link()
	return g, nil
}

func displayRel(root, abs string) string {
	if root == "" {
		return filepath.ToSlash(abs)
	}
	rel, err := filepath.Rel(canon(root), abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// wikiTargets is the file(s) a [[name]] resolves to, plus whether the name
// matched anything at all.
//
// Inside a markdown table the alias pipe must be written `\|`, or it would end
// the cell. Unescaping comes first: otherwise the target keeps a trailing
// backslash, matches no file, and the real target is reported as an orphan while
// the link is counted dangling — two wrong answers from one missed character.
func wikiName(raw string) string {
	s := strings.ReplaceAll(raw, `\|`, "|")
	s, _, _ = strings.Cut(s, "|")
	s, _, _ = strings.Cut(s, "#")
	return strings.TrimSpace(s)
}

func (g *Graph) link() {
	for _, src := range g.Docs {
		for _, m := range wikiLinkRe.FindAllStringSubmatch(src.Text, -1) {
			if strings.HasPrefix(m[0], "!") { // an embed, not a link
				continue
			}
			name := wikiName(m[1])
			if name == "" {
				continue
			}
			targets := g.byStem[stemOf(name)]
			hit := false
			for _, t := range targets {
				if t == src {
					continue
				}
				hit = true
				g.record(src, t)
			}
			if !hit && len(targets) == 0 {
				g.DanglingWiki = append(g.DanglingWiki, DanglingLink{Source: src.Rel, Name: name})
			}
		}

		for _, m := range mdLinkRe.FindAllStringSubmatch(src.Text, -1) {
			if strings.HasPrefix(m[0], "!") { // an image
				continue
			}
			target := m[1]
			if hasAnyPrefix(target, "http://", "https://", "mailto:", "#", "tel:") {
				continue
			}
			clean, _, _ := strings.Cut(target, "#")
			clean, _, _ = strings.Cut(clean, "?")
			if clean == "" || !strings.HasSuffix(strings.ToLower(clean), ".md") {
				continue
			}
			resolved := canon(filepath.Join(filepath.Dir(src.Abs), filepath.FromSlash(clean)))
			if _, err := os.Stat(resolved); err != nil {
				g.BrokenMD = append(g.BrokenMD, BrokenLink{Source: src.Rel, Target: target})
				continue
			}
			// It exists but is not a scanned document (outside the base, or not
			// markdown): a real file, so not broken, and not an edge either.
			if dst, ok := g.byAbs[resolved]; ok && dst != src {
				g.record(src, dst)
			}
		}
	}
}

func (g *Graph) record(src, dst *Doc) {
	if dst == src {
		return
	}
	g.TotalEdges++
	if src.IsHub {
		g.HubEdges++
		g.InHub[dst]++
	} else {
		g.InContent[dst]++
		if !g.Out[src][dst] {
			g.InContentSet[dst]++
		}
	}
	if src.Folder != dst.Folder {
		g.CrossFolder++
	}
	g.Out[src][dst] = true
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Exempt reports whether a document is left out of the orphan/weak/density
// accounting.
func Exempt(d *Doc) bool {
	if HubNames[d.Name] {
		return true
	}
	for _, p := range ExemptPrefixes {
		if strings.HasPrefix(d.RelBase, p) {
			return true
		}
	}
	return false
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(int(float64(n)/float64(d)*1000+0.5)) / 1000
}
