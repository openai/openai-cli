//go:build darwin || linux

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHomebrewReadmeInstall(t *testing.T) {
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(readme), "\n### Installing with Homebrew\n")
	section, _, _ = strings.Cut(section, "\n### ")
	_, start, opened := strings.Cut(section, "\n```sh\n")
	block, _, closed := strings.Cut(start, "\n```")
	if !ok || !opened || !closed {
		t.Fatal("missing Homebrew installation command")
	}

	for _, tc := range []struct {
		name     string
		exitCode int
	}{
		{"fresh install without GitHub CLI", 0},
		{"failed cask install", 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := privateInstallDirectory(t)
			bin := filepath.Join(home, "brew prefix", "bin")
			if err := os.MkdirAll(bin, 0755); err != nil {
				t.Fatal(err)
			}
			// Model only Homebrew's named cask. The CLI, Go and GitHub CLI start absent.
			const brew = `#!/bin/sh
printf '%s\n' "$*" >> "$HOME/brew.log"
[ "$#" -eq 2 ] && [ "$1" = install ] && [ "$2" = openai/tools/openai ] || exit 71
[ "$BREW_RC" = 0 ] || exit "$BREW_RC"
/bin/cp "$HOME/release" "${0%/*}/openai"
`
			if err := os.WriteFile(filepath.Join(bin, "brew"), []byte(brew), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "release"), []byte(archiveBinary), 0700); err != nil {
				t.Fatal(err)
			}
			shell, err := exec.LookPath("sh")
			if err != nil {
				t.Fatal(err)
			}
			run := func(text string) ([]byte, error) {
				cmd := exec.Command(shell, "-c", text)
				cmd.Env = []string{"HOME=" + home, "PATH=" + bin, "BREW_RC=" + strconv.Itoa(tc.exitCode)}
				return cmd.CombinedOutput()
			}
			out, err := run(block)
			if tc.exitCode == 0 {
				if err != nil {
					t.Fatalf("README install: %s, %v", out, err)
				}
				if out, err := run("openai --version"); err != nil || string(out) != "openai version 1.2.3\n" {
					t.Fatalf("fresh shell: %s, %v", out, err)
				}
			} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != tc.exitCode {
				t.Fatalf("README install: output %q, err %v; want exit %d", out, err, tc.exitCode)
			}
			if calls, err := os.ReadFile(filepath.Join(home, "brew.log")); err != nil || string(calls) != "install openai/tools/openai\n" {
				t.Fatalf("unexpected brew calls: %q, %v", calls, err)
			}
			for _, name := range []string{".openai", ".zshrc", ".bashrc"} {
				if _, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) {
					t.Fatalf("unexpected installer side effect at %s: %v", name, err)
				}
			}
		})
	}
}
