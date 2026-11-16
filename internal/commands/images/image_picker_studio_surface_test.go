package images

import (
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestImageStudioSurfacePreservesSelectedRow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := pickerForTest(t)
		m.color, m.dark, m.profile = true, dark, colorprofile.TrueColor
		selected := m.studioSelection().Render("› Model  gpt-image-2")
		line := m.studioSurfaceRow(selected, 44)
		require.Equal(t, cliterminal.FitPrompt(ansi.Strip(selected), 44), ansi.Strip(line))
		require.Equal(t, 44, ansi.StringWidth(line))
		// Each row must reset its styling before the renderer emits the divider
		// or native image payload on the same terminal row.
		require.True(t, strings.HasSuffix(line, "\x1b[m"), "%q", line)
		if dark {
			require.Contains(t, line, "48;2;36;39;50")
			require.Contains(t, line, "48;2;52;61;88")
		} else {
			require.Contains(t, line, "48;2;243;244;247")
			require.Contains(t, line, "48;2;233;239;254")
		}
	}
}

func TestImageStudioSurfaceHonorsColorAndProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		color   bool
		profile colorprofile.Profile
		shaded  bool
	}{
		{"truecolor", true, colorprofile.TrueColor, true},
		{"256-colors", true, colorprofile.ANSI256, true},
		{"16-colors", true, colorprofile.ANSI, false},
		{"no-color", false, colorprofile.TrueColor, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := pickerForTest(t)
			m.color, m.profile = tc.color, tc.profile
			got := m.studioSurfaceRow("", 44)
			require.Equal(t, strings.Repeat(" ", 44), ansi.Strip(got))
			require.Equal(t, tc.shaded, strings.Contains(got, "\x1b["))
			if tc.profile == colorprofile.ANSI256 {
				require.NotContains(t, got, "48;2;")
			}
		})
	}
}

func TestImageStudioSurfaceKeepsCellWidth(t *testing.T) {
	m := pickerForTest(t)
	for _, width := range []int{0, 1, 8, 44} {
		got := m.studioSurfaceRow("  界e\u0301🙂 sunset", width)
		require.Equal(t, width, ansi.StringWidth(got))
		require.NotContains(t, got, "\n")
	}
}
