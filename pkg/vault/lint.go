package vault

import "sort"

// Metrics quantify how neural the network is, beyond pass/fail. The orphan
// check is the floor, not the goal: a document hanging off a single MOC passes
// it while the graph is still a folder tree wearing links.
type Metrics struct {
	ContentDocs          int            `json:"content_docs"`
	Woven                int            `json:"woven"` // has >=1 contextual (non-MOC) inbound
	Weak                 int            `json:"weak"`  // only MOC inbound — a folder spoke
	Orphans              int            `json:"orphans"`
	WovenRatio           float64        `json:"woven_ratio"`
	TotalEdges           int            `json:"total_edges"`
	HubEdgeRatio         float64        `json:"hub_edge_ratio"`
	CrossFolderLinkRatio float64        `json:"cross_folder_link_ratio"`
	IndegreeHistogram    map[string]int `json:"indegree_histogram"`
}

// LintResult is the whole report, in the shape the skill has always parsed.
type LintResult struct {
	Base          string         `json:"base"`
	Store         string         `json:"store,omitempty"`
	Scanned       int            `json:"scanned"`
	BrokenMDLinks []BrokenLink   `json:"broken_md_links"`
	DanglingWiki  []DanglingLink `json:"dangling_wikilinks"`
	Orphans       []string       `json:"orphans"`
	WeakNodes     []string       `json:"weak_nodes"`
	Metrics       *Metrics       `json:"metrics,omitempty"`
	Note          string         `json:"note,omitempty"`
}

// HasIssues is what makes the human report speak at all. Dangling wikilinks are
// deliberately not in it: they may point at a note not written yet.
func (r LintResult) HasIssues() bool {
	return len(r.BrokenMDLinks) > 0 || len(r.Orphans) > 0
}

// Lint scans a file brain and reports broken links, orphans, weak nodes and the
// density metrics.
func Lint(base, label, reportRoot string) (LintResult, error) {
	g, err := Scan(base, label, reportRoot)
	if err != nil {
		return LintResult{Base: label}, err
	}

	res := LintResult{
		Base: label, Scanned: len(g.Docs),
		BrokenMDLinks: g.BrokenMD, DanglingWiki: g.DanglingWiki,
		Orphans: []string{}, WeakNodes: []string{},
	}

	hist := map[string]int{"0": 0, "1": 0, "2": 0, "3+": 0}
	contentTotal, woven := 0, 0
	for _, d := range g.Docs {
		if Exempt(d) {
			continue
		}
		contentTotal++
		deg := g.InHub[d] + g.InContent[d]
		switch {
		case deg == 0:
			hist["0"]++
		case deg == 1:
			hist["1"]++
		case deg == 2:
			hist["2"]++
		default:
			hist["3+"]++
		}
		if g.InContent[d] > 0 {
			woven++
		}
		switch {
		case deg == 0:
			res.Orphans = append(res.Orphans, d.Rel) // nothing points here at all
		case g.InContent[d] == 0:
			res.WeakNodes = append(res.WeakNodes, d.Rel) // only MOC inbound — a lonely spoke
		}
	}

	sort.Strings(res.Orphans)
	sort.Strings(res.WeakNodes)
	sort.Slice(res.BrokenMDLinks, func(i, j int) bool {
		a, b := res.BrokenMDLinks[i], res.BrokenMDLinks[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Target < b.Target
	})
	sort.Slice(res.DanglingWiki, func(i, j int) bool {
		a, b := res.DanglingWiki[i], res.DanglingWiki[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Name < b.Name
	})
	if res.BrokenMDLinks == nil {
		res.BrokenMDLinks = []BrokenLink{}
	}
	if res.DanglingWiki == nil {
		res.DanglingWiki = []DanglingLink{}
	}

	res.Metrics = &Metrics{
		ContentDocs: contentTotal, Woven: woven,
		Weak: len(res.WeakNodes), Orphans: len(res.Orphans),
		WovenRatio:           ratio(woven, contentTotal),
		TotalEdges:           g.TotalEdges,
		HubEdgeRatio:         ratio(g.HubEdges, g.TotalEdges),
		CrossFolderLinkRatio: ratio(g.CrossFolder, g.TotalEdges),
		IndegreeHistogram:    hist,
	}
	return res, nil
}
