package images

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestImagePickerCursorMovementPreservesText(t *testing.T) {
	for _, width := range []int{40, 80} {
		m := pickerForTest(t)
		m.width = width
		m.settings.prompt, m.draft, m.cursor = "dfd", []rune("dfd"), 3
		promptLine := func() string {
			view := m.View()
			require.NotNil(t, view.Cursor)
			require.Equal(t, tea.CursorBar, view.Cursor.Shape)
			require.Equal(t, 4+m.cursor, view.Cursor.X)
			require.Equal(t, 2, view.Cursor.Y)
			require.NotContains(t, view.Content, "▏")
			_, line := pickerNavigationLine(t, view.Content, "dfd")
			return strings.TrimRight(line, " ")
		}
		before := promptLine()
		for range 3 {
			pickerKey(m, tea.KeyLeft)
			require.Equal(t, before, promptLine(), "moving left must not add a gap")
		}
		for range 2 {
			pickerKey(m, tea.KeyRight)
			require.Equal(t, before, promptLine(), "moving right must not add a gap")
		}
		m.Update(tea.PasteMsg{Content: "x"})
		require.Equal(t, "dfxd", m.settings.prompt)
		require.Contains(t, ansi.Strip(m.View().Content), "dfxd")
		require.Empty(t, m.result.Args)
		pickerLayoutAssertBounds(t, m)
	}
}

func TestImagePickerCursorPreservesGraphemesAndEscapes(t *testing.T) {
	for _, test := range []struct {
		text   string
		cursor int
		caret  string
	}{
		{"dfd", 0, "d"}, {"dfd", 1, "f"}, {"dfd", 2, "d"}, {"dfd", 3, " "},
		{"e\u0301!", 0, "e\u0301"}, {"e\u0301!", 1, "e\u0301"}, {"e\u0301!", 2, "!"},
		{"1️⃣!", 1, "1️⃣"}, {"1️⃣!", 2, "1️⃣"}, {"1️⃣!", 3, "!"},
		{"👨‍👩‍👧‍👦!", 3, "👨‍👩‍👧‍👦"}, {"👨‍👩‍👧‍👦!", 7, "!"},
	} {
		rendered, x := imagePickerEditLine([]rune(test.text), test.cursor, 200)
		if test.caret == " " {
			require.Equal(t, len(rendered), x)
		} else {
			_, _, read := cliterminal.Sequence(test.caret)
			prefix, _, _ := strings.Cut(rendered, test.caret[:read])
			require.GreaterOrEqual(t, x, ansi.StringWidth(prefix))
		}
	}
	for _, text := range []string{"", "dfd", "雪 e\u0301 1️⃣ ❤️ 👨‍👩‍👧‍👦", "a\n\tb\x1b[31m"} {
		draft := []rune(text)
		for cursor := 0; cursor <= len(draft); cursor++ {
			rendered, _ := imagePickerEditLine(draft, cursor, 200)
			expected := imagePickerLine(text)
			require.Equal(t, expected, ansi.Strip(rendered))
			require.NotContains(t, rendered, "\x1b[4m")
			require.NotContains(t, rendered, "▏")
			for _, cluster := range []string{"e\u0301", "1️⃣", "❤️", "👨‍👩‍👧‍👦"} {
				if strings.Contains(text, cluster) {
					require.Contains(t, rendered, cluster, "cursor styles must not split a combined glyph")
				}
			}
		}
	}
}

func TestImagePickerFolderCursorPreservesText(t *testing.T) {
	m := pickerForTest(t)
	m.page, m.focus = "path", "path"
	m.folder.draft = []rune("/tmp/synthetic-folder")
	m.folder.cursor = len(m.folder.draft)
	pathLine := func() string {
		view := m.View()
		require.NotNil(t, view.Cursor)
		require.Equal(t, 4+m.folder.cursor, view.Cursor.X)
		require.NotContains(t, view.Content, "▏")
		_, line := pickerNavigationLine(t, view.Content, "/tmp/synthetic-folder")
		return strings.TrimRight(line, " ")
	}
	before := pathLine()
	for m.folder.cursor > 0 {
		m.folder.cursor--
		require.Equal(t, before, pathLine())
	}
}

