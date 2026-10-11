package custom

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
)

func audioUnauthorizedFailure() *openai.Error {
	return &openai.Error{StatusCode: http.StatusUnauthorized, Message: "FAKE_PRIVATE_ERROR_MESSAGE",
		Response: &http.Response{Header: http.Header{"X-Request-Id": {"req_synthetic_auth_failure"}}}}
}

func TestAudioFailureShows401BeforeEscape(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("A synthetic draft remains editable.")
	m.focus, m.setting = 2, 4
	failure := audioUnauthorizedFailure()
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	view := audioFeedbackView(t, m)
	require.Contains(t, view, "HTTP 401: Unauthorized.", "failure cause must be visible before Escape")
	require.Contains(t, view, "Check key, project, organization.")
	require.Contains(t, view, "Setup: openai help setup")
	require.Contains(t, view, "F1 details")
	require.Contains(t, view, "› Preview voice")
	require.NotContains(t, view, "FAKE_PRIVATE_ERROR_MESSAGE")
	require.NotContains(t, view, "Audio action failed.")
	require.False(t, m.quit)
	require.False(t, m.busy)
	require.ErrorIs(t, m.lastErr, failure)
	require.Nil(t, audioReviewKey(m, tea.KeyF1))
	require.Equal(t, "error", m.modal)
	view = audioFeedbackView(t, m)
	require.Contains(t, view, "Audio error")
	require.Contains(t, view, "Request ID: req_synthetic_auth_failure")
	require.NotContains(t, view, "FAKE_PRIVATE_ERROR_MESSAGE")
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Empty(t, m.modal)
	require.False(t, m.quit)
	require.Equal(t, 2, m.focus)
	require.Equal(t, 4, m.setting)
	require.Equal(t, "A synthetic draft remains editable.", m.input.Value())
	require.ErrorIs(t, m.lastErr, failure)
}

func TestAudioFailureCompactPreservesCauseRecoveryAndFocus(t *testing.T) {
	for _, focus := range []int{0, 1, 2, 3, 4} {
		t.Run(fmt.Sprintf("focus_%d", focus), func(t *testing.T) {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			m.input.SetValue("Synthetic text")
			m.focusSection(focus)
			m.setting = 7
			if focus == 4 {
				retained := audioReviewResult("retained", false)
				m.result = &retained
			}
			m.Update(audioWorkMessage{kind: "generate", err: audioUnauthorizedFailure()})
			view := audioFeedbackView(t, m)
			for _, text := range []string{"HTTP 401: Unauthorized.", "Check key, project, organization.", "Setup: openai help setup", "F1 details"} {
				require.Contains(t, view, text)
			}
			switch focus {
			case 0:
				require.Contains(t, view, "› Text")
			case 1:
				require.Contains(t, view, "› Instructions")
			case 2:
				require.Contains(t, view, "› Clear form")
			case 3:
				require.Contains(t, view, "› printf")
			case 4:
				require.Contains(t, view, "› Result:")
				require.Contains(t, view, "Duration")
			}
		})
	}
}

