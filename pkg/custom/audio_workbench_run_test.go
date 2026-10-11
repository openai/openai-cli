package custom

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
			m.note = "Audio retained. Space plays or stops. S saves the original bytes."
			paint := &tokenizerRecordedFrames{}
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
			require.True(t, strings.HasPrefix(frame, "\r"+ansi.EraseScreenBelow))
			rows := strings.Count(frame, ansi.CursorHorizontalAbsolute(size[0]))
			require.Positive(t, rows)
			require.LessOrEqual(t, rows, size[1], "stale roomy rows must not scroll above the compact viewport")
			require.NotContains(t, frame, "\x1b[2J")
			require.NotContains(t, frame, "\x1b[3J")
			if size[0] == 40 {
				require.Contains(t, frame, "Text to speech")
				require.Contains(t, frame, "Result: Coral")
				require.Contains(t, frame, "Duration 00:01.000")
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
