package images

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestStudioEditorUsesOnlyVisibleRows(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.focus = 110, 28, true, "prompt"
	m.settings.outputDir = ""
	for _, text := range []string{"", "a lake", "a lake\n", "a\nb\nc", "a\nb\nc\nd"} {
		m.draft, m.settings.prompt = []rune(text), text
		m.cursor = len(m.draft)
		view := m.View()
		rows := strings.Split(view.Content, "\n")
		promptRows := min(studioDraftRows, 1+strings.Count(text, "\n"))
		require.NotNil(t, view.Cursor)
		require.Equal(t, imageStudioBodyY+promptRows, view.Cursor.Y, text)
		require.Empty(t, rows[1+promptRows], text)
		require.Contains(t, rows[2+promptRows], "Settings", text)
		require.Contains(t, rows[len(rows)-1], "More options", text)
		require.Contains(t, view.Content, "Output")
		require.NotContains(t, view.Content, "Size / aspect")
		require.Contains(t, view.Content, "~/Downloads/gpt-images/")
		require.NotContains(t, view.Content, "(Default)")
	}
	require.Empty(t, m.settings.outputDir)
	m.settings.outputDir = "/work/art (Default)"
	require.Contains(t, m.View().Content, "/work/art (Default)")
}

func TestStudioOutputGroupingPreservesNavigation(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.focus = 110, 28, true, "prompt"
	m.draft, m.settings.prompt = []rune("first\nsecond\nthird"), "first\nsecond\nthird"
	m.cursor = len(m.draft)
	m.settings.outputDir = "/synthetic/very-long-parent/code/img-samples"
	before := m.settings
	pickerKey(m, tea.KeyDown)
	for i, label := range []string{"Model", "Size", "Quality", "Images", "Background", "Save to", "More options"} {
		require.Equal(t, i, m.selected)
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "› "+label)
		require.Contains(t, view, "code/img-samples")
		require.Contains(t, view, "…")
		require.LessOrEqual(t, len(strings.Split(view, "\n")), 17)
		for _, row := range strings.Split(view, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(row), 44)
		}
		if i < 6 {
			pickerKey(m, tea.KeyDown)
		}
	}
	require.Equal(t, before, m.settings)
	m.settings.outputDir = "/Users/synthetic/code/img-samples"
	require.Contains(t, ansi.Strip(m.View().Content), "/Users/…/code/img-samples")
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "more", m.page)
	pickerKey(m, tea.KeyEsc)
	require.Equal(t, "settings", m.page)
	m.focus, m.selected = "options", 5
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "folder", m.page)
}

func TestStudioActionsInspectOrSubmit(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.hasImages = 133, 39, true, true
	g, ok := imageStudioLayout(m.width, m.height)
	require.True(t, ok)
	_, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	require.True(t, strings.HasSuffix(m.settings.prompt, "q"))
	pickerKey(m, tea.KeyUp)
	require.Equal(t, "actions", m.focus)
	pickerKey(m, tea.KeyRight)
	require.Contains(t, m.studioActions(m.width-8), "› View command")
	pickerKey(m, tea.KeyEnter)
	require.Equal(t, "command", m.focus)
	require.Contains(t, m.View().Content, "openai images generate")
	require.Nil(t, pickerKey(m, tea.KeyEnter))
	require.Equal(t, "command", m.focus)
	require.Empty(t, m.result.Args)
	pickerKey(m, tea.KeyEsc)
	require.Equal(t, "prompt", m.focus)
	require.NotContains(t, m.View().Content, "openai images generate")
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, "images", m.focus)
	m.focus = "prompt"
	pickerKey(m, tea.KeyUp)
	m.Update(tea.WindowSizeMsg{Width: 75, Height: 24})
	require.Equal(t, "prompt", m.focus)
	m.Update(tea.WindowSizeMsg{Width: 133, Height: 39})
	require.Contains(t, m.studioHints(g, false, false), "Tab pane")
	pickerKey(m, tea.KeyUp)
	pickerKey(m, tea.KeyEnter)
	require.NotEmpty(t, m.result.Args)
	require.False(t, m.result.PrintOnly)
	require.Nil(t, pickerKey(m, tea.KeyEnter))
}

