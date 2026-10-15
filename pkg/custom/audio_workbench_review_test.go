package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func audioReviewModel(t *testing.T) *audioWorkbench {
	t.Helper()
	command := &cli.Command{Name: "openai", Writer: io.Discard, ErrWriter: io.Discard}
	m := newAudioWorkbench(t.Context(), command, func(context.Context, *cli.Command) error {
		t.Error("navigation unexpectedly invoked the speech request")
		return errors.New("unexpected speech request")
	}, audioTestStore(t))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m.color = false
	return m
}

func audioReviewKey(m *audioWorkbench, code rune) tea.Cmd {
	_, command := m.Update(tea.KeyPressMsg{Code: code})
	return command
}

func audioReviewMore(t *testing.T, m *audioWorkbench, action string) tea.Cmd {
	t.Helper()
	require.Empty(t, m.modal)
	m.focusSection(2)
	m.setting = 5
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Equal(t, "more", m.modal)
	for index, item := range m.moreItems() {
		if item == action {
			require.Equal(t, index, m.moreChoice)
			return audioReviewKey(m, tea.KeyEnter)
		}
		require.Nil(t, audioReviewKey(m, tea.KeyDown))
	}
	t.Fatalf("More action %q was unavailable", action)
	return nil
}

func audioReviewResult(id string, preview bool) audioWorkResult {
	settings := audioDefaultSettings()
	settings.Input, settings.Instructions, settings.Preview = "Synthetic text 世界", "Calm delivery", preview
	return audioWorkResult{
		entry:    audiohistory.Entry{ID: id, CreatedAt: time.Unix(1700000000, 0), Settings: settings, Bytes: 48000},
		analysis: audiomedia.Analysis{Format: "wav", Bytes: 48000, Duration: 1, Peaks: []float64{0, .1, 1, .3, 0}},
		measured: true,
	}
}

func TestAudioWorkbenchMeasurementFailureKeepsExportAfterDisplayChange(t *testing.T) {
	m := audioReviewModel(t)
	settings := audioDefaultSettings()
	settings.Input = "Synthetic retained audio"
	const audio = "This retained WAV is malformed."
	entry, err := m.store.Capture(t.Context(), io.NopCloser(strings.NewReader(audio)), settings)
	require.NoError(t, err)
	message := m.load(t.Context(), entry.ID, "generate")
	require.ErrorIs(t, message.err, audiomedia.ErrInvalidAudio)
	require.Equal(t, entry, message.result.entry)
	require.False(t, message.result.measured)
	var local *localUtilityError
	require.ErrorAs(t, message.err, &local)
	local.message = "Retained audio needs inspection."
	require.EqualError(t, message.err, local.message)
	require.Equal(t, "measure", audioFailureStage(message.kind, message.err))
	message.err = fmt.Errorf("outer context: %w", message.err)
	m.Update(message)
	require.Equal(t, entry, m.result.entry)
	require.ErrorIs(t, m.lastErr, audiomedia.ErrInvalidAudio)
	view := audioFeedbackView(t, m)
	require.Contains(t, view, "Export the retained file for inspection.")
	require.NotContains(t, view, "generating again")

	// Follow the recovery guidance through the form's real Save action.
	m.openSave()
	path := filepath.Join(t.TempDir(), "retained.wav")
	m.path.SetValue(path)
	save := audioReviewKey(m, tea.KeyEnter)
	require.NotNil(t, save)
	batch, ok := save().(tea.BatchMsg)
	require.True(t, ok)
	result, ok := batch[0]().(audioWorkMessage)
	require.True(t, ok)
	require.NoError(t, result.err)
	m.Update(result)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, audio, string(data))
	require.Contains(t, m.result.savedPaths, path)
	entries, err := m.store.List(t.Context())
	require.NoError(t, err)
	require.Equal(t, []audiohistory.Entry{entry}, entries)
}

func TestAudioReviewNavigationDoesNotGenerate(t *testing.T) {
	for setting, choices := range [][]string{audioModels, audioVoices, audioFormats} {
		t.Run(fmt.Sprintf("setting_%d", setting), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Keep this draft")
			m.focus, m.setting = 2, setting
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			require.Equal(t, "choice", m.modal)
			for range len(choices) * 2 {
				require.Nil(t, audioReviewKey(m, tea.KeyDown))
				require.False(t, m.busy)
			}
			require.Nil(t, audioReviewKey(m, tea.KeyUp))
			selected := m.choices[m.choice]
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			require.Empty(t, m.modal)
			require.False(t, m.busy)
			require.Nil(t, m.result)
			require.Equal(t, "Keep this draft", m.input.Value())
			actual := []string{m.settings.Model, m.settings.Voice, m.settings.Format}[setting]
			require.Equal(t, selected, actual)
			require.Nil(t, audioReviewKey(m, tea.KeyEnter))
			require.Nil(t, audioReviewKey(m, tea.KeyEscape))
			require.False(t, m.busy)
		})
	}
}

