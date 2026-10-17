package terminal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestAutomaticPresentationEligibility(t *testing.T) {
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
			if got := AutomaticPresentation(test.format, test.terminal, getenv); got != test.want {
				t.Fatalf("eligible = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDarkBackgroundDoesNotQueryInput(t *testing.T) {
	for _, value := range []string{"", "15;0", "0;8", "synthetic"} {
		if !DarkBackground(value) {
			t.Errorf("%q: expected safe dark default", value)
		}
	}
	for _, value := range []string{"0;7", "0;15", "0;231", "0;255"} {
		if DarkBackground(value) {
			t.Errorf("%q: ignored explicit light background", value)
		}
	}
}

func TestColorProfileRespectsDestinationAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name     string
		terminal bool
		environ  []string
		want     colorprofile.Profile
	}{
		{"redirected force color", false, []string{"FORCE_COLOR=1"}, colorprofile.NoTTY},
		{"redirected clicolor force", false, []string{"CLICOLOR_FORCE=1"}, colorprofile.NoTTY},
		{"terminal forced color", true, []string{"FORCE_COLOR=1"}, colorprofile.ANSI256},
		{"terminal no color wins", true, []string{"FORCE_COLOR=1", "NO_COLOR=1"}, colorprofile.NoTTY},
		{"terminal force zero wins", true, []string{"FORCE_COLOR=0", "CLICOLOR_FORCE=1"}, colorprofile.NoTTY},
		{"last duplicate wins", true, []string{"FORCE_COLOR=1", "NO_COLOR=1", "NO_COLOR="}, colorprofile.ANSI256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var destination strings.Builder
			if got := ColorProfile(&destination, tc.environ, tc.terminal); got != tc.want {
				t.Fatalf("color profile = %v; want %v", got, tc.want)
			}
			if destination.Len() != 0 {
				t.Fatal("color detection wrote to the output destination")
			}
		})
	}
}
