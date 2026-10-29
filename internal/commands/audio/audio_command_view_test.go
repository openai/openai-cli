package audio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/stretchr/testify/require"
)

func TestAudioCommandViewMoreAndEscapePreserveDrafts(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, focus := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%dx%d/focus_%d", size[0], size[1], focus), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.input.SetValue("Keep @literal and \\@literal.\n世界 says 'hello'.")
				m.instructions.SetValue("Keep  two spaces.\nSpeak clearly.")
				m.input.CursorEnd()
				m.instructions.CursorEnd()
				m.focusSection(focus)
				m.setting = 1
				input, instructions, settings := m.input.Value(), m.instructions.Value(), m.settings
				inputLine, inputColumn := m.input.Line(), m.input.Column()
				instructionLine, instructionColumn := m.instructions.Line(), m.instructions.Column()
				for range 2 - focus {
					m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
				}
				require.Equal(t, 2, m.focus)
				require.Nil(t, audioReviewMore(t, m, "View command"))
				require.Equal(t, audioFocusCommand, m.focus)
				require.Empty(t, m.modal)
				require.Empty(t, m.commandToPrint)
				require.False(t, m.quit)
				require.False(t, m.busy)
				require.False(t, m.input.Focused())
				require.False(t, m.instructions.Focused())
				require.Contains(t, audioFeedbackView(t, m), audioCommandTitle)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
				require.Equal(t, "more", m.modal)
				require.Equal(t, "View command", m.moreItems()[m.moreChoice])
				require.Equal(t, 2, m.focus)
				require.Equal(t, audioSettingMore, m.setting)
				require.Equal(t, input, m.input.Value())
				require.Equal(t, instructions, m.instructions.Value())
				require.Equal(t, settings, m.settings)
				require.Equal(t, inputLine, m.input.Line())
				require.Equal(t, inputColumn, m.input.Column())
				require.Equal(t, instructionLine, m.instructions.Line())
				require.Equal(t, instructionColumn, m.instructions.Column())
				require.False(t, m.input.Focused())
				require.False(t, m.instructions.Focused())
				require.Empty(t, m.commandToPrint)
				require.False(t, m.quit)
			})
		}
	}
}

func TestAudioCommandViewFormShortcutPrintsDirectly(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {101, 29}} {
		for _, focus := range []int{0, 1, 2, audioFocusCommand} {
			t.Run(fmt.Sprintf("%dx%d/focus_%d", size[0], size[1], focus), func(t *testing.T) {
				m := audioReviewModel(t)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.input.SetValue("Complete synthetic input 世界.\nKeep  spaces.")
				m.instructions.SetValue("Speak 'clearly'.")
				m.focusSection(focus)
				raw := m.commandPreview()
				_, command := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				require.NotNil(t, command)
				require.IsType(t, tea.QuitMsg{}, command())
				require.True(t, m.quit)
				require.Empty(t, m.modal)
				require.False(t, m.busy)
				require.Equal(t, raw, m.commandToPrint)
			})
		}
	}
}

