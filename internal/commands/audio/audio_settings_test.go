package audio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func audioInlineSettingsModel(t *testing.T, width, height, setting int) *audioWorkbench {
	t.Helper()
	m := audioReviewModel(t)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.input.SetValue("Keep this speech draft.")
	m.focusSection(2)
	m.setting = setting
	m.settingsExpanded = true
	return m
}

func audioInlineType(m *audioWorkbench, text string) {
	for _, character := range text {
		m.Update(tea.KeyPressMsg{Code: character, Text: string(character)})
	}
}

func TestAudioSettingsDisclosureKeyboard(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep this draft.")
	for range 2 {
		audioReviewKey(m, tea.KeyTab)
	}
	require.Equal(t, 1, m.setting)
	audioReviewKey(m, tea.KeyDown)
	require.Equal(t, audioSettingDisclosure, m.setting)
	audioReviewKey(m, tea.KeyRight)
	require.Equal(t, audioSettingDisclosure, m.setting)
	audioReviewKey(m, tea.KeyEnter)
	require.True(t, m.settingsExpanded)
	require.Equal(t, 0, m.setting)
	audioReviewKey(m, tea.KeyDown)
	require.Equal(t, 2, m.setting)
	audioReviewKey(m, tea.KeyRight)
	format := m.settings.Format
	audioReviewKey(m, tea.KeyDown)
	require.Equal(t, 3, m.setting)
	audioReviewKey(m, tea.KeyRight)
	audioReviewKey(m, tea.KeyEscape)
	require.False(t, m.settingsExpanded)
	require.Equal(t, audioSettingDisclosure, m.setting)
	require.Equal(t, format, m.settings.Format)
	require.Equal(t, 1.25, m.settings.Speed)
	require.False(t, m.busy)
	require.False(t, m.quit)
}

func TestAudioValidationRevealsInvalidFieldAndKeepsRecording(t *testing.T) {
	for _, tc := range []struct {
		name   string
		field  int
		change func(*audioWorkbench)
	}{
		{"voice", 1, func(m *audioWorkbench) { m.settings.Model, m.settings.Voice = "tts-1", "cedar" }},
		{"format", 2, func(m *audioWorkbench) { m.settings.Format = "unknown" }},
		{"speed", 3, func(m *audioWorkbench) { m.settings.Speed = 8 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Synthetic text")
			retained := audioReviewResult("previous", false)
			m.result = &retained
			tc.change(m)
			m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
			require.Same(t, &retained, m.result)
			require.False(t, m.busy)
			require.Empty(t, m.modal)
			require.Equal(t, 2, m.focus)
			require.Equal(t, tc.field, m.setting)
			require.Equal(t, tc.field != 1, m.settingsExpanded)
			message := m.fieldValidation(tc.field)
			require.NotEmpty(t, message)
			require.Contains(t, ansi.Strip(m.View().Content), message)
			audioReviewKey(m, tea.KeyRight)
			require.Empty(t, m.fieldValidation(tc.field))
			require.False(t, m.activeFailure())
			require.NoError(t, m.lastErr)
		})
	}
}

