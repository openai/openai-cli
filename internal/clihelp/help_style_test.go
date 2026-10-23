package clihelp

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func TestStyledHelpPreservesContentAndColorOptOuts(t *testing.T) {
	if !welcomeTerminal(os.Stdout) {
		t.Skip("requires terminal stdout; run this test in a PTY")
	}
	for _, name := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	for _, tc := range []struct {
		name, key, value, format string
		pipe, color              bool
	}{
		{name: "dark", key: "COLORFGBG", value: "15;0", color: true},
		{name: "light", key: "COLORFGBG", value: "0;15", color: true},
		{name: "256 colors", key: "COLORTERM", color: true},
		{name: "16 colors", key: "TERM", value: "ansi", color: true},
		{name: "no color", key: "NO_COLOR", value: "1"},
		{name: "force disabled", key: "FORCE_COLOR", value: "0"},
		{name: "clicolor disabled", key: "CLICOLOR", value: "0"},
		{name: "CI", key: "CI", value: "1"},
		{name: "dumb", key: "TERM", value: "dumb"},
		{name: "JSON", format: "json"},
		{name: "readable text", format: "text", color: true},
		{name: "pipe with forced color", key: "CLICOLOR_FORCE", value: "1", pipe: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.key != "" {
				t.Setenv(tc.key, tc.value)
			}
			if tc.name == "16 colors" {
				t.Setenv("COLORTERM", "")
			}
			var out welcomeTerminalBuffer
			var plain bytes.Buffer
			root := &cli.Command{Name: "openai", Writer: &out,
				Flags:    []cli.Flag{&cli.StringFlag{Name: "format", Value: tc.format, Usage: "Choose output."}},
				Commands: []*cli.Command{{Name: "models", Usage: "List and inspect models."}},
			}
			args, _, err := Configure(root, []string{"openai", "--help"})
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{40, 80, 100} {
				root.Writer = &plain
				want := commandHelpAtWidth(root, "openai", "", width)
				if !tc.pipe {
					root.Writer = &out
					want += "\n" // Separate terminal help from the next shell prompt.
				}
				got := commandHelpAtWidth(root, "openai", "", width)
				if ansi.Strip(got) != want {
					t.Fatalf("width %d: styling changed help text or alignment", width)
				}
				if strings.Contains(got, "\x1b") != tc.color {
					t.Fatalf("width %d: unexpected color selection: %q", width, got)
				}
				for _, control := range regexp.MustCompile(`\x1b\[[0-9;]*m`).FindAllString(got, -1) {
					parameters := strings.TrimSuffix(strings.TrimPrefix(control, "\x1b["), "m")
					// Extended color parameters can contain 1 or 4 as RGB values.
					if !regexp.MustCompile(`^(|0|3[0-7]|9[0-7]|38;5;[0-9]+|38;2;[0-9]+;[0-9]+;[0-9]+)$`).MatchString(parameters) {
						t.Fatalf("help must use foreground color without bold or underline: %q", control)
					}
				}
				if strings.Contains(got, "\n\n\n") {
					t.Fatal("help contains redundant blank rows")
				}
			}
		})
	}
}

func TestRootHelpKeepsOneIntroductionAndFooter(t *testing.T) {
	root := &cli.Command{Name: "openai", Writer: &bytes.Buffer{},
		Commands: []*cli.Command{{Name: "models", Usage: "List models."}},
		Flags:    []cli.Flag{&cli.StringFlag{Name: "format", Usage: "Choose output."}},
	}
	got := commandHelpAtWidth(root, "openai", "", 80)
	if !strings.HasPrefix(got, "OpenAI CLI\nUse the OpenAI API from your terminal.\n\nUSAGE\n  openai [GLOBAL FLAGS] COMMAND\n") {
		t.Fatalf("missing command identity: %s", got)
	}
	for _, section := range []string{"USAGE", "START HERE", "GLOBAL FLAGS", "ACTIONS", "NOTES"} {
		if strings.Count("\n"+got, "\n"+section+"\n") != 1 {
			t.Fatalf("missing or repeated section %q: %s", section, got)
		}
	}
	for _, removed := range []string{"NAME\n", "SYNOPSIS\n", "DESCRIPTION\n", "Start here", "Usage:", "What are we making today?", "Key setup:", "Add --help"} {
		if strings.Contains(got, removed) {
			t.Fatalf("obsolete presentation %q", removed)
		}
	}
	if strings.Count(got, "openai help setup") != 1 || strings.Count(got, "Command help:") != 1 {
		t.Fatal("root help repeats setup or command guidance")
	}
	if !strings.Contains(got, "--format=FORMAT\n      Choose output.") {
		t.Fatal("flag definitions lost their full description")
	}
	if strings.Index(got, "ACTIONS\n") > strings.Index(got, "GLOBAL FLAGS\n") {
		t.Fatal("commands must precede the global reference")
	}
}
