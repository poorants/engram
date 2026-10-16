package main

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The setup prompt is `/mcp__engram__setup`. Once a person has registered this
// server and logged in, one slash command finishes the machine: it checks the
// registration and installs the plugin (the skill and the capture-loop hooks);
// the plugin installs the engram binary those hooks run by itself.
//
// The procedure lives here, on the server, rather than in a README or an
// installer, so every machine reads the same current version of it — and it is
// written with this server's own address in it, which nothing on a fresh
// machine knows yet.
const setupPromptTemplate = `Set up this machine for engram, the shared brain served at {{ISSUER}}/mcp. Work through the steps in order, run each check, and fix only what is missing. Show me what you ran and what you found. Never ask me to paste a secret, a token or a password into this chat, and never print one.

1. The MCP registration. Run ` + "`claude mcp list`" + ` and confirm that "engram" is registered as an HTTP server at {{ISSUER}}/mcp and connected.
   - If it is registered as a local (stdio) command such as ` + "`engram mcp`" + ` — the client of engram 0.11 and earlier — stop and tell me to replace it:
       claude mcp remove engram -s user
       claude mcp add --transport http --scope user --callback-port 33418 engram {{ISSUER}}/mcp
     then /mcp → engram → Authenticate. On a machine reached over SSH, the login's browser callback needs a tunnel first: ssh -L 33418:localhost:33418 <this machine>.
   - If it is HTTP but shows "needs authentication", tell me to run /mcp → engram → Authenticate. If it keeps saying so after a successful login, Claude Code may be caching the old answer in ~/.claude/mcp-needs-auth-cache.json: tell me to remove the engram entry there (or the file) and restart Claude Code.
   You are reading this prompt, so the server is reachable; a failure here is the registration, not the store.

2. The plugin (the engram skill and the capture-loop hooks).
       claude plugin marketplace add https://github.com/poorants/engram    (an "already added" error is fine)
       claude plugin marketplace update engram
       claude plugin install engram@engram --scope user    (claude plugin update engram@engram if it is already installed)

3. The engram binary the hooks run (` + "`engram hook`" + `). Nothing to install: since engram 0.15.0 the plugin installs it itself — its hooks go through bin/engram-launch, which on session start puts the release pinned in the plugin's engram.version into the plugin's data directory, and a plugin update moves every machine to the next pin. It needs git (which brings sh, curl, tar and unzip on Windows) and curl on PATH; check ` + "`git --version`" + ` and ` + "`curl --version`" + `.
   An ` + "`engram`" + ` on PATH from the old installer (install.sh / install.ps1) is only a fallback now. Leave it, but tell me if ` + "`engram version`" + ` reports one older than 0.14.0 so I can remove it. If it warns about leftovers from an older client (~/.claude/engram/config.json, store.token), tell me; do not delete them yourself.

4. Report a short table with one row per step and ok / fixed / needs you, and finish by telling me to restart Claude Code so the plugin's skill and hooks load.`

// setupPrompt is the procedure with this server's issuer written in.
func setupPrompt(issuer string) string {
	return strings.ReplaceAll(setupPromptTemplate, "{{ISSUER}}", strings.TrimRight(issuer, "/"))
}

func addSetupPrompt(server *mcp.Server, issuer string) {
	text := setupPrompt(issuer)
	server.AddPrompt(&mcp.Prompt{
		Name:        "setup",
		Description: "Finish setting up this machine for engram: check the MCP registration, install the plugin (skill + hooks), which installs the engram binary its hooks run",
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "engram machine setup",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}
