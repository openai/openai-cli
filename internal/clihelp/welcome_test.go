package clihelp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/urfave/cli/v3"
)

func TestWelcomeLayout(t *testing.T) {
	for _, version := range []string{"", "1.38.0", "v2.0.0", "dev", "dev-界", "1.38.0-rc.1+abc123"} {
		for _, width := range []int{20, 28, 29, 31, 39, 40, 80} {
			for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ANSI, colorprofile.TrueColor} {
				got := renderWelcome(version, width, profile, true)
				if got == "" {
					if width >= 40 {
						t.Fatalf("missing header at width %d for %q", width, version)
					}
					continue
				}
				plain := ansi.Strip(got)
				if !strings.Contains(plain, version) || !strings.Contains(plain, "What are we making today?") {
					t.Fatalf("missing version or greeting: %q", plain)
				}
				wantHeight := 4
				if width >= 40 {
					wantHeight = 6
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

func TestWelcomeVersionEscapesControls(t *testing.T) {
	got := renderWelcome("1.2.3\x1b]2;changed\a\u202e", 100, colorprofile.NoTTY, true)
	if strings.ContainsAny(got, "\x1b\a\u202e") || !strings.Contains(got, "1.2.3") {
		t.Fatalf("unsafe version: %q", got)
	}
}

func TestWelcomeLongVersionSizing(t *testing.T) {
	const version = "1.38.0-preview.20261009.abcdef0123456789"
	if got := renderWelcome(version, 40, colorprofile.NoTTY, true); got != "" {
		t.Fatalf("long version must not be clipped into a 40-column card: %q", got)
	}
	got := renderWelcome(version, 80, colorprofile.NoTTY, true)
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
	for _, width := range []int{29, 30, 31, 39, 40, 80} {
		got := renderWelcome("1.38.0", width, colorprofile.NoTTY, true)
		lines := strings.Split(strings.TrimSuffix(got, "\n\n"), "\n")
		wantHeight, titleRow, padding := 4, 1, 1
		if width >= 31 {
			padding = 2
		}
		if width >= 40 {
			wantHeight, titleRow = 6, 2
		}
		if len(lines) != wantHeight || !strings.HasPrefix(lines[0], "┌") || !strings.HasSuffix(lines[0], "┐") ||
			!strings.HasPrefix(lines[wantHeight-1], "└") || !strings.HasSuffix(lines[wantHeight-1], "┘") {
			t.Fatalf("unexpected border at width %d: %q", width, got)
		}
		if gotWidth := ansi.StringWidth(lines[0]); gotWidth != min(width, 40) {
			t.Fatalf("card width = %d; want %d", gotWidth, min(width, 40))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) != ansi.StringWidth(lines[0]) {
				t.Fatalf("uneven border at width %d: %q", width, got)
			}
		}
		namePrefix := "│" + strings.Repeat(" ", padding) + "OpenAI CLI"
		if !strings.HasPrefix(lines[titleRow], namePrefix) || !strings.Contains(lines[titleRow], "v1.38.0") {
			t.Fatalf("title/version row wrapped at width %d: %q", width, got)
		}
		versionEnd := strings.Index(lines[titleRow], "v1.38.0") + len("v1.38.0")
		if ansi.StringWidth(lines[titleRow][:versionEnd]) != ansi.StringWidth(lines[0])-padding-1 {
			t.Fatalf("version is not right-aligned at width %d: %q", width, got)
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
		colored := renderWelcome("1.38.0", 40, colorprofile.TrueColor, dark)
		monochrome := renderWelcome("1.38.0", 40, colorprofile.NoTTY, dark)
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
			if got := welcomeEnvironment(getenv); got != tc.want {
				t.Fatalf("eligible = %v; want %v", got, tc.want)
			}
		})
	}
	if !welcomeEnvironment(func(name string) string {
		if name == "WT_SESSION" {
			return "synthetic-session"
		}
		return ""
	}) {
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
		want := 0
		if len(args) <= 1 {
			want = 1
		}
		if got := strings.Count(out.String(), "What are we making today?"); got != want {
			t.Fatalf("args=%q: got %d banners; want %d", args, got, want)
		}
		if !strings.Contains(out.String(), "models") || want == 1 && !strings.Contains(out.String(), "v9.8.7") {
			t.Fatalf("missing command or version: %q", out.String())
		}
	}
}
