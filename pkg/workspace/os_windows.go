package workspace

// Windows compares paths case-insensitively, so the "am I inside the shared
// brain" test has to as well — otherwise C:\Brain and c:\brain resolve to two
// different brains on one machine.
const isCaseInsensitive = true