func TestAudioCommandViewPendingSpeedPreviewAndPrint(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {101, 29}} {
		for _, draft := range []string{"1.23", "4.01"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], draft), func(t *testing.T) {
				m := audioInlineSettingsModel(t, size[0], size[1], 3)
				audioInlineType(m, draft)
				require.True(t, m.speedEditing)
				require.Equal(t, float64(1), m.settings.Speed)
				display := ansi.Strip(strings.Join(m.commandDisplayRows(), ""))
				if draft == "4.01" {
					require.Equal(t, "Apply a valid Speed to preview the command.", display)
					require.NotContains(t, display, "printf")
					require.NotContains(t, display, `"speed":1`)
				} else {
					require.Contains(t, display, `"speed":1.23`)
				}
				require.Equal(t, float64(1), m.settings.Speed, "rendering must not commit a draft")
				_, command := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				require.Empty(t, m.modal)
				require.False(t, m.busy)
				if draft == "4.01" {
					require.Nil(t, command)
					require.Empty(t, m.commandToPrint)
					require.False(t, m.quit)
					require.True(t, m.speedEditing)
					require.True(t, m.speedInput.Focused())
					require.Equal(t, 2, m.focus)
					require.Equal(t, 3, m.setting)
					require.Equal(t, draft, m.speedInput.Value())
					require.Equal(t, audioSpeedError, m.speedError)
					require.Equal(t, float64(1), m.settings.Speed)
					m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
					require.False(t, m.speedEditing)
					require.False(t, m.quit)
					_, command = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
					require.NotNil(t, command)
					require.True(t, m.quit)
					require.Contains(t, m.commandToPrint, `"speed":1`)
					require.NotContains(t, m.commandToPrint, `"speed":4.01`)
					return
				}
				require.NotNil(t, command)
				require.IsType(t, tea.QuitMsg{}, command())
				require.True(t, m.quit)
				require.False(t, m.speedEditing)
				require.Equal(t, 1.23, m.settings.Speed)
				require.Equal(t, m.commandPreview(), m.commandToPrint)
				require.Contains(t, m.commandToPrint, `"speed":1.23`)
			})
		}
	}
}

func TestAudioCommandViewFormShortcutPreservesAdmissionGuards(t *testing.T) {
	for _, mode := range []string{"busy", "tiny", "result"} {
		t.Run(mode, func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Retain this guarded draft.")
			result := audioReviewResult("guarded-result", false)
			m.result = &result
			switch mode {
			case "busy":
				m.busy = true
			case "tiny":
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			case "result":
				m.focusSection(4)
			}
			focus := m.focus
			_, command := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			require.Nil(t, command)
			require.Empty(t, m.modal)
			require.Empty(t, m.commandToPrint)
			require.False(t, m.quit)
			require.Equal(t, focus, m.focus)
			require.Equal(t, mode == "busy", m.busy)
			require.Same(t, &result, m.result)
			require.Equal(t, "Retain this guarded draft.", m.input.Value())
		})
	}
}

func TestAudioCommandViewPrivateOverrideNeverBecomesPrintable(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {101, 29}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioPreviewModel(t, "openai", "--header=Authorization: Bearer synthetic-private-command-canary")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.input.SetValue("Synthetic command body.")
			require.NotContains(t, audioFeedbackView(t, m), "synthetic-private-command-canary")
			for _, focus := range []int{0, audioFocusCommand} {
				m.focusSection(focus)
				require.NotContains(t, audioFeedbackView(t, m), "Output: speech.")
				display := ansi.Strip(strings.Join(m.commandDisplayRows(), ""))
				require.Contains(t, display, "unavailable with private request overrides")
				for _, private := range []string{"synthetic-private-command-canary", "Synthetic command body.", "printf", "audio speak"} {
					require.NotContains(t, display, private)
				}
				_, command := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				require.Nil(t, command)
				require.Empty(t, m.modal)
				require.Empty(t, m.commandToPrint)
				require.False(t, m.quit)
				require.False(t, m.busy)
				require.NotContains(t, audioFeedbackView(t, m), "Ctrl+P")
			}
		})
	}
}

func TestAudioCommandViewWrappingPreservesCompleteSanitizedCommand(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		for _, palette := range []string{"plain", "dark", "light"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], palette), func(t *testing.T) {
				m := audioPreviewModel(t, "'/synthetic/CLI preview\"s path/openai'", "--project=proj\x1b[2J世界's")
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.color, m.dark = palette != "plain", palette == "dark"
				m.input.SetValue(strings.Repeat("Two  spaces 世界 e\u0301 @literal \\@literal. ", 80) + "INPUT_END")
				m.instructions.SetValue(strings.Repeat("Keep 'quotes'.\n", 18) + "INSTRUCTIONS_END")
				raw := m.commandPreview()
				require.Nil(t, m.focusCommand())
				rows := m.commandDisplayRows()
				require.Greater(t, len(rows), m.commandViewportHeight())
				joined := ansi.Strip(strings.Join(rows, ""))
				require.Equal(t, readable.Text(strings.ReplaceAll(raw, "\n", "")), joined, "wrapping or styling lost command bytes")
				require.Contains(t, joined, "INPUT_END")
				require.Contains(t, joined, "INSTRUCTIONS_END")
				require.NotContains(t, joined, "\x1b")
				for _, row := range rows {
					require.NotContains(t, row, "\n", "structural breaks must produce separate display rows")
					require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth()-4)
				}
				audioFeedbackView(t, m)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
				require.Positive(t, m.commandOffset)
				audioFeedbackView(t, m)
				m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
				require.Zero(t, m.commandOffset)
				require.Equal(t, raw, m.commandPreview())
				require.Empty(t, m.modal)
				require.Empty(t, m.commandToPrint)
				require.False(t, m.busy)
				require.False(t, m.quit)
			})
		}
	}
}

