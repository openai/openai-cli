package main

import (
	"strings"
	"testing"
)

func TestMainStartupHelpTerminalOutput(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		end  string
	}{
		{"bare", nil, "Help needs no API key"},
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
			// The PTY driver sends End only after the startup pager displays its controls.
			got := fixture.run(t, "", test.args...)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("help did not return successfully: %+v", got)
			}
			if !strings.Contains(got.stdout, test.end) || (test.name != "nested-help" && !strings.Contains(got.stdout, "GLOBAL FLAGS")) {
				t.Fatalf("help omitted its reference: %q", got.stdout)
			}
			if test.name == "bare" {
				top, rest, ok := strings.Cut(got.stdout, "q: end")
				if !ok || !strings.Contains(top, "START HERE") || !strings.Contains(top, "What are we making today?") {
					t.Fatal("bare startup did not show the first page before accepting input")
				}
				if !strings.Contains(rest, "GLOBAL FLAGS") || !strings.Contains(rest, test.end) {
					t.Fatal("scrolling to the end omitted the reference")
				}
			} else {
				for _, marker := range []string{"\x1b[?1049h", "\x1b[?1002h", "\x1b[?1003h", "Space: page", "q: end"} {
					if strings.Contains(got.stdout, marker) {
						t.Errorf("explicit help captured the terminal with %q", marker)
					}
				}
			}
			assertUpdateNoticeUnchanged(t, fixture, before)
		})
	}
}
