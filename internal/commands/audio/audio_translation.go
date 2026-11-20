package audio

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/audiolive"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/modeldefaults"
	clioutput "github.com/openai/openai-cli/internal/output"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/openai/openai-go/v3/responses"
	"github.com/urfave/cli/v3"
)

// TranslationOptions supplies the application's existing credential and transport policy.
type TranslationOptions struct {
	DefaultRequestOptions  func(*cli.Command) []option.RequestOption
	RequestOptions         func(*cli.Command) ([]option.RequestOption, error)
	AuthenticationGuidance func(*cli.Command, *openai.Error) AuthenticationGuidance
}

// ConfigureLiveTranslation registers the interactive command and preserves the released browser flag.
func ConfigureLiveTranslation(root *cli.Command, policy TranslationOptions) {
	group := root.Command("audio")
	if group == nil || policy.DefaultRequestOptions == nil || policy.RequestOptions == nil {
		return
	}
	group.Commands = append(group.Commands, &cli.Command{
		Name:            "translate-live",
		Usage:           "Translate speech or text interactively.",
		UsageText:       "openai audio translate-live [OPTIONS]",
		Suggest:         true,
		HideHelpCommand: true,
		Metadata: map[string]any{
			"help-content": clihelp.Content{
				CompactNote: "Choose microphone, audio file, or typed text. Press Enter to start. Add --browser to share tab audio.",
				Description: "Choose Microphone, Audio file, or Typed text in your terminal. Select a target language.\n" +
					"Press Enter to start microphone capture, then Space to stop.\n" +
					"Audio input uses FFmpeg. Playback uses FFplay. Use --no-playback for captions alone.\n" +
					"On Windows, provide the DirectShow microphone name with --input-device. Linux uses PulseAudio.\n" +
					"Use --browser to share audio from a browser tab.",
				Examples: []clihelp.Example{
					{Description: "Choose an input in the terminal:", Command: "audio translate-live"},
					{Description: "Translate a recording into French in the terminal:", Command: "audio translate-live --file recording.wav --target-language fr"},
					{Description: "Share audio from a browser tab:", Command: "audio translate-live --browser"},
				},
			},
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "browser", Usage: "Open the browser panel for microphone or tab audio"},
			&cli.StringFlag{Name: "file", TakesFile: true, Usage: "Select a local audio file in the terminal"},
			&cli.StringFlag{Name: "target-language", Value: "en", Usage: "Initial terminal translation language (e.g. fr, es, ja)"},
			&cli.StringFlag{Name: "input-device", Usage: "Microphone name or macOS audio device index"},
			&cli.BoolFlag{Name: "no-playback", Usage: "Show terminal captions with audio playback off"},
			&cli.BoolFlag{Name: "noise-reduction", Usage: "Enable near-field noise reduction for terminal audio input"},
			&cli.StringFlag{Name: "output-dir", Value: audioDefaultSaveFolder, TakesFile: true, Usage: "Parent folder for saved terminal translations"},
		},
		Action: func(ctx context.Context, command *cli.Command) error {
			return runInteractiveTranslation(ctx, command, policy, command.Bool("browser"))
		},
	})

	command := group.Command("translate")
	if command == nil {
		return
	}
	command.Usage = "Translate a saved file into English."
	command.UsageText = "openai audio translate --file AUDIO --model whisper-1 [OPTIONS]"
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-content"] = clihelp.Content{
		CompactNote: "Supply --file and --model through flags or piped JSON/YAML. Use audio translate-live for interactive translation.",
		InputNote:   "File translation requires file and model inputs through flags or piped JSON/YAML.",
		Description: "Translate a saved recording into English. Use audio translate-live for interactive translation. The --live flag opens the browser panel.",
		Examples: []clihelp.Example{
			{Description: "Translate a saved recording into English:", Command: "audio translate --file recording.wav --model whisper-1"},
		},
	}
	next := command.Action
	command.Flags = append(command.Flags,
		&cli.BoolFlag{Name: "live", Usage: "Open the browser panel for microphone or tab audio"},
		&cli.BoolFlag{Name: "browser", Hidden: true}, // Accepted by the earlier terminal preview with --live.
	)
	command.Action = func(ctx context.Context, command *cli.Command) error {
		if !command.Bool("live") {
			if command.Bool("browser") {
				return audioFailure("Use audio translate-live --browser to open the browser panel.", nil)
			}
			return next(ctx, command)
		}
		for _, name := range []string{"model", "prompt", "response-format", "temperature"} {
			if command.IsSet(name) {
				return audioFailure("Use --file and --model for file translation into English. Omit --live.", nil)
			}
		}
		return runInteractiveTranslation(ctx, command, policy, true)
	}
}