func TestAudioCommandInspectorStaysOutsideNormalTabOrder(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioPreviewModel(t, "'/synthetic/alternate CLI/openai'")
			m.input.SetValue("Keep this synthetic draft.")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, hidden := range []string{"View command", audioCommandTitle, "printf", "/synthetic/alternate CLI/openai"} {
				require.NotContains(t, audioFeedbackView(t, m), hidden)
			}
			require.Nil(t, audioReviewMore(t, m, "View command"))
			require.Contains(t, audioFeedbackView(t, m), "printf")
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			require.Equal(t, 4, m.focus)
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			require.Equal(t, 2, m.focus)
			require.Equal(t, audioSettingMore, m.setting)

			require.Nil(t, audioReviewMore(t, m, "View command"))
			m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
			require.Equal(t, 2, m.focus)
			require.Equal(t, audioSettingMore, m.setting)
			require.Empty(t, m.modal)
			require.False(t, m.quit)
			require.Empty(t, m.commandToPrint)
		})
	}
}

func TestAudioCommandInspectorAccentsOperationAndShortcuts(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {101, 29}} {
		for _, dark := range []bool{true, false} {
			m := audioPreviewModel(t, "openai", "--project=synthetic")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.focusCommand()
			raw := m.commandPreview()
			m.color, m.dark = true, dark
			// Rejoin wrapped rows because a token can span two terminal lines.
			rows := strings.Join(m.commandDisplayRows(), "")
			for _, token := range []string{m.invocation, "--project=synthetic", "speech.wav"} {
				audioRequireTextForeground(t, rows, token, lipgloss.NewStyle())
			}
			for _, token := range []string{"printf", "OPENAI_UNTRUSTED_STDIN=true", "--output", "\\"} {
				audioRequireTextForeground(t, rows, token, m.muted())
			}
			view := m.View().Content
			audioRequireTextForeground(t, rows, "audio speak", m.accent())
			require.Contains(t, view, m.strong().Render(audioCommandTitle))
			audioRequireTextForeground(t, view, "Ctrl+P", m.accent())
			m.color = false
			require.Equal(t, ansi.Strip(view), m.View().Content)
			require.Equal(t, raw, m.commandPreview())
		}
	}
}

