package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/poorants/engram/pkg/config"
	"github.com/poorants/engram/pkg/vault"
	"github.com/poorants/engram/pkg/workspace"
)

// The brain-resolution surface: which brain feeds this directory, which file
// brain is designated, and the portable pointer that tells a plain session a
// brain exists at all.

func repoArg(v string) string {
	if strings.TrimSpace(v) != "" {
		return workspace.Abs(v)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// cmdResolve answers the one question every other part of engram asks first.
func cmdResolve(args []string) int {
	fs := flag.NewFlagSet("resolve", flag.ContinueOnError)
	repo := fs.String("repo", "", "resolve for this directory instead of the current one")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	r := workspace.Resolve(repoArg(*repo))
	if *asJSON {
		return emit(r)
	}
	if r.Source == workspace.SourceStore {
		// `base` here is NOT the brain, and naming it otherwise is how a session
		// ends up hand-editing a vault nobody reads.
		owner := r.Owner
		if owner == "" {
			owner = "(no git remote)"
		}
		line := fmt.Sprintf("store=%s scope=%s/%s source=store", r.Store, owner, r.Repo)
		if r.Base != "" {
			line += " fallback_vault=" + filepath.ToSlash(r.Base)
		}
		out("%s\n", line)
	} else {
		line := fmt.Sprintf("base=%s label=%s source=%s", filepath.ToSlash(r.Base), r.Label, r.Source)
		if r.Brain != "" {
			line += " brain=" + r.Brain
		}
		out("%s\n", line)
	}
	if r.Warning != "" {
		out("warning: %s\n", r.Warning)
	}
	return exitOK
}

func runBrain(args []string) int {
	if len(args) == 0 {
		return usageError("usage: engram brain show | set <path> | unset")
	}
	switch args[0] {
	case "show", "list":
		return cmdBrainShow(args[1:])
	case "set":
		return cmdBrainSet(args[1:])
	case "unset":
		return cmdBrainUnset(args[1:])
	}
	return usageError(fmt.Sprintf("unknown brain command %q — expected show, set or unset", args[0]))
}

func cmdBrainShow(args []string) int {
	fs := flag.NewFlagSet("brain show", flag.ContinueOnError)
	repo := fs.String("repo", "", "resolve for this directory instead of the current one")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cfg := config.Load()
	fileBrain := config.FileBrain()
	r := workspace.Resolve(repoArg(*repo))
	if *asJSON {
		return emit(map[string]any{
			"config": config.Path(), "store": cfg.StoreURL,
			"owners": cfg.Owners, "file_brain": fileBrain, "resolved": r,
		})
	}
	out("config:      %s\n", config.Path())
	store := cfg.StoreURL
	if store == "" {
		store = "(none — file brains only)"
	}
	out("store:       %s\n", store)
	if fileBrain == "" {
		fileBrain = "(none designated)"
	}
	out("file brain:  %s\n", fileBrain)
	if r.Source == workspace.SourceStore {
		owner := r.Owner
		if owner == "" {
			owner = "(no git remote)"
		}
		line := fmt.Sprintf("here:        source=store  scope=%s/%s", owner, r.Repo)
		if r.Base != "" {
			line += "  fallback_vault=" + filepath.ToSlash(r.Base)
		}
		out("%s\n", line)
	} else {
		out("here:        source=%s  base=%s\n", r.Source, filepath.ToSlash(r.Base))
	}
	if r.Warning != "" {
		out("warning:     %s\n", r.Warning)
	}
	return exitOK
}

// cmdBrainSet designates THE shared file brain. One per environment: two vaults
// means a coin flip about where a refused document went, so this replaces rather
// than adds.
func cmdBrainSet(args []string) int {
	fs := flag.NewFlagSet("brain set", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() < 1 {
		return usageError("a directory is required: engram brain set <path>")
	}
	dir := workspace.Abs(fs.Arg(0))
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return usageError("not a directory: " + dir)
	}
	previous := config.FileBrain()
	display := workspace.Display(dir)
	path, err := config.SetFileBrain(display)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	out("shared file brain designated: %s\n", display)
	out("  PARA base: %s\n", filepath.ToSlash(workspace.BrainBase(display)))
	out("  settings:  %s\n", path)
	if previous != "" && workspace.Display(previous) != display {
		out("  replaced: %s (the directory is left untouched)\n", previous)
	}
	if config.Load().StoreURL != "" {
		out("  note: a store is designated, so this is the FALLBACK vault — " +
			"where documents go when the store refuses a repo (403)\n")
	}
	return exitOK
}

func cmdBrainUnset(args []string) int {
	fs := flag.NewFlagSet("brain unset", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	previous := config.FileBrain()
	if previous == "" {
		out("no shared file brain designated\n")
		return exitOK
	}
	if _, err := config.UnsetFileBrain(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	out("shared file brain un-designated: %s (the directory is left untouched)\n", previous)
	return exitOK
}

// cmdLink writes the portable pointer block into this repo's CLAUDE.md.
//
// The designation itself lives only in the user-scope settings, because a
// machine-specific absolute path must never be committed. The side effect is
// that a plain session opening this repo has no signal a brain exists at all —
// which is what this block fixes.
func cmdLink(args []string) int {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	repo := fs.String("repo", "", "act on this repo instead of the current directory")
	remove := fs.Bool("remove", false, "strip the pointer block instead of writing it")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	root := workspace.GitRoot(repoArg(*repo))

	if *remove {
		state, f, err := workspace.RemovePointer(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return exitError
		}
		out("brain pointer %s: %s\n", state, filepath.ToSlash(f))
		return exitOK
	}

	r := workspace.Resolve(root)
	if r.Source == workspace.SourceNone {
		return usageError("no brain designated. Point at a store with `engram store set <url>`, " +
			"or designate a file brain with `engram brain set <path>`")
	}
	state, f, err := workspace.WritePointer(root, r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	target := fmt.Sprintf("%s (%s)", r.Brain, filepath.ToSlash(r.Base))
	if r.Source == workspace.SourceStore {
		owner := r.Owner
		if owner == "" {
			owner = "?"
		}
		target = fmt.Sprintf("store %s (%s/%s)", r.Store, owner, r.Repo)
	} else if r.Brain == "" {
		target = fmt.Sprintf("%s (%s)", r.Label, filepath.ToSlash(r.Base))
	}
	out("brain pointer %s: %s (-> %s)\n", state, filepath.ToSlash(f), target)
	return exitOK
}

// cmdInit creates the PARA folders of a file brain.
func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	output := fs.String("output", ".", "base directory")
	flat := fs.Bool("flat", false, "create the categories at the base, with no nested prefix")
	nested := fs.String("nested-dir", "", "nested folder name (default: brain; reuses a legacy para/ if present)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	base := workspace.Abs(*output)
	created, err := vault.InitPARA(base, *flat, *nested)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	mode := "nested"
	if *flat {
		mode = "flat"
	}
	out("PARA structure initialized (%s):\n", mode)
	for _, p := range created {
		out("  %s\n", p)
	}
	return exitOK
}
