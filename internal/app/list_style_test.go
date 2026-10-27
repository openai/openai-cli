package app

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/stretchr/testify/require"
)

func TestListStyleKeepsRedirectedOutputPlain(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	var output strings.Builder
	style := listNavigationStyle(ShowJSONOpts{Stdout: &output})
	require.Equal(t, "ID\nfile_synthetic\n", style.Table("ID\nfile_synthetic\n"))
}

func TestListStyleTerminalColorPolicy(t *testing.T) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("requires terminal stdout")
	}
	for _, name := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "COLORFGBG"} {
		t.Setenv(name, "")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	for _, test := range []struct {
		name, variable, value, format string
		colored                       bool
	}{
		{"dark", "COLORFGBG", "15;0", "auto", true},
		{"light", "COLORFGBG", "0;15", "", true},
		{"no color", "NO_COLOR", "1", "auto", false},
		{"nonempty no color", "NO_COLOR", "0", "auto", false},
		{"force zero", "FORCE_COLOR", "0", "auto", false},
		{"clicolor zero", "CLICOLOR", "0", "auto", false},
		{"dumb", "TERM", "dumb", "auto", false},
		{"explicit text", "COLORFGBG", "15;0", "text", false},
		{"explicit json", "COLORFGBG", "15;0", "json", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FORCE_COLOR", "1")
			t.Setenv(test.variable, test.value)
			style := listNavigationStyle(ShowJSONOpts{Stdout: os.Stdout, Format: test.format})
			text := style.Table("ID\nfile_synthetic\n")
			require.Equal(t, test.colored, strings.Contains(text, "\x1b["))
		})
	}
}
