package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioEscapeCancelsPendingRequestAndAllowsRetry(t *testing.T) {
	m := audioReviewModel(t)
	prior := audioReviewResult("prior", false)
	m.result = &prior
	m.input.SetValue("First draft")
	m.instructions.SetValue("Quietly.")
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	m.next = func(ctx context.Context, _ *cli.Command) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}
	generate := tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	_, cmd := m.Update(generate)
	require.Contains(t, m.View().Content, "Stop generation")
	worker := cmd().(tea.BatchMsg)[0]
	done := make(chan tea.Msg, 1)
	go func() { done <- worker() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("speech request did not start")
	}
	m.Update(tea.WindowSizeMsg{Width: 25, Height: 8})
	audioReviewKey(m, tea.KeyEscape)
	audioReviewKey(m, tea.KeyEscape)
	require.True(t, m.busy)
	require.False(t, m.quit)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("speech request did not receive cancellation")
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	require.Contains(t, m.View().Content, "Stopping generation")
	require.NotNil(t, m.View().Cursor, "the script can be edited while the request drains")
	m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	m.Update(tea.PasteMsg{Content: " again"})
	_, retry := m.Update(generate)
	require.Nil(t, retry, "the request still owns the command flags")
	require.Nil(t, audioReviewKey(m, tea.KeyEnter), "Enter must also wait for the request to drain")
	close(release)
	select {
	case message := <-done:
		m.Update(message)
	case <-time.After(3 * time.Second):
		t.Fatal("speech request did not stop")
	}
	require.False(t, m.busy)
	require.Nil(t, m.generationCancel)
	require.False(t, m.quit)
	require.NoError(t, m.lastErr)
	require.NoError(t, m.ctx.Err(), "the session remains active")
	require.Same(t, &prior, m.result)
	require.Equal(t, "Quietly.", m.instructions.Value())
	require.Equal(t, "First draft! again", m.input.Value())
	for _, size := range [][2]int{{122, 32}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		require.Contains(t, m.View().Content, audioGenerationStoppedNote)
	}

	m.next = receiveSyntheticSpeech
	_, cmd = m.Update(generate)
	generated := audioSeekWorker(t, cmd)
	require.NoError(t, generated.err)
	m.Update(generated)
	require.NotSame(t, &prior, m.result)
	require.Equal(t, "First draft! again", m.result.entry.Settings.Input)
}

func TestAudioCanceledPreviewIgnoresQueuedSuccess(t *testing.T) {
	m := audioReviewModel(t)
	original := audioReviewResult("original", false)
	m.result = &original
	m.input.SetValue("Keep this script")
	m.next = receiveSyntheticSpeech
	completion := audioSeekWorker(t, m.generate(true))
	require.NoError(t, completion.err)
	require.True(t, completion.result.entry.Settings.Preview)

	audioReviewKey(m, tea.KeyEscape)
	m.Update(completion)
	require.False(t, m.playing)
	require.Same(t, &original, m.result)
	require.Equal(t, "Keep this script", m.input.Value())
	require.False(t, m.quit)
}

func TestAudioCancelBeforeRequestStarts(t *testing.T) {
	m := audioReviewModel(t)
	m.input.SetValue("Keep this draft")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	audioReviewKey(m, tea.KeyEscape)
	msg := audioSeekWorker(t, cmd)
	require.ErrorIs(t, msg.err, context.Canceled)
	m.Update(msg)
	require.NoError(t, m.lastErr)
	require.Equal(t, "Keep this draft", m.input.Value())
	entries, err := m.store.List(t.Context())
	require.NoError(t, err)
	require.Empty(t, entries)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	require.Contains(t, m.View().Content, audioGenerationStoppedNote)
}

func TestAudioCancelPreservesQueuedRequestFailure(t *testing.T) {
	m := audioReviewModel(t)
	prior := audioReviewResult("prior", false)
	m.result = &prior
	m.input.SetValue("Keep this draft")
	failure := cli.Exit("Synthetic speech failure", 17)
	m.next = func(context.Context, *cli.Command) error { return failure }
	completion := audioSeekWorker(t, m.generate(false))
	require.ErrorIs(t, completion.err, failure)

	audioReviewKey(m, tea.KeyEscape)
	m.Update(completion)
	require.False(t, m.busy)
	require.Same(t, &prior, m.result)
	require.Equal(t, "Keep this draft", m.input.Value())
	require.Equal(t, 0, m.focus)
	require.NotNil(t, m.View().Cursor)
	require.Contains(t, m.View().Content, "Speech request failed.")
	require.NotContains(t, m.View().Content, audioGenerationStoppedNote)
	require.ErrorIs(t, m.lastErr, failure)
	var exit cli.ExitCoder
	require.ErrorAs(t, m.lastErr, &exit)
	require.Equal(t, 17, exit.ExitCode())
}

func TestAudioCancelReportsCaptureCleanupFailure(t *testing.T) {
	m := audioReviewModel(t)
	prior := audioReviewResult("prior", false)
	m.result = &prior
	m.input.SetValue("Keep this draft")
	m.instructions.SetValue("Keep this direction")
	private := errors.New("/synthetic/private/audio/location")
	failure := errors.Join(context.Canceled, audiohistory.ErrCaptureCleanup, private)
	require.NotNil(t, m.generate(false))
	audioReviewKey(m, tea.KeyEscape)
	m.Update(audioWorkMessage{kind: "generate", err: classifiedAudioFailure(audioCaptureFailure, "Could not retain completed audio in private history.", failure)})

	require.False(t, m.busy)
	require.Empty(t, m.modal)
	require.Equal(t, 0, m.focus)
	require.Same(t, &prior, m.result)
	require.Equal(t, "Keep this draft", m.input.Value())
	require.Equal(t, "Keep this direction", m.instructions.Value())
	require.ErrorIs(t, m.lastErr, audiohistory.ErrCaptureCleanup)
	require.NotContains(t, m.lastErr.Error(), private.Error())
	for _, size := range [][2]int{{122, 32}, {40, 12}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View().Content
		require.Contains(t, view, "Could not clean up partial audio.")
		require.Contains(t, view, "Check the audio history folder")
		require.NotContains(t, view, private.Error())
		require.NotNil(t, m.View().Cursor)
	}

	require.NotNil(t, m.generate(false))
	audioReviewKey(m, tea.KeyEscape)
	m.Update(audioWorkMessage{kind: "generate", err: context.Canceled})
	require.Contains(t, m.View().Content, audioGenerationStoppedNote)
	require.NotContains(t, m.View().Content, "Could not clean up partial audio.")
	require.ErrorIs(t, m.lastErr, audiohistory.ErrCaptureCleanup, "ordinary cancellation preserves an earlier error")
}

func receiveSyntheticSpeech(ctx context.Context, _ *cli.Command) error {
	body := io.NopCloser(bytes.NewReader(audioSeekWave(make([]byte, 128), 8000, 1)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/", nil)
	if err != nil {
		return err
	}
	return ctx.Value(audioCaptureKey{}).(*audioCapture).receive(&http.Response{
		Header: http.Header{"Content-Type": {"audio/wav"}}, Body: body, Request: req,
	})
}
