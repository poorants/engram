// Command engram is the single binary for an engram brain: an MCP server for a
// model in a session, and a CLI for everything that is not one — hooks,
// scripts, scheduled jobs, and a person at a terminal.
//
// One binary rather than two is the point, and it is now one binary rather than
// a binary plus an interpreter. When the transport lived in two programs, the
// cost was two credential files, two default authors, and two copies of the path
// rules to drift apart. When the skill's helpers and the capture-loop hooks were
// Python, the cost was steeper and quieter: on Windows `python3` is not a command
// even where Python is installed — the App Execution Alias of that name opens the
// Microsoft Store and exits — so the hooks were silently dead on every Windows
// machine, and nothing said so.
//
// So there are three surfaces over one client and one settings file, and nothing
// else to install: `engram mcp` for the session, `engram <verb>` for a
// subprocess, and the plugin's hook command, which is `engram hook`.
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// version is stamped at build time:
//
//	go build -ldflags "-X main.version=v0.1.0"
//
// Left as "dev" for a plain `go build`, and filled in from the module's build
// info when installed with `go install ...@v0.1.0`.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

const usage = `engram — a networked PARA knowledge brain for coding agents

usage: engram <command> [options]

  mcp                     run the MCP server over stdio (what a session launches)

the store
  search <question>       search the store — pass the question as a sentence
                          --limit --chars --archives --boost-repo --only-repo --only-owner
  get <path>              print one document
  put <path>              save a document (--file, or stdin; --note required)
  move <path> <new path>  move a document (the old path stays as an alias)
  revisions <path>        change history
  integrity               broken links, orphans, weak nodes
  status                  connection, scope, and who you write as
  scope                   owner/repo derived from this directory's git origin

  store set <url>         designate the store (--token to enable writing)
  store show              where the settings come from
  store doctor            prove the store answers, end to end
  store unset             remove the designation (--forget-token)

a file brain
  resolve                 which brain feeds this directory, and why
  brain show              the designations, and how here resolves
  brain set <path>        designate THE shared file brain (replaces any previous)
  brain unset             remove the designation (the directory is left alone)
  init                    create the PARA folders (--output --flat --nested-dir)
  lint                    broken links, orphans, weak nodes, density (--all --base)
  weave                   the concrete links that would raise the density (--base)
  link                    write this repo's CLAUDE.md brain pointer (--remove)

  hook                    the capture-loop hook — reads a Claude Code hook
                          payload on stdin. Registered by the plugin; not
                          something to run by hand.

  version                 print the version

Every command prints for a person and takes --json for a machine.

A document path is <owner>/<repo>/<area>/<name>.md, where area is one of
projects|areas|resources|archives; a repo hub MOC is <owner>/<repo>/README.md.
Given as './<area>/<name>.md' the coordinates are filled in from the current
directory's git origin.

exit codes: 0 ok · 1 error · 3 the store refused this path's owner and no local
file brain took it · 4 store unreachable
`

const (
	exitOK       = 0
	exitError    = 1
	exitRefused  = 3 // the store declined this owner group AND nothing local caught it
	exitStoreOut = 4 // the store could not be reached at all
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return exitError
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "mcp":
		return runMCP(rest)
	case "hook":
		return cmdHook(rest)
	case "search":
		return cmdSearch(rest)
	case "get":
		return cmdGet(rest)
	case "put":
		return cmdPut(rest)
	case "move":
		return cmdMove(rest)
	case "revisions":
		return cmdRevisions(rest)
	case "integrity":
		return cmdIntegrity(rest)
	case "status":
		return cmdStatus(rest)
	case "scope":
		return cmdScope(rest)
	case "store":
		return runStore(rest)
	case "resolve":
		return cmdResolve(rest)
	case "brain":
		return runBrain(rest)
	case "init":
		return cmdInit(rest)
	case "lint":
		return cmdLint(rest)
	case "weave":
		return cmdWeave(rest)
	case "link":
		return cmdLink(rest)
	case "version", "--version", "-v":
		fmt.Printf("engram %s (%s/%s, %s)\n", resolveVersion(), runtime.GOOS, runtime.GOARCH, runtime.Version())
		return exitOK
	case "help", "-h", "--help":
		fmt.Print(usage)
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", verb)
	fmt.Fprint(os.Stderr, usage)
	return exitError
}
