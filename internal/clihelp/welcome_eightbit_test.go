package clihelp

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestWelcomeEightbitKeepsTextAndFrame(t *testing.T) {
	for _, dark := range []bool{false, true} {
		plain := strings.Split(strings.TrimSuffix(renderWelcome("1.38.0", 80, colorprofile.NoTTY, dark), "\n\n"), "\n")
		for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
			got := strings.Split(strings.TrimSuffix(ansi.Strip(renderWelcome("1.38.0", 80, profile, dark)), "\n\n"), "\n")
			if len(got) != 6 || got[0] != plain[0] || got[1] != plain[1] || got[5] != plain[5] {
				t.Fatalf("mascot changed the frame or identity: %q", got)
			}
			for row := 2; row <= 4; row++ {
				cells, original := []rune(got[row]), []rune(plain[row])
				if ansi.StringWidth(got[row]) != 56 || len(cells) != 56 ||
					string(cells[:44]) != string(original[:44]) || string(cells[53:]) != string(original[53:]) ||
					strings.TrimSpace(string(cells[44:53])) == "" {
					t.Fatalf("mascot overlapped text or border: row=%d %q", row, got[row])
				}
			}
		}
	}
}

func TestWelcomeEightbitRespectsVersionSpaceAndColor(t *testing.T) {
	for _, tc := range []struct {
		version string
		profile colorprofile.Profile
		want    bool
	}{
		{"v1234567", colorprofile.TrueColor, true},
		{"v12345678", colorprofile.TrueColor, false},
		{"v界界界x", colorprofile.TrueColor, true},
		{"v界界界界", colorprofile.TrueColor, false},
		{"1.38.0", colorprofile.NoTTY, false},
		{"1.38.0", colorprofile.ASCII, false},
	} {
		got := ansi.Strip(renderWelcome(tc.version, 56, tc.profile, true))
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		if len(lines) != 6 || !strings.Contains(got, tc.version) {
			t.Fatalf("lost complete version: %q", got)
		}
		cells := []rune(lines[4])
		if len(cells) != 56 || (strings.TrimSpace(string(cells[44:53])) != "") != tc.want {
			t.Fatalf("version=%q: wrong mascot visibility: %q", tc.version, got)
		}
	}
}

func TestWelcomeEightbitLeavesUnsuitableContentUntouched(t *testing.T) {
	for _, content := range []string{"", "one line", "one\ntwo\nthree", "one\ntwo\nthree\nfour"} {
		if got := addWelcomeEightbit(content, 8, colorprofile.TrueColor, true); got != content {
			t.Fatalf("narrow content changed: %q", got)
		}
	}
}