func TestImagePickerInlineCursorMatchesRenderedText(t *testing.T) {
	for _, layout := range []struct {
		name          string
		width, height int
		folder        bool
	}{
		{"compact prompt", 40, 16, false},
		{"framed prompt", 80, 24, false},
		{"compact folder", 40, 16, true},
		{"framed folder", 80, 24, true},
	} {
		t.Run(layout.name, func(t *testing.T) {
			m := pickerForTest(t)
			m.width, m.height = layout.width, layout.height
			const draft = "cursor-check"
			m.settings.prompt, m.draft = draft, []rune(draft)
			if layout.folder {
				m.page, m.focus = "path", "path"
				m.folder.draft = []rune(draft)
			}
			for cursor := 0; cursor <= len(draft); cursor++ {
				m.cursor, m.folder.cursor = cursor, cursor
				view := m.View()
				require.NotNil(t, view.Cursor)
				row := ansi.Strip(strings.Split(view.Content, "\n")[view.Cursor.Y])
				start := strings.Index(row, draft)
				require.NotEqual(t, -1, start)
				require.Equal(t, ansi.StringWidth(row[:start])+cursor, view.Cursor.X)
				require.Equal(t, tea.CursorBar, view.Cursor.Shape)
			}
		})
	}
}

func TestImagePickerPromptFramePreservesEdgesAndCursor(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.color = 50, false
	oversized := strings.Repeat("👨‍", 30) + "👨"
	m.settings.prompt, m.draft, m.cursor = oversized, []rune(oversized), 0
	row := strings.Split(m.View().Content, "\n")[2]
	require.NotNil(t, m.View().Cursor)
	require.NotContains(t, row, "▏")
	require.NotContains(t, row, "\x1b[4m")
	require.Equal(t, oversized, m.settings.prompt, "display clipping must preserve the complete prompt")
	for _, width := range []int{50, 80, 120} {
		for _, prompt := range []string{"A tiny orange robot", "雪 e\u0301 1️⃣ ❤️", "👨‍👩‍👧‍👦", strings.Repeat("👨‍👩‍👧‍👦 ", 40)} {
			t.Run(fmt.Sprintf("width=%d/prompt=%q", width, prompt), func(t *testing.T) {
				m := pickerForTest(t)
				m.width = width
				m.settings.prompt, m.draft, m.cursor = prompt, []rune(prompt), len([]rune(prompt))
				for _, focus := range []string{"prompt", "options"} {
					m.focus = focus
					view := m.View().Content
					promptView, _, _ := strings.Cut(view, "Settings")
					frame := ansi.Strip(cliterminal.InlineFrame(promptView, width))
					require.Contains(t, frame, "╭ Prompt")
					require.Contains(t, frame, "╮")
					require.Contains(t, frame, "╯")
					require.Equal(t, 2, strings.Count(frame, "│"), "both side borders must survive painting")
					if focus == "prompt" {
						require.NotNil(t, m.View().Cursor)
						require.Less(t, m.View().Cursor.X, width-4)
					}
				}
			})
		}
	}
}

func TestImagePickerFocusKeepsTerminalBackground(t *testing.T) {
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
						for _, focus := range []string{"prompt", "options", "command", "path"} {
							m.focus = focus
							require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, m.View().Content)
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
	require.Contains(t, ansi.Strip(view), "[Ctrl+C] Quit")
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
					if !resuming {
						require.True(t, strings.HasPrefix(lines[len(lines)-1], "    ["), "footer keys must share the content inset")
					}
					pickerLayoutAssertBounds(t, m)
				})
			}
		}
	}
}

