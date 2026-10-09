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
	for _, version := range []string{"1.38.0", "v2.0.0", "dev", "1.38.0-rc.1+abc123"} {
		for _, width := range []int{20, 28, 29, 32, 40, 80} {
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
				if strings.Count(strings.TrimSpace(plain), "\n") != 3 {
					t.Fatalf("banner exceeds four lines: %q", plain)
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