func runInteractiveTranslation(ctx context.Context, command *cli.Command, policy TranslationOptions, browser bool) error {
	if command.Args().Present() {
		return audioFailure("Live translation does not accept positional arguments. Choose an input in the panel.", nil)
	}
	if browser {
		for _, name := range []string{"file", "target-language", "input-device", "no-playback", "noise-reduction", "output-dir"} {
			if command.IsSet(name) {
				return audioFailure("--"+name+" requires terminal translation. Use audio translate-live without --browser.", nil)
			}
		}
	}
	if command.IsSet("file") && command.IsSet("input-device") {
		return audioFailure("Choose one audio input with --file or --input-device.", nil)
	}
	for _, name := range []string{"format", "format-error", "raw-output", "quiet", "transform", "transform-error"} {
		if command.Root().IsSet(name) {
			return audioFailure("Live translation is interactive. Omit output-format overrides, or use file translation for command output.", nil)
		}
	}
	var output *os.File
	if !browser {
		if audiolive.LanguageName(command.String("target-language")) == "" {
			return audioFailure("Choose a supported target language in the terminal, or supply a language code such as fr.", nil)
		}
		if err := audioSaveFolderError(command.String("output-dir")); err != nil {
			return audioFailure(err.Error(), nil)
		}
		output, _ = command.Root().Writer.(*os.File)
		if output == nil || !term.IsTerminal(output.Fd()) || !term.IsTerminal(os.Stdin.Fd()) || strings.EqualFold(os.Getenv("TERM"), "dumb") {
			return audioFailure("Live translation needs an interactive terminal. Run audio translate-live with terminal input and output.", nil)
		}
	}
	options, err := policy.RequestOptions(command)
	if err != nil {
		return err
	}
	// Both SDK endpoints select bearer credentials. Preserve the explicit header precedence.
	options = append(policy.DefaultRequestOptions(command), options...)
	ctx, stopSignals := clioutput.DownloadSignalContext(ctx)
	defer stopSignals()
	connect := func(ctx context.Context, config audiolive.Config) (audiolive.Session, error) {
		session, err := connectAudioTranslation(ctx, config, options)
		return session, policy.explainAccess(command, err, browser)
	}
	if browser {
		err = audiolive.Run(ctx, audiolive.Options{
			Connect: connect, OpenBrowser: cliterminal.OpenBrowser,
			Writer: command.Root().Writer,
		})
	} else {
		translate := func(ctx context.Context, text, language, model string) (translationTextResult, error) {
			result, err := translateTypedText(ctx, text, language, model, options)
			return result, policy.explainAccess(command, err, false)
		}
		err = runTerminalTranslationUI(ctx, command, connect, translate, os.Stdin, output)
	}
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

type apiTranslationSession struct{ session *realtime.TranslationSession }

type translationTextResult struct {
	Text                      string
	InputTokens, OutputTokens *int64
}

func translateTypedText(ctx context.Context, text, languageName, model string, options []option.RequestOption) (translationTextResult, error) {
	if model == "" {
		model = modeldefaults.TranslationText
	}
	client := openai.NewClient(options...)
	result, err := client.Responses.New(ctx, responses.ResponseNewParams{
		Model:        model,
		Instructions: openai.String("Translate the user's text into " + languageName + ". Preserve meaning, formatting, and tone. Return only the translation."),
		Input:        responses.ResponseNewParamsInputUnion{OfString: openai.String(text)},
		Store:        openai.Bool(false),
	})
	if err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			return translationTextResult{}, translationFailure(apiErr.StatusCode, apiErr.Code, err)
		}
		return translationTextResult{}, audiolive.Failure("Text translation failed. Check the connection and try again.")
	}
	output := result.OutputText()
	if result.Status != responses.ResponseStatusCompleted || result.Error.Code != "" || strings.TrimSpace(output) == "" {
		return translationTextResult{}, audiolive.Failure("The service returned no complete translation. Check the text and model, then try again.")
	}
	translated := translationTextResult{Text: output}
	if result.Usage.JSON.InputTokens.Valid() {
		translated.InputTokens = &result.Usage.InputTokens
	}
	if result.Usage.JSON.OutputTokens.Valid() {
		translated.OutputTokens = &result.Usage.OutputTokens
	}
	return translated, nil
}