func TestStudioCommandScrollAndPrint(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 110, 28, true
	m.draft = nil
	m.cursor = 0
	m.insertPrompt(strings.Repeat("shell 'quoted' value ", 160) + "source end")
	pickerKey(m, tea.KeyHome)
	pickerKey(m, tea.KeyUp)
	pickerKey(m, tea.KeyLeft)
	pickerKey(m, tea.KeyEnter)
	require.Contains(t, m.View().Content, "openai images generate")
	require.NotContains(t, m.View().Content, "source end")
	pickerKey(m, tea.KeyEnd)
	rows := strings.Split(m.View().Content, "\n")[1:] // Skip the inspector heading.
	for i := range rows {
		rows[i] = strings.TrimPrefix(rows[i], "  ")
	}
	visible := strings.Join(rows, "") // Visual wraps preserve the command bytes.
	require.Contains(t, visible, "source end")
	require.NotContains(t, visible, "openai images generate")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	pickerFinishFolderCommand(m, cmd)
	require.True(t, m.result.PrintOnly)
	require.Equal(t, m.settings.prompt, pickerArg(t, m.result.Args, "--prompt"))
}

func TestStudioPromptWrapAndCaret(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 110, 28, true
	g, ok := imageStudioLayout(m.width, m.height)
	require.True(t, ok)
	m.draft = []rune(strings.Repeat("a", g.editorWidth-4) + "界e\u0301x\nline three\nlast")
	m.settings.prompt = string(m.draft)
	m.cursor = g.editorWidth - 4 + 2 // Inside e and its accent: the caret belongs to the complete glyph.
	v := m.View()
	require.Contains(t, v.Content, "界e\u0301x")
	require.NotNil(t, v.Cursor)
	require.Equal(t, 8, v.Cursor.X)
	require.Equal(t, imageStudioBodyY+2, v.Cursor.Y)
	pickerKey(m, tea.KeyUp)
	require.Equal(t, "prompt", m.focus)
	require.Equal(t, 2, m.cursor)
	pickerKey(m, tea.KeyUp)
	require.Equal(t, "actions", m.focus)
	pickerKey(m, tea.KeyDown)
	require.Equal(t, 2, m.cursor)
	pickerKey(m, tea.KeyDown)
	require.Equal(t, g.editorWidth-4+1, m.cursor)
	pickerKey(m, tea.KeyEnd)
	v = m.View()
	require.Contains(t, v.Content, "  last")
	require.Equal(t, imageStudioBodyY+3, v.Cursor.Y)
	require.NotContains(t, v.Content, strings.Repeat("a", g.editorWidth-4))
	pickerKey(m, tea.KeyDown)
	require.Equal(t, "options", m.focus)
	require.Nil(t, m.View().Cursor)
	m.generating, m.focus = true, "prompt"
	require.Nil(t, m.View().Cursor)
	require.NotContains(t, m.studioActions(m.width-8), "Ctrl+G")
	require.NotContains(t, m.studioHints(g, false, false), "generate")
	require.Contains(t, m.studioHints(g, false, false), "cancel")
}

func TestStudioFolderEditorHidesUnavailableActions(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 133, 39, true
	m.openFolderEditor("")
	require.NotRegexp(t, `Ctrl\+G|View command`, m.studioActions(m.width-8))
}

func TestStudioPaneSwitchRestoresEditor(t *testing.T) {
	for _, initial := range []string{"prompt", "options", "command", "actions"} {
		for _, images := range []bool{false, true} {
			m := pickerForTest(t)
			m.width, m.height, m.workspace, m.hasImages = 110, 28, true, images
			m.focus, m.selected, m.cursor, m.commandOffset = initial, 3, 2, 1
			prompt := string(m.draft)
			require.Nil(t, pickerKey(m, tea.KeyTab))
			require.Equal(t, "images", m.focus)
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			require.Equal(t, initial, m.focus)
			require.Equal(t, 3, m.selected)
			require.Equal(t, 2, m.cursor)
			require.Equal(t, 1, m.commandOffset)
			require.Equal(t, prompt, string(m.draft))
			require.Empty(t, m.result.Args)
		}
	}
}
