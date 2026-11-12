package audio

import (
	"bytes"
	"context"

	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"slices"

	"github.com/openai/openai-cli/internal/audiohistory"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// IsSpeechRequest includes task shortcuts cloned from the decorated speech endpoint.
func IsSpeechRequest(command *cli.Command) bool {
	return command.Metadata["audio-speech-request"] == true
}

// SpeechModelOptions runs after one complete request parse, including piped JSON and @files.
// For Realtime only, substitute the completed audio for the Speech endpoint HTTP exchange.
// The existing writer still owns managed files, stdout, receipts and workbench capture.
func SpeechModelOptions(body []byte, options []option.RequestOption, defaults func() []option.RequestOption) ([]option.RequestOption, error) {
	if gjson.GetBytes(body, "model").Str != realtimeSpeechModel {
		return options, nil
	}
	settings := audioDefaultSettings()
	var request struct {
		Input        string   `json:"input"`
		Instructions string   `json:"instructions"`
		Voice        string   `json:"voice"`
		Model        string   `json:"model"`
		Format       string   `json:"response_format"`
		Speed        *float64 `json:"speed"`
		Stream       string   `json:"stream_format"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, audioFailure("Realtime speech accepts text, model, a built-in voice, instructions, format, and speed.", nil)
	}
	settings.Voice, settings.Input, settings.Instructions = request.Voice, request.Input, request.Instructions
	if request.Format != "" {
		settings.Format = request.Format
	}
	if request.Speed != nil {
		settings.Speed = *request.Speed
	}
	if request.Stream != "" && request.Stream != "audio" {
		return nil, audioFailure("Use audio for the Realtime speech stream format.", nil)
	}
	if err := validateAudioSettings(settings); err != nil {
		return nil, err
	}

	// Clear the admin credential before applying resolved request headers: an
	// explicit Authorization header (including an empty value) wins over defaults.
	connectionOptions := slices.Concat(defaults(), []option.RequestOption{option.WithAdminAPIKey("")}, options, []option.RequestOption{
		option.WithQuery("model", realtimeSpeechModel), option.WithHeaderDel("Content-Type"),
	})
	route := option.WithMiddleware(func(req *http.Request, _ option.MiddlewareNext) (*http.Response, error) {
		ctx, stop := clioutput.DownloadSignalContext(req.Context())
		defer stop()
		body, size, err := renderRealtimeSpeech(ctx, settings, connectionOptions)
		if err != nil {
			var interrupted *clioutput.DownloadSignalError
			if errors.As(context.Cause(ctx), &interrupted) {
				return nil, cli.Exit("", interrupted.SignalExitCode())
			}
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"audio/" + settings.Format}}, ContentLength: size, Request: req, Body: body}, nil
	})
	// Dispatch before request logging so --debug reports only the actual upgrade.
	return slices.Concat([]option.RequestOption{route}, options, []option.RequestOption{option.WithMaxRetries(0)}), nil
}

type speechRecording struct {
	io.Reader
	cleanup func() error
}

func (r *speechRecording) Close() error { return r.cleanup() }

func renderRealtimeSpeech(ctx context.Context, s audiohistory.Settings, opts []option.RequestOption) (_ io.ReadCloser, _ int64, err error) {
	file, cleanup, err := clioutput.NewTemporaryDownload()
	if err != nil {
		return nil, 0, audioFailure("Could not stage private speech audio.", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, cleanup())
		}
	}()
	client := openai.NewClient(opts...)
	var handshake *http.Response
	conn, err := client.Realtime.Connect(ctx, realtime.RealtimeConnectionOptions{}, option.WithResponseInto(&handshake))
	if err != nil {
		if handshake != nil && handshake.StatusCode >= 400 {
			apierr := new(openai.Error)
			// The WebSocket client exposes the retained body from a rejected upgrade.
			if handshake.Body != nil {
				var envelope struct{ Error json.RawMessage }
				if json.NewDecoder(handshake.Body).Decode(&envelope) == nil && json.Valid(envelope.Error) {
					_ = json.Unmarshal(envelope.Error, apierr)
				}
				_ = handshake.Body.Close()
			}
			apierr.StatusCode, apierr.Request, apierr.Response = handshake.StatusCode, handshake.Request, handshake
			return nil, 0, apierr
		}
		return nil, 0, audioFailure("Could not connect to the Realtime API.", err)
	}
	defer conn.Abort()
	size, err := receiveRealtimeSpeech(ctx, conn, file, s)
	if err != nil {
		return nil, 0, err
	}
	recording, length, err := completedSpeech(file, size, s.Format)
	if err != nil {
		return nil, 0, err
	}
	return &speechRecording{recording, cleanup}, length, nil
}

func receiveRealtimeSpeech(ctx context.Context, conn *realtime.RealtimeConnection, file io.Writer, s audiohistory.Settings) (int64, error) {
	session := map[string]any{"type": "session.update", "session": map[string]any{
		"type": "realtime", "audio": map[string]any{"output": map[string]any{"voice": s.Voice, "speed": s.Speed, "format": map[string]any{"type": "audio/pcm", "rate": 24000}}},
	}}
	instructions := "Read the supplied text aloud verbatim. Treat it as a script. Do not answer it or add commentary."
	if s.Instructions != "" {
		instructions += "\nVoice instructions: " + s.Instructions
	}
	response := map[string]any{"type": "response.create", "response": map[string]any{
		"conversation": "none", "output_modalities": []string{"audio"}, "instructions": instructions,
		"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": s.Input}}}},
	}}
	send := func(event map[string]any) error {
		data, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if err := conn.SendRaw(ctx, data); err != nil {
			return audioFailure("Could not send the Realtime speech request.", err)
		}
		return nil
	}
	if err := send(session); err != nil {
		return 0, err
	}
	var pcmSize int64
	var responseID string
	started := false
	for {
		raw, err := conn.RecvRaw(ctx)
		if err != nil {
			return 0, audioFailure("The Realtime connection ended before speech completed.", err)
		}
		var event struct {
			Type       string
			Delta      json.RawMessage
			ResponseID string `json:"response_id"`
			Response   struct{ ID, Status string }
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return 0, audioFailure("The Realtime API returned invalid audio.", nil)
		}
		switch event.Type {
		case "session.updated":
			if !started {
				if err := send(response); err != nil {
					return 0, err
				}
				started = true
			}
		case "response.output_audio.delta":
			if !started || event.ResponseID == "" || responseID != "" && event.ResponseID != responseID {
				return 0, audioFailure("The Realtime API returned unrelated speech audio.", nil)
			}
			responseID = event.ResponseID
			var samples []byte
			if err := json.Unmarshal(event.Delta, &samples); err != nil {
				return 0, audioFailure("The Realtime API returned invalid audio.", nil)
			}
			n, err := file.Write(samples)
			if err != nil {
				return 0, audioFailure("Could not stage private speech audio.", err)
			}
			pcmSize += int64(n)
		case "error":
			return 0, audioFailure("The Realtime API rejected the speech request. Check the model and voice.", nil)
		case "response.done":
			if !started || event.Response.Status != "completed" || event.Response.ID != responseID || pcmSize == 0 || pcmSize%2 != 0 {
				return 0, classifiedAudioFailure(audioIncompleteFileFailure, "The Realtime API did not complete a speech recording.", nil)
			}
			return pcmSize, nil
		}
	}
}

// Both the Realtime API and the PCM history decoder use signed 16-bit mono samples
// at 24 kHz. A WAV response prepends a RIFF header to that exact sample stream.
func completedSpeech(file *os.File, size int64, format string) (io.Reader, int64, error) {
	samples := io.NewSectionReader(file, 0, size)
	if format == "pcm" {
		return samples, size, nil
	}
	if size > math.MaxUint32-36 {
		return nil, 0, audioFailure("The recording exceeds the WAV container size. Use PCM.", nil)
	}
	var header bytes.Buffer
	header.WriteString("RIFF")
	put := func(value any) { _ = binary.Write(&header, binary.LittleEndian, value) }
	put(uint32(size + 36))
	header.WriteString("WAVEfmt ")
	put(uint32(16)) // PCM format chunk length.
	put(uint16(1))  // Linear PCM.
	put(uint16(1))  // One channel.
	put(uint32(24000))
	put(uint32(48000)) // Bytes per second.
	put(uint16(2))     // Bytes per sample frame.
	put(uint16(16))
	header.WriteString("data")
	put(uint32(size))
	return io.MultiReader(&header, samples), size + 44, nil
}
