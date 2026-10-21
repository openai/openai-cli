package clihelp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHelpInvocationVerifiedRenamedExecutable(t *testing.T) {
	running, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	t.Chdir(directory)
	t.Setenv("PATH", directory)
	for _, name := range []string{"openai-current-main", "preview with spaces", "preview & quote's", "true", "time", "if"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			path := filepath.Join(directory, name)
			if runtime.GOOS == "windows" {
				// Windows can lock hard links to the running image during cleanup.
				// A symlink preserves executable identity without linking that image.
				symlinkHelpInvocationFixture(t, running, path)
			} else if err := os.Link(running, path); err != nil {
				t.Fatal(err)
			}
			local := "./" + name
			if runtime.GOOS == "windows" {
				local = `.\` + name
			}
			want := local
			if name == "preview with spaces" || name == "preview & quote's" ||
				name == "preview with spaces.exe" || name == "preview & quote's.exe" {
				want = quoteInvocation(local, "openai")
			}
			for _, input := range []string{path, local, name} {
				if got := Invocation("openai", []string{input}); got != want {
					t.Errorf("Invocation(%q) = %q; want %q", input, got, want)
				}
			}
		})
	}
}

func TestHelpInvocationRejectsDifferentRenamedExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openai-current-main")
	if err := os.WriteFile(path, []byte("unrelated executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Invocation("openai", []string{path}); got != "openai" {
		t.Fatalf("mismatched identity produced %q", got)
	}
}
