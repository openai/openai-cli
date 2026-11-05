package clihelp

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestGuideHelpWrapsProseAndPreservesShellLines(t *testing.T) {
	root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}}
	command := `$env:OPENAI_API_KEY = [System.Net.NetworkCredential]::new("", $openaiKey).Password`
	prose := "These instructions apply to this terminal session. " + strings.Repeat("Keep the original instructions. ", 4)
	got := formatGuide(root, "SETUP\n\n   "+prose+"\n       "+command+"\n")
	if !strings.Contains(got, "\n      "+command+"\n") {
		t.Fatalf("guide split or changed a shell command: %s", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimSpace(line) == command {
			continue
		}
		if len(line) > 79 || strings.TrimRight(line, " \t") != line {
			t.Fatalf("guide has overflowing prose or trailing whitespace: %q", line)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(got), " "), strings.Join(strings.Fields(prose), " ")) {
		t.Fatal("guide lost instruction words")
	}
}

func TestGuideHelpStylesLabelsAndSeparatesTerminalPrompt(t *testing.T) {
	const guide = "Shell setup\n\nCOMMANDS\nChoose a command:\n   openai help setup admin\n"
	const plain = "Shell setup\n\nCOMMANDS\nChoose a command:\n  openai help setup admin\n"
	root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{}}
	if got := formatGuide(root, guide); got != plain {
		t.Fatalf("piped guide = %q, want %q", got, plain)
	}
	if !welcomeTerminal(os.Stdout) {
		t.Skip("terminal styles and prompt spacing require a PTY")
	}
	setHelpTerminalColors(t)
	root.Writer = &welcomeTerminalBuffer{}
	style := commandHelpStyle(root)
	want := "\x1b[1mShell setup\x1b[0m\n\n" + style.render(style.section, "COMMANDS") + "\n" +
		style.render(style.note, "Choose a command:") + "\n  openai help setup admin\n\n"
	for _, source := range []string{guide, guide + "\n"} {
		if got := strings.ReplaceAll(formatGuide(root, source), "\x1b[m", "\x1b[0m"); got != strings.ReplaceAll(want, "\x1b[m", "\x1b[0m") {
			t.Fatalf("terminal guide = %q, want %q", got, want)
		}
	}
}
