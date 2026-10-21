package audio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/clihelp"
	clioutput "github.com/openai/openai-cli/internal/output"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/urfave/cli/v3"
)

// TranslationOptions supplies the application's existing credential and transport policy.
type TranslationOptions struct {
	DefaultRequestOptions func(*cli.Command) []option.RequestOption
	RequestOptions        func(*cli.Command) ([]option.RequestOption, error)
}

// ConfigureLiveTranslation adds an explicit browser workflow to the file-translation route.
// The original action still owns every invocation without --live.
func ConfigureLiveTranslation(root *cli.Command, policy TranslationOptions) {
	group := root.Command("audio")
	if group == nil {
		return
	}
	command := group.Command("translate")
	if command == nil || policy.DefaultRequestOptions == nil || policy.RequestOptions == nil {
		return
	}
	command.Usage = "Translate a saved file or live microphone and browser-tab audio."
	command.UsageText = "openai audio translate --file AUDIO --model whisper-1 [OPTIONS]\nopenai audio translate --live [GLOBAL OPTIONS]"
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-content"] = clihelp.Content{
		Summary:   command.Usage,
		InputNote: "File mode requires file and model inputs through flags or piped JSON/YAML.\nLive mode needs neither input.",
		Description: "Use --live to open a private local browser page. Select Microphone or Browser tab there.\n" +
			"Choose the target language, start capture, and stop the session to save translated audio and captions.\n" +
			"The CLI uses your configured API key. Browser audio starts only after your explicit capture choice.\n" +
			"File mode keeps its existing English translation behavior and output options.",
		Examples: []clihelp.Example{
			{Description: "Translate a saved recording into English:", Command: "audio translate --file recording.wav --model whisper-1"},
			{Description: "Translate microphone or browser-tab audio:", Command: "audio translate --live"},
		},
	}
	next := command.Action
	command.Flags = append(command.Flags, &cli.BoolFlag{Name: "live", Usage: "Translate microphone or browser-tab audio in a private local browser page"})
	command.Action = func(ctx context.Context, command *cli.Command) error {
		if !command.Bool("live") {
			return next(ctx, command)
		}
		if command.Args().Present() {
			return audioFailure("Live translation does not accept positional arguments. Select the audio source in the browser.", nil)
		}
		for _, name := range []string{"file", "model", "prompt", "response-format", "temperature"} {
			if command.IsSet(name) {
				return audioFailure("Use --live by itself for microphone or tab translation. Use --file and --model for saved audio.", nil)
			}
		}
		for _, name := range []string{"format", "format-error", "raw-output", "quiet", "transform", "transform-error"} {
			if command.Root().IsSet(name) {
				return audioFailure("Live translation opens a browser. Omit output-format overrides, or use file translation for command output.", nil)
			}
		}
		options, err := policy.RequestOptions(command)
		if err != nil {
			return err
		}
		options = append(policy.DefaultRequestOptions(command), options...)
		options = append(options, option.WithAdminAPIKey(""))
		ctx, stopSignals := clioutput.DownloadSignalContext(ctx)
		defer stopSignals()
		err = audiolive.Run(ctx, audiolive.Options{
			Connect: func(ctx context.Context, config audiolive.Config) (audiolive.Session, error) {
				return connectAudioTranslation(ctx, config, options)
			},
			OpenBrowser: cliterminal.OpenBrowser,
			Writer:      command.Root().Writer,
		})
		var interrupted *clioutput.DownloadSignalError
		if errors.As(context.Cause(ctx), &interrupted) {
			return cli.Exit("", interrupted.SignalExitCode())
		}
		var failure audiolive.Failure
		if errors.As(err, &failure) {
			return audioFailure(failure.Error(), err)
		}
		return err
	}
}

type apiTranslationSession struct{ session *realtime.TranslationSession }

