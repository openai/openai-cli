package clihelp

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func TestWelcomeLayout(t *testing.T) {
	for _, version := range []string{"", "1.38.0", "v2.0.0", "dev", "dev-界", "1.38.0-rc.1+abc123"} {
		for _, width := range []int{20, 28, 29, 31, 39, 40, 55, 56, 60, 65, 66, 70, 71, 72, 75, 76, 80, 81, 82, 100} {
			for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ASCII, colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
				got := renderWelcome(version, width, profile, true, true, "")
				if got == "" {
					if width >= 40 {
						t.Fatalf("missing header at width %d for %q", width, version)
					}
					continue
				}
				plain := ansi.Strip(got)
				if !strings.Contains(plain, version) || !strings.Contains(plain, welcomeTagline) {
					t.Fatalf("missing version or greeting: %q", plain)
				}
				wantHeight := 4
				if width >= 40 {
					wantHeight = 6
				}
				versionWidth := ansi.StringWidth(welcomeVersion(version))
				if profile > colorprofile.ASCII && versionWidth <= 16 && width >= max(76, 66+versionWidth) {
					wantHeight = 9
				}
				if strings.Count(strings.TrimSpace(plain), "\n") != wantHeight-1 {
					t.Fatalf("banner height differs from %d: %q", wantHeight, plain)
				}
				for _, line := range strings.Split(plain, "\n") {
					if ansi.StringWidth(line) > width {
						t.Fatalf("line exceeds %d columns: %q", width, line)
					}
				}
				if profile == colorprofile.NoTTY && plain != got {
					t.Fatalf("no-color banner contains escapes: %q", got)
				}
			}
		}
	}
}

func TestWelcomePixelRowFallbackUsesOnlyBackgroundCells(t *testing.T) {
	for _, row := range []string{"", ".", "#", ".##.#."} {
		got := welcomePixelRow(row, "#8AA8FF", "", 0)
		var cells strings.Builder
		background := false
		for remaining := got; remaining != ""; {
			switch {
			case strings.HasPrefix(remaining, "\x1b[48;2;138;168;255m"):
				background = true
				remaining = strings.TrimPrefix(remaining, "\x1b[48;2;138;168;255m")
			case strings.HasPrefix(remaining, "\x1b[m"):
				background = false
				remaining = strings.TrimPrefix(remaining, "\x1b[m")
			case strings.HasPrefix(remaining, "\x1b[0m"):
				background = false
				remaining = strings.TrimPrefix(remaining, "\x1b[0m")
			case strings.HasPrefix(remaining, "\x1b[49m"):
				background = false
				remaining = strings.TrimPrefix(remaining, "\x1b[49m")
			case remaining[0] == ' ':
				if background {
					cells.WriteByte('#')
				} else {
					cells.WriteByte('.')
				}
				remaining = remaining[1:]
			default:
				t.Fatalf("pixels must use spaces and background SGR only, without glyphs or reverse masks: %q", got)
			}
		}
		want := strings.NewReplacer("#", "##", ".", "..").Replace(row)
		if cells.String() != want || background {
			t.Fatalf("pixel fill or inherited background changed: got %q, want %q; output %q", cells.String(), want, got)
		}
	}
}

