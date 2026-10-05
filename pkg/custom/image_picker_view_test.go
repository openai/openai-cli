package custom

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerPromptKeepsTerminalBackground(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {49, 24}, {50, 21}, {80, 24}} {
		for _, dark := range []bool{false, true} {
			for _, color := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/dark=%t/color=%t", size[0], size[1], dark, color), func(t *testing.T) {
					m := pickerForTest(t)
					m.width, m.height, m.dark, m.color = size[0], size[1], dark, color
					for _, focus := range []string{"prompt", "options"} {
						m.focus = focus
						view := m.View().Content
						prompt, _, found := strings.Cut(view, "Settings")
						require.True(t, found)
						require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, prompt, "prompt must keep the terminal background")
						require.NotContains(t, ansi.Strip(prompt), "│", "prompt edges remain open")
						require.Contains(t, ansi.Strip(prompt), "Prompt")
						if focus == "prompt" {
							require.Contains(t, prompt, "▏", "cursor identifies the editable prompt")
						} else {
							require.Contains(t, ansi.Strip(view), "› Model", "settings retain their focus marker")
						}
					}
				})
			}
		}
	}
}

func TestImagePickerResumedPromptKeepsTerminalBackground(t *testing.T) {
	previous := imagePickerSettings{prompt: "A tiny orange robot", model: defaultSavedImageModel, size: "1024x1024", quality: "auto", background: "auto", format: "png", count: "1"}
	m, err := newImagePicker(imagePickerOptions{initial: &previous, resuming: true, Shell: "bash"})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	view := m.View().Content
	require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, view)
	require.Contains(t, ansi.Strip(view), "Describe your image")
	require.Contains(t, ansi.Strip(view), "Ctrl+C exit")
	require.NotContains(t, view, previous.prompt)
}
