//go:build !noupdate

package selfupdate

// The other half of an update: not "a newer release exists" but "put it here".
//
// This is what install.sh and install.ps1 do, moved into the binary so that
// the machine hearing the notice can act on it with one verb — and so that a
// model in a session, which is the surface that actually reads the notice,
// can run that verb rather than relay a curl one-liner to a person.
//
// Everything here is transport-free and testable: the caller downloads, these
// functions name the asset, check the hash, unpack the binary and swap it in.
// The swap is by rename, never in place: a running process keeps the file it
// opened, on Unix by inode and on Windows by the old name moving aside, so an
// update while the MCP server is serving costs nothing — the next session
// starts the new binary.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// releaseTag is a clean version tag. A development build — `dev`, or
// v0.5.0-4-g61f71fb(-dirty) from git describe — is deliberately not one:
// replacing it with "the latest release" may be a downgrade, and that is a
// decision to make, not to have made.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// IsRelease reports whether v is a release tag rather than a development build.
func IsRelease(v string) bool { return releaseTag.MatchString(strings.TrimSpace(v)) }

// AssetName is the release asset for a platform, spelled exactly as `make dist`
// writes it and the installers read it. Windows ships as a zip because that is
// what Windows can open without a third tool; everything else is a tarball.
func AssetName(tag, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("engram_%s_windows_%s.zip", tag, goarch)
	}
	return fmt.Sprintf("engram_%s_%s_%s.tar.gz", tag, goos, goarch)
}

// BinaryName is the executable's file name on a platform.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "engram.exe"
	}
	return "engram"
}

// Checksum finds asset's expected sha256 in a SHA256SUMS file (the
// `sha256sum` format: hash, two spaces, name). An asset the file does not list
// is refused rather than installed unchecked — a release whose checksum list
// is out of step with its assets is exactly the release not to trust.
func Checksum(sums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in SHA256SUMS — refusing to install an unverified asset", asset)
}

// Verify checks data against the sha256 hex digest want.
func Verify(data []byte, want string) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != strings.ToLower(strings.TrimSpace(want)) {
		return fmt.Errorf("checksum mismatch: downloaded %s, release says %s — the download is corrupt or tampered with", got[:12], want[:12])
	}
	return nil
}

// ExtractBinary pulls the executable out of a release asset — a .tar.gz or a
// .zip — by the name the platform uses. Anything else in the archive is
// ignored; anything missing is an error.
func ExtractBinary(asset string, data []byte, goos string) ([]byte, error) {
	want := BinaryName(goos)
	switch {
	case strings.HasSuffix(asset, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s is not a zip archive: %w", asset, err)
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) != want {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	case strings.HasSuffix(asset, ".tar.gz"):
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s is not a gzip archive: %w", asset, err)
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == want {
				return io.ReadAll(tr)
			}
		}
	default:
		return nil, fmt.Errorf("%s: not an archive this binary knows how to open", asset)
	}
	return nil, fmt.Errorf("%s does not contain %s", asset, want)
}

// Replaced says what Replace did — where the new binary is, and where the old
// one was parked (Windows only; "" elsewhere).
type Replaced struct {
	Path   string
	Parked string
}

// Replace installs bin as the executable at exe.
//
// The new file is written beside the old one first (same directory, so the
// final step is a rename on one filesystem, which is atomic) and only then
// swapped in. On Unix the rename replaces the directory entry and a running
// process keeps its open inode. Windows refuses to unlink a running
// executable but allows renaming it, so there the old file moves aside to
// `<exe>.old` and the new one takes the name; a swap that fails halfway puts
// the old one back, so a failed update costs the new version and never the
// working one. Parked copies from earlier updates are swept first — the ones
// a process still holds simply stay until next time.
//
// park is exposed for tests; callers pass runtime.GOOS == "windows".
func Replace(exe string, bin []byte, park bool) (Replaced, error) {
	exe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return Replaced{}, fmt.Errorf("could not resolve the executable's path: %w", err)
	}
	fresh := exe + ".new"
	if err := os.WriteFile(fresh, bin, 0o755); err != nil {
		return Replaced{}, fmt.Errorf("could not write the new binary beside the old one: %w", err)
	}
	if !park {
		if err := os.Rename(fresh, exe); err != nil {
			_ = os.Remove(fresh)
			return Replaced{}, fmt.Errorf("could not move the new binary into place: %w", err)
		}
		return Replaced{Path: exe}, nil
	}

	// Sweep what earlier updates parked, then pick a name nothing holds.
	if old, _ := filepath.Glob(exe + ".old*"); len(old) > 0 {
		for _, p := range old {
			_ = os.Remove(p)
		}
	}
	parked := exe + ".old"
	if _, err := os.Stat(parked); err == nil {
		parked = fmt.Sprintf("%s.old-%d", exe, time.Now().UnixNano()%1_000_000)
	}
	if _, err := os.Stat(exe); err == nil {
		if err := os.Rename(exe, parked); err != nil {
			_ = os.Remove(fresh)
			return Replaced{}, fmt.Errorf("could not move the running binary aside: %w", err)
		}
	}
	if err := os.Rename(fresh, exe); err != nil {
		// Put the old one back: a failed update must leave the machine as it was.
		_ = os.Rename(parked, exe)
		_ = os.Remove(fresh)
		return Replaced{}, fmt.Errorf("could not move the new binary into place (the old one was restored): %w", err)
	}
	return Replaced{Path: exe, Parked: parked}, nil
}
