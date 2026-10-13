package clihelp

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestWelcomeEncodingPreservesLayoutAndVersion(t *testing.T) {
	for _, width := range []int{29, 39, 40, 55, 56, 80} {
		for _, unicode := range []bool{false, true} {
			for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ANSI, colorprofile.TrueColor} {
				for _, version := range []string{"1.38.0", "v123456789012345"} {
					got := renderWelcome(version, width, profile, true, unicode, "")
					if got == "" && width < 40 {
						continue // Complete identity/version can exceed the narrow fallback.
					}
					plain := ansi.Strip(got)
					if !strings.Contains(plain, version) {
						t.Fatalf("lost version: %q", got)
					}
					for _, line := range strings.Split(plain, "\n") {
						if ansi.StringWidth(line) > width {
							t.Fatalf("overflow at width %d: %q", width, got)
						}
					}
					if !unicode {
						for _, char := range plain {
							if char != '\n' && (char < ' ' || char > '~') {
								t.Fatalf("non-ASCII decoration: %q", got)
							}
						}
					}
					if strings.ContainsAny(ansi.Strip(got), "\x1b\x00\x07") || profile == colorprofile.NoTTY && got != plain {
						t.Fatalf("unexpected terminal controls: %q", got)
					}
				}
			}
		}
	}
}

func TestWelcomeASCIIKeepsNonASCIIRevisionReadable(t *testing.T) {
	for _, tc := range []struct{ version, displayed string }{
		{"dev-界", `dev-\u754c`},
		{`dev-"quoted"`, `dev-"quoted"`},
		{`dev-a\b`, `dev-a\b`},
		{"dev-é", `dev-\u00e9`},
		{"dev-🐙", `dev-\U0001f419`},
	} {
		got := renderWelcome(tc.version, 80, colorprofile.NoTTY, true, false, "")
		if !strings.Contains(got, tc.displayed) {
			t.Fatalf("ASCII fallback lost the revision: %q", got)
		}
	}
}

func TestWelcomeUnicodeLocalePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"unknown", nil, false},
		{"UTF-8 locale", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"UTF8 locale", map[string]string{"LANG": "C.utf8"}, true},
		{"C locale", map[string]string{"LANG": "C"}, false},
		{"ctype overrides language", map[string]string{"LC_CTYPE": "C", "LANG": "en_US.UTF-8"}, false},
		{"all overrides ctype", map[string]string{"LC_ALL": "en_US.UTF-8", "LC_CTYPE": "C"}, true},
		{"Windows Terminal", map[string]string{"WT_SESSION": "synthetic"}, true},
		{"explicit ASCII wins", map[string]string{"WT_SESSION": "synthetic", "LC_ALL": "C"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := welcomeUnicode(func(name string) string { return tc.env[name] }); got != tc.want {
				t.Fatalf("Unicode = %v; want %v", got, tc.want)
			}
		})
	}
}
