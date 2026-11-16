package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestAudioWorkbenchDrawSynchronizesGeometryAfterNonKeyMessage(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {28, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
			m.input.SetValue("Keep this synthetic draft.")
			m.instructions.SetValue("Keep these instructions.")
			result := audioReviewResult("retained", false)
			m.result, m.focus = &result, 4
			m.note = "Audio retained. Space plays the recording. S opens Save."
			paint := &audioRecordedFrames{}
			inline := &audioWorkbenchInline{model: m, painter: paint, profile: colorprofile.ASCII, started: true}

			// A non-key update runs before the queued WindowSizeMsg. The draw
			// observes the new terminal geometry while the model still has 90x34.
			m.Update(audioWorkTick(time.Unix(1700000000, 0)))
			require.Equal(t, 90, m.width)
			require.Equal(t, 34, m.height)
			require.Greater(t, strings.Count(m.View().Content, "\n")+1, size[1], "the stale view must exceed the resized viewport")
			inline.drawFrame(size[0], size[1])

			require.Equal(t, size[0], m.width)
			require.Equal(t, size[1], m.height)
			require.Equal(t, "Keep this synthetic draft.", m.input.Value())
			require.Equal(t, "Keep these instructions.", m.instructions.Value())
			require.Same(t, &result, m.result)
			frame := paint.String()
			require.True(t, strings.HasPrefix(frame, ansi.ResetModeTextCursorEnable+"\r"+ansi.EraseScreenBelow))
			rows := strings.Count(frame, ansi.CursorHorizontalAbsolute(size[0]))
			require.Positive(t, rows)
			require.LessOrEqual(t, rows, size[1], "stale roomy rows must not scroll above the compact viewport")
			require.NotContains(t, frame, "\x1b[2J")
			require.NotContains(t, frame, "\x1b[3J")
			if size[0] == 40 {
				require.Contains(t, frame, "Text to speech")
				require.Regexp(t, `Coral +WAV`, frame)
				require.Contains(t, frame, "00:00.000 / 00:01.000")
			} else {
				require.Contains(t, frame, "Resize to 40 x 12.")
				require.Contains(t, frame, "Ctrl+C exits.")
				require.NotContains(t, frame, "Result:")
				_, command := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
				require.NotNil(t, command)
				require.True(t, m.quit)
				require.Equal(t, 130, m.exitCode)
			}
		})
	}
}

type audioRecordedFrames struct{ strings.Builder }

func (p *audioRecordedFrames) Control(data string) { _, _ = p.WriteString(data) }
func (p *audioRecordedFrames) Frame(data string)   { _, _ = p.WriteString(data) }

func TestAudioInlineSeparatesAppleTerminalPromptMarks(t *testing.T) {
	for _, program := range []string{"Apple_Terminal", "iTerm.app"} {
		t.Run(program, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", program)
			t.Setenv("KITTY_WINDOW_ID", "")
			m := audioReviewModel(t)
			paint := &audioRecordedFrames{}
			inline := &audioWorkbenchInline{model: m, painter: paint, profile: colorprofile.ASCII}
			inline.Init()
			if program == "Apple_Terminal" {
				require.Contains(t, paint.String(), "\r\n\r\n"+ansi.CursorUp(1)+ansi.SaveCursor)
				require.Contains(t, paint.String(), ansi.RequestCursorPositionReport)
				require.NotContains(t, paint.String(), ansi.RequestModeAutoWrap)
				inline.observeWrap(tea.CursorPositionMsg{X: 1}, m.width, m.height)
			} else {
				require.Contains(t, paint.String(), ansi.RequestModeAutoWrap)
				require.NotContains(t, paint.String(), ansi.RequestCursorPositionReport)
			}
			inline.start()
			paint.Reset()
			inline.drawFrame(133, 39)
			first := paint.String()
			require.NotContains(t, first, "\n")
			require.Contains(t, first, ansi.SaveCursor)
			require.Contains(t, first, ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable)

			paint.Reset()
			m.focusSection(2)
			inline.drawFrame(110, 28)
			changed := paint.String()
			require.NotContains(t, changed, "\n")
			require.Contains(t, changed, ansi.RestoreCursor+"\r"+ansi.EraseScreenBelow)
			require.NotContains(t, changed, ansi.SetModeTextCursorEnable)
			if program == "Apple_Terminal" {
				require.True(t, strings.HasSuffix(changed, ansi.SaveCursor+ansi.CursorDown(1)))
			} else {
				require.True(t, strings.HasSuffix(changed, ansi.SaveCursor))
			}
			require.NotContains(t, first+changed, "\x1b[3J")
		})
	}
}

func TestAudioAppleTerminalPreservesWrapping(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	for _, tc := range []struct {
		name    string
		column  int // Negative means no reply.
		width   int
		restore bool
		hard    bool
	}{
		{"wrap enabled", 1, 100, false, false},
		{"wrap disabled", 99, 100, true, false},
		{"no reply", -1, 100, false, true},
		{"unexpected column", 30, 100, false, true},
		{"width changed", 99, 80, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioReviewModel(t)
			paint := &audioRecordedFrames{}
			p := &audioWorkbenchInline{model: m, painter: paint, profile: colorprofile.ASCII}
			p.Init()
			if tc.column >= 0 {
				p.observeWrap(tea.CursorPositionMsg{X: tc.column}, tc.width, m.height)
			}
			p.start()
			require.Equal(t, tc.restore, strings.Contains(paint.String(), ansi.SetModeAutoWrap))
			paint.Reset()
			p.drawFrame(tc.width, m.height)
			require.Contains(t, paint.String(), "Text to speech")
			require.Equal(t, tc.hard, strings.Contains(paint.String(), "\r\n"))
			require.Equal(t, tc.restore, strings.Contains(p.close(), ansi.ResetModeAutoWrap))
		})
	}
}

func TestAudioFramedWorkspaceFillsViewportWithoutClearingHistory(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {101, 29}, {186, 40}} {
		m := audioReviewModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		content := m.View().Content
		require.Len(t, strings.Split(content, "\n"), size[1])
		require.False(t, strings.HasSuffix(content, "\n"))
		paint := &audioRecordedFrames{}
		inline := &audioWorkbenchInline{model: m, painter: paint, profile: colorprofile.ASCII, started: true}
		inline.drawFrame(size[0], size[1])
		frame := paint.String()
		require.Equal(t, size[1], strings.Count(frame, ansi.CursorHorizontalAbsolute(size[0])))
		require.Contains(t, frame, "\r"+ansi.CursorUp(size[1]-1)+ansi.SaveCursor)
		require.True(t, strings.HasSuffix(frame, ansi.SetCursorStyle(5)+ansi.SetModeTextCursorEnable))
		require.NotContains(t, frame, "\x1b[2J")
		require.NotContains(t, frame, "\x1b[3J")
		inline.drawFrame(size[0], size[1])
		require.Equal(t, frame, paint.String(), "unchanged redraw must not scroll the screen")
	}
}
