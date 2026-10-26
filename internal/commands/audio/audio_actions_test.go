package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioActionsInstructionsExampleIsEditableAndSeededOnce(t *testing.T) {
	m := audioReviewModel(t)
	require.Equal(t, "Speak warmly and clearly.", m.instructions.Value())
	m.focusSection(1)
	m.instructions.SetValue("")
	m.Update(tea.PasteMsg{Content: "Keep this edited direction."})
	for _, size := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 100, Height: 32}} {
		m.Update(size)
		require.Equal(t, "Keep this edited direction.", m.instructions.Value())
	}
	m.instructions.SetValue("")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 34})
	require.Empty(t, m.instructions.Value(), "Deleting the example must not reinsert it")
	m.modal, m.moreChoice = "more", 2
	audioReviewKey(m, tea.KeyEnter)
	require.Empty(t, m.instructions.Value(), "Clear must leave an empty draft")
	result := audioReviewResult("history-without-instructions", false)
	result.entry.Settings.Instructions = ""
	m.Update(audioWorkMessage{kind: "open", result: result})
	require.Empty(t, m.instructions.Value(), "History restoration must not invent Instructions")
}

func TestAudioActionsLegacyModelPreservesInstructionDraft(t *testing.T) {
	m := audioReviewModel(t)
	m.instructions.SetValue("An edited instruction stays in the draft.")
	m.focus, m.setting = 2, 0
	audioReviewKey(m, tea.KeyEnter)
	m.choice = slices.Index(m.choices, "tts-1")
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.False(t, m.instructionsSupported())
	require.Equal(t, "An edited instruction stays in the draft.", m.instructions.Value())
	require.Empty(t, m.effectiveSettings().Instructions)
	require.NotContains(t, m.commandPreview(), `"instructions":`)
	require.Contains(t, audioFeedbackView(t, m), "Not used by this model")
	m.focusSection(0)
	audioReviewKey(m, tea.KeyTab)
	require.Equal(t, 2, m.focus, "Tab must skip the disabled editor")
	m.setting = 0
	audioReviewKey(m, tea.KeyUp)
	require.Equal(t, 0, m.focus, "Up must skip the disabled editor")
	m.focus, m.setting = 2, 0
	audioReviewKey(m, tea.KeyEnter)
	m.choice = slices.Index(m.choices, "gpt-4o-mini-tts")
	audioReviewKey(m, tea.KeyEnter)
	require.Equal(t, m.instructions.Value(), m.effectiveSettings().Instructions)
}

func checkAudioActionsGenerationStoresOnlySentInstructions(t *testing.T, buildRequestOptions func(*cli.Command) ([]option.RequestOption, error)) {
	for _, model := range audioModels {
		t.Run(model, func(t *testing.T) {
			var requests atomic.Int32
			bodies := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				bodies <- body
				w.Header().Set("Content-Type", "audio/pcm")
				_, _ = w.Write([]byte{0, 0, 0, 0})
			}))
			defer server.Close()
			command := audioGenerateTestCommand()
			command.Action = func(ctx context.Context, command *cli.Command) error {
				m := newAudioWorkbench(ctx, command, func(ctx context.Context, command *cli.Command) error {
					options, err := buildRequestOptions(command)
					if err != nil {
						return err
					}
					client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("sk-fake-audio-form"), option.WithMaxRetries(0))
					response, err := client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{}, options...)
					if err != nil {
						return err
					}
					defer response.Body.Close()
					return ctx.Value(audioCaptureKey{}).(*audioCapture).receive(response)
				}, audioTestStore(t))
				m.input.SetValue("Synthetic retained inputs")
				m.settings.Model, m.settings.Voice, m.settings.Format = model, "alloy", "pcm"
				require.Zero(t, requests.Load())
				generate := m.generate(false)
				require.NotNil(t, generate)
				batch, ok := generate().(tea.BatchMsg)
				require.True(t, ok)
				require.Len(t, batch, 2)
				result, ok := batch[0]().(audioWorkMessage)
				require.True(t, ok)
				require.NoError(t, result.err)
				m.Update(result)
				body := <-bodies
				if strings.HasPrefix(model, "tts-1") {
					require.NotContains(t, body, "instructions")
					require.Empty(t, result.result.entry.Settings.Instructions)
				} else {
					require.Equal(t, audioInstructionsExample, body["instructions"])
					require.Equal(t, audioInstructionsExample, result.result.entry.Settings.Instructions)
				}
				require.Equal(t, audioInstructionsExample, m.instructions.Value())
				require.Equal(t, int32(1), requests.Load())
				entries, err := m.store.List(ctx)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.Equal(t, result.result.entry.Settings, entries[0].Settings)
				return nil
			}
			require.NoError(t, command.Run(t.Context(), []string{"speech"}))
		})
	}
}

func TestAudioActionsVoiceMenuMatchesLocalValidation(t *testing.T) {
	for _, model := range audioModels {
		m := audioReviewModel(t)
		m.settings.Model = model
		m.focus, m.setting = 2, 1
		require.Nil(t, audioReviewKey(m, tea.KeyEnter))
		require.NotEmpty(t, m.choices)
		for _, voice := range m.choices {
			settings := audioDefaultSettings()
			settings.Input, settings.Model, settings.Voice = "Synthetic voice selection", model, voice
			require.NoError(t, validateAudioSettings(settings))
		}
	}
}

