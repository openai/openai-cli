package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/stretchr/testify/require"
)

func TestTerminalTranslationEditsAndSavesTypedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &terminalTranslationModel{ctx: ctx, cancel: cancel, width: 80, height: 24, folder: t.TempDir(),
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}},
		form:    terminalTranslationForm{textModel: "gpt-5-mini"}}
	draft := "@local.wav\n" + strings.Repeat("A line\n", 120) + "Hello"
	m.translateText = func(ctx context.Context, text, language, model string) (translationTextResult, error) {
		if text != draft || language != "French" || model != "example-model" {
			t.Errorf("unexpected typed translation settings")
		}
		input, output := int64(4), int64(0)
		return translationTextResult{Text: "Bonjour", InputTokens: &input, OutputTokens: &output}, nil
	}
	key := func(code rune) { m.Update(tea.KeyPressMsg{Code: code}) }
	paste := func(value string) { m.Update(tea.PasteMsg{Content: value}) }
	key(tea.KeyRight)
	require.Contains(t, m.View().Content, "Audio file")
	key(tea.KeyRight)
	key(tea.KeyEnter)
	paste(draft)
	key(tea.KeyEnter)
	key(tea.KeyUp)
	key(tea.KeyUp)
	key(tea.KeyEnter)
	key(tea.KeyDown)
	key(tea.KeyEnter)
	key(tea.KeyDown)
	key(tea.KeyEnter)
	key(tea.KeyDown)
	key(tea.KeyEnter)
	require.NotNil(t, m.form.editor)
	m.form.editor.SetValue("example-model")
	key(tea.KeyEnter)
	require.False(t, m.started)
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	require.True(t, m.started)
	m.workers.Wait()
	m.Update(terminalTranslationTick{})
	require.True(t, m.result.Complete)
	require.Contains(t, m.View().Content, "Response time")
	require.Contains(t, m.View().Content, "Output 0 tokens")
	key('s')
	m.workers.Wait()
	m.Update(terminalTranslationTick{})
	require.True(t, m.saved)
	content, err := os.ReadFile(filepath.Join(m.savedPath, "translation.txt"))
	require.NoError(t, err)
	require.Equal(t, "Bonjour", string(content))
	content, err = os.ReadFile(filepath.Join(m.savedPath, "original.txt"))
	require.NoError(t, err)
	require.Equal(t, draft, string(content))
	require.NoFileExists(t, filepath.Join(m.savedPath, "translation.wav"))
	stage := m.result.Directory
	key('n')
	require.NoDirExists(t, stage)
	require.False(t, m.started)
	require.Equal(t, draft, m.form.text)
}

func TestTerminalTranslationCancelsTypedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &terminalTranslationModel{ctx: ctx, cancel: cancel, width: 80, height: 24,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}},
		form:    terminalTranslationForm{source: translationText, text: "Keep this draft"}}
	m.translateText = func(ctx context.Context, _, _, _ string) (translationTextResult, error) {
		<-ctx.Done()
		return translationTextResult{}, audiolive.Failure("The translator stopped.")
	}
	m.focusSetupAction()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case <-m.done:
	case <-time.After(3 * time.Second):
		t.Fatal("typed request did not cancel")
	}
	m.Update(terminalTranslationTick{})
	require.True(t, m.finished)
	require.NoError(t, m.lastErr)
	require.False(t, m.hasResult())
	require.Contains(t, m.View().Content, "Stopped")
	m.Update(tea.KeyPressMsg{Code: 'n'})
	require.Equal(t, "Keep this draft", m.form.text)
}

