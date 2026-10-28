package clihelp

import (
	"bytes"
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
