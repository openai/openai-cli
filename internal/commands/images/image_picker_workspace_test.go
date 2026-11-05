package images

import (
	"context"
	clioutput "github.com/openai/openai-cli/internal/output"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/stretchr/testify/require"
)

func TestImagePickerWorkspaceCompactFormKeepsGeometryDuringGeneration(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {49, 24}, {80, 24}, {96, 28}, {140, 40}} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace = size[0], size[1], true
		for _, focus := range []string{"prompt", "options", "command"} {
			m.focus = focus
			m.generating = false
			before := ansi.Strip(m.View().Content)
			m.generating = true
			m.note = "A notice must not move the form."
			during := ansi.Strip(m.View().Content)
			require.Equal(t, strings.Count(before, "\n"), strings.Count(during, "\n"), "size=%v focus=%s", size, focus)
			require.LessOrEqual(t, len(strings.Split(during, "\n")), m.viewHeight())
			require.LessOrEqual(t, m.viewHeight(), 20)
			require.NotContains(t, before, "Ctrl+C quit")
			if focus == "command" {
				require.NotContains(t, before, "[Ctrl+P]")
			} else {
				require.Contains(t, before, "Ctrl+G generate")
			}
			require.Contains(t, during, "Ctrl+C cancel generation")
			require.NotContains(t, during, "[Enter]")
			for _, marker := range []string{"Create image", "Settings"} {
				beforeRow, _ := pickerNavigationLine(t, before, marker)
				duringRow, _ := pickerNavigationLine(t, during, marker)
				require.Equal(t, beforeRow, duringRow)
			}
			if m.viewHeight() >= 12 {
				beforeRow, _ := pickerNavigationLine(t, before, "Command")
				duringRow, _ := pickerNavigationLine(t, during, "Command")
				require.Equal(t, beforeRow, duringRow)
			}
			for _, line := range strings.Split(m.View().Content, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), m.width)
			}
		}
		if size == [2]int{96, 28} {
			require.GreaterOrEqual(t, m.height-m.viewHeight()-2, 7, "full settings retain an image viewport and expansion")
		}
	}
}

func TestImagePickerWorkspaceOverviewAndLongCommandStayLive(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 28, true
	m.settings.size, m.settings.format = "auto", "jpeg"
	m.settings.prompt = strings.Repeat("synthetic flower ", 40) + "final petal"
	m.draft, m.cursor = []rune(m.settings.prompt), len([]rune(m.settings.prompt))
	view := m.View()
	text := ansi.Strip(view.Content)
	for _, value := range []string{m.settings.model, "Size / aspect", "Quality", "Images", "Background", "JPEG", m.folderRow().value, "final petal"} {
		require.Contains(t, text, value)
	}
	require.Contains(t, text, "Command   openai images generate")
	require.NotContains(t, text, "Command preview ·")
	require.NotNil(t, view.Cursor)
	require.Equal(t, tea.CursorBar, view.Cursor.Shape)
	m.focus = "command"
	require.Contains(t, ansi.Strip(m.View().Content), "openai images generate")
	require.True(t, m.scrollCommand("end"))
	require.Contains(t, ansi.Strip(m.View().Content), "final petal")
	for _, size := range [][2]int{{40, 12}, {140, 40}, {96, 28}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		require.True(t, m.scrollCommand("end"))
		require.Contains(t, ansi.Strip(m.View().Content), "petal'", "the final word stays accessible after resize")
	}
}

func TestImagePickerWorkspaceCompactChoicesRemainAccessible(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {96, 28}} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.focus = size[0], size[1], true, "options"
		for _, row := range m.rows() {
			pickerLayoutOpenRow(t, m, row.id)
			if m.page == "choose" {
				for range len(m.rows()) {
					view := ansi.Strip(m.View().Content)
					require.Contains(t, view, "› "+m.rows()[m.selected].label)
					require.Contains(t, view, "Esc prompt")
					if m.selected < len(m.rows())-1 {
						pickerKey(m, tea.KeyDown)
					}
				}
			}
			m.focusPrompt()
			m.focus = "options"
		}
		require.Empty(t, m.result.Args, "navigating settings must not submit a request")
	}
}

