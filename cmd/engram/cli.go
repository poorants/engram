package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"strings"

	"github.com/poorants/engram/pkg/brain"
	"github.com/poorants/engram/pkg/config"
	"github.com/poorants/engram/pkg/identity"
	"github.com/poorants/engram/pkg/vault"
	"github.com/poorants/engram/pkg/workspace"
)

// The CLI is the second surface over pkg/brain, next to the brain_* MCP tools.
// Same client, same settings, same identity resolution — only the caller
// differs.
//
// It exists because an MCP tool can be called by exactly one thing: the model,
// inside a session. Everything else that needs the store is a plain process — a
// hook, a scheduled job, the engram skill's own helpers — and without this they
// would each carry their own HTTP client, which means a second place to put the
// token, a second default author, and a second copy of the path rules to drift
// from this one.
//
// The local file brain used to stay OUT of here, on the reasoning that a
// release-channel binary should not know a plugin's layout. It is in now,
// because the thing on the other side of that boundary was a Python helper and a
// helper that needs an interpreter is a helper that does not run — on Windows
// `python3` is not a command even where Python is installed. What the boundary
// was actually protecting survives in a different shape: the vault's location is
// resolved by pkg/workspace from the SHARED settings file, never from anything
// under the plugin directory, so the binary still has no idea where the plugin
// is installed.
//
// Output is human-readable by default and JSON behind --json. That is the other
// half of the same move: the reshaping that made a store answer readable used to
// live in the skill's store.py, and a session that reads raw JSON pays for every
// field it did not need.

// clients builds the store client and the identity resolver from one settings
// load, so both surfaces of one command agree about the address and the byline.
// --store mirrors the MCP flag so a relocated store is reachable from either
// with one spelling.
func clients(fs *flag.FlagSet, args []string) (*brain.Client, *identity.Resolver, config.Config, []string, error) {
	storeURL := fs.String("store", "", "store address (default: the configured one)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, nil, config.Config{}, nil, err
	}
	cfg := config.Load()
	if u := strings.TrimSpace(*storeURL); u != "" {
		normalized, err := config.NormalizeURL(u)
		if err != nil {
			return nil, nil, config.Config{}, nil, err
		}
		cfg.StoreURL = normalized
	}
	return brain.New(cfg.Brain()), identity.New(cfg.Author, nil), cfg, positional, nil
}

// parseInterspersed parses flags that appear AFTER positional arguments.
//
// Go's flag package stops at the first non-flag argument, so
// `engram put <path> --note "why"` would silently leave --note unset — and an
// unset --note is rejected with "note is empty", an error about the wrong thing
// entirely. Every caller writes the path first, because that is how the command
// reads, so accepting only flags-first would be a trap rather than a
// convention. This is the standard loop: parse, take one positional, parse the
// rest.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// fail turns a store error into the right exit code, so a caller can branch
// without parsing prose.
func fail(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	switch {
	case brain.Refused(err):
		return exitRefused
	case brain.Unreachable(err):
		return exitStoreOut
	}
	return exitError
}

func emit(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	return exitOK
}

func usageError(msg string) int {
	fmt.Fprintln(os.Stderr, "error:", msg)
	return exitError
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- scope: which repo am I standing in -------------------------------------

// originRe pulls owner/repo out of a git remote URL in either spelling
// (git@host:group/repo.git, https://host/group/repo.git). The pattern itself
// lives in pkg/workspace, which the hook and the resolver share — one derivation
// of the owner coordinate, because it is the confidentiality boundary and two
// copies would eventually disagree about it.
var originRe = workspace.OriginRe

// repoScope derives the document root from the CURRENT DIRECTORY's git origin.
//
// This is the one thing the CLI can do that the MCP tools cannot: an MCP server
// is spawned once per session and has no idea which checkout a given call is
// about, which is why those tools require a full path from the caller. A CLI
// runs IN the directory, so the derivation is unambiguous.
//
// It is also the confidentiality boundary in practice. Scope is never chosen —
// the remote decides it — so knowledge from a repo the store does not admit
// cannot be filed into it by someone forgetting where they were.
func repoScope() (owner, repo string, err error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("could not read the working directory: %w", err)
	}
	if owner, repo = workspace.OriginCoords(wd); owner != "" && repo != "" {
		return owner, repo, nil
	}
	if url := workspace.RemoteURL(wd); url != "" {
		return "", "", fmt.Errorf("could not read owner/repo out of the git origin: %q", url)
	}
	return "", "", fmt.Errorf("could not read the git origin — there is nothing to build a document path from")
}