func connectAudioTranslation(ctx context.Context, config audiolive.Config, options []option.RequestOption) (audiolive.Session, error) {
	if config.Model == "" {
		config.Model = modeldefaults.Translation
	}
	if config.TranscriptionModel == "" {
		config.TranscriptionModel = modeldefaults.TranslationTranscript
	}
	client := openai.NewClient(options...)
	var response *http.Response
	session, err := client.Realtime.ConnectTranslation(ctx, config.Model, realtime.TranslationConnectionOptions{}, option.WithResponseInto(&response))
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
		"transcription":   map[string]string{"model": config.TranscriptionModel},
		"noise_reduction": nil,
	}
	if config.NoiseReduction {
		input["noise_reduction"] = map[string]string{"type": "near_field"}
	}
	data, _ := json.Marshal(map[string]any{"type": "session.update", "session": map[string]any{"audio": map[string]any{"input": input, "output": map[string]any{"language": config.Language}}}})
	updateSent := false
	for {
		raw, err := session.RecvRaw(ctx)
		if err != nil {
			return nil, translationSetupInterrupted(ctx)
		}
		var event translationEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, audiolive.Failure("The service returned an unreadable translation response. Try again.")
		}
		switch event.Type {
		case "error":
			return nil, translationEventFailure(event.Error, updateSent)
		case "session.created":
			if !updateSent {
				if err := session.SendRaw(ctx, data); err != nil {
					return nil, translationSetupInterrupted(ctx)
				}
				updateSent = true
			}
		case "session.updated":
			if !updateSent {
				return nil, audiolive.Failure("The API acknowledged settings before creating a translation session. Check the API address.")
			}
			keep = true
			return &apiTranslationSession{session: session}, nil
		case "session.closed":
			return nil, translationSetupInterrupted(ctx)
		}
	}
}

func translationSetupInterrupted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return audiolive.Failure("The API connection opened, but session setup did not finish. Retry the session.")
}

type translationEvent struct {
	ItemID       string                  `json:"item_id"`
	ContentIndex int                     `json:"content_index"`
	Transcript   *string                 `json:"transcript"`
	Type         string                  `json:"type"`
	Delta        string                  `json:"delta"`
	ElapsedMS    *float64                `json:"elapsed_ms"`
	Format       string                  `json:"format"`
	SampleRate   int                     `json:"sample_rate"`
	Channels     int                     `json:"channels"`
	Error        translationServiceError `json:"error"`
	Usage        struct {
		TotalTokens  int64  `json:"total_tokens"`
		InputTokens  *int64 `json:"input_tokens"`
		OutputTokens *int64 `json:"output_tokens"`
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
		update := audiolive.Event{
			Type: event.Type, Tokens: event.Usage.TotalTokens,
			InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens,
		}
		switch event.Type {
		case "session.input_transcript.delta", "conversation.item.input_audio_transcription.delta",
			"input_audio_buffer.transcription.delta", "input_audio_transcription.delta":
			update.Type, update.Delta = "session.input_transcript.delta", event.Delta
			update.ItemID, update.ContentIndex, update.ElapsedMS = event.ItemID, event.ContentIndex, event.ElapsedMS
			return update, nil
		case "session.input_transcript.completed", "conversation.item.input_audio_transcription.completed",
			"conversation.item.input_audio_transcription.done", "input_audio_buffer.transcription.completed",
			"input_audio_buffer.transcription.done", "input_audio_transcription.completed", "input_audio_transcription.done":
			if event.Transcript == nil {
				continue
			}
			update.Type, update.Transcript = "session.input_transcript.completed", event.Transcript
			update.ItemID, update.ContentIndex, update.ElapsedMS = event.ItemID, event.ContentIndex, event.ElapsedMS
			return update, nil
		case "session.output_transcript.delta", "session.output_audio.delta", "session.closed":
			update.Delta, update.ElapsedMS = event.Delta, event.ElapsedMS
			update.Format, update.SampleRate, update.Channels = event.Format, event.SampleRate, event.Channels
			return update, nil
		case "error":
			return audiolive.Event{Type: "error", Message: translationFailure(0, event.Error.Code, nil).Error()}, nil
		default:
			if update.Tokens > 0 || update.InputTokens != nil || update.OutputTokens != nil {
				update.Type = "usage"
				return update, nil
			}
		}
	}
}

