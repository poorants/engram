package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/poorants/engram/pkg/brain"
	"github.com/poorants/engram/pkg/config"
	"github.com/poorants/engram/pkg/identity"
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
// What stays OUT: the local file brain that the engram skill falls back to when
// the store refuses a repo. That vault's location lives in the skill's own
// resolution rules, and teaching a release-channel binary to read a plugin's
// layout would tie the two together. Instead a refusal is reported in a form a
// caller can branch on — exit code exitRefused, plus `"status": "refused"` on
// stdout — and the skill does the local write it already knows how to do.

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
// (git@host:group/repo.git, https://host/group/repo.git).
var originRe = regexp.MustCompile(`[:/]([^/:]+)/([^/]+?)(?:\.git)?/*$`)

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
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", "", fmt.Errorf("could not read the git origin — there is nothing to build a document path from")
	}
	m := originRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", "", fmt.Errorf("could not read owner/repo out of the git origin: %q", strings.TrimSpace(string(out)))
	}
	return m[1], m[2], nil
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
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	owner, repo, err := repoScope()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	return emit(map[string]any{"owner": owner, "repo": repo, "root": owner + "/" + repo})
}

// --- reads ------------------------------------------------------------------

func cmdSearch(args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum results, 1..50")
	archives := fs.Bool("archives", false, "also search archived documents")
	boost := fs.String("boost-repo", "", "lift this repo's documents (a boost, not a filter)")
	onlyRepos := fs.String("only-repo", "", "restrict to these repos (comma-separated) — a filter, not a boost")
	onlyOwners := fs.String("only-owner", "", "restrict to these owners (comma-separated)")
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
	return emit(res)
}

func cmdGet(args []string) int {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	out := fs.String("out", "", "write the body to this file and omit it from the response")
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
	if f := strings.TrimSpace(*out); f != "" {
		body, _ := doc["body"].(string)
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return exitError
		}
		delete(doc, "body")
		doc["savedTo"] = f
	}
	return emit(doc)
}

func cmdRevisions(args []string) int {
	fs := flag.NewFlagSet("revisions", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum revisions listed")
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
	return emit(res)
}

func cmdIntegrity(args []string) int {
	fs := flag.NewFlagSet("integrity", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "maximum entries listed per category")
	c, _, _, _, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	res, err := c.Integrity(context.Background(), *limit)
	if err != nil {
		return fail(err)
	}
	return emit(res)
}

// --- writes -----------------------------------------------------------------

func cmdPut(args []string) int {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	file := fs.String("file", "", "body file (stdin when omitted)")
	note := fs.String("note", "", "one line on why this revision exists (required)")
	author := fs.String("author", "", "recorded author (resolved automatically when omitted)")
	dryRun := fs.Bool("dry-run", false, "validate without writing")
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
		return emit(map[string]any{
			"dryRun": true, "path": target.Path, "bytes": target.Bytes,
			"note": target.Note, "author": target.Author,
		})
	}

	res, err := c.Put(ctx, path, body, *note, who)
	if err != nil {
		// A refusal is not a failure to hide: the caller (the engram skill) owns
		// a local file brain for exactly this case, and exitRefused is how it is
		// told. Everything else — including an unreachable store — stays an
		// error, because a write that went nowhere must never look like one that
		// landed.
		if brain.Refused(err) {
			fmt.Fprintln(os.Stderr, "error:", err)
			_ = emit(map[string]any{"status": "refused", "path": path, "author": who})
			return exitRefused
		}
		return fail(err)
	}
	if res == nil {
		res = map[string]any{}
	}
	res["path"] = path
	res["author"] = who
	return emit(res)
}

func cmdMove(args []string) int {
	fs := flag.NewFlagSet("move", flag.ContinueOnError)
	author := fs.String("author", "", "recorded author (resolved automatically when omitted)")
	dryRun := fs.Bool("dry-run", false, "validate without moving")
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
		return emit(map[string]any{"dryRun": true, "from": from, "to": target.Path, "author": target.Author})
	}
	res, err := c.Move(ctx, from, to, who)
	if err != nil {
		return fail(err)
	}
	return emit(res)
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
	c, ident, cfg, _, err := clients(fs, args)
	if err != nil {
		return usageError(err.Error())
	}
	ctx := context.Background()

	out := map[string]any{
		"store":    c.BaseURL(),
		"canWrite": c.CanWrite(),
		"author":   ident.Author(ctx, ""),
	}
	if cfg.Author != "" {
		out["authorSource"] = "configured"
	}
	if len(cfg.FromEnv) > 0 {
		out["fromEnv"] = cfg.FromEnv
	}

	owner, repo, scopeErr := repoScope()
	if scopeErr == nil {
		out["owner"], out["repo"] = owner, repo
		out["root"] = owner + "/" + repo
	} else {
		out["scopeError"] = scopeErr.Error()
	}

	if !c.Configured() {
		out["reachable"] = false
		out["error"] = brain.ErrNoStore.Error()
		_ = emit(out)
		return exitError
	}

	h, err := c.Healthz(ctx)
	if err != nil {
		out["reachable"] = false
		out["error"] = err.Error()
		_ = emit(out)
		return exitStoreOut
	}
	out["reachable"] = true
	out["docs"] = h.Docs

	sc, err := c.StoreScopes(ctx)
	if err != nil {
		out["scopesError"] = err.Error()
		return emit(out)
	}
	out["allowedOwners"] = sc.AllowedOwners
	out["present"] = sc.Present
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
		out["writesHere"] = accepted
	}
	return emit(out)
}
