package custom

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestCodexTerminalGuideEligibility(t *testing.T) {
	for _, test := range []struct {
		name, format, variable, value string
		terminal, want                bool
	}{
		{"auto terminal", "auto", "TERM", "xterm-256color", true, true},
		{"case insensitive auto", "AUTO", "TERM", "xterm", true, true},
		{"pipe", "auto", "TERM", "xterm", false, false},
		{"explicit text", "text", "TERM", "xterm", true, false},
		{"explicit json", "json", "TERM", "xterm", true, false},
		{"dumb", "auto", "TERM", "Dumb", true, false},
		{"CI", "auto", "CI", "true", true, false},
		{"CI disabled", "auto", "CI", "false", true, true},
		{"CI zero", "auto", "CI", "0", true, true},
		{"GitHub CI", "auto", "GITHUB_ACTIONS", "1", true, false},
		{"GitLab CI", "auto", "GITLAB_CI", "1", true, false},
		{"Azure CI", "auto", "TF_BUILD", "1", true, false},
		{"Buildkite CI", "auto", "BUILDKITE", "1", true, false},
		{"Jenkins CI", "auto", "JENKINS_URL", "synthetic", true, false},
		{"TeamCity CI", "auto", "TEAMCITY_VERSION", "synthetic", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			getenv := func(name string) string {
				if name == test.variable {
					return test.value
				}
				return ""
			}
			if got := codexTerminalGuideEligible(test.format, test.terminal, getenv); got != test.want {
				t.Fatalf("eligible = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCodexTerminalGuidePreservesInstructions(t *testing.T) {
	guide := codexGuide()
	for _, width := range []int{40, 80, 200} {
		for _, dark := range []bool{false, true} {
			text := ansi.Strip(renderCodexTerminalGuide(guide, width, dark))
			for _, installation := range guide.Installation {
				if !strings.Contains(text, "\n  "+installation.Command+"\n") {
					t.Errorf("width %d: changed copyable install command %q", width, installation.Command)
				}
			}
			for _, destination := range guide.Destinations {
				if !strings.Contains(text, "\n"+destination.URL+"\n") {
					t.Errorf("width %d: changed copyable URL %q", width, destination.URL)
				}
			}
			for _, want := range []string{"A separate CLI named codex.", "\n  codex\n", guide.DefaultConfig, guide.ProjectConfig,
				"CODEX_HOME", "\n  approval_policy = \"on-request\"\n", "\n  sandbox_mode = \"workspace-write\"\n"} {
				if !strings.Contains(text, want) {
					t.Errorf("width %d: guide missing %q", width, want)
				}
			}
			for _, line := range strings.Split(text, "\n") {
				if strings.HasPrefix(line, "https://") {
					continue // Links retain their logical line and soft-wrap in terminals.
				}
				if ansi.StringWidth(line) > min(width, 80) {
					t.Errorf("width %d: layout exceeds terminal width: %q", width, line)
				}
			}
		}
	}
}

func TestCodexTerminalGuideEscapesExternalText(t *testing.T) {
	guide := codexGuide()
	guide.Command = "synthetic\x1b]52;c;payload\x07\u202e"
	guide.Destinations[0].URL = "https://synthetic.invalid/\x1b[2J"
	text := ansi.Strip(renderCodexTerminalGuide(guide, 80, true))
	if strings.ContainsAny(text, "\x1b\x07\u202e") || !strings.Contains(text, `\u202e`) {
		t.Fatalf("unsafe guide: %q", text)
	}
	if !strings.Contains(text, "payload") || !strings.Contains(text, "synthetic.invalid") {
		t.Fatal("escaping dropped ordinary text")
	}
}

func TestCodexTerminalGuideColorPolicyAndFailures(t *testing.T) {
	for _, profile := range []colorprofile.Profile{colorprofile.NoTTY, colorprofile.ASCII, colorprofile.ANSI, colorprofile.TrueColor} {
		var output strings.Builder
		if err := writeCodexTerminalContent(t.Context(), &output, profile, codexGuide(), 40, true); err != nil {
			t.Fatal(err)
		}
		if profile == colorprofile.NoTTY && strings.Contains(output.String(), "\x1b") {
			t.Fatal("NO_COLOR output contains terminal escapes")
		}
		if strings.Contains(output.String(), "\x1b]") || strings.Contains(output.String(), "\x1b[?25") {
			t.Fatal("static guide queried or modified terminal state")
		}
	}
	failure := errors.New("synthetic write failure")
	for _, writer := range []io.Writer{codexGuideFailWriter{failure}, codexGuideShortWriter{}} {
		err := writeCodexTerminalContent(t.Context(), writer, colorprofile.NoTTY, codexGuide(), 40, false)
		if !errors.Is(err, failure) && !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("lost output failure: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output strings.Builder
	if err := writeCodexTerminalContent(ctx, &output, colorprofile.TrueColor, codexGuide(), 40, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("canceled guide wrote output")
	}
}

func TestCodexTerminalGuideBackgroundDoesNotQueryInput(t *testing.T) {
	for _, value := range []string{"", "15;0", "0;8", "synthetic"} {
		if !codexGuideDarkBackground(value) {
			t.Errorf("%q: expected safe dark default", value)
		}
	}
	for _, value := range []string{"0;7", "0;15", "0;231", "0;255"} {
		if codexGuideDarkBackground(value) {
			t.Errorf("%q: ignored explicit light background", value)
		}
	}
}

type codexGuideFailWriter struct{ err error }

func (w codexGuideFailWriter) Write([]byte) (int, error) { return 0, w.err }

type codexGuideShortWriter struct{}

func (codexGuideShortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