func TestImagePickerWorkspaceFolderPathRetainsCursorAndControls(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {140, 40}} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace = size[0], size[1], true
		m.page, m.focus = "path", "path"
		m.folder.draft, m.folder.cursor = []rune("/synthetic/folder"), 4
		view := m.View()
		text := ansi.Strip(view.Content)
		require.Contains(t, text, "/synthetic/folder")
		require.Contains(t, text, "Tab complete")
		require.Contains(t, text, "Esc back")
		require.NotNil(t, view.Cursor)
		require.Equal(t, tea.CursorBar, view.Cursor.Shape)
		require.Equal(t, 8, view.Cursor.X, "the native cursor must not insert a cell")
		require.Less(t, view.Cursor.Y, m.viewHeight())
	}
}

func TestImagePickerWorkspaceOverviewKeepsLongestSettingsVisible(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 80, 24, true
	m.settings.size, m.settings.quality = "1536x1024", "xhigh"
	m.settings.count, m.settings.background = "10", "transparent"
	text := ansi.Strip(m.View().Content)
	for _, value := range []string{"Landscape", "Extra high", "Images", "10", "Transparent", "PNG"} {
		require.Contains(t, text, value)
	}
}

func TestImagePickerWorkspaceNoticesDoNotReduceImageArea(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 96, 28, true
	w := &imagePickerWorkspace{out: out}
	require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
	boundary := w.top
	for _, note := range []string{"Add a prompt first.", "Could not read that folder.", "All images saved. Edit your prompt or settings to create more."} {
		m.note = note
		require.NoError(t, w.draw(m, colorprofile.ASCII, false, true))
		require.Equal(t, boundary, w.top)
		require.Contains(t, w.painted[boundary], note)
	}
}

func TestImagePickerWorkspaceProgressAndScrollingDoNotRepaintHeader(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	m := pickerForTest(t)
	m.width, m.height, m.workspace, m.generating = 96, 28, true, true
	w := &imagePickerWorkspace{out: out, since: time.Now()}
	for range 40 {
		w.rows = append(w.rows, "Synthetic image row")
	}
	require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
	position, err := out.Seek(0, io.SeekCurrent)
	require.NoError(t, err)
	w.since = time.Now().Add(-2 * time.Second)
	require.NoError(t, w.draw(m, colorprofile.ASCII, true, false))
	w.scroll(3)
	require.Equal(t, 3, w.offset)
	require.NoError(t, w.draw(m, colorprofile.ASCII, true, false))
	_, err = out.Seek(position, io.SeekStart)
	require.NoError(t, err)
	updates, err := io.ReadAll(out)
	require.NoError(t, err)
	for _, header := range []string{"Create image", "Settings", "Command", "\x1b[2J"} {
		require.NotContains(t, string(updates), header)
	}
	require.Contains(t, string(updates), "2s elapsed")
	for _, size := range [][2]int{{40, 12}, {20, 4}, {96, 28}} {
		m.width, m.height = size[0], size[1]
		require.NoError(t, w.draw(m, colorprofile.ASCII, true, true))
		require.LessOrEqual(t, len(w.painted), size[1])
		require.Less(t, w.top, size[1])
	}
}

func TestImagePickerWorkspaceKeepsSavedReceiptsAfterDroppedEvents(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "screen")
	require.NoError(t, err)
	t.Cleanup(func() { out.Close() })
	w := &imagePickerWorkspace{out: out, started: true, send: func(tea.Msg) {}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, w.saved(ctx, []imageoutput.SavedImage{{Path: "synthetic image.png"}, {Path: "second synthetic.png"}}, "off"))
	require.NoError(t, w.close())
	require.NoError(t, w.close())
	_, err = out.Seek(0, io.SeekStart)
	require.NoError(t, err)
	text, err := io.ReadAll(out)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(text), `Saved image: "synthetic image.png"`))
	require.NotContains(t, string(text), "\x1b[?1000r")
	require.NotContains(t, string(text), "\x1b[?1049")
	require.Contains(t, string(text), `Saved image: "second synthetic.png"`)
}

