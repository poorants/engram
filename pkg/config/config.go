// Package config resolves where the engram store is and who is writing to it.
//
// One file holds the settings, and TWO programs read it: this binary and the
// engram skill's Python helpers. That is deliberate — when the address lived in
// one place and the token in another, "engram works but cannot write" became a
// question nobody could answer without opening two files. The file is plain
// JSON so a person can read it, and the token is NOT in it: config.json is a
// file people open and paste from, and a secret in it eventually gets copied
// somewhere it should not be.
//
//	<config dir>/engram/config.json   settings (address, cached owner list, author)
//	<config dir>/engram/store.token   the store's token, mode 0600
//
// <config dir> is $ENGRAM_CONFIG_DIR, else $CLAUDE_CONFIG_DIR, else ~/.claude —
// the same ladder the skill's workspace.py walks, so both halves land on one
// file without either one being told where the other put it.
//
// Environment beats file for every value, so a CI job or a one-off shell can
// point at a different store without editing anything:
//
//	ENGRAM_STORE_URL   the store origin
//	ENGRAM_TOKEN       the store's token (it authorises reads as well)
//	ENGRAM_AUTHOR      the byline stamped on revisions
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/poorants/engram/pkg/brain"
)

// FileName and TokenName are the two files, side by side.
const (
	FileName  = "config.json"
	TokenName = "store.token"
)

// Config is the resolved settings — what the binary actually acts on.
type Config struct {
	// StoreURL is the store origin. Empty means unconfigured, and every call
	// then fails with brain.ErrNoStore rather than against a built-in address
	// that exists on one network and nowhere else.
	StoreURL string
	// Token is the store's one shared credential — it authorises reads as well
	// as writes. Empty is a legitimate setup, but a limited one: writes refuse,
	// and reads reach only a store serving public ones.
	Token string
	// Author is an explicit byline (ENGRAM_AUTHOR or the config file). Empty
	// means "resolve it" — see package identity for the order.
	Author string
	// Owners is the store's allow-list as cached by the last `store set`. It is
	// a convenience for offline scope reporting, never an authority: the store
	// decides, and it answers 403 when it disagrees.
	Owners []string

	// Path is the config file consulted (reported by `engram store show`),
	// whether or not it exists.
	Path string
	// FromEnv names the values the environment overrode, so a surprising
	// address can be traced without guessing.
	FromEnv []string
}

// Brain projects the settings onto the transport's own config type.
func (c Config) Brain() brain.Config {
	return brain.Config{BaseURL: c.StoreURL, Token: c.Token}
}

// Dir is the directory holding config.json and store.token.
func Dir() string {
	if v := strings.TrimSpace(os.Getenv("ENGRAM_CONFIG_DIR")); v != "" {
		return filepath.Join(expandHome(v), "engram")
	}
	if v := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); v != "" {
		return filepath.Join(expandHome(v), "engram")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".claude", "engram")
}

// Path is the config file's location.
func Path() string { return filepath.Join(Dir(), FileName) }

// TokenPath is the token file's location.
func TokenPath() string { return filepath.Join(Dir(), TokenName) }

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// raw is the on-disk document. It is read as a free-form map rather than a
// struct because the skill keeps its own keys in the same file (the file-brain
// registry it falls back to when the store refuses a repo). Writing through a
// struct would silently drop them.
type raw map[string]any

func readRaw(path string) raw {
	b, err := os.ReadFile(path)
	if err != nil {
		return raw{}
	}
	var out raw
	if err := json.Unmarshal(b, &out); err != nil {
		// A corrupt config must not stop a read-only command that the
		// environment could still satisfy. Report it where it matters: `store
		// show` and `store doctor` both surface an empty resolution plainly.
		return raw{}
	}
	return out
}

