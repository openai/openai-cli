package audio

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestAudioNativeCursorTracksUnicodeEditors(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, focus := range []int{0, 1} {
			t.Run(fmt.Sprintf("%dx%d/focus_%d", size[0], size[1], focus), func(t *testing.T) {
				m := audioReviewModel(t)
				m.input.SetValue("")
				m.instructions.SetValue("")
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.focusSection(focus)
				m.Update(tea.PasteMsg{Content: "界🙂e\u0301Z"})
				before := m.View()
				require.NotNil(t, before.Cursor)
				m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
				view := m.View()
				require.Equal(t, before.Content, view.Content, "moving the caret must not paint a block into the text")
				assertAudioNativeCursorBefore(t, view, "Z")
				require.Equal(t, tea.CursorBar, view.Cursor.Shape)
				require.True(t, view.Cursor.Blink)
				require.Equal(t, before.Cursor.X-1, view.Cursor.X)
				require.Equal(t, before.Cursor.Y, view.Cursor.Y)
				x := 4
				if size[0] >= 60 {
					x = 3
					if focus == 1 {
						x = 21
					}
				}
				require.Equal(t, x+ansi.StringWidth("界🙂e\u0301"), view.Cursor.X)
				for _, editor := range []bool{m.input.VirtualCursor(), m.instructions.VirtualCursor(), m.speedInput.VirtualCursor(), m.path.VirtualCursor(), m.saveFolder.VirtualCursor()} {
					require.False(t, editor)
				}
			})
		}
	}
}

func TestAudioNativeCursorRetainsWrappedPositionAcrossResize(t *testing.T) {
	for _, focus := range []int{0, 1} {
		m := audioReviewModel(t)
		r := audioReviewResult("cursor-resize", false)
		m.result = &r
		m.input.SetValue("")
		m.instructions.SetValue("")
		m.focusSection(focus)
		text := strings.Repeat("界🙂e\u0301 wrapped text ", 35) + "TAILZ"
		m.Update(tea.PasteMsg{Content: text})
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		editor := &m.input
		if focus == 1 {
			editor = &m.instructions
		}
		line, column := editor.Line(), editor.Column()
		for _, size := range [][2]int{{101, 29}, {60, 27}, {60, 28}, {60, 27}, {60, 24}, {40, 12}, {109, 24}, {110, 24}, {137, 41}, {101, 29}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := m.View()
			assertAudioNativeCursorBefore(t, view, "Z")
			require.Equal(t, text, editor.Value())
			require.Equal(t, line, editor.Line())
			require.Equal(t, column, editor.Column())
			require.Less(t, view.Cursor.X, size[0]-1)
			require.Less(t, view.Cursor.Y, size[1])
		}
	}
}

func TestAudioNativeCursorFollowsEditableSpeedAndSaveFields(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.focusSection(2)
		m.setting = 3
		m.settingsExpanded = true
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m.Update(tea.PasteMsg{Content: "9Z"})
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.NotEmpty(t, m.speedError)
		m.speedInput.CursorEnd()
		// Speed arrows adjust values, so position this probe through its editor.
		m.speedInput.CursorStart()
		view := m.View()
		assertAudioNativeCursorBefore(t, view, "9Z")
		require.Contains(t, ansi.Strip(view.Content), m.speedError)
		require.NotContains(t, ansi.Strip(view.Content), "Setting        Value")
		m.cancelInlineSpeed()

		m.modal = "speed"
		m.speedInput.SetValue("2Z")
		m.speedInput.Focus()
		m.speedInput.CursorStart()
		assertAudioNativeCursorBefore(t, m.View(), "2Z")

		m.modal = ""
		r := audioReviewResult("cursor-save", false)
		m.result = &r
		m.openSave()
		for _, focus := range []int{0, 1} {
			m.focusSave(focus)
			editor := &m.path
			if focus == 1 {
				editor = &m.saveFolder
			}
			editor.SetValue(strings.Repeat("folder世界/", 10) + "TAILZ")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			editor.MoveToEnd()
			m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			assertAudioNativeCursorBefore(t, m.View(), "Z")
		}
		m.focusSave(2)
		require.Nil(t, m.View().Cursor)
	}
}

