package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioVoiceBodyEmbeddingPreservesReplayAndOwnership(t *testing.T) {
	source := &recordingReadCloser{reader: strings.NewReader("audio_sample")}
	sampleData := strings.NewReader("synthetic unread sample")
	sample := &recordingReadCloser{reader: sampleData}
	original := requestconfig.FileUpload{Reader: source, FileName: "voice-type.txt", MediaType: "text/plain"}
	command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: t.Context()}}
	input := map[string]any{"type": original, "name": "Synthetic", "consent": "cons_synthetic", "audio_sample": requestconfig.FileUpload{Reader: sample}}
	body, err := embedAudioVoicesMultipartBody(command, input, &onceStdinReader{})
	require.NoError(t, err)
	require.Equal(t, original, input["type"])
	require.EqualValues(t, 1, source.closeCount.Load())
	require.Zero(t, sample.closeCount.Load())
	require.Equal(t, len("synthetic unread sample"), sampleData.Len())
	replay, ok := body["type"].(requestconfig.FileUpload)
	require.True(t, ok)
	require.Equal(t, original.FileName, replay.FileName)
	require.Equal(t, original.MediaType, replay.MediaType)
	require.EqualValues(t, len("audio_sample"), replay.Size)
	require.True(t, replay.KnownSize)
	data, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, "audio_sample", string(data))
	require.NoError(t, requestconfig.CloseFileUploads(body))
	require.EqualValues(t, 1, source.closeCount.Load())
	require.EqualValues(t, 1, sample.closeCount.Load())
}

func TestAudioVoiceBodyEmbeddingValidationFailureClosesOwnedInputs(t *testing.T) {
	for _, tc := range []struct{ name, voiceType string }{
		{"missing consent", "audio_sample"}, {"invalid type", "synthetic-private-invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &recordingReadCloser{reader: strings.NewReader(tc.voiceType)}
			sampleData := strings.NewReader("synthetic unread sample")
			sample := &recordingReadCloser{reader: sampleData}
			command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: t.Context()}}
			body, err := embedAudioVoicesMultipartBody(command, map[string]any{
				"type": requestconfig.FileUpload{Reader: source}, "name": "Synthetic", "audio_sample": requestconfig.FileUpload{Reader: sample},
			}, &onceStdinReader{})
			require.Nil(t, body)
			if tc.voiceType == "audio_sample" {
				require.ErrorContains(t, err, `Required flags "consent" not set`)
			} else {
				var typeError *voiceTypeError
				require.ErrorAs(t, err, &typeError)
				require.NotContains(t, err.Error(), "synthetic-private")
			}
			require.EqualValues(t, 1, source.closeCount.Load())
			require.EqualValues(t, 1, sample.closeCount.Load())
			require.Equal(t, len("synthetic unread sample"), sampleData.Len())
		})
	}
}

func TestAudioVoiceBodyEmbeddingTypeOpenFailureClosesOwnedInputs(t *testing.T) {
	sampleData := strings.NewReader("synthetic unread sample")
	sample := &recordingReadCloser{reader: sampleData}
	command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: t.Context()}}
	body, err := embedAudioVoicesMultipartBody(command, map[string]any{
		"type": "@" + filepath.Join(t.TempDir(), "synthetic-private-missing.txt"),
		"name": "Synthetic", "audio_sample": requestconfig.FileUpload{Reader: sample},
	}, &onceStdinReader{})
	require.Nil(t, body)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.EqualValues(t, 1, sample.closeCount.Load())
	require.Equal(t, len("synthetic unread sample"), sampleData.Len())
}

func TestAudioVoiceBodyEmbeddingReadFailureRetainsCauses(t *testing.T) {
	readErr := errors.New("synthetic-private-read-error")
	closeErr := errors.New("synthetic-private-close-error")
	source := &recordingReadCloser{reader: errorReader{err: readErr}, closeErr: closeErr}
	sample := &recordingReadCloser{reader: strings.NewReader("synthetic unread sample")}
	command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: t.Context()}}
	body, err := embedAudioVoicesMultipartBody(command, map[string]any{
		"type": requestconfig.FileUpload{Reader: source}, "name": "Synthetic", "audio_sample": requestconfig.FileUpload{Reader: sample},
	}, &onceStdinReader{})
	require.Nil(t, body)
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	require.ErrorContains(t, err, "--type")
	require.NotContains(t, err.Error(), "synthetic-private")
	require.EqualValues(t, 1, source.closeCount.Load())
	require.EqualValues(t, 1, sample.closeCount.Load())
}

func TestAudioVoiceBodyEmbeddingPartialFailureClosesOnce(t *testing.T) {
	source := &recordingReadCloser{reader: strings.NewReader("audio_sample")}
	sampleData := strings.NewReader("synthetic unread sample")
	sample := &recordingReadCloser{reader: sampleData}
	command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: t.Context()}}
	body, err := embedAudioVoicesMultipartBody(command, map[string]any{
		"type": requestconfig.FileUpload{Reader: source}, "name": "Synthetic", "consent": "cons_synthetic",
		// This case checks a visited reader before a later expansion failure.
		// It does not cover pre-owned readers after the failing array element.
		"audio_sample": []any{requestconfig.FileUpload{Reader: sample}, FilePathValue(filepath.Join(t.TempDir(), "synthetic-private-missing.wav"))},
	}, &onceStdinReader{})
	require.Nil(t, body)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.EqualValues(t, 1, source.closeCount.Load())
	require.EqualValues(t, 1, sample.closeCount.Load())
	require.Equal(t, len("synthetic unread sample"), sampleData.Len())
}

func TestAudioVoiceBodyEmbeddingCancellationReleasesOwnedRead(t *testing.T) {
	source := newCloseUnblocksReader(errors.New("synthetic source closed"))
	sampleData := strings.NewReader("synthetic unread sample")
	sample := &recordingReadCloser{reader: sampleData}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := &cli.Command{Name: "create", Metadata: map[string]any{audioVoicesContextMetadata: ctx}}
	input := map[string]any{
		"type": requestconfig.FileUpload{Reader: source}, "name": "Synthetic", "consent": "cons_synthetic", "audio_sample": requestconfig.FileUpload{Reader: sample},
	}
	type outcome struct {
		body map[string]any
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		body, err := embedAudioVoicesMultipartBody(command, input, &onceStdinReader{})
		done <- outcome{body, err}
	}()
	select {
	case <-source.readStarted:
	case <-time.After(multipartTestTimeout):
		t.Fatal("voice type inspection did not start")
	}
	cancel()
	select {
	case got := <-done:
		require.Nil(t, got.body)
		require.ErrorIs(t, got.err, context.Canceled)
	case <-time.After(multipartTestTimeout):
		t.Fatal("cancellation did not release voice type source")
	}
	require.EqualValues(t, 1, source.closeCount.Load())
	require.EqualValues(t, 1, sample.closeCount.Load())
	require.Equal(t, len("synthetic unread sample"), sampleData.Len())
}