func (r raw) section(key string) map[string]any {
	if m, ok := r[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// Load resolves the settings: file first, environment on top.
func Load() Config {
	path := Path()
	doc := readRaw(path)
	store := doc.section("store")

	cfg := Config{
		StoreURL: strings.TrimRight(str(store, "url"), "/"),
		Author:   str(doc, "author"),
		Path:     path,
	}
	if owners, ok := store["owners"].([]any); ok {
		for _, o := range owners {
			if s, ok := o.(string); ok && strings.TrimSpace(s) != "" {
				cfg.Owners = append(cfg.Owners, strings.TrimSpace(s))
			}
		}
	}
	if b, err := os.ReadFile(TokenPath()); err == nil {
		cfg.Token = strings.TrimSpace(string(b))
	}

	if v := strings.TrimSpace(os.Getenv("ENGRAM_STORE_URL")); v != "" {
		cfg.StoreURL = strings.TrimRight(v, "/")
		cfg.FromEnv = append(cfg.FromEnv, "ENGRAM_STORE_URL")
	}
	if v := strings.TrimSpace(os.Getenv("ENGRAM_TOKEN")); v != "" {
		cfg.Token = v
		cfg.FromEnv = append(cfg.FromEnv, "ENGRAM_TOKEN")
	}
	if v := strings.TrimSpace(os.Getenv("ENGRAM_AUTHOR")); v != "" {
		cfg.Author = v
		cfg.FromEnv = append(cfg.FromEnv, "ENGRAM_AUTHOR")
	}
	return cfg
}

// NormalizeURL reduces a user-typed address to the origin the store's API hangs
// off (scheme://host[:port]). A pasted deep link — the viewer's /doc/... page is
// the one people have open when they run this — otherwise becomes a base URL
// that appends to itself.
func NormalizeURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("the store address is empty")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("not a usable address (%q): %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("the store address must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("the store address has no host: %q", s)
	}
	return u.Scheme + "://" + u.Host, nil
}

// SetStore writes the address (and, when non-empty, the cached owner list) into
// config.json, preserving every key this binary does not own.
func SetStore(storeURL string, owners []string) (string, error) {
	path := Path()
	doc := readRaw(path)
	store := doc.section("store")
	store["url"] = storeURL
	if len(owners) > 0 {
		store["owners"] = owners
	}
	doc["store"] = store
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	return path, write(path, doc)
}

// SetAuthor records the byline in the config file. Empty clears it.
func SetAuthor(author string) (string, error) {
	path := Path()
	doc := readRaw(path)
	if strings.TrimSpace(author) == "" {
		delete(doc, "author")
	} else {
		doc["author"] = strings.TrimSpace(author)
	}
	return path, write(path, doc)
}

// UnsetStore removes the store designation, leaving the rest of the file alone.
func UnsetStore(forgetToken bool) (string, error) {
	path := Path()
	doc := readRaw(path)
	delete(doc, "store")
	if err := write(path, doc); err != nil {
		return path, err
	}
	if forgetToken {
		if err := os.Remove(TokenPath()); err != nil && !os.IsNotExist(err) {
			return path, err
		}
	}
	return path, nil
}

// WriteToken stores the store's token beside the config at 0600. It is named
// for what it does to the file, not for what the token permits.
func WriteToken(token string) (string, error) {
	p := TokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return p, err
	}
	return p, os.WriteFile(p, []byte(strings.TrimSpace(token)+"\n"), 0o600)
}

func write(path string, doc raw) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// --- the file brain ---------------------------------------------------------
//
// The `brains` section used to be owned by the skill's workspace.py and read by
// nothing else. It moved here when the skill's helpers did, and the split it
// was protecting still holds — it is just no longer a split between two
// languages. What the store owns (`store`) and what the file vault owns
// (`brains`) are still separate keys, written by separate calls, so neither
// erases the other.

// FileBrainName is the fixed key of THE one shared file brain per environment.
// Designating replaces rather than adds: two vaults means a coin flip about
// where a refused document went.
const FileBrainName = "shared"

// FileBrain is the designated shared file brain's container directory, or "".
func FileBrain() string {
	doc := readRaw(Path())
	brains := doc.section("brains")
	if len(brains) == 0 {
		return ""
	}
	if m, ok := brains[FileBrainName].(map[string]any); ok {
		return str(m, "path")
	}
	// A brain registered under some other name still counts — an older
	// designation must not silently stop resolving.
	for _, v := range brains {
		if m, ok := v.(map[string]any); ok {
			if p := str(m, "path"); p != "" {
				return p
			}
		}
	}
	return ""
}

// SetFileBrain designates the shared file brain, replacing any previous one and
// preserving the store section.
func SetFileBrain(dir string) (string, error) {
	path := Path()
	doc := readRaw(path)
	doc["brains"] = map[string]any{FileBrainName: map[string]any{"path": dir}}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	return path, write(path, doc)
}

// UnsetFileBrain removes the designation. The directory is left untouched — a
// settings change must never delete somebody's notes.
func UnsetFileBrain() (string, error) {
	path := Path()
	doc := readRaw(path)
	delete(doc, "brains")
	return path, write(path, doc)
}