func TestAudioActionsRecoveryUsesOneScreen(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep this draft")
	failure := audioUnauthorizedFailure()
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	view := audioFeedbackView(t, m)
	require.Contains(t, view, "Error details")
	require.Contains(t, view, "1. Check your API key and project access.")
	require.Contains(t, view, "Esc back")
	require.NotContains(t, view, "API setup")
	require.NotContains(t, view, "Back to form")
	for _, key := range []rune{tea.KeyEnter, tea.KeyTab, 's', 'd', tea.KeyF1} {
		require.Nil(t, audioReviewKey(m, key))
		require.Equal(t, view, audioFeedbackView(t, m))
		require.False(t, m.busy)
	}
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Empty(t, m.modal)
	require.Equal(t, 2, m.focus)
	require.Equal(t, audioSettingMore, m.setting)
	require.Equal(t, "Keep this draft", m.input.Value())
	require.ErrorIs(t, m.lastErr, failure)
	require.Nil(t, audioReviewMore(t, m, "Last error"))
	require.Equal(t, "recovery", m.modal)
	require.Equal(t, view, audioFeedbackView(t, m))
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Equal(t, "more", m.modal)
}

func TestAudioActionsNewSuccessRemovesObsoleteRecovery(t *testing.T) {
	for _, parent := range []string{"", "info", "save"} {
		t.Run("parent_"+parent, func(t *testing.T) {
			m := audioReviewModel(t)
			if parent == "info" {
				m.openInformation("Help", "Keep this help page.", "more")
			} else {
				m.modal = parent
			}
			result := audioReviewResult("retained", false)
			m.result, m.focus, m.busy = &result, 4, true
			m.Update(audioWorkMessage{kind: "play", playID: m.playID, err: audioUnauthorizedFailure()})
			require.Equal(t, "recovery", m.modal)
			m.Update(audioWorkMessage{kind: "export", entryID: result.entry.ID, path: "saved.wav"})
			require.Equal(t, parent, m.modal)
			require.False(t, m.activeFailure())
			require.NoError(t, m.lastErr)
			require.NotContains(t, audioFeedbackView(t, m), "could not verify")
		})
	}
}

func TestAudioActionsRecoveryRestoresInterruptedInformation(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	original := strings.Repeat("Keep this help page.\n", 12)
	m.openInformation("Help", original, "more")
	for range 3 {
		audioReviewKey(m, tea.KeyDown)
	}
	m.Update(audioWorkMessage{kind: "play", playID: m.playID, err: audioUnauthorizedFailure()})
	require.Equal(t, "info", m.recoveryParent)
	require.Contains(t, audioFeedbackView(t, m), "Error details")
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "info", m.modal)
	require.Equal(t, "Help", m.infoTitle)
	require.Equal(t, original, m.infoText)
	require.Equal(t, "more", m.infoParent)
	require.Equal(t, 3, m.infoOffset)
}

func TestAudioActionsCommandRowsPreserveSpacesAndUnicode(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue(strings.Repeat("  spaces   界🙂e\u0301  ", 35))
	m.instructions.SetValue("  Keep   every space and @literal.  ")
	for _, width := range []int{40, 90} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 12})
		m.openInformation(audioCommandTitle, m.commandPreview(), "more")
		rows := m.infoRows()
		require.Equal(t, m.commandPreview(), strings.Join(rows, ""))
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth())
		}
		require.Greater(t, len(rows), m.informationHeight())
		audioReviewKey(m, tea.KeyEnd)
		require.Equal(t, len(rows)-m.informationHeight(), m.infoOffset)
		require.Nil(t, audioReviewKey(m, tea.KeyEnter))
		require.False(t, m.busy)
	}
}

func TestAudioActionsLocalValidationKeepsOriginalExitContract(t *testing.T) {
	m := audioReviewModel(t)
	m.lastFinished = time.Time{}
	m.input.SetValue("")
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Equal(t, "recovery", m.modal)
	require.NoError(t, m.lastErr)
	require.Contains(t, audioFeedbackView(t, m), "Enter text before generating speech.")
	audioReviewKey(m, tea.KeyF1)
	require.Equal(t, "recovery", m.modal)
	view := audioFeedbackView(t, m)
	require.Contains(t, view, "Text is empty.")
	require.Contains(t, view, "1. Enter text before generating speech.")
	require.NotContains(t, view, "API key")
	require.False(t, m.busy)
	audioReviewKey(m, tea.KeyEscape)
	require.NotNil(t, audioReviewKey(m, tea.KeyEscape))
	require.Zero(t, m.exitCode)
}

func TestAudioHistoryOpensDirectlyWithoutGenerating(t *testing.T) {
	m := audioReviewModel(t)
	result := audioSaveControllerResult(t, m, "Synthetic retained bytes")
	m.input.SetValue("A draft must remain editable")
	m.focus, m.setting = 2, audioSettingHistory
	command := audioReviewKey(m, tea.KeyEnter)
	require.NotNil(t, command)
	message := audioSaveControllerRun(t, command)
	require.Equal(t, "history", message.kind)
	require.NoError(t, message.err)
	m.Update(message)
	require.Equal(t, "history", m.modal)
	require.Len(t, m.entries, 1)
	require.Same(t, result, m.result)
	require.Equal(t, "A draft must remain editable", m.input.Value())
}