func TestImagePickerResumedPromptShowsControls(t *testing.T) {
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
					require.Contains(t, lines[len(lines)-1], "[Ctrl+C] Quit")
					require.Contains(t, view, "[Enter] ")
					if focus != "path" || size[0] == 80 {
						require.NotContains(t, lines[len(lines)-1], "…", "context controls must remain complete")
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

func TestImagePickerResumedPromptRetainsQuietCommandAndSettings(t *testing.T) {
	prompt := strings.Repeat("orange robot ", 8) + "final flower"
	m, err := newImagePicker(imagePickerOptions{Prompt: prompt, Shell: "bash", resuming: true, initialNote: "All images saved. Edit your prompt or settings to create more."})
	require.NoError(t, err)
	m.width, m.height = 80, 24
	view := ansi.Strip(m.View().Content)
	for _, line := range m.commandLines(74) {
		require.Contains(t, view, line)
	}
	rows := m.rows()
	require.Len(t, rows, 7)
	for _, row := range rows {
		require.Contains(t, view, row.label)
	}
	require.Contains(t, view, m.note)
	require.Contains(t, view, "[Ctrl+C] Quit")
	require.Contains(t, view, "[Ctrl+G] Generate")
	pickerLayoutAssertBounds(t, m)
}

func TestImagePickerFooterControlsSurviveResizeAndNotices(t *testing.T) {
	for _, width := range []int{40, 49, 60, 80, 120} {
		for height := 12; height <= 24; height++ {
			for _, focus := range []string{"prompt", "options", "command", "path"} {
				t.Run(fmt.Sprintf("%dx%d/%s", width, height, focus), func(t *testing.T) {
					m := pickerForTest(t)
					m.width, m.height, m.focus = width, height, focus
					m.insertPrompt(strings.Repeat("synthetic prompt ", 30))
					m.note = "File type changed to PNG to keep transparency."
					if focus == "path" {
						m.page = "path"
					}
					view := ansi.Strip(m.View().Content)
					lines := strings.Split(view, "\n")
					footer := lines[len(lines)-1]
					require.Contains(t, footer, "[Ctrl+C] Quit")
					require.Contains(t, view, "[Enter]")
					require.NotContains(t, footer, "…", "never clip essential controls")
					require.True(t, strings.HasPrefix(footer, "    ["), "align controls with settings and prompt text")
					require.Empty(t, m.result.Args)
					pickerLayoutAssertBounds(t, m)
				})
			}
		}
	}
}

func TestImagePickerCommandFocusPreservesContentWithoutFrame(t *testing.T) {
	for _, width := range []int{50, 80, 120} {
		for _, prompt := range []string{"A tiny orange robot", strings.Repeat("雪 e\u0301 1️⃣ ❤️ 👨‍👩‍👧‍👦 ", 20)} {
			m := pickerForTest(t)
			m.width, m.height, m.settings.prompt = width, 24, prompt
			for _, focus := range []string{"prompt", "options", "command"} {
				m.focus = focus
				commandWidth, commandHeight := m.commandDimensions()
				for _, offset := range []int{0, len(m.commandLines(commandWidth)) - commandHeight} {
					m.commandOffset = max(0, offset)
					view := m.View().Content
					painted := ansi.Strip(view)
					require.NotContains(t, painted, "╭ Command")
					lines, _, _ := m.commandPreview(commandWidth, commandHeight)
					if focus == "command" {
						require.Contains(t, painted, "› "+lines[0], "retain a focus marker without color")
						for _, line := range lines {
							require.Contains(t, painted, line, "preserve every preview byte")
						}
					} else {
						require.Contains(t, painted, "openai images generate", "unfocused preview always starts at the command name")
						for _, line := range lines {
							require.Contains(t, painted, line)
						}
					}
					pickerLayoutAssertBounds(t, m)
				}
			}
		}
	}
}

func TestImagePickerFooterKeepsKeysDistinctWithoutSelectionFill(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			m := pickerForTest(t)
			m.width, m.height, m.focus, m.dark, m.color = 80, 24, "options", dark, color
			lines := strings.Split(m.View().Content, "\n")
			footer := strings.Join(lines[len(lines)-2:], "\n")
			for _, hint := range []string{"[↑↓] Move", "[Enter] Select", "[Ctrl+G] Generate", "[Tab] Switch", "[Ctrl+C] Quit"} {
				require.Contains(t, ansi.Strip(footer), hint)
			}
			require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, footer, "available shortcuts are not selected rows")
			if color {
				require.NotEqual(t, ansi.Strip(footer), footer)
			}
		}
	}
}

func TestImagePickerCompactFolderKeepsNavigationHintComplete(t *testing.T) {
	for width := 40; width <= 44; width++ {
		for height := 12; height <= 14; height++ {
			for _, focus := range []string{"prompt", "options", "command"} {
				m := pickerForTest(t)
				m.width, m.height, m.focus, m.page = width, height, focus, "folder"
				view := ansi.Strip(m.View().Content)
				_, heading := pickerNavigationLine(t, view, "Save to")
				if focus == "command" {
					require.Contains(t, heading, "[↑↓] Scroll")
				} else {
					require.Contains(t, heading, "[Tab] Switch")
				}
				require.NotContains(t, heading, "…")
				pickerLayoutAssertBounds(t, m)
			}
		}
	}
}

func TestImagePickerCommandWrapKeepsFlagsAndModelWhole(t *testing.T) {
	for _, width := range []int{34, 74, 98} {
		m := pickerForTest(t)
		m.insertPrompt("synthetic 雪 prompt with 'quotes' and spaces")
		lines := m.commandLines(width)
		wrapped := strings.Join(lines, "\n")
		for _, token := range []string{m.settings.model, "--output-format", "--background", "--quality", "--prompt"} {
			require.Contains(t, wrapped, token, "wrap between words that fit the viewport")
		}
		require.Equal(t, formatImagePickerCommandBash(m.settings.args()), strings.Join(lines, ""))
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), width)
		}
	}
}
