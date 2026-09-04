package brain

import (
	"strings"
	"testing"
)

// The store is the authority on these rules; this client mirrors them so an
// obviously-wrong call comes back with the rule instead of costing a round trip
// and a bare 4xx. What these tests pin down is that the mirror never gets
// LOOSER than the original — a call this passes and the store rejects is a
// wasted trip, but a call this passes that the store ACCEPTS wrongly would be
// an edit in the wrong place.

func client() *Client {
	return New(Config{BaseURL: "http://store.invalid", Token: "t"})
}

func ptrInt(n int) *int       { return &n }
func ptrStr(s string) *string { return &s }

const doc = "acme/shared/areas/blog/writing-style.md"

func TestPreparePatchAcceptsEachAddressForm(t *testing.T) {
	c := client()
	for name, edit := range map[string]Edit{
		"section": {Section: "## Notes", Body: "## Notes\n\nnew\n"},
		"anchor":  {Anchor: "one exact phrase", Body: "another"},
		"lines":   {StartLine: 12, EndLine: ptrInt(14), Expect: ptrStr("old\ntext\n"), Body: "new\n"},
		"insert":  {StartLine: 12, EndLine: ptrInt(12), Expect: ptrStr(""), Body: "inserted\n"},
		"delete":  {Section: "## Gone", Body: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.PreparePatch(doc, PatchRequest{
				Edits: []Edit{edit}, Note: "why",
			}); err != nil {
				t.Fatalf("rejected a legitimate %s edit: %v", name, err)
			}
		})
	}
}

func TestPreparePatchRefusals(t *testing.T) {
	c := client()
	cases := []struct {
		name string
		req  PatchRequest
		want string
	}{
		{"no address", PatchRequest{Edits: []Edit{{Body: "x"}}, Note: "n"}, "no address"},
		{"two addresses", PatchRequest{Edits: []Edit{{Section: "A", Anchor: "b", Body: "x"}}, Note: "n"},
			"more than one address"},
		{"line range without expect", PatchRequest{
			Edits: []Edit{{StartLine: 3, EndLine: ptrInt(4), Body: "x"}}, Note: "n"},
			"expect"},
		{"end before start", PatchRequest{
			Edits: []Edit{{StartLine: 9, EndLine: ptrInt(4), Expect: ptrStr("x"), Body: "y"}}, Note: "n"},
			"before start_line"},
		{"start without end", PatchRequest{
			Edits: []Edit{{StartLine: 9, Expect: ptrStr("x"), Body: "y"}}, Note: "n"},
			"end_line"},
		{"no edits", PatchRequest{Note: "n"}, "no edits"},
		{"no note", PatchRequest{Edits: []Edit{{Section: "A", Body: "x"}}}, "note is empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.PreparePatch(doc, tc.req)
			if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("message does not say what to fix (%q missing): %v", tc.want, err)
			}
		})
	}
}

// An empty expect is a real expectation — "this range holds nothing", which is
// how an insertion proves it is aimed at a gap and not at a line of text. It
// must not be confused with expect being absent, which is refused.
func TestEmptyExpectIsNotTheSameAsNoExpect(t *testing.T) {
	c := client()
	if _, err := c.PreparePatch(doc, PatchRequest{
		Edits: []Edit{{StartLine: 5, EndLine: ptrInt(5), Expect: ptrStr(""), Body: "x\n"}},
		Note:  "n",
	}); err != nil {
		t.Fatalf("an empty expect was treated as missing: %v", err)
	}
}

func TestPreparePatchStillChecksTheAddress(t *testing.T) {
	c := client()
	_, err := c.PreparePatch("acme/README.md", PatchRequest{
		Edits: []Edit{{Section: "A", Body: "x"}}, Note: "n",
	})
	if err == nil {
		t.Fatal("a malformed document path was accepted")
	}
}

func TestPreparePatchNeedsATokenToWrite(t *testing.T) {
	c := New(Config{BaseURL: "http://store.invalid"})
	_, err := c.PreparePatch(doc, PatchRequest{Edits: []Edit{{Section: "A", Body: "x"}}, Note: "n"})
	if err != ErrNoToken {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestAddressNamesTheFormForMessages(t *testing.T) {
	for want, e := range map[string]Edit{
		"line range": {StartLine: 1, EndLine: ptrInt(2)},
		"section":    {Section: "A"},
		"anchor":     {Anchor: "a"},
		"":           {},
	} {
		if got := e.Address(); got != want {
			t.Fatalf("Address() = %q, want %q", got, want)
		}
	}
}