// expandPath resolves "./<area>/<name>.md" against the current repo. Anything
// else passes through untouched — an explicit full path always wins, so a
// caller deliberately writing into another repo's scope is never silently
// redirected into this one.
func expandPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if !strings.HasPrefix(p, "./") {
		return p, nil
	}
	owner, repo, err := repoScope()
	if err != nil {
		return "", err
	}
	return owner + "/" + repo + "/" + strings.TrimPrefix(p, "./"), nil
}

func cmdScope(args []string) int {
	fs := flag.NewFlagSet("scope", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	owner, repo, err := repoScope()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	if *asJSON {
		return emit(map[string]any{"owner": owner, "repo": repo, "root": owner + "/" + repo})
	}
	out("%s/%s\n", owner, repo)
	return exitOK
}

// --- reads ------------------------------------------------------------------

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum results, 1..50")
	archives := fs.Bool("archives", false, "also search archived documents")
	boost := fs.String("boost-repo", "", "lift this repo's documents (a boost, not a filter)")
	onlyRepos := fs.String("only-repo", "", "restrict to these repos (comma-separated) — a filter, not a boost")
	onlyOwners := fs.String("only-owner", "", "restrict to these owners (comma-separated)")
	chars := fs.Int("chars", 400, "characters shown per chunk (human output only)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, _, _, pos, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	q := strings.Join(pos, " ")
	if strings.TrimSpace(q) == "" {
		return usageError("the query is empty — pass the question as a sentence")
	}
	// A search run inside a checkout is almost always about THAT repo, so its
	// documents are boosted unless the caller said otherwise. This is the CLI
	// using the one thing it knows and the MCP tools cannot — and it is a boost,
	// never a filter, so nothing is hidden: what another repo already solved
	// still appears, lower down. An explicit --boost-repo or any --only-* wins,
	// and a directory with no git remote simply skips it.
	opts := brain.SearchOpts{
		Query: q, Limit: *limit, Archives: *archives, BoostRepo: strings.TrimSpace(*boost),
		OnlyRepos: splitList(*onlyRepos), OnlyOwners: splitList(*onlyOwners),
	}
	if opts.BoostRepo == "" && len(opts.OnlyRepos) == 0 && len(opts.OnlyOwners) == 0 {
		if _, repo, err := repoScope(); err == nil {
			opts.BoostRepo = repo
		}
	}
	res, err := c.Search(context.Background(), opts)
	if err != nil {
		return fail(err)
	}
	if res != nil && opts.BoostRepo != "" {
		// Echoed so a caller can tell the user which repo got the thumb on the
		// scale; a ranking nobody can explain is one nobody trusts.
		res["boostRepo"] = opts.BoostRepo
	}
	if *asJSON {
		return emit(res)
	}
	renderSearch(res, q, *chars)
	return exitOK
}

func cmdGet(args []string) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	outFile := fs.String("out", "", "write the body to this file and omit it from the response")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, _, _, pos, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(pos) < 1 {
		return usageError("a document path is required")
	}
	path, err := expandPath(pos[0])
	if err != nil {
		return usageError(err.Error())
	}
	doc, err := c.Doc(context.Background(), path)
	if err != nil {
		return fail(err)
	}
	if f := strings.TrimSpace(*outFile); f != "" {
		body, _ := doc["body"].(string)
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return exitError
		}
		delete(doc, "body")
		doc["savedTo"] = f
	}
	if *asJSON {
		return emit(doc)
	}
	renderDoc(doc)
	return exitOK
}

