//go:build !windows

package workspace

// Linux is case-sensitive. macOS usually is not, but its default filesystem is
// case-PRESERVING, and folding here would make two genuinely distinct paths on a
// case-sensitive volume collide — the rarer bug is the one worth avoiding.
const isCaseInsensitive = false