func TestAudioFailedGenerationRetainsPlayerAndRecovery(t *testing.T) {
	m := audioReviewModel(t)
	prior := audioReviewResult("usable", false)
	m.result = &prior
	failure := audioUnauthorizedFailure()
	m.Update(audioWorkMessage{kind: "generate", err: failure})
	require.Same(t, &prior, m.result)
	require.ErrorIs(t, m.lastErr, failure)
	require.Empty(t, m.modal)
	require.True(t, m.retainedGenerationFailure)
	audioReviewKey(m, tea.KeyTab)
	audioReviewKey(m, tea.KeyF1)
	require.Equal(t, "recovery", m.modal)
	audioReviewKey(m, tea.KeyEscape)
	require.Empty(t, m.modal)
	require.Equal(t, 1, m.focus)
	require.True(t, m.instructions.Focused())
	require.False(t, m.busy)
	require.True(t, m.activeFailure())
	m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	require.Equal(t, "save", m.modal)
	m.saveFolder.SetValue("")
	audioReviewKey(m, tea.KeyEnter)
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "Enter a folder for saving.")
	require.NotContains(t, view, m.failure.Summary)
	audioReviewKey(m, tea.KeyF1)
	require.Equal(t, "recovery", m.modal)
	require.Contains(t, ansi.Strip(m.View().Content), m.failure.Summary)
	audioReviewKey(m, tea.KeyEscape)
	require.Equal(t, "save", m.modal)
	require.Contains(t, ansi.Strip(m.View().Content), "Enter a folder for saving.")
	audioReviewKey(m, tea.KeyEscape)
	m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult("success", false)})
	require.False(t, m.retainedGenerationFailure)
	require.False(t, m.activeFailure())
	require.NoError(t, m.lastErr)
}

func TestAudioInlineSpeedTypingAndEnterStayOnForm(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioInlineSettingsModel(t, size[0], size[1], 3)
			m.settings.Speed = 1.25
			audioInlineType(m, "2.5")
			require.True(t, m.speedEditing)
			require.Equal(t, "2.5", m.speedInput.Value(), "first digit must replace the displayed value")
			require.Equal(t, 1.25, m.settings.Speed, "typing must not commit a draft")
			require.Empty(t, m.modal)
			require.Contains(t, ansi.Strip(m.View().Content), "2.5")
			_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Nil(t, command)
			require.Equal(t, 2.5, m.settings.Speed)
			require.False(t, m.speedEditing)
			require.Empty(t, m.speedError)
			require.False(t, m.busy)
			require.Empty(t, m.modal)
			require.Equal(t, 2, m.focus)
			require.Equal(t, 3, m.setting)
			require.Equal(t, "Keep this speech draft.", m.input.Value())
			// Enter offers editing without opening a separate screen. Paste replaces
			// the selected current value and retains exact supported precision.
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.True(t, m.speedEditing)
			require.Empty(t, m.modal)
			m.Update(tea.PasteMsg{Content: "1.23"})
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Equal(t, 1.23, m.settings.Speed)
			require.False(t, m.busy)
		})
	}
}

func TestAudioInlineSpeedInvalidDraftBlocksNavigationAndGeneration(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, value := range []string{"", ".", "0.24", "4.01", "NaN", "+Inf", "1\n2", "1x"} {
			t.Run(fmt.Sprintf("%v/%q", size, value), func(t *testing.T) {
				m := audioInlineSettingsModel(t, size[0], size[1], 3)
				m.Update(tea.PasteMsg{Content: value})
				for _, key := range []tea.KeyPressMsg{
					{Code: tea.KeyEnter}, {Code: tea.KeyDown}, {Code: tea.KeyUp},
					{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift},
					{Code: tea.KeyHome}, {Code: tea.KeyEnd},
					{Code: 'g', Mod: tea.ModCtrl}, {Code: 'o', Mod: tea.ModCtrl},
					{Code: tea.KeyLeft}, {Code: tea.KeyRight},
				} {
					_, command := m.Update(key)
					require.Nil(t, command, key.String())
					require.True(t, m.speedEditing, key.String())
					require.Equal(t, value, m.speedInput.Value(), key.String())
					require.Equal(t, float64(1), m.settings.Speed)
					require.Equal(t, 2, m.focus)
					require.Equal(t, 3, m.setting)
					require.False(t, m.busy)
					require.False(t, m.quit)
					require.Empty(t, m.modal)
					require.Equal(t, audioSpeedError, m.speedError)
				}
				view := strings.Join(strings.Fields(ansi.Strip(m.View().Content)), " ")
				require.Contains(t, view, audioSpeedError)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				require.False(t, m.speedEditing)
				require.Empty(t, m.speedError)
				require.False(t, m.quit)
				require.False(t, m.settingsExpanded)
				require.Equal(t, audioSettingDisclosure, m.setting)
				require.Equal(t, float64(1), m.settings.Speed)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				require.True(t, m.quit)
			})
		}
	}
}

