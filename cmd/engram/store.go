package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/poorants/engram/pkg/brain"
	"github.com/poorants/engram/pkg/config"
	"github.com/poorants/engram/pkg/identity"
)

// `engram store …` is setup, and setup is a DETERMINISTIC act. Every command
// here either succeeds or says exactly which half is missing; none of them
// leaves a half-configured install that looks fine until the first write.
//
// That is also why `doctor` checks the write token rather than only the
// connection: "the store is up" and "I can write to it" are different facts,
// and a check that proves only the first one lets someone finish a setup
// read-only and discover it at the end of a session, when a save fails.

const storeUsage = `usage: engram store <command>

  set <url>       designate the store
                  --token <t>   write token (stored at 0600 beside the config)
                  --author <a>  the byline stamped on revisions from this machine
  show            where the settings come from, without touching the network
  doctor          prove the store answers and this machine can write to it
  unset           remove the designation (--forget-token also deletes the token)
`

func runStore(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, storeUsage)
		return exitError
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "set":
		return storeSet(rest)
	case "show":
		return storeShow(rest)
	case "doctor":
		return storeDoctor(rest)
	case "unset":
		return storeUnset(rest)
	case "-h", "--help", "help":
		fmt.Print(storeUsage)
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "error: unknown store command %q\n\n", verb)
	fmt.Fprint(os.Stderr, storeUsage)
	return exitError
}

func storeSet(args []string) int {
	fs := flag.NewFlagSet("store set", flag.ContinueOnError)
	token := fs.String("token", "", "write token")
	author := fs.String("author", "", "byline stamped on revisions from this machine")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return exitError
	}
	if len(pos) < 1 {
		return usageError("a store address is required, e.g. engram store set http://brain.example:8081")
	}
	url, err := config.NormalizeURL(pos[0])
	if err != nil {
		return usageError(err.Error())
	}

	// Ask the store which owner groups it admits and cache the answer, so
	// everything afterwards — including hooks, which must not touch the network
	// — can report scope offline. A store that cannot be reached right now is
	// not a reason to refuse the designation: the address may be correct and the
	// service simply not up yet.
	var owners []string
	c := brain.New(brain.Config{BaseURL: url, Token: strings.TrimSpace(*token)})
	ctx := context.Background()
	scopes, scopeErr := c.StoreScopes(ctx)
	if scopeErr == nil {
		owners = scopes.AllowedOwners
	}

	path, err := config.SetStore(url, owners)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	fmt.Printf("store:  %s\n", url)
	fmt.Printf("config: %s\n", path)
	if scopeErr != nil {
		fmt.Printf("note:   could not read the allowed owner groups yet (%v) — scope will be decided by the store's own answer\n", scopeErr)
	} else if len(owners) > 0 {
		fmt.Printf("owners: %s\n", strings.Join(owners, ", "))
	}

	if strings.TrimSpace(*author) != "" {
		if _, err := config.SetAuthor(*author); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return exitError
		}
		fmt.Printf("author: %s\n", strings.TrimSpace(*author))
	}

	if strings.TrimSpace(*token) == "" {
		fmt.Println("token:  not set — reads work, writes will be refused. Re-run with --token to enable writing.")
		return exitOK
	}
	tp, err := config.WriteToken(*token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	fmt.Printf("token:  %s (0600)\n", tp)
	fmt.Println("\nnext: engram store doctor")
	return exitOK
}