func TestWelcomeColoredWordmarkFitsCompleteMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, version, displayed string
		width, card, height      int
	}{
		{"below old threshold", "1.38.0", "v1.38.0", 55, 40, 6},
		{"old threshold", "1.38.0", "v1.38.0", 56, 40, 6},
		{"below new threshold", "1.38.0", "v1.38.0", 75, 40, 6},
		{"exact new threshold", "1.38.0", "v1.38.0", 76, 76, 9},
		{"wide terminal", "1.38.0", "v1.38.0", 100, 76, 9},
		{"fifteen cells below fit", "v12345678901234", "v12345678901234", 80, 40, 6},
		{"fifteen cells exact fit", "v12345678901234", "v12345678901234", 81, 81, 9},
		{"sixteen cells below fit", "v123456789012345", "v123456789012345", 81, 40, 6},
		{"sixteen cells exact fit", "v123456789012345", "v123456789012345", 82, 82, 9},
		{"version prefix below fit", "123456789012345", "v123456789012345", 81, 40, 6},
		{"version prefix exact fit", "123456789012345", "v123456789012345", 82, 82, 9},
		{"wide characters below fit", "v界界界界界界界x", "v界界界界界界界x", 81, 40, 6},
		{"wide characters exact fit", "v界界界界界界界x", "v界界界界界界界x", 82, 82, 9},
		{"version prefix exceeds limit", "1234567890123456", "v1234567890123456", 100, 40, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
				got := renderWelcome(tc.version, tc.width, profile, true, true, "")
				plain := ansi.Strip(got)
				lines := strings.Split(strings.TrimSuffix(plain, "\n\n"), "\n")
				if len(lines) != tc.height || strings.ContainsAny(plain, "█▀▄") {
					t.Fatalf("profile %v: wrong color layout or legacy block glyphs: %q", profile, got)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != tc.card {
						t.Fatalf("profile %v: expected %d-column frame: %q", profile, tc.card, got)
					}
				}
				if !strings.Contains(plain, tc.displayed) || !strings.Contains(plain, welcomeTitle) {
					t.Fatalf("complete identity/version lost: %q", got)
				}
				if tc.height == 9 {
					titleRow, captionColumn := 2, 3
					if profile > colorprofile.ANSI {
						titleRow, captionColumn = 3, 20
					}
					for row, text := range map[int]string{titleRow: welcomeTitle, titleRow + 1: tc.displayed} {
						index := strings.Index(lines[row], text)
						if index < 0 || ansi.StringWidth(lines[row][:index]) != 63 {
							t.Fatalf("metadata overlaps artwork or shifts columns: %q", got)
						}
					}
					if !strings.HasSuffix(lines[titleRow+1], tc.displayed+strings.Repeat(" ", tc.card-64-ansi.StringWidth(tc.displayed))+"│") ||
						strings.Index(lines[7], welcomeTagline) < 0 ||
						ansi.StringWidth(strings.Split(lines[7], welcomeTagline)[0]) != captionColumn {
						t.Fatalf("version padding or tagline changed: %q", got)
					}
					stampColumn := strings.Index(lines[8], ">_")
					if profile > colorprofile.ANSI {
						if stampColumn < 0 || ansi.StringWidth(lines[8][:stampColumn]) != tc.card-9 {
							t.Fatalf("cursor stamp shifted: %q", got)
						}
					} else if stampColumn >= 0 || strings.ContainsAny(plain, "▇▂") {
						t.Fatalf("ANSI16 fallback gained detailed decoration: %q", got)
					}
				}
			}
		})
	}
}

func TestWelcomePixelWordmarkGradient(t *testing.T) {
	for _, tc := range []struct {
		dark   bool
		colors []string
	}{
		{true, []string{"138;168;255", "128;158;242", "117;148;228", "107;137;215", "96;127;201", "86;117;188"}},
		{false, []string{"49;89;188", "60;98;191", "71;107;195", "81;115;198", "92;124;202", "103;133;205"}},
	} {
		got := renderWelcome("1.38.0", 76, colorprofile.TrueColor, tc.dark, true, "")
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		if len(lines) != 9 {
			t.Fatalf("missing six-row pixel wordmark: %q", got)
		}
		for row, color := range tc.colors {
			column := 3
			if row == 0 || row == 5 {
				column = 5
			}
			cell := welcomeAppearanceCells(t, got)[(row+1)*77+column]
			paint := cell.background
			if row == 0 {
				paint = cell.foreground
			}
			if paint != strings.ReplaceAll(color, ";", ",") {
				t.Fatalf("pixel row %d lost its visible color: %+v", row, cell)
			}
		}
	}
	backgroundSGR := regexp.MustCompile("\x1b\\[(?:4[0-7]|10[0-7])m")
	got := renderWelcome("1.38.0", 76, colorprofile.ANSI, true, true, "")
	colors := backgroundSGR.FindAllString(got, -1)
	if len(colors) == 0 {
		t.Fatalf("ANSI wordmark has no background pixels: %q", got)
	}
	for _, color := range colors {
		if color != colors[0] {
			t.Fatalf("ANSI wordmark must retain one focus color: %q", got)
		}
	}
}

func TestWelcomeOriginalFallbacksRemain(t *testing.T) {
	for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ASCII, colorprofile.TrueColor} {
		for _, unicode := range []bool{false, true} {
			if profile > colorprofile.ASCII && unicode {
				continue
			}
			for _, width := range []int{56, 65, 66, 72, 76, 82} {
				plain := ansi.Strip(renderWelcome("1.38.0", width, profile, true, unicode, ""))
				lines := strings.Split(strings.TrimSuffix(plain, "\n\n"), "\n")
				if len(lines) != 6 || !strings.Contains(lines[1], welcomeTitle) || !strings.Contains(lines[2], "v1.38.0") {
					t.Fatalf("original fallback layout changed: %q", plain)
				}
				for _, line := range lines {
					if ansi.StringWidth(line) != 56 {
						t.Fatalf("original fallback width changed: %q", plain)
					}
				}
				if unicode != strings.ContainsAny(plain, "█▀▄") || !unicode && strings.ContainsAny(plain, "┌┐└┘│─") {
					t.Fatalf("encoding or monochrome fallback changed: %q", plain)
				}
			}
		}
	}
}

