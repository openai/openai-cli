package main

import (
	"strings"
	"testing"
)

func TestMainStartupHelpReturnsWithoutInput(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		end  string
	}{
		{"bare", nil, "openai --help"},
		{"long-help", []string{"--help"}, "Help needs no API key"},
		{"short-help", []string{"-h"}, "Help needs no API key"},
		{"help-command", []string{"help"}, "Help needs no API key"},
		{"nested-help", []string{"models", "--help"}, "Global flags: openai --help"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUpdateNoticeFixture(t)
			fixture.env["OPENAI_CLI_NO_UPDATE_CHECK"] = "1"
			fixture.env["OPENAI_BASE_URL"] = "not%url"
			before := updateNoticeSnapshot(t, fixture.home)
			// All streams are terminals. The driver sends no keyboard input.
			got := fixture.run(t, "", test.args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("help did not return successfully: %+v", got)
			}
			if !strings.Contains(got.stdout, test.end) {
				t.Fatalf("help omitted %q: %q", test.end, got.stdout)
			}
			if test.name == "bare" {
				checkStartupOverview(t, got.stdout)
			} else if test.name != "nested-help" && !strings.Contains(got.stdout, "GLOBAL FLAGS") {
				t.Fatalf("explicit help omitted its reference: %q", got.stdout)
			}
			for _, marker := range []string{"\x1b[?1049h", "\x1b[?1002h", "\x1b[?1003h", "Space: page", "q: quit"} {
				if strings.Contains(got.stdout, marker) {
					t.Errorf("help captured the terminal with %q", marker)
				}
			}
			assertUpdateNoticeUnchanged(t, fixture, before)
		})
	}
}

func checkStartupOverview(t *testing.T, text string) {
	t.Helper()
	for _, want := range []string{"What are we making today?", "Use the OpenAI API from your terminal.", "START HERE", "openai explore", "openai help setup", "openai --help"} {
		if !strings.Contains(text, want) {
			t.Errorf("bare startup omitted %q: %q", want, text)
		}
	}
	if strings.Contains(text, "SHORTCUTS") || strings.Contains(text, "--base-url=URL") {
		t.Errorf("bare startup printed the command reference: %q", text)
	}
}