func TestAudioFailureDetailsRestoreParentWithoutActions(t *testing.T) {
	for _, tc := range []struct {
		name, parent string
		focus        int
	}{
		{"choice", "choice", 2}, {"save", "save", 2}, {"speed", "speed", 2},
		{"text", "", 0}, {"instructions", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Keep the draft")
			m.focusSection(tc.focus)
			m.setting = 1
			m.choices, m.choice = audioVoices, 2
			m.modal = tc.parent
			m.path.SetValue("preserved-output.wav")
			m.speedInput.SetValue("1.25")
			if tc.parent == "save" {
				m.path.Focus()
			} else if tc.parent == "speed" {
				m.speedInput.Focus()
			}
			m.Update(audioWorkMessage{kind: "play", playID: m.playID, err: audioUnauthorizedFailure()})
			require.Nil(t, audioReviewKey(m, tea.KeyF1))
			require.Equal(t, "error", m.modal)
			for _, key := range []rune{tea.KeyDown, tea.KeyUp, tea.KeyEnter, tea.KeyTab} {
				require.Nil(t, audioReviewKey(m, key))
			}
			_, generate := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			require.Nil(t, generate, "Error details must consume the generation shortcut")
			require.Nil(t, audioReviewKey(m, tea.KeyF1))
			require.Equal(t, tc.parent, m.failureParent)
			m.Update(tea.PasteMsg{Content: "Do not insert"})
			require.False(t, m.busy)
			require.False(t, m.playing)
			require.Equal(t, 2, m.choice)
			require.Equal(t, "Keep the draft", m.input.Value())
			require.Equal(t, "preserved-output.wav", m.path.Value())
			require.Equal(t, "1.25", m.speedInput.Value())
			restore := audioReviewKey(m, tea.KeyEscape)
			if tc.parent == "choice" {
				require.Nil(t, restore)
			} else {
				require.NotNil(t, restore, "Restoring an editor must restart its caret")
			}
			require.Equal(t, tc.parent, m.modal)
			require.False(t, m.quit)
			require.Equal(t, tc.parent == "save", m.path.Focused())
			require.Equal(t, tc.parent == "speed", m.speedInput.Focused())
			require.Equal(t, tc.focus == 0, m.input.Focused())
			require.Equal(t, tc.focus == 1, m.instructions.Focused())
		})
	}
}

func TestAudioFailureDetailsScrollAndResizeRemainBounded(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	failure := audioUnauthorizedFailure()
	failure.Response.Header.Set("X-Request-Id", "req_"+strings.Repeat("a", 210)+"_tail")
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	audioReviewKey(m, tea.KeyF1)
	require.Contains(t, audioFeedbackView(t, m), "HTTP 401: Unauthorized.")
	for range 100 {
		audioReviewKey(m, tea.KeyDown)
	}
	require.Contains(t, audioFeedbackView(t, m), "_tail")
	require.LessOrEqual(t, m.failureOffset, len(m.failureDetailRows())-m.failureDetailHeight())
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	audioReviewKey(m, tea.KeyUp)
	require.Zero(t, m.failureOffset)
	require.Contains(t, audioFeedbackView(t, m), "HTTP 401: Unauthorized.")
	require.False(t, m.busy)
}

func TestAudioFailureFeedbackDoesNotOutliveItsNote(t *testing.T) {
	m := audioReviewModel(t)
	failure := audioUnauthorizedFailure()
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	require.True(t, m.activeFailure())
	m.focus, m.setting = 2, 7
	audioReviewKey(m, tea.KeyEnter)
	require.False(t, m.activeFailure())
	require.ErrorIs(t, m.lastErr, failure, "Clear must preserve the existing exit-error contract")
	require.NotContains(t, audioFeedbackView(t, m), "HTTP 401")
	audioReviewKey(m, tea.KeyF1)
	require.Empty(t, m.modal)
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	result := audioReviewResult("successful-retry", false)
	m.Update(audioWorkMessage{kind: "generate", result: result})
	require.NoError(t, m.lastErr)
	require.False(t, m.activeFailure())
	require.NotContains(t, audioFeedbackView(t, m), "HTTP 401")
	require.Contains(t, audioFeedbackView(t, m), "Audio retained.")
}

func TestAudioFailureDetailsRespectBusyAndTinyGuards(t *testing.T) {
	for _, guard := range []string{"busy", "tiny"} {
		t.Run(guard, func(t *testing.T) {
			m := audioReviewModel(t)
			failure := audioUnauthorizedFailure()
			m.Update(audioWorkMessage{kind: "generate", err: failure})
			if guard == "busy" {
				m.busy = true
			} else {
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			}
			require.Nil(t, audioReviewKey(m, tea.KeyF1))
			require.Empty(t, m.modal)
			require.True(t, errors.Is(m.lastErr, failure))
			_, quit := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			require.NotNil(t, quit)
			require.Equal(t, 130, m.exitCode)
		})
	}
}
