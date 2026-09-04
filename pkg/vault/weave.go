package vault

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// The weave finder is the muscle behind the Weave Workflow. Lint MEASURES how
// neural the network is; this FINDS the concrete moves that raise it, turning
// lonely spokes (only a MOC points at them) into nodes reachable by context.
//
// Output is advisory. The skill judges which candidates are real and weaves
// those; forcing every one of them is over-structuring, which is its own failure.

const (
	missingLinkCap = 50
	conceptCap     = 30
)

// MissingLink is a document that mentions another note's concept in prose
// without linking to it. Adding that link gives the target a contextual inbound,
// often across a folder boundary — the cheapest way to dissolve a spoke.
type MissingLink struct {
	Target        string   `json:"target"`
	TargetIsSpoke bool     `json:"target_is_spoke"`
	Anchor        string   `json:"anchor"`
	MentionedIn   []string `json:"mentioned_in"`
	Mentions      int      `json:"mentions"`
}

// ConceptCandidate is a term recurring across several documents in DIFFERENT
// folders with no note of its own. Promoting it to a shared atomic note and
// routing those documents through it creates the cross-folder connective tissue
// a star topology lacks.
type ConceptCandidate struct {
	Phrase     string   `json:"phrase"`
	DocCount   int      `json:"doc_count"`
	Folders    []string `json:"folders"`
	SampleDocs []string `json:"sample_docs"`
}

// WeaveSummary is the headline count.
type WeaveSummary struct {
	MissingLinks              int `json:"missing_links"`
	MissingLinksDissolveSpoke int `json:"missing_links_that_dissolve_a_spoke"`
	ConceptCandidates         int `json:"concept_candidates"`
}

// WeaveResult is the advisory report.
type WeaveResult struct {
	Base              string             `json:"base"`
	Scanned           int                `json:"scanned"`
	Summary           WeaveSummary       `json:"summary"`
	MissingLinks      []MissingLink      `json:"missing_links"`
	ConceptCandidates []ConceptCandidate `json:"concept_candidates"`
	Note              string             `json:"note,omitempty"`
}

// specific keeps only anchors specific enough to avoid false matches:
// multi-word, or a non-ASCII (e.g. Korean) term. A single common English word
// matches everywhere and would bury the real candidates.
func specific(phrase string) bool {
	p := strings.TrimSpace(phrase)
	if utf8.RuneCountInString(p) < 4 {
		return false
	}
	if strings.Contains(p, " ") {
		return true
	}
	for _, r := range p {
		if r > 127 {
			return true
		}
	}
	return false
}