func TestAudioReviewClearPreservesHistoryAndExports(t *testing.T) {
	m := audioReviewModel(t)
	settings := audioDefaultSettings()
	settings.Input = "Retained synthetic text"
	entry, err := m.store.Capture(t.Context(), io.NopCloser(strings.NewReader("synthetic saved bytes")), settings)
	require.NoError(t, err)
	exported := filepath.Join(t.TempDir(), "export.wav")
	require.NoError(t, os.WriteFile(exported, []byte("independent exported bytes"), 0600))
	result, prior := audioReviewResult(entry.ID, true), audioReviewResult("prior", false)
	m.result, m.priorResult = &result, &prior
	m.input.SetValue("New draft")
	m.instructions.SetValue("New instructions")
	playContext, cancel := context.WithCancel(t.Context())
	m.playing, m.playCancel = true, cancel
	require.Nil(t, audioReviewMore(t, m, "Clear form"))
	require.ErrorIs(t, playContext.Err(), context.Canceled)
	require.False(t, m.playing)
	require.Nil(t, m.result)
	require.Nil(t, m.priorResult)
	require.Empty(t, m.input.Value())
	require.Empty(t, m.instructions.Value())
	require.Equal(t, audioDefaultSettings(), m.settings)
	entries, err := m.store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, entry.ID, entries[0].ID)
	snapshot, err := m.store.Open(t.Context(), entry.ID)
	require.NoError(t, err)
	bytes, err := io.ReadAll(snapshot.File)
	require.NoError(t, err)
	require.NoError(t, snapshot.Close())
	require.Equal(t, "synthetic saved bytes", string(bytes))
	bytes, err = os.ReadFile(exported)
	require.NoError(t, err)
	require.Equal(t, "independent exported bytes", string(bytes))
}

func TestAudioReviewBusyAndSmallLayoutsRetainCancellation(t *testing.T) {
	for _, size := range [][2]int{{100, 32}, {40, 12}, {39, 11}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := audioReviewModel(t)
			m.input.SetValue("Unchanged draft")
			m.busy = true
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, message := range []tea.Msg{
				tea.KeyPressMsg{Code: tea.KeyEnter},
				tea.KeyPressMsg{Code: tea.KeyTab},
				tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl},
				tea.KeyPressMsg{Code: 'x', Text: "x"},
				tea.PasteMsg{Content: "Do not insert"},
			} {
				_, command := m.Update(message)
				require.Nil(t, command)
				require.Equal(t, "Unchanged draft", m.input.Value())
				require.True(t, m.busy)
			}
			playContext, cancel := context.WithCancel(t.Context())
			m.playing, m.playCancel = true, cancel
			_, quit := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			require.NotNil(t, quit)
			require.True(t, m.quit)
			require.Equal(t, 130, m.exitCode)
			require.ErrorIs(t, playContext.Err(), context.Canceled)
			require.Empty(t, m.View().Content)
			m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult("late", false)})
			require.Nil(t, m.result, "late completion changed an exited form")
		})
	}
}

func TestAudioReviewSmallLayoutBlocksInvisibleEditing(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep me")
	m.Update(tea.WindowSizeMsg{Width: 39, Height: 11})
	_, command := m.Update(tea.PasteMsg{Content: "Invisible"})
	require.Nil(t, command)
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Equal(t, "Keep me", m.input.Value())
	require.False(t, m.busy)
	require.Contains(t, m.View().Content, "Ctrl+C exits")
}

func TestAudioReviewStalePlaybackCannotReplaceCurrentState(t *testing.T) {
	m := audioReviewModel(t)
	result := audioReviewResult("selected", false)
	m.result = &result
	m.playID, m.playing, m.note = 9, true, "Current playback"
	failure := errors.New("old playback failed")
	m.Update(audioWorkMessage{kind: "play", playID: 8, err: failure})
	require.True(t, m.playing)
	require.Equal(t, "Current playback", m.note)
	require.NoError(t, m.lastErr)
	m.Update(audioWorkMessage{kind: "play", playID: 9})
	require.False(t, m.playing)
	require.Same(t, &result, m.result)
	m.Update(audioWorkMessage{kind: "play", playID: 9, err: context.Canceled})
	require.NoError(t, m.lastErr)
}

func TestAudioReviewQueuedSubmitDoesNotStartPlayback(t *testing.T) {
	m := audioReviewModel(t)
	m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult("completed", false)})
	require.Equal(t, 4, m.focus)
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.False(t, m.playing, "queued submission became an unsolicited playback action")
}

