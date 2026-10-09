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
					require.Contains(t, frame, "╭ Prompt")
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
	require.NotContains(t, ansi.Strip(view), "Ctrl+C exit")
	require.NotContains(t, view, previous.prompt)
}

func TestImagePickerSectionSpacingAndAlignment(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		for _, color := range []bool{false, true} {
			for _, resuming := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/color=%t/resuming=%t", size[0], size[1], color, resuming), func(t *testing.T) {
					m, err := newImagePicker(imagePickerOptions{Prompt: "Synthetic robot", Shell: "bash", resuming: resuming})
					require.NoError(t, err)
					m.width, m.height, m.color = size[0], size[1], color
					lines := strings.Split(ansi.Strip(m.View().Content), "\n")
					require.Equal(t, "  Create image", lines[0])
					require.Contains(t, lines[1], "Prompt", "the prompt must immediately follow the title")
					require.Equal(t, 4, ansi.StringWidth(strings.Split(lines[1], "Prompt")[0]))
					require.Contains(t, lines[2], "Synthetic robot")
					require.Equal(t, 4, ansi.StringWidth(strings.Split(lines[2], "Synthetic robot")[0]))
					headingRow := 3
					if size[0] == 80 {
						require.Contains(t, lines[3], "╰")
						headingRow = 4
					}
					require.True(t, strings.HasPrefix(lines[headingRow], "    Settings"), "settings must follow the prompt without a blank row")
					require.True(t, strings.HasPrefix(lines[headingRow+1], "    Model"), "section headings and content must share their inset")
					pickerLayoutAssertBounds(t, m)
				})
			}
		}
	}
}

func TestImagePickerResumedPromptHidesOnlyPromptHint(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}} {
		for _, resuming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/resuming=%t", size[0], size[1], resuming), func(t *testing.T) {
				m, err := newImagePicker(imagePickerOptions{Prompt: "Synthetic robot", Shell: "bash", resuming: resuming})
				require.NoError(t, err)
				m.width, m.height = size[0], size[1]
				for _, focus := range []string{"prompt", "options", "command", "path", "prompt"} {
					m.focus, m.page = focus, "settings"
					if focus == "path" {
						m.page = "path"
					}
					view := ansi.Strip(m.View().Content)
					lines := strings.Split(view, "\n")
					if focus == "prompt" && resuming {
						require.NotContains(t, view, "Ctrl+C exit")
						require.NotContains(t, view, "Enter generate")
					} else {
						require.Contains(t, lines[len(lines)-1], "Ctrl+C exit")
						require.Contains(t, lines[len(lines)-1], "Enter ")
						if focus != "path" || size[0] == 80 {
							require.NotContains(t, lines[len(lines)-1], "…", "context controls must remain complete")
						}
					}
					pickerLayoutAssertBounds(t, m)
				}
				m.width, m.height = 39, 11
				view := ansi.Strip(m.View().Content)
				require.Contains(t, view, "Resize to at least 40 x 12.")
				require.Contains(t, view, "Esc prompt · Ctrl+C quit")
				require.Empty(t, m.result.Args, "view changes must not submit the draft")
			})
		}
	}
}

func TestImagePickerResumedPromptRetainsFullCommandAndSettings(t *testing.T) {
	prompt := strings.Repeat("orange robot ", 8) + "final flower"
	m, err := newImagePicker(imagePickerOptions{Prompt: prompt, Shell: "bash", resuming: true, initialNote: "All images saved. Edit your prompt or settings to create more."})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	view := ansi.Strip(m.View().Content)
	commandLines := m.commandLines(74)
	require.Len(t, commandLines, 4, "exercise the complete command preview budget")
	for _, line := range commandLines {
		require.Contains(t, view, line)
	}
	rows := m.rows()
	require.Len(t, rows, 7)
	for _, row := range rows {
		require.Contains(t, view, row.label)
	}
	require.Contains(t, view, m.note)
	require.NotContains(t, view, "Ctrl+C exit")
	pickerLayoutAssertBounds(t, m)
}
