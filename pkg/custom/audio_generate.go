package custom

import (
	"context"
	"math"
	"mime"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/audiohistory"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

var audioModels = []string{"gpt-4o-mini-tts", "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15", "tts-1", "tts-1-1106", "tts-1-hd", "tts-1-hd-1106"}
var audioVoices = []string{"alloy", "ash", "ballad", "cedar", "coral", "echo", "fable", "marin", "nova", "onyx", "sage", "shimmer", "verse"}
var audioFormats = []string{"mp3", "opus", "aac", "flac", "wav", "pcm"}

const audioAuditionText = "Hello. This is a preview of this voice."

func audioDefaultSettings() audiohistory.Settings {
	return audiohistory.Settings{Model: "gpt-4o-mini-tts", Voice: "coral", Format: "wav", Speed: 1}
}

func audioSupportsVoice(model, voice string) bool {
	return !strings.HasPrefix(model, "tts-1") || !slices.Contains([]string{"ballad", "cedar", "marin", "verse"}, voice)
}

func audioModelVoices(model string) []string {
	var voices []string
	for _, voice := range audioVoices {
		if audioSupportsVoice(model, voice) {
			voices = append(voices, voice)
		}
	}
	return voices
}

func audioWorkbenchRequested(command *cli.Command) bool {
	if command.Args().Present() {
		return false
	}
	for _, flag := range command.Flags {
		if flag.IsSet() {
			return false
		}
	}
	for _, name := range []string{"format", "format-error", "transform", "transform-error", "raw-output", "quiet"} {
		if command.Root().IsSet(name) {
			return false
		}
	}
	return true
}

func audioWorkbenchWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		out, ok := command.Root().Writer.(*os.File)
		if !ok || out != os.Stdout || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(out.Fd()) ||
			strings.EqualFold(os.Getenv("TERM"), "dumb") || !audioWorkbenchRequested(command) {
			return next(ctx, command)
		}
		store, err := audiohistory.Default()
		if err != nil {
			return classifiedAudioFailure(audioHistoryFailure, "Could not open private audio history.", err)
		}
		return runAudioWorkbench(ctx, command, next, store, os.Stdin, out)
	}
}

// The generated request builder recognizes InRequest. Literal editor values use
// the existing protected-input marker, preserving both @ and backslash-@ text.
type audioLiteralFlag struct {
	cli.Flag
	requestflag.InRequest
	value any
}

func (f audioLiteralFlag) Get() any    { return f.value }
func (f audioLiteralFlag) IsSet() bool { return true }

type audioCaptureKey struct{}
type audioCapture struct {
	store    audiohistory.Store
	settings audiohistory.Settings
	entry    audiohistory.Entry
	received bool
}

func (c *audioCapture) receive(response *http.Response) error {
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || kind == "text/event-stream" || !strings.HasPrefix(kind, "audio/") && kind != "application/octet-stream" {
		return classifiedAudioFailure(audioIncompleteFileFailure, "The speech response did not contain a finite audio file.", err)
	}
	c.entry, err = c.store.Capture(response.Request.Context(), response.Body, c.settings)
	if err != nil {
		return classifiedAudioFailure(audioCaptureFailure, "Could not retain completed audio in private history.", err)
	}
	c.received = true
	return nil
}

func generateAudio(ctx context.Context, command *cli.Command, next cli.ActionFunc, store audiohistory.Store, settings audiohistory.Settings) (audiohistory.Entry, error) {
	if err := validateAudioSettings(settings); err != nil {
		return audiohistory.Entry{}, err
	}
	values := map[string]any{
		"input": untrustedStdinValue(settings.Input), "instructions": untrustedStdinValue(settings.Instructions),
		"model": untrustedStdinValue(settings.Model), "voice": untrustedStdinValue(settings.Voice),
		"response-format": untrustedStdinValue(settings.Format), "speed": settings.Speed,
		"stream-format": untrustedStdinValue("audio"),
	}
	if settings.Instructions == "" {
		delete(values, "instructions")
	}
	original := command.Flags
	command.Flags = slices.Clone(original)
	defer func() { command.Flags = original }()
	for i, flag := range command.Flags {
		value, exists := values[flag.Names()[0]]
		if !exists {
			continue
		}
		request, ok := flag.(requestflag.InRequest)
		if !ok {
			return audiohistory.Entry{}, audioFailure("The speech request controls are unavailable.", nil)
		}
		command.Flags[i] = audioLiteralFlag{Flag: flag, InRequest: request, value: value}
	}
	capture := &audioCapture{store: store, settings: settings}
	err := next(context.WithValue(ctx, audioCaptureKey{}, capture), command)
	if err != nil {
		return capture.entry, err
	}
	if !capture.received {
		return audiohistory.Entry{}, classifiedAudioFailure(audioIncompleteFileFailure, "The speech request did not finish an audio file.", nil)
	}
	return capture.entry, nil
}

func validateAudioSettings(s audiohistory.Settings) error {
	if strings.TrimSpace(s.Input) == "" {
		return classifiedAudioFailure(audioEmptyTextFailure, "Enter text before generating speech.", nil)
	}
	if !slices.Contains(audioFormats, s.Format) || math.IsNaN(s.Speed) || math.IsInf(s.Speed, 0) || s.Speed < .25 || s.Speed > 4 {
		return classifiedAudioFailure(audioInvalidSettingsFailure, "Choose a supported format and speed from 0.25 to 4.", nil)
	}
	if strings.HasPrefix(s.Model, "tts-1") {
		if s.Instructions != "" {
			return classifiedAudioFailure(audioInstructionsFailure, "This model does not support instructions. Clear instructions or choose gpt-4o-mini-tts.", nil)
		}
		if !audioSupportsVoice(s.Model, s.Voice) {
			return classifiedAudioFailure(audioVoiceFailure, "This voice needs gpt-4o-mini-tts. Choose another voice or model.", nil)
		}
	}
	return nil
}

func audioFailure(message string, cause error) error {
	return &localcommands.LocalError{Message: message, Cause: cause}
}