func TestAudioCommandInspectorPreservesDraftAndExecutable(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 24}, {101, 29}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Expansion draft. " + strings.Repeat("Keep complete text 世界. ", 80))
			m.instructions.SetValue("Keep  spaces.\nSpeak 'clearly'.")
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.input.CursorEnd()
			m.instructions.CursorEnd()
			input, instructions, settings := m.input.Value(), m.instructions.Value(), m.settings
			inputLine, inputColumn := m.input.Line(), m.input.Column()
			instructionLine, instructionColumn := m.instructions.Line(), m.instructions.Column()
			require.Nil(t, m.focusCommand())
			raw := m.commandPreview()
			require.Empty(t, m.modal)
			require.Equal(t, audioFocusCommand, m.focus)
			require.Greater(t, m.commandViewportHeight(), 1, "expanded command must expose multiple rows")
			view := audioFeedbackView(t, m)
			for _, label := range []string{audioCommandTitle, "Output", "speech.wav", "c copy", "Esc back", "Ctrl+P"} {
				require.Contains(t, view, label)
			}
			require.Nil(t, audioReviewKey(m, tea.KeyEnd))
			require.Equal(t, max(0, len(m.commandDisplayRows())-m.commandViewportHeight()), m.commandOffset)
			for _, resize := range []tea.WindowSizeMsg{{Width: 40, Height: 12}, {Width: 101, Height: 29}} {
				m.Update(resize)
				audioFeedbackView(t, m)
				require.LessOrEqual(t, m.commandOffset, max(0, len(m.commandDisplayRows())-m.commandViewportHeight()))
				require.Equal(t, raw, m.commandPreview())
			}
			require.Nil(t, audioReviewKey(m, tea.KeyEscape))
			require.Equal(t, "more", m.modal)
			m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			require.Equal(t, 2, m.focus)
			require.Equal(t, audioSettingMore, m.setting)
			require.False(t, m.quit)
			require.Equal(t, input, m.input.Value())
			require.Equal(t, instructions, m.instructions.Value())
			require.Equal(t, settings, m.settings)
			require.Equal(t, inputLine, m.input.Line())
			require.Equal(t, inputColumn, m.input.Column())
			require.Equal(t, instructionLine, m.instructions.Line())
			require.Equal(t, instructionColumn, m.instructions.Column())
			require.Equal(t, raw, m.commandPreview())
			require.Empty(t, m.commandToPrint)
			require.False(t, m.busy)

		})
	}
}

func TestAudioCommandInspectorPreservesAdmissionAndPrivacyGuards(t *testing.T) {
	for _, mode := range []string{"busy", "tiny", "private"} {
		t.Run(mode, func(t *testing.T) {
			flags := []string{}
			if mode == "private" {
				flags = append(flags, "--header=Authorization: Bearer fake-private-expansion-canary")
			}
			m := audioPreviewModel(t, "openai", flags...)
			m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
			m.input.SetValue("Retain this draft.")
			m.focusCommand()
			if mode == "busy" {
				m.busy = true
			}
			if mode == "tiny" {
				m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
			}
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			_, print := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			require.Nil(t, print)
			require.Empty(t, m.commandToPrint)
			require.False(t, m.quit)
			if mode == "private" {
				view := audioFeedbackView(t, m)
				for _, omitted := range []string{"fake-private-expansion-canary", "printf", "audio speak", "Ctrl+P"} {
					require.NotContains(t, view, omitted)
				}
			}
		})
	}
}

