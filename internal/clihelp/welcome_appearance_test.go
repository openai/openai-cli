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
	want := welcomeAppearanceCells(t, string(approved))
	for _, width := range []int{76, 80, 100} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			got := welcomeAppearanceCells(t, renderWelcome("1.38.0", width, colorprofile.TrueColor, true, true))
			if len(got) != len(want) {
				t.Fatalf("approved cell count changed: got %d, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("approved appearance changed at row %d column %d: got %+v, want %+v", i/77, i%77, got[i], want[i])
				}
			}
		})
	}
}

type welcomeAppearanceCell struct {
	char       rune
	foreground string
	background string
	bold       bool
}

func welcomeAppearanceCells(t *testing.T, text string) []welcomeAppearanceCell {
	t.Helper()
	fg, bg := "default-fg", "default-bg"
	bold, reverse := false, false
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
					fg, bg, bold, reverse = "default-fg", "default-bg", false, false
				case 1:
					bold = true
				case 7:
					reverse = true
				case 22:
					bold = false
				case 27:
					reverse = false
				case 39:
					fg = "default-fg"
				case 49:
					bg = "default-bg"
				case 38, 48:
					if i+4 >= len(codes) || codes[i+1] != 2 {
						t.Fatalf("expected RGB color in %q", rest[:end+1])
					}
					color := fmt.Sprintf("%d,%d,%d", codes[i+2], codes[i+3], codes[i+4])
					if codes[i] == 38 {
						fg = color
					} else {
						bg = color
					}
					i += 4
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
		cell := welcomeAppearanceCell{char, fg, bg, bold}
		if reverse {
			cell.foreground, cell.background = bg, fg
		}
		if char == ' ' {
			cell.foreground, cell.bold = "", false
		} else if char == '\n' {
			cell.foreground, cell.background, cell.bold = "", "", false
		}
		cells = append(cells, cell)
		rest = rest[size:]
	}
	return cells
}
