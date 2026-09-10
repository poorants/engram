//go:build !noupdate

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/poorants/engram/pkg/selfupdate"
)

// `engram update` — the verb the update notice names.
//
// It does what the installers do (pick the asset for this platform, check it
// against SHA256SUMS, swap it in by rename) from inside the binary, so that
// the machine hearing "0.6.0 is out" can act with one command — and so that
// the model in a session, which is who actually reads the notice, can run it
// instead of relaying a one-liner. The settings, the token and the store are
// untouched; the running MCP server keeps serving from the old file and the
// next session starts the new one.
//
// A development build is not updated without --force. `make install` leaves
// a v0.5.0-4-gabc build that is NEWER than the latest release, and "update"
// quietly replacing it with the release would be a downgrade wearing the
// right name.

// releaseBase is where the assets live — the same place install.sh downloads
// from, under the same repo the notice checks.
const releaseBase = "https://github.com/" + updateRepo + "/releases/download/"

// downloadTimeout bounds one asset. The binaries are ~8 MB; a minute is
// generous on any connection that is going to succeed at all.
const downloadTimeout = 90 * time.Second

func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	version := fs.String("version", "", "install this release tag (default: the latest)")
	force := fs.Bool("force", false, "replace a development build, or reinstall the current version")
	check := fs.Bool("check", false, "say what would happen and do nothing")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	current := resolveVersion()
	target := strings.TrimSpace(*version)
	if target == "" {
		tag, err := latestReleaseTag(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not find the latest release: %v\n", err)
			return exitStoreOut
		}
		target = tag
	}
	if !strings.HasPrefix(target, "v") {
		target = "v" + target
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: could not find this binary's own path:", err)
		return exitError
	}
	report := map[string]any{"current": current, "target": target, "path": exe, "updated": false}

	switch {
	case target == current && !*force:
		report["status"] = "current"
		if *asJSON {
			return emit(report)
		}
		out("engram %s is the latest release — nothing to do\n", current)
		return exitOK
	case !selfupdate.IsRelease(current) && !*force:
		report["status"] = "development build"
		if *asJSON {
			return emit(report)
		}
		out("this is a development build (%s), not a release; the latest release is %s.\n"+
			"Replacing it may be a downgrade. Pass --force to do it anyway.\n", current, target)
		return exitError
	}
	if *check {
		report["status"] = "would update"
		if *asJSON {
			return emit(report)
		}
		out("engram %s → %s would be installed at %s\n", current, target, exe)
		return exitOK
	}

	asset := selfupdate.AssetName(target, runtime.GOOS, runtime.GOARCH)
	if !*asJSON {
		out("engram %s → %s (%s/%s)\n", current, target, runtime.GOOS, runtime.GOARCH)
	}
	sums, err := fetch(ctx, releaseBase+target+"/SHA256SUMS")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not download the checksum list for %s: %v\n", target, err)
		return exitStoreOut
	}
	want, err := selfupdate.Checksum(sums, asset)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	data, err := fetch(ctx, releaseBase+target+"/"+asset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: could not download %s: %v\n", asset, err)
		return exitStoreOut
	}
	if err := selfupdate.Verify(data, want); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	bin, err := selfupdate.ExtractBinary(asset, data, runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}
	done, err := selfupdate.Replace(exe, bin, runtime.GOOS == "windows")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitError
	}

	// Prove it: the file under the old name answers with the new version. A
	// swap that "worked" and left an unrunnable binary is the one failure
	// worth catching here, because the next thing to run it is a session.
	got, err := exec.Command(done.Path, "version").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: the new binary at %s does not run: %v\n", done.Path, err)
		return exitError
	}
	report["updated"] = true
	report["status"] = "updated"
	report["version_output"] = strings.TrimSpace(string(got))
	if done.Parked != "" {
		report["parked"] = done.Parked
	}
	if *asJSON {
		return emit(report)
	}
	out("installed: %s\n%s\n", done.Path, strings.TrimSpace(string(got)))
	out("The settings, the token and the store are untouched. A running MCP server keeps the old\n" +
		"binary until its session ends; start a new session to use this one.\n")
	return exitOK
}

// fetch downloads one URL whole. Assets are small enough that streaming them
// buys nothing, and the checksum needs the whole thing anyway.
func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "engram/"+resolveVersion())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