func TestAudioInlineSpeedValidNavigationCommitsWithoutGeneration(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyTab}, {Code: tea.KeyEnd}} {
		t.Run(key.String(), func(t *testing.T) {
			m := audioInlineSettingsModel(t, 90, 34, 3)
			m.Update(tea.PasteMsg{Content: "1.23"})
			m.Update(key)
			require.Equal(t, 1.23, m.settings.Speed)
			require.False(t, m.speedEditing)
			require.False(t, m.speedInput.Focused())
			require.False(t, m.busy)
			require.False(t, m.focus == 2 && m.setting == 3)
		})
	}
}

func TestAudioInlineSpeedCorrectionClearsValidation(t *testing.T) {
	m := audioInlineSettingsModel(t, 90, 34, 3)
	audioInlineType(m, "4.01")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, audioSpeedError, m.speedError)
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "4.0", m.speedInput.Value())
	require.Empty(t, m.speedError)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, float64(4), m.settings.Speed)
	require.False(t, m.speedEditing)
	require.False(t, m.busy)
}

func TestAudioInlineSpeedArrowsAdjustQuarterStepsAndPreserveDraft(t *testing.T) {
	m := audioInlineSettingsModel(t, 90, 34, 3)
	for _, tc := range []struct {
		speed float64
		key   rune
		want  float64
	}{{1.23, tea.KeyRight, 1.48}, {1.23, tea.KeyLeft, .98}, {.25, tea.KeyLeft, .25}, {4, tea.KeyRight, 4}} {
		m.settings.Speed = tc.speed
		m.Update(tea.KeyPressMsg{Code: tc.key})
		require.InDelta(t, tc.want, m.settings.Speed, 1e-9)
		require.False(t, m.speedEditing)
	}
	m.settings.Speed = 1
	m.Update(tea.PasteMsg{Content: "1.23"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, "1.48", m.speedInput.Value())
	require.Equal(t, float64(1), m.settings.Speed)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, float64(1), m.settings.Speed, "Escape must discard stepped drafts too")
	require.False(t, m.quit)
}

func TestAudioInlineChoicesCycleSupportedValuesWithoutActions(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for setting, choices := range [][]string{audioModels, audioModelVoices("tts-1"), audioFormats} {
			t.Run(fmt.Sprintf("%v/%d", size, setting), func(t *testing.T) {
				m := audioInlineSettingsModel(t, size[0], size[1], setting)
				if setting == 1 {
					m.settings.Model, m.settings.Voice = "tts-1", choices[0]
				}
				if setting == 2 {
					m.settings.Format = choices[0]
				}
				retained := audioReviewResult("retained", false)
				m.result = &retained
				playContext, cancel := context.WithCancel(t.Context())
				defer cancel()
				m.playing, m.playCancel = true, cancel
				for i := 1; i <= len(choices)*2; i++ {
					_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
					require.Nil(t, command)
					actual := []string{m.settings.Model, m.settings.Voice, m.settings.Format}[setting]
					require.Equal(t, choices[i%len(choices)], actual)
					require.False(t, m.busy)
					require.Empty(t, m.modal)
					require.Same(t, &retained, m.result)
					require.NoError(t, playContext.Err())
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
				require.Equal(t, choices[len(choices)-1], []string{m.settings.Model, m.settings.Voice, m.settings.Format}[setting])
				// Enter retains the optional full chooser, with the current choice.
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, "choice", m.modal)
				require.Equal(t, choices[len(choices)-1], m.choices[m.choice])
				require.False(t, m.busy)
			})
		}
	}
}