func TestWelcomeVersionEscapesControls(t *testing.T) {
	got := renderWelcome("1.2.3\x1b]2;changed\a\u202e", 100, colorprofile.NoTTY, true, true, "")
	if strings.ContainsAny(got, "\x1b\a\u202e") || !strings.Contains(got, "1.2.3") {
		t.Fatalf("unsafe version: %q", got)
	}
}

func TestWelcomeLongVersionSizing(t *testing.T) {
	const version = "1.38.0-preview.20261009.abcdef0123456789"
	if got := renderWelcome(version, 40, colorprofile.NoTTY, true, true, ""); got != "" {
		t.Fatalf("long version must not be clipped into a 40-column card: %q", got)
	}
	got := renderWelcome(version, 80, colorprofile.NoTTY, true, true, "")
	lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
	if len(lines) != 6 || !strings.Contains(lines[2], "OpenAI CLI") || !strings.Contains(lines[2], "v"+version) {
		t.Fatalf("expanded card lost its complete title/version row: %q", got)
	}
	cardWidth := ansi.StringWidth(lines[0])
	if cardWidth <= 40 || cardWidth > 80 {
		t.Fatalf("expanded card width = %d; want 41..80", cardWidth)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) != cardWidth {
			t.Fatalf("expanded card has uneven rows: %q", got)
		}
	}
}

func TestWelcomeBorderAndVersionAlignment(t *testing.T) {
	for _, tc := range []struct{ width, card, height, titleRow, versionRow int }{
		{29, 29, 4, 1, 1}, {30, 30, 4, 1, 1}, {31, 31, 4, 1, 1}, {39, 39, 4, 1, 1},
		{40, 40, 6, 2, 2}, {55, 40, 6, 2, 2}, {56, 56, 6, 1, 2}, {80, 56, 6, 1, 2},
	} {
		got := renderWelcome("1.38.0", tc.width, colorprofile.NoTTY, true, true, "")
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		if len(lines) != tc.height || !strings.HasPrefix(lines[0], "┌") || !strings.HasSuffix(lines[0], "┐") ||
			!strings.HasPrefix(lines[tc.height-1], "└") || !strings.HasSuffix(lines[tc.height-1], "┘") {
			t.Fatalf("unexpected border at width %d: %q", tc.width, got)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != tc.card {
				t.Fatalf("uneven or oversized frame at width %d: %q", tc.width, got)
			}
		}
		name := strings.Index(lines[tc.titleRow], welcomeTitle)
		version := strings.Index(lines[tc.versionRow], "v1.38.0")
		if name < 0 || version < 0 {
			t.Fatalf("missing literal identity/version at width %d: %q", tc.width, got)
		}
		if tc.titleRow != tc.versionRow {
			if ansi.StringWidth(lines[tc.titleRow][:name]) != ansi.StringWidth(lines[tc.versionRow][:version]) {
				t.Fatalf("wordmark metadata is misaligned: %q", got)
			}
		} else {
			padding := 2
			if tc.width < 31 {
				padding = 1 // The complete 25-column tagline still fits at width 29.
			}
			if !strings.HasSuffix(lines[tc.versionRow], "v1.38.0"+strings.Repeat(" ", padding)+"│") {
				t.Fatalf("compact version is not right-aligned: %q", got)
			}
		}
	}
}

func TestWelcomeWordmarkFallsBackForLongVersions(t *testing.T) {
	for _, version := range []string{"1.38.0-preview.20261009", "dev-界界界界界界界"} {
		got := renderWelcome(version, 56, colorprofile.NoTTY, true, true, "")
		if !strings.Contains(got, version) || strings.ContainsAny(got, "▄▀█") {
			t.Fatalf("long version must survive in the text fallback: %q", got)
		}
		for _, line := range strings.Split(got, "\n") {
			if ansi.StringWidth(line) > 56 {
				t.Fatalf("fallback overflow: %q", got)
			}
		}
	}
}

