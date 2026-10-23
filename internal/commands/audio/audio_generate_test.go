package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func audioGenerateTestCommand() *cli.Command {
	return &cli.Command{Name: "speech", Reader: strings.NewReader(""), Writer: io.Discard, ErrWriter: io.Discard, Flags: []cli.Flag{
		&requestflag.Flag[string]{Name: "input", BodyPath: "input", Required: true},
		&requestflag.Flag[string]{Name: "instructions", BodyPath: "instructions"},
		&requestflag.Flag[string]{Name: "model", BodyPath: "model", Required: true},
		&requestflag.Flag[any]{Name: "voice", BodyPath: "voice", Required: true},
		&requestflag.Flag[string]{Name: "response-format", BodyPath: "response_format", Default: "mp3"},
		&requestflag.Flag[float64]{Name: "speed", BodyPath: "speed", Default: 1},
		&requestflag.Flag[string]{Name: "stream-format", BodyPath: "stream_format", Default: "audio"},
		&cli.StringFlag{Name: "output"},
	}}
}

func checkAudioGenerationPreservesLiteralEditorValuesAndRestoresFlags(t *testing.T, buildRequestOptions func(*cli.Command) ([]option.RequestOption, error)) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "synthetic.txt"))
	require.NoError(t, os.WriteFile(path, []byte("MUST_NOT_READ_THIS_FILE"), 0600))
	for _, literal := range []string{"@" + path, `\@` + path, "@file://" + path, "@data://" + path, "First line\nSecond 世界\tline"} {
		t.Run(literal, func(t *testing.T) {
			receivedRequests := make(chan map[string]any, 2)
			requests := new(atomic.Int32)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var received map[string]any
				require.Equal(t, "/audio/speech", r.URL.Path)
				require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
				receivedRequests <- received
				w.Header().Set("Content-Type", "audio/wav")
				_, _ = io.WriteString(w, "synthetic audio bytes")
			}))
			defer server.Close()
			store := audioTestStore(t)
			settings := audiohistory.Settings{Input: literal, Instructions: literal, Model: "@literal-model",
				Voice: `\@literal-voice`, Format: "wav", Speed: 1.25}
			command := audioGenerateTestCommand()
			failure := errors.New("synthetic first-attempt failure")
			command.Action = func(ctx context.Context, command *cli.Command) error {
				originalFlags := slices.Clone(command.Flags)
				originalContents := requestflag.ExtractRequestContents(command)
				var previous *audioCapture
				for attempt := range 2 {
					entry, err := generateAudio(ctx, command, func(requestContext context.Context, command *cli.Command) error {
						capture, ok := requestContext.Value(audioCaptureKey{}).(*audioCapture)
						require.True(t, ok)
						require.NotSame(t, previous, capture)
						previous = capture
						options, err := buildRequestOptions(command)
						require.NoError(t, err)
						if attempt == 0 {
							return failure
						}
						client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("sk-fake-audio-request"), option.WithMaxRetries(0))
						response, err := client.Audio.Speech.New(requestContext, openai.AudioSpeechNewParams{}, options...)
						if err != nil {
							return err
						}
						defer response.Body.Close()
						return capture.receive(response)
					}, store, settings)
					if attempt == 0 {
						require.ErrorIs(t, err, failure)
						require.Empty(t, entry.ID)
					} else {
						require.NoError(t, err)
						require.Equal(t, settings, entry.Settings)
						require.Equal(t, int64(len("synthetic audio bytes")), entry.Bytes)
					}
					require.Equal(t, originalFlags, command.Flags, "temporary flags escaped the generation attempt")
					require.Equal(t, originalContents, requestflag.ExtractRequestContents(command))
					require.Nil(t, ctx.Value(audioCaptureKey{}))
				}
				return nil
			}
			require.NoError(t, command.Run(t.Context(), []string{"speech", "--input", "original input", "--instructions", "original instructions",
				"--model", "original-model", "--voice", "alloy", "--response-format", "mp3", "--speed", "0.75", "--stream-format", "sse", "--output", "original-output"}))
			require.Equal(t, int32(1), requests.Load())
			received := <-receivedRequests
			require.Equal(t, map[string]any{"input": literal, "instructions": literal, "model": settings.Model,
				"voice": settings.Voice, "response_format": "wav", "speed": 1.25, "stream_format": "audio"}, received)
			encoded, err := json.Marshal(received)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "MUST_NOT_READ_THIS_FILE")
		})
	}
}