func TestAudioNativeCursorHidesOutsideEditableFields(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("")
	view := m.View()
	require.NotNil(t, view.Cursor)
	rows := strings.Split(ansi.Strip(view.Content), "\n")
	require.Contains(t, rows[1], "Text to speech")
	require.Contains(t, rows[2], "Type what you want to hear…")
	require.Equal(t, 3, view.Cursor.X, "placeholder must not move the insertion point")
	require.Equal(t, 2, view.Cursor.Y)
	for _, modal := range []string{"info", "more", "choice", "history", "recovery"} {
		m.modal = modal
		require.Nil(t, m.View().Cursor, modal)
	}
	m.modal = ""
	r := audioReviewResult("cursor-hidden", false)
	m.result = &r
	for _, focus := range []int{2, audioFocusCommand, 4} {
		m.focusSection(focus)
		require.Nil(t, m.View().Cursor)
	}
	m.focusSection(0)
	m.busy = true
	require.Nil(t, m.View().Cursor)
	m.busy = false
	m.focusSection(1)
	m.settings.Model = "tts-1"
	require.Nil(t, m.View().Cursor)
	m.focusSection(0)
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
	require.Nil(t, m.View().Cursor)
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 29})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.True(t, m.quit)
	require.Nil(t, m.View().Cursor)
}

func assertAudioNativeCursorBefore(t *testing.T, view tea.View, suffix string) {
	t.Helper()
	require.NotNil(t, view.Cursor)
	rows := strings.Split(ansi.Strip(view.Content), "\n")
	require.GreaterOrEqual(t, view.Cursor.Y, 0)
	require.Less(t, view.Cursor.Y, len(rows))
	row := rows[view.Cursor.Y]
	index := strings.LastIndex(row, suffix)
	require.GreaterOrEqual(t, index, 0, "cursor row must contain %q: %q", suffix, row)
	require.Equal(t, ansi.StringWidth(row[:index]), view.Cursor.X, "native cursor left the insertion point")
}

func TestAudioNativeCursorRunnerRepaintsMovementAndRestoresOrigin(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("abcde")
	m.input.MoveToEnd()
	paint := &audioRecordedFrames{}
	receipt := &audioCursorOutput{output: audioCursorWriteFunc(func(_ context.Context, data []byte) (int, error) { return len(data), nil })}
	inline := &audioWorkbenchInline{model: m, painter: paint, profile: colorprofile.ASCII, started: true, modesActive: true, cursorOutput: receipt}
	inline.drawFrame(101, 29)
	first := paint.String()
	require.Contains(t, first, ansi.SaveCursor)
	require.True(t, strings.HasSuffix(first, ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable))
	_, err := receipt.WriteContext(t.Context(), []byte(first))
	require.NoError(t, err)
	paint.Reset()
	before := inline.content
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	inline.drawFrame(101, 29)
	require.Equal(t, before, inline.content)
	moved := paint.String()
	require.True(t, strings.HasPrefix(moved, ansi.ResetModeTextCursorEnable+ansi.RestoreCursor))
	require.Contains(t, moved, "Text to speech", "cursor-only frames must remain complete under coalescing")
	require.Contains(t, moved, "│  abcde")
	require.Contains(t, moved, ansi.CursorHorizontalAbsolute(m.View().Cursor.X+1)+ansi.SetCursorStyle(5))
	inline.drawFrame(101, 29)
	require.Equal(t, moved, paint.String())
	paint.Reset()
	m.focusSection(2)
	inline.drawFrame(101, 29)
	require.Contains(t, paint.String(), ansi.ResetModeTextCursorEnable+ansi.RestoreCursor)
	require.NotContains(t, paint.String(), ansi.SetModeTextCursorEnable)
	cleanup := inline.close()
	require.Contains(t, cleanup, ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow+ansi.SetCursorStyle(0))
	require.True(t, strings.HasSuffix(cleanup, ansi.ResetModeBracketedPaste+ansi.SetModeTextCursorEnable))
	require.Empty(t, inline.close())
}