func connectAudioTranslation(ctx context.Context, config audiolive.Config, options []option.RequestOption) (audiolive.Session, error) {
	client := openai.NewClient(options...)
	var response *http.Response
	session, err := client.Realtime.ConnectTranslation(ctx, "gpt-realtime-translate", realtime.TranslationConnectionOptions{}, option.WithResponseInto(&response))
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		return nil, translationFailure(status, "", err)
	}
	keep := false
	defer func() {
		if !keep {
			session.Abort()
		}
	}()
	input := map[string]any{
		"transcription":   map[string]string{"model": "gpt-realtime-whisper"},
		"noise_reduction": nil,
	}
	if config.NoiseReduction {
		input["noise_reduction"] = map[string]string{"type": "near_field"}
	}
	data, _ := json.Marshal(map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": input, "output": map[string]any{"language": config.Language}}}})
	if err = session.SendRaw(ctx, data); err != nil {
		return nil, translationFailure(0, "", err)
	}
	for {
		raw, err := session.RecvRaw(ctx)
		if err != nil {
			return nil, translationFailure(0, "", err)
		}
		var event translationEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, audiolive.Failure("The service returned an unreadable translation response. Try again.")
		}
		if event.Type == "error" {
			return nil, translationFailure(0, event.Error.Code, nil)
		}
		if event.Type == "session.updated" {
			keep = true
			return &apiTranslationSession{session: session}, nil
		}
	}
}

type translationEvent struct {
	Type       string `json:"type"`
	Delta      string `json:"delta"`
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
	Error      struct {
		Code string `json:"code"`
	} `json:"error"`
	Usage struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage"`
}

func (s *apiTranslationSession) Append(ctx context.Context, audio []byte) error {
	data, _ := json.Marshal(map[string]string{"type": "session.input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(audio)})
	return s.session.SendRaw(ctx, data)
}
func (s *apiTranslationSession) FinishInput(ctx context.Context) error {
	return s.session.SendRaw(ctx, json.RawMessage(`{"type":"session.close"}`))
}
func (s *apiTranslationSession) Abort() { s.session.Abort() }
func (s *apiTranslationSession) Receive(ctx context.Context) (audiolive.Event, error) {
	for {
		raw, err := s.session.RecvRaw(ctx)
		if err != nil {
			return audiolive.Event{}, err
		}
		var event translationEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return audiolive.Event{}, errors.New("unreadable translation event")
		}
		switch event.Type {
		case "session.input_transcript.delta", "session.output_transcript.delta", "session.output_audio.delta", "session.closed":
			return audiolive.Event{Type: event.Type, Delta: event.Delta, Tokens: event.Usage.TotalTokens, Format: event.Format, SampleRate: event.SampleRate, Channels: event.Channels}, nil
		case "error":
			return audiolive.Event{Type: "error", Message: translationFailure(0, event.Error.Code, nil).Error()}, nil
		default:
			if event.Usage.TotalTokens > 0 {
				return audiolive.Event{Type: "usage", Tokens: event.Usage.TotalTokens}, nil
			}
		}
	}
}

func translationFailure(status int, code string, err error) error {
	message := "Could not connect to live translation. Check your network, proxy, and API address."
	switch {
	case code == "invalid_api_key":
		message = "Your API key was not accepted. Run openai help setup and check the selected project."
	case status == 401:
		message = "The API could not verify your access. Run openai help setup and check your key, project, and organization."
	case status == 400:
		message = "The API rejected the live translation settings. Check the API address and update the CLI."
	case status == 403 || code == "permission_denied":
		message = "Your project cannot start live translation. Check model access and API key permissions."
	case status == 429 || code == "rate_limit_exceeded" || code == "insufficient_quota":
		message = "An API usage limit blocked translation. Check project quota and retry later."
	case status == 404 || code == "model_not_found":
		message = "Live translation is unavailable at this API address. Check the API address and model access."
	case status >= 500:
		message = "The translation service could not start. Try again later."
	case errors.Is(err, context.DeadlineExceeded):
		message = "The translation connection timed out. Check your network and try again."
	case code != "":
		message = "The service could not translate this audio. Stop the session and try again."
	}
	return audiolive.Failure(message)
}
