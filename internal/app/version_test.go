package app

import (
	"strings"
	"testing"
)

// TestVersionString: -version and the on-screen identity line print
// "spotui <version> (<revision>)" — name, release version and the build
// revision, so a bug report says exactly which binary produced it.
func TestVersionString(t *testing.T) {
	got := VersionString()
	if !strings.HasPrefix(got, "spotui "+Version+" (") {
		t.Errorf("VersionString() = %q, want \"spotui %s (<rev>)\"", got, Version)
	}
	if !strings.HasSuffix(got, ")") {
		t.Errorf("VersionString() = %q, want the revision inside parentheses", got)
	}
	if Version == "" {
		t.Errorf("Version must never be empty")
	}
	if rev := Revision(); !strings.Contains(got, "("+rev+")") {
		t.Errorf("VersionString() = %q should carry the revision %q", got, rev)
	}
}