func TestAudioInlineChoiceLettersAndUnavailableVoice(t *testing.T) {
	m := audioInlineSettingsModel(t, 90, 34, 1)
	audioInlineType(m, "s")
	require.Equal(t, "sage", m.settings.Voice)
	audioInlineType(m, "s")
	require.Equal(t, "shimmer", m.settings.Voice)
	audioInlineType(m, "s")
	require.Equal(t, "sage", m.settings.Voice)
	handled, command := m.updateInlineSetting(tea.KeyPressMsg{Code: 'p', Text: "p"})
	require.False(t, handled, "P must remain available for explicit audition")
	require.Nil(t, command)
	m.settings.Voice = "cedar"
	m.setting = 0
	audioInlineType(m, "t")
	require.Equal(t, "tts-1", m.settings.Model)
	require.Equal(t, "cedar", m.settings.Voice, "model selection must preserve the voice draft")
	m.setting = 1
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, "alloy", m.settings.Voice, "unsupported current voice must enter the supported choices")
	m.setting = 2
	audioInlineType(m, "m")
	require.Equal(t, "mp3", m.settings.Format)
	require.False(t, m.busy)
	require.Empty(t, m.modal)
}

func TestAudioInlineSettingsRespectBusyModalAndSizeGuards(t *testing.T) {
	for _, state := range []string{"busy", "modal", "small", "editor", "result"} {
		t.Run(state, func(t *testing.T) {
			m := audioInlineSettingsModel(t, 90, 34, 3)
			switch state {
			case "busy":
				m.busy = true
			case "modal":
				m.modal = "info"
			case "small":
				m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
			case "editor":
				m.focusSection(0)
			case "result":
				result := audioReviewResult("retained", false)
				m.result = &result
				m.focusSection(4)
			}
			for _, message := range []tea.Msg{tea.PasteMsg{Content: "1.25"}, tea.KeyPressMsg{Code: '2', Text: "2"}} {
				m.Update(message)
				require.False(t, m.speedEditing)
				require.Equal(t, float64(1), m.settings.Speed)
			}
		})
	}
}

func TestAudioInlineSpeedRecoveryPreservesDraftAndRestoresFocus(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {90, 34}} {
		for _, trigger := range []string{"playback failure", "F1"} {
			t.Run(fmt.Sprintf("%v/%s", size, trigger), func(t *testing.T) {
				m := audioInlineSettingsModel(t, size[0], size[1], 3)
				retained := audioReviewResult("retained", false)
				m.result, m.playID, m.playing = &retained, 17, true
				failure := audioWorkMessage{kind: "play", playID: 17, err: errors.New("synthetic player failure")}
				if trigger == "F1" {
					m.Update(failure)
					require.Equal(t, "recovery", m.modal)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					require.Equal(t, audioSettingMore, m.setting)
					for step := 0; step < 3 && m.setting != 3; step++ {
						m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
					}
					require.Equal(t, 3, m.setting)
				}
				audioInlineType(m, "4.01")
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, audioSpeedError, m.speedError)
				if trigger == "playback failure" {
					m.Update(failure)
				} else {
					m.Update(tea.KeyPressMsg{Code: tea.KeyF1})
				}
				require.Equal(t, "recovery", m.modal)
				require.True(t, m.speedEditing)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				require.Empty(t, m.modal)
				require.Equal(t, 2, m.focus)
				require.Equal(t, 3, m.setting)
				require.True(t, m.speedInput.Focused())
				require.True(t, m.speedEditing)
				require.Equal(t, "4.01", m.speedInput.Value())
				require.Equal(t, audioSpeedError, m.speedError)
				require.Equal(t, float64(1), m.settings.Speed)
				require.Same(t, &retained, m.result)
				// Leave the repeat-submit cooldown behind. The invalid draft itself
				// must block this explicit generation shortcut after recovery.
				m.lastFinished = time.Now().Add(-time.Second)
				_, command := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
				require.Nil(t, command)
				require.False(t, m.busy)
				require.True(t, m.speedEditing)
				require.Equal(t, 3, m.setting)
				m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.Equal(t, float64(4), m.settings.Speed)
				require.False(t, m.speedEditing)
				require.False(t, m.busy)
			})
		}
	}
}