func TestAudioCommandViewRefreshesAfterDraftSettingsHistoryAndContextChanges(t *testing.T) {
	m := audioPreviewModel(t, "openai", "--project=initial-project")
	m.Update(tea.WindowSizeMsg{Width: 101, Height: 29})
	m.input.SetValue(strings.Repeat("Initial input. ", 40))
	m.instructions.SetValue("Initial instructions.")
	assertCurrent := func() string {
		t.Helper()
		rows := m.commandDisplayRows()
		display := ansi.Strip(strings.Join(rows, ""))
		require.Equal(t, readable.Text(strings.ReplaceAll(m.commandPreview(), "\n", "")), display)
		for _, row := range rows {
			require.LessOrEqual(t, ansi.StringWidth(row), m.audioWidth()-4)
		}
		return display
	}
	initial := assertCurrent()
	require.Equal(t, initial, assertCurrent(), "unchanged redraw altered the command")
	m.input.SetValue("Edited input 世界.")
	require.Contains(t, assertCurrent(), `"input":"Edited input 世界."`)
	m.instructions.SetValue("Edited instructions.")
	require.Contains(t, assertCurrent(), `"instructions":"Edited instructions."`)
	m.settings.Voice, m.settings.Format, m.settings.Speed = "nova", "flac", 2.5
	for _, fragment := range []string{`"voice":"nova"`, `"response_format":"flac"`, `"speed":2.5`, "speech.flac"} {
		require.Contains(t, assertCurrent(), fragment)
	}
	m.settings.Model = "tts-1"
	require.Contains(t, assertCurrent(), `"model":"tts-1"`)
	require.NotContains(t, assertCurrent(), `"instructions":`)
	require.Equal(t, "Edited instructions.", m.instructions.Value())

	retained := audioReviewResult("command-history", false)
	retained.entry.Settings.Input = "Retained history input."
	retained.entry.Settings.Instructions = "Retained history instructions."
	retained.entry.Settings.Speed = 1.75
	m.Update(audioWorkMessage{kind: "open", result: retained})
	require.Contains(t, assertCurrent(), `"input":"Retained history input."`)
	require.Contains(t, assertCurrent(), `"instructions":"Retained history instructions."`)
	require.Contains(t, assertCurrent(), `"speed":1.75`)
	require.Nil(t, audioReviewMore(t, m, "Clear form"))
	require.Contains(t, assertCurrent(), `"input":""`)
	require.NotContains(t, assertCurrent(), "Retained history")
	require.NotContains(t, assertCurrent(), `"instructions":`)

	m.input.SetValue(strings.Repeat("Resize this complete command 世界. ", 8))
	wide := append([]string(nil), m.commandDisplayRows()...)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	assertCurrent()
	require.Greater(t, len(m.commandDisplayRows()), len(wide))
	m.color, m.dark = true, true
	assertCurrent()
	m.dark = false
	assertCurrent()
	m.invocation = "'/synthetic/alternate CLI/openai'"
	require.Contains(t, assertCurrent(), m.invocation)
	require.Contains(t, assertCurrent(), "'--project=initial-project'")
	require.Contains(t, assertCurrent(), "audio speak")

	other := audioPreviewModel(t, "openai", "--project=replacement-project")
	m.command = other.command
	require.Contains(t, assertCurrent(), "--project=replacement-project")
	require.NotContains(t, assertCurrent(), "initial-project")
	private := audioPreviewModel(t, "openai", "--header=Authorization: Bearer fake-private-cache-canary")
	m.command = private.command
	display := ansi.Strip(strings.Join(m.commandDisplayRows(), ""))
	require.Contains(t, display, "unavailable with private request overrides")
	for _, omitted := range []string{"printf", "replacement-project", "fake-private-cache-canary", "Resize this complete command"} {
		require.NotContains(t, display, omitted)
	}
	m.command = other.command
	require.Contains(t, assertCurrent(), "--project=replacement-project")
}

func TestAudioPlayerActionPositionsStayStableAcrossStates(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {101, 29}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := audioResultNavigationModel(t, size[0], size[1])
			position := func(t *testing.T, label string) [2]int {
				t.Helper()
				for row, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
					// The timing status can share the action's "At end" label.
					if !strings.Contains(line, "Save audio") {
						continue
					}
					if column := strings.Index(line, label); column >= 0 {
						return [2]int{row, ansi.StringWidth(line[:column])}
					}
				}
				t.Fatalf("missing action %q", label)
				return [2]int{}
			}
			play, save := position(t, "Play audio"), position(t, "Save audio")
			for _, state := range []string{"save focused", "waveform focused", "playing", "finished", "at end", "saved path"} {
				t.Run(state, func(t *testing.T) {
					label := "Play audio"
					m.playing, m.result.finished, m.result.position = false, false, 0
					switch state {
					case "save focused":
						m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
					case "waveform focused":
						m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
					case "playing":
						m.playing, label = true, "Stop audio"
					case "finished":
						m.result.finished, label = true, "Replay audio"
					case "at end":
						m.result.position, label = time.Duration(m.result.analysis.Duration)*time.Second, "At end"
					case "saved path":
						m.result.savedPaths = []string{"/synthetic/" + strings.Repeat("long-directory/", 8) + "speech.wav"}
					}
					require.Equal(t, play, position(t, label))
					require.Equal(t, save, position(t, "Save audio"))
					audioFeedbackView(t, m)
				})
			}
		})
	}
}

// Existing command contract tests compare the public clipboard/print bytes.
func (m *audioWorkbench) commandPreview() string {
	raw, _ := m.rawCommand()
	return raw
}