func TestWelcomeWordmarkMetadataFitsBeforePadding(t *testing.T) {
	for _, version := range []string{"v123456789012345", "v界界界界界界界x"} {
		got := renderWelcome(version, 56, colorprofile.NoTTY, true, true, "")
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		if len(lines) != 6 || !strings.Contains(lines[1], welcomeTitle) || !strings.HasSuffix(lines[2], version+"    │") {
			t.Fatalf("complete 16-cell version must fit beside the wordmark: %q", got)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != 56 {
				t.Fatalf("metadata changed the frame width: %q", got)
			}
		}
	}
}

func TestWelcomeLightAndDarkThemes(t *testing.T) {
	for _, colorfgbg := range []string{"0;7", "0;15", "0;231", "0;255", "15;0", ""} {
		dark := welcomeDarkBackground(colorfgbg)
		wantDark := colorfgbg == "15;0" || colorfgbg == ""
		if dark != wantDark {
			t.Fatalf("COLORFGBG=%q: dark=%v; want %v", colorfgbg, dark, wantDark)
		}
		colored := renderWelcome("1.38.0", 40, colorprofile.TrueColor, dark, true, "")
		monochrome := renderWelcome("1.38.0", 40, colorprofile.NoTTY, dark, true, "")
		if ansi.Strip(colored) != monochrome {
			t.Fatal("theme changed the banner layout or content")
		}
	}
}

func TestWelcomeEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        bool
	}{
		{"TERM", "xterm-256color", true}, {"TERM", "", false}, {"TERM", "DUMB", false},
		{"CI", "true", false}, {"CI", "FALSE", true}, {"CI", "0", true},
		{"GITHUB_ACTIONS", "true", false}, {"GITLAB_CI", "1", false}, {"TF_BUILD", "True", false},
		{"BUILDKITE", "true", false}, {"JENKINS_URL", "https://invalid.example", false}, {"TEAMCITY_VERSION", "1", false},
		{"NO_COLOR", "1", true}, {"FORCE_COLOR", "0", true},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			getenv := func(name string) string {
				if name == tc.name {
					return tc.value
				}
				if name == "TERM" {
					return "xterm-256color"
				}
				return ""
			}
			for _, goos := range []string{"darwin", "linux", "windows"} {
				want := tc.want || goos == "windows" && tc.name == "TERM" && tc.value == ""
				if got := welcomeEnvironment(getenv, goos); got != want {
					t.Errorf("%s eligible = %v; want %v", goos, got, want)
				}
			}
		})
	}
	if !welcomeEnvironment(func(name string) string {
		if name == "WT_SESSION" {
			return "synthetic-session"
		}
		return ""
	}, "windows") {
		t.Fatal("Windows Terminal without TERM should remain eligible")
	}
}

type welcomeTerminalBuffer struct{ bytes.Buffer }

func (*welcomeTerminalBuffer) Fd() uintptr { return os.Stdout.Fd() }

func TestWelcomeRepeatedConfigurationInteractive(t *testing.T) {
	if !welcomeTerminal(os.Stdin) || !welcomeTerminal(os.Stdout) || !welcomeTerminal(os.Stderr) {
		t.Skip("requires an interactive terminal")
	}
	t.Setenv("TERM", "xterm-256color")
	for _, name := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "TF_BUILD", "BUILDKITE", "JENKINS_URL", "TEAMCITY_VERSION"} {
		t.Setenv(name, "")
	}
	var out welcomeTerminalBuffer
	root := &cli.Command{Name: "openai", Version: "9.8.7", Reader: os.Stdin, Writer: &out,
		Before: func(ctx context.Context, _ *cli.Command) (context.Context, error) {
			t.Fatal("welcome ran request setup")
			return ctx, nil
		},
		Commands: []*cli.Command{{Name: "models", Usage: "List models"}},
	}
	for _, args := range [][]string{{"openai"}, {"openai"}, {"openai", "--help"}, {"openai"}, {"openai", "help"}, nil} {
		out.Reset()
		normalized, _, err := Configure(root, args)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.Run(t.Context(), normalized); err != nil {
			t.Fatal(err)
		}
		wantBanner := len(args) <= 1
		if got := strings.Count(out.String(), welcomeTagline); got != 0 && !wantBanner || got != 1 && wantBanner {
			t.Fatalf("args=%q: banner count=%d, want banner=%t", args, got, wantBanner)
		}
		if !strings.Contains(out.String(), "models") || !strings.Contains(ansi.Strip(out.String()), "USAGE\n") {
			t.Fatalf("missing complete reference: %q", out.String())
		}
	}
}
