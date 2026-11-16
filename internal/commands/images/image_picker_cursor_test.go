package images

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestImagePickerNativeCursorLifecycle(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cursor-output")
	require.NoError(t, err)
	defer f.Close()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &imagePickerInline{output: &imagePickerOutput{File: f, cancel: cancel}}
	p.start()
	read := func() string {
		data, err := os.ReadFile(f.Name())
		require.NoError(t, err)
		return string(data)
	}
	view := tea.NewView("Create image\nPrompt\ndfd\nControls")
	view.Cursor = tea.NewCursor(7, 2)
	view.Cursor.Shape = tea.CursorBar
	p.paint(view, 80, 24)
	initial := read()
	require.Contains(t, initial, ansi.SaveCursor)
	require.Contains(t, initial, ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable)
	require.NotContains(t, initial, "▏")
	view.Cursor = tea.NewCursor(5, 2)
	p.paint(view, 80, 24)
	move := strings.TrimPrefix(read(), initial)
	require.Equal(t, ansi.ResetModeTextCursorEnable+ansi.RestoreCursor+ansi.CursorDown(2)+
		ansi.CursorHorizontalAbsolute(6)+ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable, move)
	p.paint(view, 80, 24)
	require.Equal(t, initial+move, read(), "unchanged views must not write again")
	view.Cursor = nil
	before := read()
	p.paint(view, 80, 24)
	require.Equal(t, ansi.ResetModeTextCursorEnable+ansi.RestoreCursor, strings.TrimPrefix(read(), before))
	before = read()
	p.paint(view, 40, 12)
	require.Contains(t, strings.TrimPrefix(read(), before), ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow)
	before = read()
	p.close()
	require.True(t, strings.HasPrefix(strings.TrimPrefix(read(), before), ansi.RestoreCursor))
	require.Contains(t, strings.TrimPrefix(read(), before), ansi.SetCursorStyle(0))
	p.close()
	require.False(t, p.anchored)
}

func TestImagePickerFolderInputHasNormalWeight(t *testing.T) {
	m := pickerForTest(t)
	m.page, m.focus = "path", "path"
	for _, path := range []string{"", "/tmp/synthetic-folder"} {
		m.folder.draft, m.folder.cursor = []rune(path), len([]rune(path))
		label := path
		if label == "" {
			label = "Enter a folder path…"
		}
		view := m.View()
		i, _ := pickerNavigationLine(t, view.Content, label)
		line := strings.Split(view.Content, "\n")[i]
		require.NotContains(t, line, "\x1b[1m")
		require.NotContains(t, line, "›")
		require.NotContains(t, line, "▏")
		require.NotNil(t, view.Cursor)
		require.Equal(t, label, strings.TrimSpace(ansi.Strip(line)))
	}
}

func TestImagePickerAutoExplanationInValueColumn(t *testing.T) {
	m := pickerForTest(t)
	m.page, m.field, m.focus = "choose", "size", "options"
	m.settings.size = "auto"
	view := ansi.Strip(m.View().Content)
	_, auto := pickerNavigationLine(t, view, "Model chooses")
	_, square := pickerNavigationLine(t, view, "Square")
	require.Equal(t, ansi.StringWidth(square[:strings.Index(square, "1024")]),
		ansi.StringWidth(auto[:strings.Index(auto, "Model chooses")]))
	require.Greater(t, strings.Index(auto, "✓"), strings.Index(auto, "Model chooses"))
}
