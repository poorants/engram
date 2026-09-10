//go:build !noupdate

package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIsReleaseTellsATagFromADevelopmentBuild(t *testing.T) {
	for _, v := range []string{"v0.5.0", "v1.12.3", " v0.6.0 "} {
		if !IsRelease(v) {
			t.Errorf("%q must be a release", v)
		}
	}
	for _, v := range []string{"dev", "v0.5.0-4-g61f71fb", "v0.5.0-dirty", "0.5.0", ""} {
		if IsRelease(v) {
			t.Errorf("%q must NOT be a release — replacing it is a decision, not a default", v)
		}
	}
}

func TestAssetNameMatchesWhatMakeDistWrites(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}:   "engram_v0.6.0_linux_amd64.tar.gz",
		{"darwin", "arm64"}:  "engram_v0.6.0_darwin_arm64.tar.gz",
		{"windows", "amd64"}: "engram_v0.6.0_windows_amd64.zip",
	}
	for k, want := range cases {
		if got := AssetName("v0.6.0", k[0], k[1]); got != want {
			t.Errorf("AssetName(%v) = %q, want %q", k, got, want)
		}
	}
}

func TestChecksumFindsTheAssetAndRefusesAnUnlistedOne(t *testing.T) {
	sums := []byte("abc123  engram_v0.6.0_linux_amd64.tar.gz\nDEF456  engram_v0.6.0_windows_amd64.zip\n")
	got, err := Checksum(sums, "engram_v0.6.0_windows_amd64.zip")
	if err != nil || got != "def456" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := Checksum(sums, "engram_v0.6.0_darwin_arm64.tar.gz"); err == nil {
		t.Fatal("an asset missing from SHA256SUMS must be refused, not installed unchecked")
	}
}

func TestVerifyAcceptsTheRightHashAndNothingElse(t *testing.T) {
	data := []byte("the binary")
	sum := sha256.Sum256(data)
	if err := Verify(data, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := Verify(data, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a wrong hash must be refused")
	}
}

func targz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipped(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractBinaryFromBothArchiveShapes(t *testing.T) {
	bin := []byte("#!/bin/sh\necho engram\n")
	got, err := ExtractBinary("engram_v0.6.0_linux_amd64.tar.gz", targz(t, "engram", bin), "linux")
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("tar.gz: %v %q", err, got)
	}
	got, err = ExtractBinary("engram_v0.6.0_windows_amd64.zip", zipped(t, "engram.exe", bin), "windows")
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("zip: %v %q", err, got)
	}
	// The wrong name inside the archive is a missing binary, not a near miss.
	if _, err := ExtractBinary("x.tar.gz", targz(t, "README.md", bin), "linux"); err == nil {
		t.Fatal("an archive without the binary must be refused")
	}
	if _, err := ExtractBinary("x.rar", bin, "linux"); err == nil {
		t.Fatal("an unknown archive type must be refused")
	}
}

func TestReplaceByRenameLeavesTheNewBinaryUnderTheOldName(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "engram")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Replace(exe, []byte("new"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Fatalf("binary = %q", got)
	}
	if r.Parked != "" {
		t.Errorf("nothing is parked on a rename-over platform, got %q", r.Parked)
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error("the staging file must not be left behind")
	}
	st, _ := os.Stat(exe)
	if st.Mode()&0o111 == 0 {
		t.Error("the new binary must be executable")
	}
}

func TestReplaceByParkingKeepsTheOldBinaryAsideAndSweepsEarlierOnes(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "engram.exe")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A leftover from an earlier update, which nothing holds any more.
	if err := os.WriteFile(exe+".old-123", []byte("older"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Replace(exe, []byte("new"), true)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Fatalf("binary = %q", got)
	}
	if got, _ := os.ReadFile(r.Parked); string(got) != "old" {
		t.Fatalf("the running binary must be parked intact, got %q at %q", got, r.Parked)
	}
	if _, err := os.Stat(exe + ".old-123"); !os.IsNotExist(err) {
		t.Error("an earlier parked copy must be swept")
	}
	// Parking a second time with the first still parked picks another name.
	r2, err := Replace(exe, []byte("newer"), true)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Parked == "" {
		t.Fatal("the second update must park too")
	}
}

func TestReplaceRefusesAnUnresolvablePath(t *testing.T) {
	if _, err := Replace(filepath.Join(t.TempDir(), "missing", "engram"), []byte("x"), false); err == nil {
		t.Fatal("a path that does not exist cannot be replaced")
	}
}
