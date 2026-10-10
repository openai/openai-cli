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
			if len(got) != 7 || got[0] != plain[0] || got[1] != plain[1] || got[6] != plain[5] {
				t.Fatalf("mascot changed the frame or identity: %q", got)
			}
			for offset, first := range []int{43, 39, 38, 39} {
				row := offset + 2
				original := []rune("│" + strings.Repeat(" ", 54) + "│")
				if row < 5 {
					original = []rune(plain[row])
				}
				cells := []rune(got[row])
				if ansi.StringWidth(got[row]) != 56 || len(cells) != 56 ||
					string(cells[:first]) != string(original[:first]) || string(cells[53:]) != string(original[53:]) ||
					strings.TrimSpace(string(cells[first:53])) == "" {
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
		{"v123456", colorprofile.TrueColor, true},
		{"v1234567", colorprofile.TrueColor, false},
		{"v界界界", colorprofile.TrueColor, true},
		{"v界界界x", colorprofile.TrueColor, false},
		{"1.38.0", colorprofile.NoTTY, false},
		{"1.38.0", colorprofile.ASCII, false},
	} {
		got := ansi.Strip(renderWelcome(tc.version, 56, tc.profile, true))
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		wantHeight := 6
		if tc.want {
			wantHeight = 7
		}
		if len(lines) != wantHeight || !strings.Contains(got, tc.version) {
			t.Fatalf("lost complete version: %q", got)
		}
		cells := []rune(lines[4])
		if len(cells) != 56 || (strings.TrimSpace(string(cells[38:53])) != "") != tc.want {
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
	content := "one\ntwo\n" + strings.Repeat("x", 40) + "\nfour"
	if got := addWelcomeEightbit(content, 50, colorprofile.TrueColor, true); got != content {
		t.Fatalf("later overlap must omit the whole sprite without adding a row: %q", got)
	}
}
