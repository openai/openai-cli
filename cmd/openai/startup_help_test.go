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
		{"bare", nil, "Help needs no API key"},
		{"long-help", []string{"--help"}, "Help needs no API key"},
		{"short-help", []string{"-h"}, "Help needs no API key"},
		{"help-command", []string{"help"}, "Help needs no API key"},
		{"nested-help", []string{"models", "--help"}, "Key setup: openai help setup"},
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
			if !strings.Contains(got.stdout, "GLOBAL FLAGS") || !strings.Contains(got.stdout, test.end) {
				t.Fatalf("help omitted the complete reference: %q", got.stdout)
			}
			if test.name == "bare" && !strings.Contains(got.stdout, "What are we making today?") {
				t.Fatalf("bare help omitted the welcome banner: %q", got.stdout)
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
