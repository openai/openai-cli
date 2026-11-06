package images

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWorkspaceBatchStyleKeepsFocusDistinct(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.color, m.dark = 96, 28, true, true, dark
		m.focus = "prompt"
		view := m.View().Content
		lines := strings.Split(view, "\n")
		titleIndex, _ := pickerNavigationLine(t, view, "Create image")
		title := lines[titleIndex]
		require.NotContains(t, title, "\x1b[1m")
		for _, label := range []string{"Settings", "Command", "Ctrl+C"} {
			index, _ := pickerNavigationLine(t, view, label)
			line := lines[index]
			require.NotContains(t, line, "\x1b[1m", label)
		}
		footer := lines[len(lines)-1]
		require.Contains(t, ansi.Strip(footer), "Ctrl+G generate")
		require.Contains(t, ansi.Strip(footer), "Ctrl+C exit")
		require.NotContains(t, ansi.Strip(footer), "[")
		require.NotEqual(t, ansi.Strip(footer), footer)
		require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, view, "typing must not paint a selected row")
		m.focus, m.selected = "options", 0
		view = m.View().Content
		require.Regexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, view, "only a focused option uses the selected fill")
		m.generating = true
		require.NotRegexp(t, `\x1b\[(?:[0-9]+;)*48[;:]`, m.View().Content)
	}
}

func TestImagePickerWorkspacePromptAlignsWithTitleImage(t *testing.T) {
	for _, width := range []int{40, 49, 80, 96, 109} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace = width, 28, true
		for _, prompt := range []string{"", "abc", "界e\u0301🙂"} {
			m.settings.prompt = prompt
			m.draft, m.cursor = []rune(prompt), 0
			view := m.View()
			lines := strings.Split(ansi.Strip(view.Content), "\n")
			titleRow, _ := pickerNavigationLine(t, view.Content, "Create image")
			titlePrefix, _, _ := strings.Cut(lines[titleRow], "image")
			require.Equal(t, ansi.StringWidth(titlePrefix), view.Cursor.X)
			prefix := ansi.Truncate(lines[view.Cursor.Y], view.Cursor.X, "")
			require.True(t, strings.HasSuffix(strings.TrimRight(prefix, " "), "Prompt"))
		}
	}
}

func TestImagePickerWorkspaceTableAndPromptSurviveResize(t *testing.T) {
	m := pickerForTest(t)
	m.workspace, m.color, m.height = true, false, 41
	m.settings.count = "3"
	m.draft = []rune(strings.Repeat("界", 90) + "ab")
	m.cursor = len(m.draft) - 1
	for _, width := range []int{109, 50, 40, 109} {
		m.width = width
		view := m.View()
		lines := strings.Split(ansi.Strip(view.Content), "\n")
		prompt := lines[view.Cursor.Y]
		require.True(t, strings.HasSuffix(ansi.Truncate(prompt, view.Cursor.X, ""), "a"))
		if width == 109 {
			require.Contains(t, prompt, "3 images per run│")
		} else {
			require.NotContains(t, prompt, "per run")
		}
		require.Contains(t, lines[view.Cursor.Y+1], "────")
		_, columns := pickerNavigationLine(t, view.Content, "Settings")
		_, option := pickerNavigationLine(t, view.Content, "Model")
		col := func(line, token string) int {
			left, _, found := strings.Cut(line, token)
			require.True(t, found)
			return ansi.StringWidth(left)
		}
		require.Equal(t, col(columns, "Settings"), col(option, "Model"))
		require.Equal(t, col(columns, "Value"), col(option, "gpt-image-"))
	}
}

func TestImagePickerWorkspaceFooterWrapsByContent(t *testing.T) {
	style := lipgloss.NewStyle()
	for _, width := range []int{40, 49, 70, 71, 72, 73, 74, 79, 80, 83, 84, 89, 90, 96} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace = width, 28, true
		for _, images := range []bool{false, true} {
			m.hasImages = images
			for _, focus := range []string{"prompt", "options", "command", "path", "images"} {
				m.focus, m.generating = focus, false
				idle := m.workspaceFooter(width-8, style, style)
				if focus == "prompt" || focus == "options" {
					require.Contains(t, idle[0], "Ctrl+G generate", "width=%d focus=%s", width, focus)
					require.Contains(t, strings.Join(idle, "\n"), "Ctrl+C exit")
				}
				m.generating = true
				busy := m.workspaceFooter(width-8, style, style)
				require.Len(t, busy, len(idle))
				require.Contains(t, busy[0], "Ctrl+C cancel generation")
				for _, line := range append(idle, busy...) {
					require.LessOrEqual(t, ansi.StringWidth(line), width-8)
				}
			}
		}
	}
}

func TestImagePickerWorkspaceFirstImageKeepsFormGeometry(t *testing.T) {
	for width := 40; width <= 100; width++ {
		m := pickerForTest(t)
		m.width, m.height, m.workspace = width, 20, true
		for _, focus := range []string{"prompt", "options", "command", "path"} {
			m.focus, m.hasImages, m.generating = focus, false, true
			before := m.View().Content
			m.hasImages = true
			after := m.View().Content
			require.Equal(t, strings.Count(before, "\n"), strings.Count(after, "\n"), "width=%d focus=%s", width, focus)
			beforeRow, _ := pickerNavigationLine(t, before, "Command")
			afterRow, _ := pickerNavigationLine(t, after, "Command")
			require.Equal(t, beforeRow, afterRow, "width=%d focus=%s", width, focus)
		}
	}
}

func TestImagePickerWorkspaceCommandExpandsOnlyOnFocus(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 80, 28, true
	m.settings.prompt = strings.Repeat("synthetic petals ", 20) + "final bloom"
	m.draft = []rune(m.settings.prompt)
	m.commandOffset = 2
	view := ansi.Strip(m.View().Content)
	row, line := pickerNavigationLine(t, view, "Command")
	require.Contains(t, line, "Command   openai images generate")
	require.Contains(t, line, "…")
	footer, _ := pickerNavigationLine(t, view, "Ctrl+C")
	require.Greater(t, footer, row)
	require.Contains(t, strings.Split(view, "\n")[footer-1], "╰")
	require.NotContains(t, view, " of ")
	m.focus = "command"
	m.scrollCommand("home")
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "openai images generate")
	require.Contains(t, view, "↑↓ scroll")
	m.scrollCommand("end")
	require.Contains(t, ansi.Strip(m.View().Content), "final bloom'")
}

func TestImagePickerWorkspaceFormFramePreservesContentAndControls(t *testing.T) {
	for _, width := range []int{40, 49, 80, 90, 96, 109} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.color = width, 40, true, false
		view := m.View()
		text := ansi.Strip(view.Content)
		for _, label := range []string{"Model", "Size / aspect", "Quality", "Images", "Background", "Save to", "More options"} {
			require.Contains(t, text, label, "width=%d", width)
		}
		require.Contains(t, text, "Ctrl+C exit")
		require.NotContains(t, view.Content, "\x1b[1;")
		if width >= 50 {
			require.Contains(t, text, "╭")
			require.Contains(t, text, "╰")
		}
		for _, line := range strings.Split(view.Content, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width)
		}
	}
}