func storeShow(args []string) int {
	fs := flag.NewFlagSet("store show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cfg := config.Load()
	out := map[string]any{
		"config":   cfg.Path,
		"store":    cfg.StoreURL,
		"canWrite": cfg.Token != "",
		"author":   cfg.Author,
		"owners":   cfg.Owners,
		"fromEnv":  cfg.FromEnv,
	}
	if *asJSON {
		return emit(out)
	}
	fmt.Printf("config: %s\n", cfg.Path)
	if cfg.StoreURL == "" {
		fmt.Println("store:  (none) — run `engram store set <url>`")
	} else {
		fmt.Printf("store:  %s\n", cfg.StoreURL)
	}
	fmt.Printf("token:  %s (%s)\n", yesNo(cfg.Token != ""), config.TokenPath())
	if cfg.Author != "" {
		fmt.Printf("author: %s (configured)\n", cfg.Author)
	} else {
		fmt.Println("author: (resolved automatically — git config user.name, then the OS user)")
	}
	if len(cfg.Owners) > 0 {
		fmt.Printf("owners: %s (cached from the store)\n", strings.Join(cfg.Owners, ", "))
	}
	if len(cfg.FromEnv) > 0 {
		fmt.Printf("env:    %s overrode the file\n", strings.Join(cfg.FromEnv, ", "))
	}
	return exitOK
}

func yesNo(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}

// storeDoctor walks the whole path a real call takes and reports each step, so
// a failure names the step rather than the symptom.
func storeDoctor(args []string) int {
	fs := flag.NewFlagSet("store doctor", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	cfg := config.Load()
	ctx := context.Background()

	fmt.Printf("config    %s\n", cfg.Path)
	if cfg.StoreURL == "" {
		fmt.Println("store     NOT SET")
		fmt.Println("\nfix: engram store set <url> --token <write token>")
		return exitError
	}
	fmt.Printf("store     %s\n", cfg.StoreURL)

	c := brain.New(cfg.Brain())
	h, err := c.Healthz(ctx)
	if err != nil {
		fmt.Printf("reach     FAILED — %v\n", err)
		fmt.Println("\nThe address may be wrong, or the service is not up. On the machine hosting it:")
		fmt.Println("  docker compose ps && docker compose logs --tail 50 app")
		return exitStoreOut
	}
	if !h.OK {
		fmt.Println("reach     ok, but the store cannot reach its database")
		return exitStoreOut
	}
	fmt.Printf("reach     ok — %d documents indexed\n", h.Docs)

	scopes, err := c.StoreScopes(ctx)
	if err != nil {
		fmt.Printf("owners    could not read (%v)\n", err)
	} else if len(scopes.AllowedOwners) == 0 {
		fmt.Println("owners    NONE — the store admits nothing, so every write will be refused.")
		fmt.Println("          Set ENGRAM_OWNERS in the server's .env and restart it.")
	} else {
		fmt.Printf("owners    %s\n", strings.Join(scopes.AllowedOwners, ", "))
	}

	if owner, repo, err := repoScope(); err == nil {
		admitted := false
		for _, o := range scopes.AllowedOwners {
			if o == owner {
				admitted = true
				break
			}
		}
		verdict := "NOT admitted — documents from this repo stay in a local file brain"
		if admitted {
			verdict = "admitted — documents from this repo go to the store"
		}
		fmt.Printf("here      %s/%s — %s\n", owner, repo, verdict)
	} else {
		fmt.Printf("here      no git origin — %v\n", err)
	}

	author := identity.New(cfg.Author, nil).Author(ctx, "")
	fmt.Printf("author    %s\n", author)

	if !c.CanWrite() {
		fmt.Println("write     NO TOKEN — reads work, every write will be refused.")
		fmt.Println("\nfix: engram store set " + cfg.StoreURL + " --token <write token>")
		return exitError
	}
	if err := c.VerifyToken(ctx); err != nil {
		fmt.Printf("write     REJECTED — %v\n", err)
		fmt.Println("\nThe token does not match the server's ENGRAM_INGEST_TOKEN.")
		return exitError
	}
	fmt.Println("write     ok — the token is accepted")
	return exitOK
}

func storeUnset(args []string) int {
	fs := flag.NewFlagSet("store unset", flag.ContinueOnError)
	forget := fs.Bool("forget-token", false, "also delete the stored write token")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	path, err := config.UnsetStore(*forget)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	fmt.Printf("store designation removed (%s)\n", path)
	if *forget {
		fmt.Println("write token deleted")
	}
	return exitOK
}
