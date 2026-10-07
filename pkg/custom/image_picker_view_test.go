package custom

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerPromptFramePreservesEdgesAndCursor(t *testing.T) {
	for _, width := range []int{50, 80, 120} {
		for _, prompt := range []string{"A tiny orange robot", "雪 e\u0301 1️⃣ ❤️", "👨‍👩‍👧‍👦", strings.Repeat("👨‍👩‍👧‍👦 ", 40)} {
			t.Run(fmt.Sprintf("width=%d/prompt=%q", width, prompt), func(t *testing.T) {
				m := pickerForTest(t)
				m.width = width
				m.settings.prompt, m.draft, m.cursor = prompt, []rune(prompt), len([]rune(prompt))
				for _, focus := range []string{"prompt", "options"} {
					m.focus = focus
					view := m.View().Content
					frame := ansi.Strip(imagePickerInlineFrame(view, width))
					require.Contains(t, frame, "╭─ Prompt")
					require.Contains(t, frame, "╮")
					require.Contains(t, frame, "╯")
					require.Equal(t, 2, strings.Count(frame, "│"), "both side borders must survive painting")
					if focus == "prompt" {
						require.Contains(t, frame, "▏", "long prompts must retain their cursor")
					}
				}
			})
		}
	}
}

func TestImagePickerPromptHighlightIsOnlyForTypedInput(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {49, 24}, {50, 21}, {80, 24}} {
		for _, dark := range []bool{false, true} {
			for _, color := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/dark=%t/color=%t", size[0], size[1], dark, color), func(t *testing.T) {
					m := pickerForTest(t)
					m.width, m.height, m.dark, m.color = size[0], size[1], dark, color
					for _, text := range []string{"", "A tiny orange robot"} {
						m.settings.prompt, m.draft, m.cursor = text, []rune(text), len([]rune(text))
						view := m.View().Content
						prompt, _, found := strings.Cut(view, "Settings")
						require.True(t, found)
						require.Contains(t, ansi.Strip(prompt), "Create image")
						require.NotContains(t, ansi.Strip(prompt), "openai")
						if color && text != "" && size[0] >= 50 && size[1] >= 21 {
							require.Regexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, prompt)
						} else {
							require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, prompt)
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