func TestImagePickerWorkspaceIgnoresOldRequestClock(t *testing.T) {
	w := &imagePickerWorkspace{since: time.Now()}
	p := &imagePickerInline{workspace: w, busy: true}
	handled, cmd := p.updateWorkspace(imagePickerWorkspaceTick{started: w.since.Add(-time.Second)})
	require.True(t, handled)
	require.Nil(t, cmd, "old requests must not create another clock or repaint")
}

func TestImagePickerWorkspaceRequestCompletionKeepsCancellationStatus(t *testing.T) {
	for _, code := range []int{0, 143, 129} {
		m := pickerForTest(t)
		m.result = imagePickerResult{Canceled: true, ExitCode: code}
		p := &imagePickerInline{model: m, workspace: &imagePickerWorkspace{}, busy: true, requestCancel: func() {}}
		handled, cmd := p.updateWorkspace(imagePickerWorkspaceDone{err: context.Canceled, draftSaved: true})
		require.True(t, handled)
		require.NotNil(t, cmd)
		require.NoError(t, p.err, "the canceled request must not replace the selected exit status")
		require.Equal(t, code, m.result.ExitCode)
	}
}

func TestImagePickerWorkspaceSeparatesSectionsWithoutCommandPadding(t *testing.T) {
	m := pickerForTest(t)
	m.width, m.height, m.workspace = 240, 60, true
	text := ansi.Strip(m.View().Content)
	lines := strings.Split(text, "\n")
	command, _ := pickerNavigationLine(t, text, "Command")
	require.Contains(t, lines[command-1], "──")
	require.Contains(t, lines[command-2], "More options")
	require.Contains(t, lines[command], "openai images generate")
	require.Contains(t, lines[command+1], "╰")
	require.Contains(t, lines[command+2], "Ctrl+G generate")
	require.Equal(t, command+3, len(lines))
	require.NotContains(t, text, "[Enter] Generate")
	require.NotContains(t, text, "[↓] Settings")
	require.NotContains(t, text, "[↑↓] Move")
}

func TestImagePickerWorkspaceSpinnerAdvancesWithoutElapsedSecond(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("LC_ALL", "C")
	w := &imagePickerWorkspace{since: time.Now()}
	before := w.progress()
	w.animationFrame++
	after := w.progress()
	require.NotEqual(t, before, after)
	require.Contains(t, before, "Generating image · 0s elapsed")
	require.Contains(t, after, "Generating image · 0s elapsed")
	w.stage = clioutput.LoadingSaving
	require.Contains(t, w.progress(), "Saving image")
}

func TestImagePickerWorkspaceShowsAllSettingsWhileTyping(t *testing.T) {
	for _, width := range []int{40, 80, 96, 240} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.focus = width, 40, true, "prompt"
		text := ansi.Strip(m.View().Content)
		for _, label := range []string{"Model", "Size / aspect", "Quality", "Images", "Background", "Save to", "More options"} {
			require.Contains(t, text, label)
		}
		require.NotContains(t, text, "[Tab] Sections")
		require.NotContains(t, text, "Ctrl+C quit")
	}
}

func TestImagePickerWorkspaceInlinePromptKeepsNativeCursor(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {96, 28}, {240, 40}} {
		m := pickerForTest(t)
		m.width, m.height, m.workspace, m.focus = size[0], size[1], true, "prompt"
		m.settings.prompt = "abcdef"
		m.draft, m.cursor = []rune("abcdef"), 3
		view := m.View()
		lines := strings.Split(ansi.Strip(view.Content), "\n")
		require.NotNil(t, view.Cursor)
		require.Equal(t, tea.CursorBar, view.Cursor.Shape)
		require.Contains(t, lines[view.Cursor.Y], "Prompt abcdef")
		require.True(t, strings.HasSuffix(ansi.Truncate(lines[view.Cursor.Y], view.Cursor.X, ""), "abc"))
		require.NotContains(t, view.Content, "╭ Prompt")
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), m.width)
		}
	}
}
