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

func TestMainExploreKeepsControlNamedLauncher(t *testing.T) {
	work, binary := buildNativeShellCLI(t)
	launcher := filepath.Join(work, "preview\u202eCLI")
	if err := os.Link(filepath.Join(work, binary), launcher); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "openai"), []byte("#!/bin/sh\nexit 42\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", home+string(os.PathListSeparator)+os.Getenv("PATH"))
	const offline = "http://127.0.0.1:1"
	t.Run("unknown parent", func(t *testing.T) {
		cmd := exec.Command(launcher, "explore", "tokenizer encodings")
		cmd.Dir = work
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TERM=dumb", "OPENAI_BASE_URL=" + offline}
		out, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "copyable commands") ||
			strings.Contains(string(out), "tokenizer encodings") || strings.ContainsRune(string(out), '\u202e') {
			t.Fatalf("unknown shell received a runnable alternative or lost its recovery message: %v: %q", err, out)
		}
	})
	for _, shell := range []nativeShell{
		{name: "fish", executable: "fish", args: []string{"--no-config", "-c"}},
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				t.Skipf("%s is unavailable", shell.name)
			}
			shell.executable = path
			// Keep the shell as the CLI's parent so its quoting rules apply.
			suffix := "; exit $?"
			if shell.name == "fish" {
				suffix = "; exit $status"
			}
			run := func(command string) string {
				t.Helper()
				got := runNativeShell(t, shell, work, home, offline, command+suffix)
				if got.code != 0 || got.stderr != "" || strings.ContainsRune(got.stdout, '\u202e') {
					t.Fatalf("exit %d or unsafe output: stdout %q, stderr %q", got.code, got.stdout, got.stderr)
				}
				return got.stdout
			}
			query := terminal.ShellQuoter(shell.name)(launcher) + " explore "
			for _, tc := range []struct{ query, command, want string }{
				{"'tokenizer encodings'", " tokenizer encodings", "o200k_base"},
				{"'tokenizer count'", ` tokenizer count --text "Hello, world!"`, "Tokens: 4"},
			} {
				results := run(query + tc.query)
				var copied string
				for line := range strings.SplitSeq(results, "\n") {
					command, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
					if strings.HasSuffix(command, tc.command) {
						copied = command
						break
					}
				}
				if copied == "" {
					t.Fatalf("missing %q in search results: %q", tc.command, results)
				}
				if out := run(copied); !strings.Contains(out, tc.want) {
					t.Fatalf("copied command returned %q; want %q", out, tc.want)
				}
			}
			view := run(query + "'tokenizer view'")
			if strings.Contains(view, "tokenizer view < draft.txt") != (shell.name == "bash") {
				t.Fatalf("wrong shell filtering: %q", view)
			}
		})
	}
}