func cmdRevisions(args []string) int {
	fs := flag.NewFlagSet("revisions", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum revisions listed")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, _, _, pos, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(pos) < 1 {
		return usageError("a document path is required")
	}
	path, err := expandPath(pos[0])
	if err != nil {
		return usageError(err.Error())
	}
	res, err := c.Revisions(context.Background(), path, *limit)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return emit(res)
	}
	renderRevisions(res, path)
	return exitOK
}

func cmdIntegrity(args []string) int {
	fs := flag.NewFlagSet("integrity", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum entries listed per category")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, _, _, _, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	res, err := c.Integrity(context.Background(), *limit)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return emit(res)
	}
	renderIntegrity(res)
	return exitOK
}

// --- writes -----------------------------------------------------------------

func cmdPut(args []string) int {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	file := fs.String("file", "", "body file (stdin when omitted)")
	note := fs.String("note", "", "one line on why this revision exists (required)")
	author := fs.String("author", "", "recorded author (resolved automatically when omitted)")
	dryRun := fs.Bool("dry-run", false, "validate without writing")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, ident, _, pos, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(pos) < 1 {
		return usageError("a document path is required")
	}
	path, err := expandPath(pos[0])
	if err != nil {
		return usageError(err.Error())
	}
	body, err := readBody(*file)
	if err != nil {
		return usageError(err.Error())
	}

	ctx := context.Background()
	who := ident.Author(ctx, *author)

	if *dryRun {
		target, err := c.PreparePut(path, body, *note, who)
		if err != nil {
			return usageError(err.Error())
		}
		summary := map[string]any{
			"dryRun": true, "path": target.Path, "bytes": target.Bytes,
			"note": target.Note, "author": target.Author,
		}
		if *asJSON {
			return emit(summary)
		}
		out("dry run: %s  (%d bytes, author %s)\n", target.Path, target.Bytes, target.Author)
		return exitOK
	}

	res, err := c.Put(ctx, path, body, *note, who)
	if err != nil {
		// A refusal is not a failure. The store is alive and declined this
		// path's owner group, and that knowledge belonged in the local file
		// brain anyway — so it goes there, here, rather than being handed back
		// for something else to place.
		//
		// An UNREACHABLE store is the opposite and must never take this branch.
		// Reading an outage as a refusal puts the document in a file nobody
		// reads while everyone believes it was recorded, and that belief
		// outlives the outage by months.
		if brain.Refused(err) {
			return putRefused(path, body, who, err, *asJSON)
		}
		return fail(err)
	}
	if res == nil {
		res = map[string]any{}
	}
	res["path"] = path
	res["author"] = who
	res["status"] = "ok"
	if *asJSON {
		return emit(res)
	}
	out("ok: store: %s  (author %s)\n", path, who)
	return exitOK
}

// putRefused writes a scope-refused document into the local file brain.
//
// Exit code: 0 when the document landed, exitRefused when it landed NOWHERE.
// The store's refusal is reported either way, but a write that succeeded must
// not look like a failure — a caller that sees non-zero retries, and a model
// that sees non-zero says the save failed when the file is sitting right there.
func putRefused(path, body, who string, refusal error, asJSON bool) int {
	r := workspace.Resolve(here())
	file, writeErr := vault.WriteRefused(r.FallbackBase(), path, body)
	if writeErr != nil {
		fmt.Fprintln(os.Stderr, "error:", refusal)
		summary := map[string]any{
			"status": "refused", "path": path, "author": who,
			"error": writeErr.Error(),
		}
		if asJSON {
			_ = emit(summary)
		} else {
			out("failed: the store refused this path (%v) and it could not be written locally: %v\n"+
				"  Designate a file brain with: engram brain set <path>\n", refusal, writeErr)
		}
		return exitRefused
	}
	summary := map[string]any{"status": "local", "path": path, "author": who, "file": file}
	if asJSON {
		return emit(summary)
	}
	out("local: the store refused this path (%v) → wrote the local file brain: %s\n", refusal, file)
	return exitOK
}

