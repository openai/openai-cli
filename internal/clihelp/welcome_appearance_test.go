package clihelp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/colorprofile"
)

// The fixture is the user's approved terminal preview, independent of the
// renderer. Compare displayed cells so equivalent SGR resets do not matter.
func TestWelcomeApprovedAppearance(t *testing.T) {
	approved, err := os.ReadFile("testdata/welcome-approved.ansi")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256} {
		var reference strings.Builder
		writer := colorprofile.Writer{Forward: &reference, Profile: profile}
		if _, err := writer.Write(approved); err != nil {
			t.Fatal(err)
		}
		want := welcomeAppearanceCells(t, reference.String())
		for _, width := range []int{76, 80, 100} {
			for _, terminalProgram := range []string{"", "Apple_Terminal", "vscode", "iTerm.app"} {
				name := terminalProgram
				if name == "" {
					name = "default"
				}
				t.Run(profile.String()+"/"+strconv.Itoa(width)+"/"+name, func(t *testing.T) {
					got := welcomeAppearanceCells(t, renderWelcome("1.38.0", width, profile, true, true, terminalProgram))
					if len(got) != len(want) {
						t.Fatalf("approved cell count changed: got %d, want %d", len(got), len(want))
					}
					for i, cell := range want {
						column := i % 77
						if terminalProgram == "Apple_Terminal" && i/77 == 8 &&
							(column >= 1 && column <= 64 || column >= 71 && column <= 73) {
							cell.char = '⎯'
						}
						if got[i] != cell {
							t.Fatalf("approved appearance changed at row %d column %d: got %+v, want %+v", i/77, column, got[i], cell)
						}
					}
				})
			}
		}
	}
}

func TestWelcomeAppleTerminalPreservesFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		width         int
		profile       colorprofile.Profile
		unicode       bool
	}{
		{"narrow", "1.38.0", 75, colorprofile.TrueColor, true},
		{"ASCII", "1.38.0", 80, colorprofile.TrueColor, false},
		{"no color", "1.38.0", 80, colorprofile.NoTTY, true},
		{"16 colors", "1.38.0", 80, colorprofile.ANSI, true},
		{"long version", "1.38.0-preview.20261013", 100, colorprofile.TrueColor, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := renderWelcome(tc.version, tc.width, tc.profile, true, tc.unicode, "")
			got := renderWelcome(tc.version, tc.width, tc.profile, true, tc.unicode, "Apple_Terminal")
			if got != want {
				t.Fatalf("Apple Terminal changed the fallback: got %q, want %q", got, want)
			}
		})
	}
}

type welcomeAppearanceCell struct {
	char       rune
	foreground string
	background string
	bold       bool
	underline  bool
}

func welcomeAppearanceCells(t *testing.T, text string) []welcomeAppearanceCell {
	t.Helper()
	fg, bg := "default-fg", "default-bg"
	bold, reverse, underline := false, false, false
	var cells []welcomeAppearanceCell
	for rest := strings.TrimRight(text, "\n"); rest != ""; {
		if strings.HasPrefix(rest, "\x1b[") {
			end := strings.IndexByte(rest, 'm')
			if end < 0 {
				t.Fatalf("unterminated or non-SGR terminal control: %q", rest)
			}
			params := strings.Split(rest[2:end], ";")
			if params[0] == "" {
				params[0] = "0"
			}
			codes := make([]int, len(params))
			for i, param := range params {
				value, err := strconv.Atoi(param)
				if err != nil {
					t.Fatalf("unexpected terminal control: %q", rest[:end+1])
				}
				codes[i] = value
			}
			for i := 0; i < len(codes); i++ {
				switch codes[i] {
				case 0:
					fg, bg, bold, reverse, underline = "default-fg", "default-bg", false, false, false
				case 1:
					bold = true
				case 4:
					underline = true
				case 7:
					reverse = true
				case 22:
					bold = false
				case 24:
					underline = false
				case 27:
					reverse = false
				case 39:
					fg = "default-fg"
				case 49:
					bg = "default-bg"
				case 38, 48:
					kind := codes[i]
					var color string
					if i+4 < len(codes) && codes[i+1] == 2 {
						color = fmt.Sprintf("%d,%d,%d", codes[i+2], codes[i+3], codes[i+4])
						i += 4
					} else if i+2 < len(codes) && codes[i+1] == 5 {
						color = fmt.Sprintf("indexed-%d", codes[i+2])
						i += 2
					} else {
						t.Fatalf("expected RGB or indexed color in %q", rest[:end+1])
					}
					if kind == 38 {
						fg = color
					} else {
						bg = color
					}
				default:
					t.Fatalf("unexpected SGR %d", codes[i])
				}
			}
			rest = rest[end+1:]
			continue
		}
		char, size := utf8.DecodeRuneInString(rest)
		if char == utf8.RuneError || char == '\x1b' {
			t.Fatalf("invalid character or non-SGR control: %q", rest)
		}
		cell := welcomeAppearanceCell{char, fg, bg, bold, underline}
		if reverse {
			cell.foreground, cell.background = bg, fg
		}
		if char == ' ' && !underline {
			cell.foreground, cell.bold = "", false
		} else if char == '\n' {
			cell.foreground, cell.background, cell.bold, cell.underline = "", "", false, false
		}
		cells = append(cells, cell)
		rest = rest[size:]
	}
	if bold || reverse || underline || fg != "default-fg" || bg != "default-bg" {
		t.Fatalf("welcome leaves terminal styles active: bold=%t reverse=%t underline=%t fg=%s bg=%s", bold, reverse, underline, fg, bg)
	}
	return cells
}
