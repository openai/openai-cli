package updatenotice

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemVerifierAcceptsOnlyProtectedPrograms(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := systemUpdateVerifier(); got != path {
		t.Fatalf("private verifier: got %q", got)
	}
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{{path, 0777}, {dir, 0777}} {
		if err := os.Chmod(tc.name, tc.mode); err != nil {
			t.Fatal(err)
		}
		if got := systemUpdateVerifier(); got != "" {
			t.Fatalf("writable verifier path: got %q", got)
		}
		if err := os.Chmod(tc.name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(t.TempDir(), "bin")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", alias)
	if got := systemUpdateVerifier(); got != "" {
		t.Fatalf("indirect verifier path: got %q", got)
	}
}
