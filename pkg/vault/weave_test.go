package vault

import "testing"

// The whole point of the weave finder: a document that TALKS about another note
// without linking to it. That pair is the cheapest link in the graph, and the
// one nobody notices by reading.
func TestMentionWithoutALinkIsACandidate(t *testing.T) {
	base := write(t, map[string]string{
		"resources/token-rotation.md": "# token rotation\n",
		"areas/runbook.md":            "# runbook\nWe follow the token rotation procedure here.\n",
		"areas/linked.md":             "# linked\nSee [[token-rotation]].\n",
	})
	res, err := Weave(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	var found *MissingLink
	for i := range res.MissingLinks {
		if res.MissingLinks[i].Target == "resources/token-rotation.md" {
			found = &res.MissingLinks[i]
		}
	}
	if found == nil {
		t.Fatalf("token-rotation.md is mentioned in runbook.md without a link; got %+v", res.MissingLinks)
	}
	if !has(found.MentionedIn, "areas/runbook.md") {
		t.Errorf("mentioned_in = %v, want areas/runbook.md", found.MentionedIn)
	}
	// linked.md already links it, so proposing that pair again is noise.
	if has(found.MentionedIn, "areas/linked.md") {
		t.Errorf("linked.md already links the target; mentioned_in = %v", found.MentionedIn)
	}
	// linked.md is a content doc that links it, so the target is already woven —
	// the candidate is still worth surfacing, just not as a spoke fix.
	if found.TargetIsSpoke {
		t.Error("a content doc already links this target, so it is not a lonely spoke")
	}
}

// A single common English word matches everywhere. Anchors have to be specific
// or the report is all false positives and nobody reads it — but a CJK term is
// specific at one token, which is why the rule is not simply "two words".
func TestAnchorsMustBeSpecific(t *testing.T) {
	for _, tc := range []struct {
		phrase string
		want   bool
	}{
		{"token rotation", true},
		{"api", false},
		{"logs", false},
		{"토큰 회전", true},
		{"회전정책", true},
		{"a b", false},
	} {
		if got := specific(tc.phrase); got != tc.want {
			t.Errorf("specific(%q) = %v, want %v", tc.phrase, got, tc.want)
		}
	}
}

// A term recurring across several folders with no note of its own is the shape
// of a missing shared concept. One folder is just a document's own vocabulary.
func TestConceptCandidateNeedsThreeDocsAcrossTwoFolders(t *testing.T) {
	base := write(t, map[string]string{
		"areas/a.md":     "# a\nThe **write fence** matters.\n",
		"areas/b.md":     "# b\nAgain the **write fence**.\n",
		"resources/c.md": "# c\nThe **write fence** once more.\n",
		"areas/d.md":     "# d\nOnly here: **local flavour** appears.\n",
		"areas/e.md":     "# e\n**local flavour** twice.\n",
		"areas/f.md":     "# f\n**local flavour** thrice.\n",
	})
	res, err := Weave(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	var phrases []string
	for _, c := range res.ConceptCandidates {
		phrases = append(phrases, c.Phrase)
	}
	if !has(phrases, "write fence") {
		t.Errorf("write fence spans two folders in three docs; got %v", phrases)
	}
	if has(phrases, "local flavour") {
		t.Errorf("local flavour lives in one folder — not a shared concept; got %v", phrases)
	}
}

// A term that already has a note is not a candidate for a new one.
func TestATermWithANoteIsNotACandidate(t *testing.T) {
	base := write(t, map[string]string{
		"resources/write-fence.md": "# write fence\n",
		"areas/a.md":               "# a\nThe **write fence**.\n",
		"areas/b.md":               "# b\nThe **write fence**.\n",
		"projects/c.md":            "# c\nThe **write fence**.\n",
	})
	res, err := Weave(base, "brain", base)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.ConceptCandidates {
		if c.Phrase == "write fence" {
			t.Fatalf("write-fence.md already exists — proposing it again is noise")
		}
	}
}
