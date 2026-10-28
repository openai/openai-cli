package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestAudioActionsEmptyPlayerExplainsGenerationWithoutWork(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, action := range []int{0, 1} {
			m := audioReviewModel(t)
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.input.SetValue("Keep this synthetic draft 世界")
			m.instructions.SetValue("Keep this delivery")
			m.path.SetValue("unchanged.wav")
			m.saveFolder.SetValue("/synthetic/unchanged")
			m.lastFinished = time.Now() // Empty feedback must not be swallowed by result debounce.
			m.focusSection(4)
			m.resultAction = action
			settings, started, playID := m.settings, m.started, m.playID
			for _, key := range []rune{' ', tea.KeyEnter, 's', tea.KeyEnter} {
				require.Nil(t, audioReviewKey(m, key))
				require.Equal(t, "Generate speech first.", m.playerNotice)
				require.Equal(t, 4, m.focus)
				require.Equal(t, action, m.resultAction)
				require.Nil(t, m.result)
				require.Empty(t, m.modal)
				require.False(t, m.busy)
				require.False(t, m.playing)
				require.False(t, m.quit)
				require.Nil(t, m.playCancel)
				require.Nil(t, m.playDone)
				require.Equal(t, playID, m.playID)
				require.Equal(t, started, m.started)
				require.Equal(t, settings, m.settings)
				require.Equal(t, "Keep this synthetic draft 世界", m.input.Value())
				require.Equal(t, "Keep this delivery", m.instructions.Value())
				require.Equal(t, "unchanged.wav", m.path.Value())
				require.Equal(t, "/synthetic/unchanged", m.saveFolder.Value())
				require.NoError(t, m.lastErr)
			}
			_, err := os.Stat(m.store.Directory)
			require.ErrorIs(t, err, os.ErrNotExist, "empty controls created history files")
		}
	}
}

func TestAudioActionsEmptyPlayerRespectsGuards(t *testing.T) {
	for _, guard := range []string{"busy", "tiny", "info", "recovery", "invalid-speed"} {
		t.Run(guard, func(t *testing.T) {
			m := audioReviewModel(t)
			m.focusSection(4)
			switch guard {
			case "busy":
				m.busy = true
			case "tiny":
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			case "info":
				m.openInformation("Help", "Synthetic help", "")
			case "recovery":
				m.Update(audioWorkMessage{kind: "generate", err: audioUnauthorizedFailure()})
			case "invalid-speed":
				m.focusSection(2)
				m.setting = 3
				m.beginInlineSpeed()
				m.speedInput.SetValue("invalid")
			}
			focus, modal, playID := m.focus, m.modal, m.playID
			for _, key := range []tea.KeyPressMsg{
				{Code: ' '}, {Code: tea.KeyEnter}, {Code: 's'}, {Code: 'o', Mod: tea.ModCtrl},
			} {
				_, command := m.Update(key)
				require.Nil(t, command)
				require.Equal(t, focus, m.focus)
				require.Equal(t, modal, m.modal)
				require.Empty(t, m.playerNotice)
				require.Equal(t, playID, m.playID)
				require.False(t, m.playing)
				require.Nil(t, m.result)
				require.False(t, m.quit)
			}
			if guard == "invalid-speed" {
				require.True(t, m.speedEditing)
				require.Equal(t, audioSpeedError, m.speedError)
				require.Equal(t, 1.0, m.settings.Speed)
			}
		})
	}
}

func TestAudioActionsEmptyPlayerNoticeClearsForNewWorkAndClear(t *testing.T) {
	for _, action := range []string{"generate", "generated-result", "opened-result", "clear"} {
		t.Run(action, func(t *testing.T) {
			m := audioReviewModel(t)
			m.focusSection(4)
			require.Nil(t, audioReviewKey(m, 's'))
			require.Equal(t, audioNoGenerationNotice, m.playerNotice)
			switch action {
			case "generate":
				m.input.SetValue("Synthetic generation is scheduled, never executed")
				_, command := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
				require.NotNil(t, command)
				require.True(t, m.busy)
			case "generated-result", "opened-result":
				kind := "generate"
				if action == "opened-result" {
					kind = "open"
				}
				_, command := m.Update(audioWorkMessage{kind: kind, result: audioReviewResult("synthetic-recording", false)})
				require.Nil(t, command)
				require.NotNil(t, m.result)
			case "clear":
				require.Nil(t, audioReviewMore(t, m, "Clear form"))
				require.Nil(t, m.result)
			}
			require.Empty(t, m.playerNotice)
		})
	}
}

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
	require.Contains(t, view, "Check your API key and project access.")
	require.NotContains(t, view, "1. Check your API key and project access.")
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
		m.focusCommand()
		rows := m.commandDisplayRows()
		require.Equal(t, strings.ReplaceAll(m.commandPreview(), "\n", ""), ansi.Strip(strings.Join(rows, "")))
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth())
		}
		require.Greater(t, len(rows), m.commandViewportHeight())
		audioReviewKey(m, tea.KeyEnd)
		require.Equal(t, len(rows)-m.commandViewportHeight(), m.commandOffset)
		require.Nil(t, audioReviewKey(m, tea.KeyEscape))
		require.Equal(t, "more", m.modal)
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
	require.Contains(t, view, "Enter text before generating speech.")
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