// Weave finds the high-leverage moves that raise the network's density.
func Weave(base, label, reportRoot string) (WeaveResult, error) {
	g, err := Scan(base, label, reportRoot)
	if err != nil {
		return WeaveResult{Base: label}, err
	}
	res := WeaveResult{
		Base: label, Scanned: len(g.Docs),
		MissingLinks: []MissingLink{}, ConceptCandidates: []ConceptCandidate{},
	}

	// anchors: the phrases that ought to link to a given note, and the set of
	// terms that already have a node so they are not proposed as new concepts.
	anchors := map[*Doc][]string{}
	noded := map[string]bool{}
	lowerText := make(map[*Doc]string, len(g.Docs))

	for _, d := range g.Docs {
		lowerText[d] = strings.ToLower(d.Text)
		var set []string
		add := func(s string) {
			for _, e := range set {
				if e == s {
					return
				}
			}
			set = append(set, s)
		}
		stemPhrase := strings.TrimSpace(strings.ReplaceAll(d.Stem, "-", " "))
		if specific(stemPhrase) {
			add(stemPhrase)
			noded[strings.ToLower(stemPhrase)] = true
		}
		if m := h1Re.FindStringSubmatch(d.Text); m != nil {
			title := strings.TrimSpace(formatStrip.ReplaceAllString(m[1], ""))
			noded[strings.ToLower(title)] = true
			if specific(title) && utf8.RuneCountInString(title) <= 40 {
				add(title)
			}
		}
		if !d.IsHub {
			sort.Strings(set)
			anchors[d] = set
		}
	}

	// 1. missing links
	for _, note := range g.Docs {
		set, ok := anchors[note]
		if !ok || len(set) == 0 {
			continue
		}
		var mentionedIn []string
		for _, d := range g.Docs {
			if d == note || d.IsHub || g.Out[d][note] {
				continue
			}
			for _, a := range set {
				if strings.Contains(lowerText[d], strings.ToLower(a)) {
					mentionedIn = append(mentionedIn, d.Rel)
					break
				}
			}
		}
		if len(mentionedIn) == 0 {
			continue
		}
		sort.Strings(mentionedIn)
		total := len(mentionedIn)
		if len(mentionedIn) > 8 {
			mentionedIn = mentionedIn[:8]
		}
		res.MissingLinks = append(res.MissingLinks, MissingLink{
			Target: note.Rel, TargetIsSpoke: g.InContentSet[note] == 0,
			Anchor: set[0], MentionedIn: mentionedIn, Mentions: total,
		})
	}
	// highest leverage first: dissolves a spoke, then by how many documents
	// mention it.
	sort.SliceStable(res.MissingLinks, func(i, j int) bool {
		a, b := res.MissingLinks[i], res.MissingLinks[j]
		if a.TargetIsSpoke != b.TargetIsSpoke {
			return a.TargetIsSpoke
		}
		if a.Mentions != b.Mentions {
			return a.Mentions > b.Mentions
		}
		return a.Target < b.Target
	})

	// 2. concept candidates
	termDocs := map[string]map[*Doc]bool{}
	for _, d := range g.Docs {
		var raw []string
		for _, m := range boldRe.FindAllStringSubmatch(d.Text, -1) {
			raw = append(raw, m[1])
		}
		for _, m := range dquoteRe.FindAllStringSubmatch(d.Text, -1) {
			raw = append(raw, m[1])
		}
		for _, r := range raw {
			term := strings.TrimSpace(formatStrip.ReplaceAllString(r, ""))
			if !specific(term) || noded[strings.ToLower(term)] {
				continue
			}
			if termDocs[term] == nil {
				termDocs[term] = map[*Doc]bool{}
			}
			termDocs[term][d] = true
		}
	}
	for term, docset := range termDocs {
		if len(docset) < 3 {
			continue
		}
		folderSet := map[string]bool{}
		var samples []string
		for d := range docset {
			folderSet[d.Folder] = true
			samples = append(samples, d.Rel)
		}
		if len(folderSet) < 2 {
			continue
		}
		folders := make([]string, 0, len(folderSet))
		for f := range folderSet {
			folders = append(folders, f)
		}
		sort.Strings(folders)
		sort.Strings(samples)
		if len(samples) > 6 {
			samples = samples[:6]
		}
		res.ConceptCandidates = append(res.ConceptCandidates, ConceptCandidate{
			Phrase: term, DocCount: len(docset), Folders: folders, SampleDocs: samples,
		})
	}
	sort.SliceStable(res.ConceptCandidates, func(i, j int) bool {
		a, b := res.ConceptCandidates[i], res.ConceptCandidates[j]
		if len(a.Folders) != len(b.Folders) {
			return len(a.Folders) > len(b.Folders)
		}
		if a.DocCount != b.DocCount {
			return a.DocCount > b.DocCount
		}
		return a.Phrase < b.Phrase
	})

	spokeFixes := 0
	for _, m := range res.MissingLinks {
		if m.TargetIsSpoke {
			spokeFixes++
		}
	}
	res.Summary = WeaveSummary{
		MissingLinks:              len(res.MissingLinks),
		MissingLinksDissolveSpoke: spokeFixes,
		ConceptCandidates:         len(res.ConceptCandidates),
	}
	if len(res.MissingLinks) > missingLinkCap {
		res.MissingLinks = res.MissingLinks[:missingLinkCap]
	}
	if len(res.ConceptCandidates) > conceptCap {
		res.ConceptCandidates = res.ConceptCandidates[:conceptCap]
	}
	return res, nil
}
