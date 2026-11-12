package audio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestTerminalTranslationWaitsForVisibleStartAndPreservesStopAtSmallSize(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checks := 0
	m := &terminalTranslationModel{ctx: ctx, cancel: cancel, width: 30, height: 8, options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}}, validateCapture: func(string) error { checks++; return errors.New("capture unavailable") }}
	m.focusSetupAction()
	require.Nil(t, m.Init())
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, 0, checks)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, 1, checks)
	require.False(t, m.started)
	require.Contains(t, m.View().Content, "capture unavailable")
	m.started = true
	m.stop = make(chan struct{})
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	m.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	select {
	case <-m.stop:
	default:
		t.Fatal("small terminal prevented stopping capture")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)
	require.Equal(t, 130, m.exitCode)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
func TestTerminalTranslationRenderingSanitizesCaptionsAndFits(t *testing.T) {
	for _, size := range [][2]int{{40, 16}, {80, 24}, {120, 40}} {
		m := &terminalTranslationModel{width: size[0], height: size[1], color: true, dark: true, folder: "~/Downloads/gpt-audio", options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}}, state: terminalTranslationUpdate{Original: strings.Repeat("世界 hello ", 400), Translation: "caption\x1b[2J\u202e x\nline\ttab"}}
		content := m.View().Content
		require.NotContains(t, content, "\x1b[2J")
		require.NotContains(t, content, "\u202e")
		require.LessOrEqual(t, len(strings.Split(content, "\n")), size[1])
		for _, line := range strings.Split(content, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
		require.Contains(t, content, "Start")
		require.NotContains(t, content, "Background")
	}
}
func TestTerminalTranslationCanRetryFromSetupWithDraft(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	m := &terminalTranslationModel{ctx: ctx, cancel: cancel, width: 45, height: 16,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "fr"}},
		form:    terminalTranslationForm{source: translationText, text: "A sample", textModel: "sample-model"}}
	m.translateText = func(_ context.Context, text, language, model string) (translationTextResult, error) {
		calls++
		if text != "A sample" || language != "French" || model != "sample-model" {
			t.Error("retry changed the draft, target language, or model")
		}
		if calls == 1 {
			return translationTextResult{}, audiolive.Failure("Access denied. Check model permissions.")
		}
		return translationTextResult{Text: "Exemple"}, nil
	}
	m.focusSetupAction()
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	m.Update(enter)
	m.workers.Wait()
	m.Update(terminalTranslationTick{})
	require.False(t, m.started)
	require.Equal(t, 1, calls)
	require.Equal(t, "Start", m.fields()[m.form.row])
	view := m.View().Content
	require.Contains(t, view, "Retry translation")
	require.Contains(t, view, "Access denied")
	require.NotContains(t, view, "Interrupted")
	require.NotContains(t, view, "Save to")
	require.Error(t, m.lastErr)

	m.Update(enter)
	m.workers.Wait()
	m.Update(terminalTranslationTick{})
	require.Equal(t, 2, calls)
	require.NoError(t, m.lastErr)
	require.True(t, m.result.Complete)
	require.Contains(t, m.View().Content, "Exemple")
	require.NoError(t, m.result.Close())
}

func TestTerminalTranslationKeepsPartialCaptionsOnFailure(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stage.close()) })
	_, err = stage.files[2].WriteString("Received words")
	require.NoError(t, err)
	require.NoError(t, stage.finish())
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &terminalTranslationModel{ctx: t.Context(), cancelRun: cancel, started: true, width: 80, height: 24, folder: t.TempDir(),
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}},
		latest:  terminalTranslationUpdate{Translation: "Received words"},
		result:  terminalTranslationResult{stage: stage, TranslationPath: filepath.Join(stage.directory, "translation.txt"), Err: audiolive.Failure("Connection lost.")},
		done:    make(chan struct{})}
	close(m.done)
	m.Update(terminalTranslationTick{})
	require.True(t, m.finished)
	require.True(t, m.hasResult())
	view := m.View().Content
	require.Contains(t, view, "Received words")
	require.Contains(t, view, "S save")
	require.NotContains(t, view, "Retry translation")
	m.Update(tea.KeyPressMsg{Code: 's'})
	m.workers.Wait()
	m.Update(terminalTranslationTick{})
	require.True(t, m.saved)
	data, err := os.ReadFile(filepath.Join(m.savedPath, "translation.txt"))
	require.NoError(t, err)
	require.Equal(t, "Received words", string(data))
}

func TestTerminalTranslationExportIsCompletePrivateAndCollisionSafe(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stage.close()) })
	audio := []byte{1, 2, 3, 4}
	_, err = stage.files[0].Write(audio)
	require.NoError(t, err)
	_, err = stage.files[1].WriteString("Hola.\n")
	require.NoError(t, err)
	_, err = stage.files[2].WriteString("Hello.\n")
	require.NoError(t, err)
	require.NoError(t, stage.finish())
	result := terminalTranslationResult{stage: stage, AudioPath: filepath.Join(stage.directory, "translation.pcm"), OriginalPath: filepath.Join(stage.directory, "original.txt"), TranslationPath: filepath.Join(stage.directory, "translation.txt"), AudioBytes: 4, SampleRate: 24000, Channels: 1, Complete: true}
	destination := t.TempDir()
	first, err := saveTerminalTranslation(t.Context(), result, destination)
	require.NoError(t, err)
	second, err := saveTerminalTranslation(t.Context(), result, destination)
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	data, err := os.ReadFile(filepath.Join(first, "translation.wav"))
	require.NoError(t, err)
	require.Len(t, data, 48)
	require.Equal(t, "RIFF", string(data[:4]))
	require.Equal(t, audio, data[44:])
	captions, err := os.ReadFile(filepath.Join(first, "translation.txt"))
	require.NoError(t, err)
	require.Equal(t, "Hello.\n", string(captions))
	manifest, err := os.ReadFile(filepath.Join(first, "session.json"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `"complete":true`)
	require.FileExists(t, result.AudioPath)
}
func TestTerminalTranslationFailedExportRollsBackOnlyOwnedFiles(t *testing.T) {
	destination := t.TempDir()
	keep := filepath.Join(destination, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("keep"), 0600))
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stage.close()) })
	require.NoError(t, stage.finish())
	result := terminalTranslationResult{stage: stage, AudioBytes: 4, SampleRate: 24000, Channels: 1}
	_, err = saveTerminalTranslation(t.Context(), result, destination)
	require.Error(t, err)
	files, err := os.ReadDir(destination)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "keep.txt", files[0].Name())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = saveTerminalTranslation(ctx, result, destination)
	require.ErrorIs(t, err, context.Canceled)
}