func TestAudioNativeCursorCleanupRequiresAcknowledgedOrigin(t *testing.T) {
	for _, name := range []string{"canceled", "partial before save", "partial inside save", "partial after save", "complete"} {
		t.Run(name, func(t *testing.T) {
			m := audioReviewModel(t)
			entered := make(chan struct{})
			receipt := &audioCursorOutput{output: audioCursorWriteFunc(func(ctx context.Context, data []byte) (int, error) {
				close(entered)
				switch name {
				case "canceled":
					<-ctx.Done()
					return 0, ctx.Err()
				case "partial before save":
					return strings.Index(string(data), ansi.SaveCursor), io.ErrShortWrite
				case "partial inside save":
					return strings.Index(string(data), ansi.SaveCursor) + 1, io.ErrShortWrite
				case "partial after save":
					return strings.Index(string(data), ansi.SaveCursor) + len(ansi.SaveCursor), io.ErrShortWrite
				}
				return len(data), nil
			})}
			failed := make(chan struct{}, 1)
			painter := cliterminal.NewPainter(receipt, func() { failed <- struct{}{} })
			defer painter.Stop()
			inline := &audioWorkbenchInline{model: m, painter: painter, profile: colorprofile.ASCII, started: true, modesActive: true, cursorOutput: receipt}
			inline.drawFrame(101, 29)
			audioCursorReceive(t, entered)
			if strings.HasPrefix(name, "partial") {
				audioCursorReceive(t, failed)
			}
			err := painter.Stop()
			if strings.HasPrefix(name, "partial") {
				require.ErrorIs(t, err, io.ErrShortWrite)
			}
			cleanup := inline.close()
			acknowledged := name == "partial after save" || name == "complete"
			require.Equal(t, acknowledged, receipt.anchored.Load())
			require.Equal(t, acknowledged, strings.Contains(cleanup, ansi.RestoreCursor))
			require.Equal(t, acknowledged, strings.Contains(cleanup, ansi.EraseScreenBelow))
			require.Contains(t, cleanup, ansi.SetCursorStyle(0))
			require.Contains(t, cleanup, ansi.SetModeTextCursorEnable)
		})
	}
}

func TestAudioNativeCursorCoalescedFramesKeepInitializedOrigin(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("first draft")
	entered, release := make(chan struct{}), make(chan struct{})
	writes := make(chan string, 2)
	first := true
	receipt := &audioCursorOutput{output: audioCursorWriteFunc(func(ctx context.Context, data []byte) (int, error) {
		if first {
			first = false
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		writes <- string(data)
		return len(data), nil
	})}
	painter := cliterminal.NewPainter(receipt, func() {})
	defer painter.Stop()
	inline := &audioWorkbenchInline{model: m, painter: painter, profile: colorprofile.ASCII, started: true, modesActive: true, cursorOutput: receipt}
	inline.drawFrame(101, 29)
	audioCursorReceive(t, entered)
	m.input.SetValue("latest draft")
	m.input.MoveToEnd()
	inline.drawFrame(101, 29)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	inline.drawFrame(101, 29)
	close(release)
	initial, latest := audioCursorReceive(t, writes), audioCursorReceive(t, writes)
	require.NotContains(t, initial, ansi.RestoreCursor)
	require.Contains(t, initial, ansi.SaveCursor)
	require.True(t, strings.HasPrefix(latest, ansi.ResetModeTextCursorEnable+ansi.RestoreCursor))
	require.Contains(t, latest, "Text to speech")
	require.Contains(t, latest, "│  latest draft")
	require.Contains(t, latest, ansi.CursorHorizontalAbsolute(m.View().Cursor.X+1)+ansi.SetCursorStyle(5))
	require.NoError(t, painter.Stop())
	require.Contains(t, inline.close(), ansi.RestoreCursor)
}

type audioCursorWriteFunc func(context.Context, []byte) (int, error)

func (f audioCursorWriteFunc) WriteContext(ctx context.Context, data []byte) (int, error) {
	return f(ctx, data)
}

func TestAudioNativeCursorCleanupBeforeFirstFrame(t *testing.T) {
	m := audioReviewModel(t)
	inline := &audioWorkbenchInline{model: m, painter: &audioRecordedFrames{}}
	require.Empty(t, inline.close())
	inline.Init()
	cleanup := inline.close()
	require.NotContains(t, cleanup, ansi.RestoreCursor)
	require.NotContains(t, cleanup, ansi.EraseScreenBelow)
	require.NotContains(t, cleanup, ansi.SetCursorStyle(0))
	require.Contains(t, cleanup, ansi.ResetModeBracketedPaste+ansi.SetModeTextCursorEnable)
}

func audioCursorReceive[T any](t *testing.T, messages <-chan T) T {
	t.Helper()
	select {
	case value := <-messages:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("cursor output worker did not reach the expected write boundary")
		var zero T
		return zero
	}
}