func TestAudioReviewWorkersCannotStartAfterTeardown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	workers := &audioWorkers{}
	called := false
	command := workers.command(ctx, func(context.Context) audioWorkMessage {
		called = true
		return audioWorkMessage{}
	})
	workers.close(cancel)
	require.Nil(t, command())
	require.False(t, called)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestAudioReviewAuditionsRestoreTheOriginalResult(t *testing.T) {
	m := audioReviewModel(t)
	original := audioReviewResult("original", false)
	m.result = &original
	m.input.SetValue("My complete speech")
	m.instructions.SetValue("My instructions")
	for _, id := range []string{"audition_one", "audition_two"} {
		m.Update(audioWorkMessage{kind: "generate", result: audioReviewResult(id, true)})
		require.Equal(t, id, m.result.entry.ID)
		require.Same(t, &original, m.priorResult)
		require.Equal(t, "My complete speech", m.input.Value())
		require.Equal(t, "My instructions", m.instructions.Value())
	}
	require.Nil(t, audioReviewKey(m, tea.KeyEscape))
	require.Same(t, &original, m.result)
	require.Nil(t, m.priorResult)
	require.Equal(t, 0, m.focus)
	require.False(t, m.quit)
}

func TestAudioReviewHistoryRestoresSettingsWithoutGeneration(t *testing.T) {
	m := audioReviewModel(t)
	retained := audioReviewResult("history", false)
	retained.entry.Settings.Model = "gpt-4o-mini-tts-2025-12-15"
	retained.entry.Settings.Voice = "verse"
	retained.entry.Settings.Speed = 1.75
	_, command := m.Update(audioWorkMessage{kind: "open", result: retained})
	require.Nil(t, command)
	require.False(t, m.busy)
	require.Equal(t, retained.entry.Settings, m.settings)
	require.Equal(t, retained.entry.Settings.Input, m.input.Value())
	require.Equal(t, retained.entry.Settings.Instructions, m.instructions.Value())
	m.focus, m.setting = 2, 1
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.Nil(t, audioReviewKey(m, tea.KeyDown))
	require.Nil(t, audioReviewKey(m, tea.KeyEnter))
	require.NotEqual(t, m.settings.Voice, m.result.entry.Settings.Voice)
	require.Equal(t, "verse", m.result.entry.Settings.Voice)
}

func TestAudioReviewMetadataCannotControlTheTerminal(t *testing.T) {
	m := audioReviewModel(t)
	retained := audioReviewResult("history", false)
	retained.entry.Settings.Model = "model\x1b[2J"
	retained.entry.Settings.Voice = "voice\x1b]52;c;c3ludGhldGlj\x07"
	m.Update(audioWorkMessage{kind: "open", result: retained})
	for _, focus := range []int{0, 1, 2, 4} {
		m.focus = focus
		view := m.View().Content
		require.NotContains(t, view, "\x1b[2J", "retained model executed a terminal control")
		require.NotContains(t, view, "\x1b]52", "retained voice executed a clipboard control")
		require.NotContains(t, view, "\x07")
	}
	require.Nil(t, audioReviewMore(t, m, "Show command"))
	for _, row := range m.infoRows() {
		require.NotContains(t, row, "\x1b[2J")
		require.NotContains(t, row, "\x1b]52")
		require.NotContains(t, row, "\x07")
	}
}

func TestAudioReviewLayoutsRespectCellsAndColorOptOut(t *testing.T) {
	sgr := regexp.MustCompile(`\x1b\[[0-9;:]*m`)
	for _, size := range [][2]int{{1, 1}, {39, 11}, {40, 12}, {60, 18}, {80, 24}, {120, 40}} {
		for _, color := range []bool{false, true} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%dx%d/color_%t/dark_%t", size[0], size[1], color, dark), func(t *testing.T) {
					m := audioReviewModel(t)
					m.color, m.dark = color, dark
					m.input.SetValue(strings.Repeat("世界 e\u0301 👩‍💻 ", 12))
					m.instructions.SetValue("Retain this\nsecond line")
					result := audioReviewResult("synthetic", false)
					m.result = &result
					m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					for _, focus := range []int{0, 1, 2, 4} {
						m.focus = focus
						view := m.View().Content
						require.True(t, utf8.ValidString(view))
						rows := strings.Split(view, "\n")
						require.LessOrEqual(t, len(rows), max(1, size[1]-1))
						for _, row := range rows {
							require.LessOrEqual(t, ansi.StringWidth(row), max(1, size[0]-1))
						}
						if !color {
							for _, sequence := range sgr.FindAllString(view, -1) {
								require.Regexp(t, `^\x1b\[(?:0|1|7|22|27)?m$`, sequence,
									"NO_COLOR permits a monochrome caret, but no foreground or background colors")
							}
						}
						if size[0] >= 40 && size[1] >= 12 {
							if focus < 2 && size[0] >= 50 && size[1] >= 24 && color {
								require.Contains(t, ansi.Strip(view), "╭ "+[]string{"Text", "Instructions"}[focus]+" ", "focused editor disappeared")
							} else {
								require.Contains(t, ansi.Strip(view), "› ", "focus disappeared")
							}
						}
					}
				})
			}
		}
	}
}
