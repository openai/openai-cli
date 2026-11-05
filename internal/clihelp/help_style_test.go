package clihelp

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func setHelpTerminalColors(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
}

func TestStyledHelpPreservesContentAndColorOptOuts(t *testing.T) {
	if !welcomeTerminal(os.Stdout) {
		t.Skip("requires terminal stdout; run this test in a PTY")
	}
	setHelpTerminalColors(t)
	for _, tc := range []struct {
		name, key, value, format string
		pipe, color              bool
	}{
		{name: "dark", key: "COLORFGBG", value: "15;0", color: true},
		{name: "light", key: "COLORFGBG", value: "0;15", color: true},
		{name: "256 colors", key: "COLORTERM", color: true},
		{name: "16 colors", key: "TERM", value: "ansi", color: true},
		{name: "no color", key: "NO_COLOR", value: "1"},
		{name: "nonempty NO_COLOR", key: "NO_COLOR", value: "0"},
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
				root.UsageText = "openai models [NAME | --model NAME]"
				authored := commandSynopsis(root, "openai models", "openai", width)
				root.UsageText = ""
				if ansi.Strip(authored) != "  openai models [NAME | --model NAME]\n" ||
					strings.Contains(authored, "\x1b") != tc.color {
					t.Fatalf("authored usage lost syntax or terminal styling: %q", authored)
				}
				if ansi.Strip(got) != want {
					t.Fatalf("width %d: styling changed help text or alignment", width)
				}
				if strings.Contains(got, "\x1b") != tc.color {
					t.Fatalf("width %d: unexpected color selection: %q", width, got)
				}
				if tc.color && (!strings.Contains(got, "\x1b[1mOpenAI CLI\x1b[") ||
					!strings.HasPrefix(authored, "  \x1b[1mopenai models\x1b[")) {
					t.Fatalf("expected bold terminal-foreground root and authored titles: %q, %q", got, authored)
				}
				if strings.Contains(got, "\n\n\n") {
					t.Fatal("help contains redundant blank rows")
				}
			}
		})
	}
}

func TestHelpThemeSeparatesHeadingsFlagsAndDefinitions(t *testing.T) {
	for _, tc := range []struct {
		name, background, accent, muted string
		profile                         colorprofile.Profile
	}{
		{"dark", "15;0", "\x1b[38;2;138;168;255m", "\x1b[38;2;164;172;194m", colorprofile.TrueColor},
		{"light", "0;15", "\x1b[38;2;49;89;188m", "\x1b[38;2;101;112;135m", colorprofile.TrueColor},
		{"16-color dark", "15;0", "\x1b[94m", "", colorprofile.ANSI},
		{"16-color light", "0;15", "\x1b[34m", "", colorprofile.ANSI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COLORFGBG", tc.background)
			style := newHelpStyle(tc.profile)
			heading := style.render(style.section, "FLAGS")
			if !strings.HasPrefix(heading, tc.accent+"FLAGS\x1b[") || ansi.Strip(heading) != "FLAGS" {
				t.Errorf("section heading must use the blue accent: %q", heading)
			}
			value := style.render(style.value, "=MODEL")
			note := style.render(style.note, "Model note.\n\n")
			if tc.muted == "" {
				if value != "=MODEL" || note != "Model note.\n\n" {
					t.Errorf("16-color values and notes must keep the terminal foreground: %q, %q", value, note)
				}
			} else if !strings.HasPrefix(value, tc.muted+"=MODEL\x1b[") ||
				!strings.HasPrefix(note, tc.muted+"Model note.\x1b[") {
				t.Errorf("expected muted values and notes: %q, %q", value, note)
			}
			if ansi.Strip(value) != "=MODEL" || ansi.Strip(note) != "Model note.\n\n" || !strings.HasSuffix(note, "\n\n") {
				t.Errorf("styling changed text or decorated an empty row: %q, %q", value, note)
			}
			label := style.flag("--model", "MODEL")
			if label != "--model"+value || style.flag("--stream", "") != "--stream" {
				t.Errorf("ordinary flag names must keep the terminal foreground: %q", label)
			}
			for _, width := range []int{40, 80} {
				name := "--model=MODEL"
				row := style.entry(name, "Select a model.", label, style.text, width, helpDescriptionColumn(width, []string{name}))
				if !strings.Contains(row, "  Select a model.\n") {
					t.Errorf("width %d: definition must keep the terminal foreground: %q", width, row)
				}
			}
		})
	}
}

func TestActionHelpFlagRolesAcrossReferences(t *testing.T) {
	if !welcomeTerminal(os.Stdout) {
		t.Skip("requires terminal stdout; run this test in a PTY")
	}
	setHelpTerminalColors(t)
	for _, tc := range []struct {
		name, flagPrefix string
		args             []string
	}{
		{"overview", "--filter", []string{"inspect", "--help"}},
		{"full", "--filter", []string{"help", "--all", "inspect"}},
		{"one flag", "\x1b[1m--filter\x1b[", []string{"inspect", "-h", "--filter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out welcomeTerminalBuffer
			action := &cli.Command{Name: "inspect", Usage: "Inspect a record.", Flags: []cli.Flag{
				&cli.StringFlag{Name: "filter", Usage: "Filter the results."},
			}}
			root := &cli.Command{Name: "openai", Writer: &out, Commands: []*cli.Command{action}}
			args, _, err := Configure(root, append([]string{"openai"}, tc.args...))
			if err != nil {
				t.Fatal(err)
			}
			if err := root.Run(t.Context(), args); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(out.String(), "\n")
			for _, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(ansi.Strip(line)), "--filter") {
					if !strings.HasPrefix(strings.TrimLeft(line, " "), tc.flagPrefix) {
						t.Fatalf("wrong style for flag name: %q", line)
					}
					if !strings.Contains(out.String(), "  Filter the results.\n") {
						t.Fatalf("definition must keep the terminal foreground: %q", out.String())
					}
					return
				}
			}
			t.Fatalf("did not find flag row: %q", out.String())
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
	if !strings.Contains(strings.Join(strings.Fields(got), " "), "--format=FORMAT Choose output.") {
		t.Fatal("flag definitions lost their full description")
	}
	if strings.Index(got, "ACTIONS\n") > strings.Index(got, "GLOBAL FLAGS\n") {
		t.Fatal("commands must precede the global reference")
	}
}