func cmdMove(args []string) int {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	author := fs.String("author", "", "recorded author (resolved automatically when omitted)")
	dryRun := fs.Bool("dry-run", false, "validate without moving")
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, ident, _, pos, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	if len(pos) < 2 {
		return usageError("a source path and a destination path are required")
	}
	from, err := expandPath(pos[0])
	if err != nil {
		return usageError(err.Error())
	}
	to, err := expandPath(pos[1])
	if err != nil {
		return usageError(err.Error())
	}
	ctx := context.Background()
	who := ident.Author(ctx, *author)
	if *dryRun {
		target, err := c.PrepareMove(from, to, who)
		if err != nil {
			return usageError(err.Error())
		}
		if *asJSON {
			return emit(map[string]any{"dryRun": true, "from": from, "to": target.Path, "author": target.Author})
		}
		out("dry run: %s\n    → %s  (author %s)\n", from, target.Path, target.Author)
		return exitOK
	}
	res, err := c.Move(ctx, from, to, who)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return emit(res)
	}
	if sOf(res, "status") != "moved" {
		fmt.Fprintf(os.Stderr, "%s: %s\n", sOf(res, "status"), from)
		return exitError
	}
	line := fmt.Sprintf("moved: %s\n    → %s", from, to)
	if n := nOf(res, "relinked"); n != "?" && n != "0" {
		line += fmt.Sprintf("  (%s dangling link(s) reconnected)", n)
	}
	out("%s\n  The old path is kept as an alias, so existing links still reach it.\n", line)
	return exitOK
}

// readBody takes the document from a file or, when none is named, stdin — so a
// caller that already has the text in a pipe does not have to invent a temp
// file.
func readBody(file string) (string, error) {
	if f := strings.TrimSpace(file); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("could not read the body file (%s): %w", f, err)
		}
		return string(b), nil
	}
	st, err := os.Stdin.Stat()
	if err == nil && st.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("no body — pass --file or pipe it on stdin")
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", fmt.Errorf("the body is empty")
	}
	return string(b), nil
}

// --- status -----------------------------------------------------------------

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	c, ident, cfg, _, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	ctx := context.Background()

	st := map[string]any{
		"store":    c.BaseURL(),
		"canWrite": c.CanWrite(),
		"author":   ident.Author(ctx, ""),
	}
	if cfg.Author != "" {
		st["authorSource"] = "configured"
	}
	if len(cfg.FromEnv) > 0 {
		st["fromEnv"] = cfg.FromEnv
	}

	// report is how every exit from here prints: one shape, whichever branch
	// reached it, so a failure line is never formatted differently from a
	// success line.
	report := func(code int) int {
		if *asJSON {
			_ = emit(st)
			return code
		}
		renderStatus(st)
		return code
	}

	owner, repo, scopeErr := repoScope()
	if scopeErr == nil {
		st["owner"], st["repo"] = owner, repo
		st["root"] = owner + "/" + repo
	} else {
		st["scopeError"] = scopeErr.Error()
	}

	if !c.Configured() {
		st["reachable"] = false
		st["error"] = brain.ErrNoStore.Error()
		return report(exitError)
	}

	h, err := c.Healthz(ctx)
	if err != nil {
		st["reachable"] = false
		st["error"] = err.Error()
		return report(exitStoreOut)
	}
	st["reachable"] = true
	st["docs"] = h.Docs

	sc, err := c.StoreScopes(ctx)
	if err != nil {
		st["scopesError"] = err.Error()
		return report(exitOK)
	}
	st["allowedOwners"] = sc.AllowedOwners
	st["present"] = sc.Present
	if scopeErr == nil {
		// The question a caller actually has: do MY documents belong in the
		// store, or in the local file brain? Answering it before a write beats
		// discovering it from a 403 after one.
		accepted := false
		for _, o := range sc.AllowedOwners {
			if o == owner {
				accepted = true
				break
			}
		}
		st["writesHere"] = accepted
	}
	return report(exitOK)
}