func TestTerminalTranslationSourceSelectionNeverStartsCapture(t *testing.T) {
	m := &terminalTranslationModel{width: 40, height: 16,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}}}
	m.focusSetupAction()
	key := func(code rune) { m.Update(tea.KeyPressMsg{Code: code}) }
	view := m.View().Content
	for _, expected := range []string{"[Microphone]", "Audio file", "Typed text", "Translate into", "› Start microphone", "More settings"} {
		require.Contains(t, view, expected)
	}
	require.NotContains(t, view, "Transcription")
	require.NotContains(t, view, "\x1b")
	key(tea.KeyRight)
	key(tea.KeyLeft)
	key(tea.KeyEnter)
	require.False(t, m.started)
	require.Equal(t, "Start", m.fields()[m.form.row])
	key(tea.KeyUp)
	key(tea.KeyEnter)
	require.Contains(t, m.View().Content, "› English")
	key(tea.KeyDown)
	key(tea.KeyEscape)
	require.Equal(t, "en", m.options.Config.Language)
	require.False(t, m.form.languagesOpen)
	key(tea.KeyEnter)
	key(tea.KeyDown)
	key(tea.KeyEnter)
	require.Equal(t, "fr", m.options.Config.Language)
	require.Equal(t, "Start", m.fields()[m.form.row])
	key(tea.KeyDown)
	key(tea.KeyEnter)
	require.True(t, m.form.advanced)
	for _, expected := range []string{"Microphone", "Audio model", "Transcription", "Playback", "Noise reduction", "Save folder"} {
		key(tea.KeyDown)
		view := m.View().Content
		require.Contains(t, view, "› "+expected)
		require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
		for _, line := range strings.Split(view, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), m.width)
		}
	}
	require.False(t, m.started)
}

func TestTerminalTranslationSetupPreservesRecoveryOrder(t *testing.T) {
	m := &terminalTranslationModel{width: 40, height: 16, retrySetup: true,
		options: terminalTranslationOptions{Config: audiolive.Config{Language: "en"}},
		note:    "The translation service could not connect. Check your API connection and try again."}
	m.focusSetupAction()
	view := m.View().Content
	require.Contains(t, view, "› Retry translation")
	require.Contains(t, strings.Join(strings.Fields(view), " "), m.note)
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
}

func TestTerminalTranslationCompactResultsIncludeControls(t *testing.T) {
	tokens := int64(1234567)
	m := &terminalTranslationModel{width: 40, height: 16, started: true, finished: true,
		state:  terminalTranslationUpdate{Original: strings.Repeat("original ", 20), Translation: strings.Repeat("translated ", 20), HasLatency: true, Latency: time.Second, InputTokens: &tokens, OutputTokens: &tokens, Peaks: []float64{.5, 1}},
		result: terminalTranslationResult{stage: &terminalTranslationStage{}, AudioBytes: 1000, SampleRate: 100, Channels: 1, Complete: true},
		note:   "Synthetic warning"}
	view := m.View().Content
	require.LessOrEqual(t, len(strings.Split(view, "\n")), 16)
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 40)
	}
	for _, label := range []string{"Original", "Translation", "[P] Play", "[←→] Seek", "[S] Save", "[N] New", "[Esc] Exit"} {
		require.Contains(t, view, label)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 500*time.Millisecond, m.seekPosition)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Zero(t, m.seekPosition)
}

func TestTerminalTranslationReadsEntireTranscriptInPages(t *testing.T) {
	stage, err := newTerminalTranslationStage()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stage.close()) })
	original := "Original\x1b[2J\u202e text"
	translated := "Beginning " + strings.Repeat("世界 ab ", 700) + "end of transcript"
	_, err = stage.files[1].WriteString(original)
	require.NoError(t, err)
	_, err = stage.files[2].WriteString(translated)
	require.NoError(t, err)
	require.NoError(t, stage.finish())
	m := &terminalTranslationModel{width: 40, height: 16, started: true, finished: true,
		result: terminalTranslationResult{stage: stage, Complete: true}, state: terminalTranslationUpdate{Translation: "display tail"}}
	key := func(code rune) { m.Update(tea.KeyPressMsg{Code: code}) }
	key('t')
	require.Contains(t, m.View().Content, "Beginning ")
	var all strings.Builder
	for m.transcript != nil {
		all.WriteString(strings.Join(m.transcript.rows, ""))
		if m.transcript.next == m.transcript.reader.Size() {
			break
		}
		before := m.transcript.next
		key(tea.KeyPgDown)
		require.Greater(t, m.transcript.next, before)
	}
	require.Equal(t, translated, all.String())
	require.Contains(t, strings.Join(m.transcript.rows, ""), "end of transcript")
	key(tea.KeyPgUp)
	require.NotContains(t, strings.Join(m.transcript.rows, ""), "end of transcript")
	key(tea.KeyTab)
	view := m.View().Content
	require.Contains(t, view, "Original")
	require.NotContains(t, view, "\x1b")
	require.NotContains(t, view, "\u202e")
	key(tea.KeyEscape)
	require.Nil(t, m.transcript)
	require.Contains(t, m.View().Content, "[T] Read")
}
