package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/poorants/engram/pkg/brain"
)

// The MCP server is spawned once per session and cannot know which checkout a
// given call is about, so the document address —
// <owner>/<repo>/<area>/<name>.md, or <owner>/<repo>/README.md for a repo hub
// MOC — is the caller's to supply; the schemas spell the form out and pkg/brain
// refuses malformed ones. The store itself refuses owners outside its
// allow-list (403), so a document from a repo the store does not admit cannot
// land here by mistake. The CLI, which runs IN a directory, is the surface that
// can fill the coordinates in from `origin`.
//
// There is deliberately no brain_delete: the engram contract is "never delete,
// move to archives", and archiving is brain_move with an archives/ target.

// AuthorFunc resolves the name to stamp on a revision, given whatever the
// caller passed explicitly (often nothing). A function rather than the resolver
// type so this adapter keeps knowing only MCP and pkg/brain — the identity
// rules stay one layer up, where both surfaces share them. A nil AuthorFunc
// passes the caller's value through untouched.
type AuthorFunc func(ctx context.Context, explicit string) string

// Register registers the brain_* tools on the server. They hit the same
// /api/search the human viewer uses, so people and agents see one ranking.
func Register(server *mcp.Server, cfg brain.Config, authorOf AuthorFunc) {
	c := brain.New(cfg)
	if authorOf == nil {
		authorOf = func(_ context.Context, explicit string) string { return explicit }
	}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(true)}

	type searchIn struct {
		Query     string   `json:"query" jsonschema:"The search query — pass the user's question as a whole sentence. Do not break it into keywords; the ranking is tuned on natural questions"`
		Limit     int      `json:"limit,omitempty" jsonschema:"Maximum results, 1..50 (default 6)"`
		Archives  bool     `json:"archives,omitempty" jsonschema:"true also searches archived documents"`
		BoostRepo string   `json:"boostRepo,omitempty" jsonschema:"Lift this repo's documents without excluding the rest — pass the repo you are working in"`
		OnlyRepos []string `json:"onlyRepos,omitempty" jsonschema:"Restrict to these repos (a filter, not a boost — it hides what other repos already solved)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_search",
		Description: "Search the shared brain (the team knowledge store). Uses the same single ranking as the human search page. " +
			"Results are CHUNKS with their heading_path, not whole documents — follow up with brain_get when you need the full text. " +
			"No token needed. If the store is unreachable this fails; there is no fallback and no cached answer.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		out, err := c.Search(ctx, brain.SearchOpts{
			Query: in.Query, Limit: in.Limit, Archives: in.Archives,
			BoostRepo: in.BoostRepo, OnlyRepos: in.OnlyRepos,
		})
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	type getIn struct {
		Path string `json:"path" jsonschema:"Document path — <owner>/<repo>/<area>/<name>.md (e.g. acme/shared/resources/git-conventions.md). A repo hub MOC has no area: <owner>/<repo>/README.md"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_get",
		Description: "Fetch one document in full — body, outgoing links, backlinks, and recent history. " +
			"The path is usually taken verbatim from a brain_search hit.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, any, error) {
		out, err := c.Doc(ctx, in.Path)
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	type revisionsIn struct {
		Path  string `json:"path" jsonschema:"Document path — <owner>/<repo>/<area>/<name>.md"`
		Limit int    `json:"limit,omitempty" jsonschema:"Maximum revisions, 1..200 (default 20)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_revisions",
		Description: "The change history of one document — who changed it, when, and why. This is the git log of the brain; " +
			"each revision's note is its commit message.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in revisionsIn) (*mcp.CallToolResult, any, error) {
		out, err := c.Revisions(ctx, in.Path, in.Limit)
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	type integrityIn struct {
		Limit int `json:"limit,omitempty" jsonschema:"Maximum entries listed per category (default 50)"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_integrity",
		Description: "Health of the brain's link graph — broken links, orphans, and weak nodes (documents reachable only from a folder MOC). " +
			"Use it before tidying the brain to see where to start.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in integrityIn) (*mcp.CallToolResult, any, error) {
		out, err := c.Integrity(ctx, in.Limit)
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	type putIn struct {
		Path   string `json:"path" jsonschema:"Document path — <owner>/<repo>/<area>/<name>.md. Take owner/repo from the working repo's git origin; area is PARA (projects|areas|resources|archives). Only a repo hub MOC omits the area: <owner>/<repo>/README.md. At most 5 levels below the document root"`
		Body   string `json:"body" jsonschema:"The full document (markdown). This is an upsert: the existing document is REPLACED wholesale and the previous body is kept in revisions. For a PARTIAL edit of an existing document use brain_patch instead — it sends only the changed part"`
		Note   string `json:"note" jsonschema:"One line on why this revision exists (the commit message of the history). Required"`
		Author string `json:"author,omitempty" jsonschema:"Recorded author. Omit to resolve it automatically (ENGRAM_AUTHOR, then git config user.name, then the OS user)"`
		DryRun bool   `json:"dryRun,omitempty" jsonschema:"Preview — validate and report without writing. Recommended before a new document or a large replacement"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_put",
		Description: "Save a document to the shared brain (create and update are the same call — an upsert). The previous body stays in revisions, so a write is reversible. " +
			"Needs the store token. A path whose owner group the store does not admit is refused with 403 — that document belongs in engram's local file brain. " +
			"An unchanged body is reported as 'unchanged' rather than written again.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in putIn) (*mcp.CallToolResult, any, error) {
		if in.DryRun {
			target, err := c.PreparePut(in.Path, in.Body, in.Note, authorOf(ctx, in.Author))
			if err != nil {
				return fail(err.Error())
			}
			return jsonResult(map[string]any{
				"dryRun": true, "path": target.Path, "bytes": target.Bytes,
				"note": target.Note, "author": target.Author,
				"warning": "Call again without dryRun to actually write. The existing document is replaced wholesale (the previous body is kept in revisions).",
			})
		}
		out, err := c.Put(ctx, in.Path, in.Body, in.Note, authorOf(ctx, in.Author))
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	// One address form per edit, checked in pkg/brain before the call leaves.
	// The schema spells out what each one costs to get wrong, because the
	// failure mode this tool has to avoid is not "the edit was rejected" — it
	// is "the edit landed somewhere else".
	type editIn struct {
		StartLine      int     `json:"startLine,omitempty" jsonschema:"1-indexed first line to replace. Pair with endLine, which is EXCLUSIVE: one line n is endLine n+1, and endLine == startLine inserts before line n without replacing. A line range REQUIRES expect"`
		EndLine        int     `json:"endLine,omitempty" jsonschema:"1-indexed line to stop before (EXCLUSIVE)"`
		Section        string  `json:"section,omitempty" jsonschema:"Address a heading and everything under it, up to the next heading of the same or shallower depth. Matches the heading text ('Notes'), the raw heading line ('## Notes'), or the full heading path brain_search prints ('Guide > Notes'). A query matching two headings is refused, not guessed"`
		IncludeHeading *bool   `json:"includeHeading,omitempty" jsonschema:"Default true — the heading line is part of the section, so your body must repeat it. False replaces only the prose under the heading"`
		Anchor         string  `json:"anchor,omitempty" jsonschema:"An exact substring, matched literally (not a regex), that must occur EXACTLY ONCE in the document. Two matches is a refusal — extend the anchor until it is unique. Use this for an edit inside a line"`
		Expect         *string `json:"expect,omitempty" jsonschema:"The literal text you believe occupies the addressed range right now, copied from brain_get. Compared character for character; trailing newlines are the only tolerated difference. This is what proves the edit is aimed where you think — always send it. Required for a line range"`
		Body           string  `json:"body" jsonschema:"What replaces the addressed range. \"\" deletes it"`
	}
	type patchIn struct {
		Path       string   `json:"path" jsonschema:"Document path — <owner>/<repo>/<area>/<name>.md"`
		Edits      []editIn `json:"edits" jsonschema:"The changes, applied together or not at all. Every edit is resolved against the document AS YOU READ IT, so line numbers do not shift under each other. Overlapping edits are refused"`
		Note       string   `json:"note" jsonschema:"One line on why this revision exists (the commit message of the history). Required"`
		BaseSha256 string   `json:"baseSha256,omitempty" jsonschema:"The sha256 brain_get returned for the version you read. Send it: it is the only thing that catches an edit aimed correctly at a document somebody else has since changed"`
		Author     string   `json:"author,omitempty" jsonschema:"Recorded author. Omit to resolve it automatically"`
		DryRun     bool     `json:"dryRun,omitempty" jsonschema:"Preview — returns a unified diff of what would change, and writes nothing"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_patch",
		Description: "Change PART of an existing document — send only the edit, not the whole body. " +
			"Prefer this over brain_put for any edit to a document that already exists; brain_put re-sends the entire document, which for a long one costs far more than the change. " +
			"Address each edit by section (a heading and what is under it), by anchor (an exact substring that occurs exactly once), or by line range — and pass `expect`, the current text of that range, so a misaimed edit is refused instead of applied. " +
			"Ambiguity is never resolved by guessing: an address matching two places comes back as a conflict listing both. " +
			"Every edit lands or none does, and the result is one ordinary revision, so it stays reversible exactly like a put. " +
			"A 409 means the document disagrees with the request — re-read it and re-aim rather than retrying unchanged.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in patchIn) (*mcp.CallToolResult, any, error) {
		req := brain.PatchRequest{
			BaseSHA256: in.BaseSha256,
			Note:       in.Note,
			Author:     authorOf(ctx, in.Author),
			DryRun:     in.DryRun,
		}
		for _, e := range in.Edits {
			edit := brain.Edit{
				StartLine: e.StartLine, Section: e.Section, Anchor: e.Anchor,
				IncludeHeading: e.IncludeHeading, Expect: e.Expect, Body: e.Body,
			}
			// Absent and zero are different answers for a line bound, and the
			// wire format has to keep them apart or "no line address" reads as
			// "line 0".
			if e.EndLine != 0 || e.StartLine != 0 {
				end := e.EndLine
				edit.EndLine = &end
			}
			req.Edits = append(req.Edits, edit)
		}
		out, err := c.Patch(ctx, in.Path, req)
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})

	type moveIn struct {
		Path   string `json:"path" jsonschema:"Current path — <owner>/<repo>/<area>/<name>.md"`
		To     string `json:"to" jsonschema:"Destination path, same form. Archiving IS a move: change the area to archives (there is no delete tool)"`
		Author string `json:"author,omitempty" jsonschema:"Recorded author. Omit to resolve it automatically"`
		DryRun bool   `json:"dryRun,omitempty" jsonschema:"Preview — validate and report without moving"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "brain_move",
		Description: "Move a document — rename, reclassify, or archive. The old path is kept as an alias, so links written elsewhere keep resolving. " +
			"The brain has no delete: a finished document is moved to archives. Needs the store token.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in moveIn) (*mcp.CallToolResult, any, error) {
		if in.DryRun {
			target, err := c.PrepareMove(in.Path, in.To, authorOf(ctx, in.Author))
			if err != nil {
				return fail(err.Error())
			}
			return jsonResult(map[string]any{
				"dryRun": true, "from": in.Path, "to": target.Path, "author": target.Author,
				"warning": "Call again without dryRun to actually move. The old path is kept as an alias.",
			})
		}
		out, err := c.Move(ctx, in.Path, in.To, authorOf(ctx, in.Author))
		if err != nil {
			return fail(err.Error())
		}
		return jsonResult(out)
	})
}
