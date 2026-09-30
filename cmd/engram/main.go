// Command engram is the binary on both ends of an engram brain that is reached
// only over MCP.
//
// On the host next to the store it is `engram serve`: the remote MCP server,
// with its own OAuth, and the one process that holds the store credential. On
// a person's machine it is `engram hook`, the capture-loop hook the plugin
// registers — and nothing else. That machine has no store address, no token
// and no settings file: a session reaches the brain through the `engram` MCP
// registration, and the hook decides whether to speak from the git origin
// alone.
//
// Until 0.11 there was also a CLI over the store, a stdio MCP server, a local
// file brain and a self-updater here. Each was a second way to do what the
// remote MCP server now does once, for everyone, so they were removed.
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

  serve     run the remote MCP server next to the store (engram serve --help)
  hook      the capture-loop hook — reads a Claude Code hook payload on stdin.
            Registered by the plugin; not something to run by hand.
  version   print the version
  help      print this text

A session reaches the brain only through the remote MCP server:

  claude mcp add --transport http --scope user --callback-port 33418 \
    engram https://<host>/mcp

then /mcp → engram → Authenticate, and run /mcp__engram__setup in Claude Code
to install the plugin and this binary for the hooks.
`

const (
	exitOK    = 0
	exitError = 1
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return exitError
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "serve":
		return cmdServe(rest)
	case "hook":
		return cmdHook(rest)
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