// Keep only the authored message and access classification. Peer diagnostics can contain private data.
type translationAccessError struct {
	audiolive.Failure
	status int // Zero if the service rejected session creation without specifying a cause.
	code   string
}

func (e *translationAccessError) Unwrap() error { return e.Failure }

func (policy TranslationOptions) explainAccess(command *cli.Command, err error, browser bool) error {
	var access *translationAccessError
	if browser || policy.AuthenticationGuidance == nil || !errors.As(err, &access) {
		return err
	}
	help := policy.AuthenticationGuidance(command, &openai.Error{StatusCode: access.status, Code: access.code})
	if help.Action == "" {
		return err
	}
	if help.Summary == "" {
		if access.status == http.StatusForbidden {
			help.Summary = "The API denied access to translation."
		} else {
			help.Summary = "The API could not verify your credentials."
		}
	}
	failure := audiolive.Failure(help.Summary + "\n" + help.Action)
	if help.PanelAction != "" {
		return translationSetupHelp{Failure: failure, summary: help.Summary, action: help.PanelAction}
	}
	return failure
}

func translationFailure(status int, code string, err error) error {
	message := "Could not open a translation connection. Check the API endpoint and its WebSocket support."
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var network net.Error
	switch {
	case code == "invalid_api_key":
		status = http.StatusUnauthorized
		message = "Your API key was not accepted. Run openai help setup and check the selected project."
	case status == 401:
		message = "The API could not verify your access. Run openai help setup and check your key, project, and organization."
	case status == 400:
		message = "The API rejected the live translation settings. Check the API address and update the CLI."
	case status == 403 || code == "permission_denied":
		status = http.StatusForbidden
		message = "Your project cannot start live translation. Check model access and API key permissions."
	case status == 429 || code == "rate_limit_exceeded" || code == "insufficient_quota":
		message = "An API usage limit blocked translation. Check project quota and retry later."
	case status == 404 || code == "model_not_found":
		message = "Live translation is unavailable at this API address. Check the API address and model access."
	case status >= 500:
		message = "The translation service could not start. Try again later."
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout():
		message = "The translation connection timed out. Check your network and try again."
	case errors.As(err, &dns):
		message = "The network could not resolve the host. Check DNS and the configured API or proxy address."
	case errors.As(err, &cert):
		message = "The TLS certificate could not be verified. Check the endpoint certificate and your trusted certificates."
	case errors.Is(err, syscall.ECONNREFUSED):
		message = "The connection was refused. Check that the configured API endpoint or proxy accepts connections."
	case status == http.StatusProxyAuthRequired:
		message = "The proxy requires authentication. Check your proxy credentials."
	case status >= 100 && status < 400 || status == http.StatusUpgradeRequired:
		message = "The endpoint did not complete the WebSocket upgrade. Check that your API endpoint and proxy support WebSockets."
	case code != "":
		message = "The service could not translate this audio. Stop the session and try again."
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &translationAccessError{Failure: audiolive.Failure(message), status: status, code: code}
	}
	return audiolive.Failure(message)
}