func TestAudioGenerationCatalogCompatibility(t *testing.T) {
	models := []string{"gpt-4o-mini-tts", "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15", "tts-1", "tts-1-1106", "tts-1-hd", "tts-1-hd-1106"}
	voices := []string{"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable", "marin", "nova", "onyx", "sage", "shimmer", "verse"}
	formats := []string{"mp3", "opus", "aac", "flac", "wav", "pcm"}
	require.Equal(t, models, audioModels)
	require.Equal(t, voices, audioVoices)
	require.Equal(t, formats, audioFormats)
	for _, model := range models {
		for _, voice := range voices {
			for _, format := range formats {
				for _, speed := range []float64{0.25, 1, 4} {
					settings := audiohistory.Settings{Input: "Synthetic speech", Model: model, Voice: voice, Format: format, Speed: speed}
					err := validateAudioSettings(settings)
					unsupported := strings.HasPrefix(model, "tts-1") && slices.Contains([]string{"ballad", "cedar", "marin", "verse"}, voice)
					if unsupported {
						require.Error(t, err, "%s/%s/%s/%v", model, voice, format, speed)
					} else {
						require.NoError(t, err, "%s/%s/%s/%v", model, voice, format, speed)
					}
				}
			}
		}
	}
	for _, model := range models {
		settings := audiohistory.Settings{Input: "Synthetic speech", Instructions: "Speak clearly", Model: model, Voice: "alloy", Format: "wav", Speed: 1}
		err := validateAudioSettings(settings)
		if strings.HasPrefix(model, "tts-1") {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
}

func checkAudioGenerationSendsSelectedRequestSettings(t *testing.T, buildRequestOptions func(*cli.Command) ([]option.RequestOption, error)) {
	base := audioDefaultSettings()
	base.Input = "Synthetic selected settings"
	var selections []audiohistory.Settings
	for _, model := range []string{"gpt-4o-mini-tts", "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15", "tts-1", "tts-1-1106", "tts-1-hd", "tts-1-hd-1106"} {
		settings := base
		settings.Model = model
		selections = append(selections, settings)
	}
	for _, voice := range []string{"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable", "marin", "nova", "onyx", "sage", "shimmer", "verse"} {
		settings := base
		settings.Voice = voice
		selections = append(selections, settings)
	}
	for _, format := range []string{"mp3", "opus", "aac", "flac", "wav", "pcm"} {
		settings := base
		settings.Format = format
		selections = append(selections, settings)
	}
	for _, speed := range []float64{.25, 4} {
		settings := base
		settings.Speed = speed
		selections = append(selections, settings)
	}
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		received <- body
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "synthetic audio bytes")
	}))
	defer server.Close()
	store := audioTestStore(t)
	command := audioGenerateTestCommand()
	command.Action = func(ctx context.Context, command *cli.Command) error {
		for _, settings := range selections {
			entry, err := generateAudio(ctx, command, func(ctx context.Context, command *cli.Command) error {
				options, err := buildRequestOptions(command)
				if err != nil {
					return err
				}
				client := openai.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("sk-fake-audio-settings"), option.WithMaxRetries(0))
				response, err := client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{}, options...)
				if err != nil {
					return err
				}
				defer response.Body.Close()
				return ctx.Value(audioCaptureKey{}).(*audioCapture).receive(response)
			}, store, settings)
			require.NoError(t, err)
			require.Equal(t, settings, entry.Settings)
			body := <-received
			require.Equal(t, settings.Model, body["model"])
			require.Equal(t, settings.Voice, body["voice"])
			require.Equal(t, settings.Format, body["response_format"])
			require.Equal(t, settings.Speed, body["speed"])
			require.Equal(t, settings.Input, body["input"])
			require.Equal(t, "audio", body["stream_format"])
			require.False(t, command.IsSet("input"))
		}
		return nil
	}
	require.NoError(t, command.Run(t.Context(), []string{"speech"}))
}

func TestAudioGenerationRejectsInvalidSettingsBeforeDispatch(t *testing.T) {
	for _, speed := range []float64{-1, 0, .249, 4.001, math.NaN(), math.Inf(1), math.Inf(-1)} {
		settings := audioDefaultSettings()
		settings.Input, settings.Speed = "Synthetic speech", speed
		called := false
		_, err := generateAudio(t.Context(), audioGenerateTestCommand(), func(context.Context, *cli.Command) error {
			called = true
			return nil
		}, audioTestStore(t), settings)
		require.Error(t, err)
		require.False(t, called, "invalid speed reached a paid request")
	}
}

func TestAudioCaptureRejectsNonAudioAndPreservesPreviewIdentity(t *testing.T) {
	settings := audioDefaultSettings()
	settings.Input, settings.Preview = audioAuditionText, true
	for _, contentType := range []string{"application/json", "text/event-stream", "text/plain", "invalid ; content"} {
		capture := &audioCapture{store: audioTestStore(t), settings: settings}
		response := &http.Response{Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(nil)), Request: (&http.Request{}).WithContext(t.Context())}
		require.Error(t, capture.receive(response))
		require.False(t, capture.received)
		require.Empty(t, capture.entry.ID)
		require.NoError(t, response.Body.Close())
	}
	capture := &audioCapture{store: audioTestStore(t), settings: settings}
	response := &http.Response{Header: http.Header{"Content-Type": []string{"audio/wav"}}, Body: io.NopCloser(strings.NewReader("synthetic audition")), Request: (&http.Request{}).WithContext(t.Context())}
	require.NoError(t, capture.receive(response))
	require.True(t, capture.received)
	require.True(t, capture.entry.Settings.Preview)
}

func audioTestStore(t *testing.T) audiohistory.Store {
	t.Helper()
	return audiohistory.Store{Directory: filepath.Join(t.TempDir(), "history")}
}
