//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/terminal"
)

func TestMainHelpExampleKeepsRenamedLauncher(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	work, binary := buildNativeShellCLI(t)
	home := t.TempDir()
	decoy := filepath.Join(home, "openai")
	if err := os.WriteFile(decoy, []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
	shell := nativeShell{name: "bash", executable: bash, args: []string{"--noprofile", "--norc", "-c"}}
	for _, name := range []string{"preview\nCLI", "preview\u202eCLI"} {
		path := filepath.Join(work, name)
		if err := os.Link(filepath.Join(work, binary), path); err != nil {
			t.Fatal(err)
		}
		// The trailing exit keeps Bash as the parent for shell detection.
		page := runNativeShell(t, shell, work, home, "http://127.0.0.1:1", terminal.ShellQuote(path)+" responses create --help; exit $?")
		if page.code != 0 || page.stderr != "" || strings.Contains(page.stdout, name) {
			t.Fatalf("help exposed its raw launcher name or failed for %q", name)
		}
		var example string
		for line := range strings.SplitSeq(page.stdout, "\n") {
			if command, found := strings.CutPrefix(strings.TrimSpace(line), "$ "); found {
				example = command
				break
			}
		}
		if example == "" {
			t.Fatalf("missing copyable Bash example for %q", name)
		}
		copied := runNativeShell(t, shell, work, home, "http://127.0.0.1:1", example+" --help; exit $?")
		if copied.code != 0 || copied.stderr != "" || copied.stdout != page.stdout {
			t.Errorf("copied example must reopen the same help for %q; got exit %d", name, copied.code)
		}
	}
}
