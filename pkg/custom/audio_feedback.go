package custom

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

// audioFeedback contains only authored guidance and validated response metadata.
// It does not replace the original error or determine its exit status.
type audioFeedback struct {
	Summary    string
	Action     string
	RequestID  string
	StatusCode int
}

type audioFailureKind uint8

const (
	audioEmptyTextFailure audioFailureKind = iota + 1
	audioInvalidSettingsFailure
	audioInstructionsFailure
	audioVoiceFailure
	audioIncompleteFileFailure
	audioCaptureFailure
	audioHistoryFailure
	audioMeasurementFailure
)

// Classification comes from the failure site, independently of display wording.
// Keep the local wrapper in the chain for safe presentation and original causes.
type audioClassifiedFailure struct {
	kind    audioFailureKind
	failure *localcommands.LocalError
}

func (e *audioClassifiedFailure) Error() string { return e.failure.Error() }
func (e *audioClassifiedFailure) Unwrap() error { return e.failure }

func classifiedAudioFailure(kind audioFailureKind, message string, cause error) error {
	return &audioClassifiedFailure{kind: kind, failure: &localcommands.LocalError{Message: message, Cause: cause}}
}

func audioFailureFeedback(command *cli.Command, operation string, failure error) audioFeedback {
	if failure == nil {
		return audioFeedback{}
	}
	if errors.Is(failure, context.Canceled) {
		return audioFeedback{Summary: "Audio operation canceled.", Action: "Check History before generating again."}
	}
	var apierr *openai.Error
	if errors.As(failure, &apierr) && apierr != nil {
		return audioAPIErrorFeedback(command, apierr)
	}
	stage := audioFailureStage(operation, failure)
	var classified *audioClassifiedFailure
	if errors.As(failure, &classified) && classified != nil {
		switch classified.kind {
		case audioEmptyTextFailure:
			return audioFeedback{Summary: "Text is empty.", Action: "Enter text before generating speech."}
		case audioInvalidSettingsFailure:
			return audioFeedback{Summary: "Format or speed is invalid.", Action: "Choose a listed format and speed from 0.25 to 4."}
		case audioInstructionsFailure:
			return audioFeedback{Summary: "Model cannot use Instructions.", Action: "Clear Instructions or choose gpt-4o-mini-tts."}
		case audioVoiceFailure:
			return audioFeedback{Summary: "Voice cannot use this Model.", Action: "Choose another Voice or gpt-4o-mini-tts."}
		case audioIncompleteFileFailure:
			return audioFeedback{Summary: "No complete audio file returned.", Action: "Check API usage before generating again."}
		}
	}
	var receipt *saveReceiptError
	if errors.As(failure, &receipt) {
		return audioFeedback{Summary: "Save receipt failed.", Action: "Check the destination before saving again."}
	}
	switch {
	case errors.Is(failure, audiomedia.ErrPlayerUnavailable):
		return audioFeedback{Summary: "Playback needs FFplay.", Action: "Install FFmpeg with FFplay on PATH. Retained audio remains available."}
	case errors.Is(failure, audiomedia.ErrDecoderUnavailable):
		if stage == "play" {
			return audioFeedback{Summary: "Playback needs FFmpeg.", Action: "Install FFmpeg on PATH. Retained audio remains available."}
		}
		return audioFeedback{Summary: "Measurement needs FFmpeg.", Action: "Install FFmpeg on PATH. Retained audio remains available."}
	case errors.Is(failure, audiomedia.ErrPlaybackFailed):
		return audioFeedback{Summary: "Playback failed.", Action: "Check FFplay and audio output. Retained audio remains available."}
	case errors.Is(failure, audiomedia.ErrInvalidAudio):
		return audioFeedback{Summary: "Audio could not be decoded.", Action: "The audio is malformed or incomplete. Export the retained file for inspection."}
	case errors.Is(failure, audiomedia.ErrUnsupportedFormat):
		return audioFeedback{Summary: "Audio format is unsupported.", Action: "Export the retained file for inspection."}
	case errors.Is(failure, audiomedia.ErrAnalysisFailed):
		return audioFeedback{Summary: "Audio measurement failed.", Action: "Check FFmpeg. Export the retained file for inspection."}
	case errors.Is(failure, audiohistory.ErrInvalid):
		return audioFeedback{Summary: "History could not be verified.", Action: "Check private cache permissions and retained records."}
	case errors.Is(failure, audiohistory.ErrChanged):
		return audioFeedback{Summary: "History changed during this action.", Action: "Reopen History and select the result again."}
	}
	feedback := audioStageFeedback(stage)
	if errors.Is(failure, os.ErrPermission) {
		feedback.Summary = audioStageLabel(stage) + ": access denied."
		if stage == "export" {
			feedback.Action = "Choose a writable destination. Retained audio remains available."
		} else {
			feedback.Action = "Check local file permissions before repeating this action."
		}
		return feedback
	}
	if errors.Is(failure, syscall.ENOSPC) {
		feedback.Summary = audioStageLabel(stage) + ": storage is full."
		feedback.Action = "Free local storage before repeating this action."
		return feedback
	}
	if errors.Is(failure, os.ErrNotExist) {
		feedback.Summary = audioStageLabel(stage) + ": file unavailable."
		if stage == "export" {
			feedback.Action = "Choose an existing destination folder. Retained audio remains available."
		} else if stage == "play" {
			feedback.Action = "Reopen History and select an available result."
		}
		return feedback
	}
	var network net.Error
	if errors.Is(failure, context.DeadlineExceeded) || errors.As(failure, &network) && network.Timeout() {
		feedback.Summary = audioStageLabel(stage) + ": timed out."
		return feedback
	}
	var transport *url.Error
	var connection *net.OpError
	if stage == "generate" && (errors.As(failure, &transport) || errors.As(failure, &connection)) {
		return audioFeedback{Summary: "Speech connection failed.", Action: "Check the connection and API usage before generating again."}
	}
	if (stage == "generate" || stage == "capture") && errors.Is(failure, io.ErrUnexpectedEOF) {
		return audioFeedback{Summary: "Speech response was incomplete.", Action: "Check API usage before generating again."}
	}
	return feedback
}

func audioAPIErrorFeedback(command *cli.Command, apierr *openai.Error) audioFeedback {
	feedback := audioFeedback{Summary: "The API could not complete the request.", Action: "The cause is unavailable. Check settings and API status before generating again.", RequestID: readableAPIRequestID(apierr)}
	switch apierr.StatusCode {
	case http.StatusUnauthorized:
		feedback.Summary = "The API could not verify your access."
		if apierr.Code == "invalid_api_key" {
			feedback.Summary = "The API did not accept this key."
		}
		feedback.Action = "Check your API key and project access."
	case http.StatusForbidden:
		feedback.Summary = "The API denied access to this request."
		feedback.Action = "Check your API key's permissions and project access."
	case http.StatusNotFound:
		feedback.Summary = "The model or service was not found."
		feedback.Action = "Check Model, project access, and the configured API endpoint."
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		feedback.Summary = "The API could not use these settings."
		feedback.Action = "Review Text, Instructions, and Settings."
		_, path := readableParameterFlag(command, apierr.Param)
		label := map[string]string{"input": "Text", "instructions": "Instructions", "model": "Model", "voice": "Voice", "response_format": "Format", "speed": "Speed"}[path]
		if label != "" {
			feedback.Action = "Review " + label + " and its supported values."
		}
		switch apierr.Code {
		case "missing_required_parameter", "missing_required_argument":
			if label != "" {
				feedback.Action = "Provide " + label + " before generating."
			}
		case "unknown_parameter", "unsupported_parameter":
			feedback.Action = "Choose a Model that supports these settings."
		case "context_length_exceeded", "string_above_max_length", "array_above_max_length", "too_many_items":
			feedback.Action = "Shorten Text or Instructions to fit the Model's limits."
		case "content_policy_violation":
			feedback.Summary = "The API declined this content."
			feedback.Action = "Review Text and Instructions before generating again."
		}
	case http.StatusTooManyRequests:
		feedback.Summary = "The API is limiting requests right now."
		feedback.Action = "Wait before generating again."
		switch apierr.Code {
		case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded":
			feedback.Summary = "An API usage or billing limit was reached."
			feedback.Action = "Check project billing and usage limits."
		}
	case http.StatusConflict:
		feedback.Summary = "The request conflicts with the current service state."
		feedback.Action = "Check API status before generating again."
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		feedback.Summary = "The speech request took too long."
		feedback.Action = "The API may have received the request. Check usage before generating again."
	default:
		if apierr.StatusCode >= 500 && apierr.StatusCode <= 599 {
			feedback.Summary = "The speech service had a problem."
			feedback.Action = "Check API status and usage before generating again."
		}
	}
	if (apierr.StatusCode == 400 || apierr.StatusCode == 404 || apierr.StatusCode == 422) && apierr.Code == "model_not_found" {
		feedback.Summary = "This model is unavailable to your project."
		feedback.Action = "Check Model and project access."
	}
	if apierr.StatusCode >= 100 && apierr.StatusCode <= 599 {
		feedback.StatusCode = apierr.StatusCode
	}
	return feedback
}

// Typed local failures establish the failed stage after generation succeeds.
// Unknown wrappers and operation strings never become display text.
func audioFailureStage(operation string, failure error) string {
	var classified *audioClassifiedFailure
	if errors.As(failure, &classified) && classified != nil {
		switch classified.kind {
		case audioCaptureFailure:
			return "capture"
		case audioHistoryFailure:
			return "history"
		case audioMeasurementFailure:
			return "measure"
		}
	}
	switch operation {
	case "generate", "export", "history":
		return operation
	case "open":
		return "history"
	case "play":
		return "play"
	}
	return "audio"
}

func audioStageLabel(stage string) string {
	switch stage {
	case "generate":
		return "Speech request"
	case "capture":
		return "Audio retention"
	case "export":
		return "Save"
	case "history":
		return "History"
	case "measure":
		return "Measurement"
	case "play":
		return "Playback"
	default:
		return "Audio operation"
	}
}

func audioStageFeedback(stage string) audioFeedback {
	feedback := audioFeedback{Summary: audioStageLabel(stage) + " failed.", Action: "The cause is unavailable. Review this action before repeating it."}
	switch stage {
	case "generate", "capture":
		feedback.Action = "Check History and API usage before generating again."
	case "export":
		feedback.Action = "Check the destination before saving again. Retained audio remains available."
	case "history":
		feedback.Action = "Check retained records and private cache permissions."
	case "measure":
		feedback.Action = "Retained audio remains available. Export the file for inspection."
	case "play":
		feedback.Action = "Check History, the player, and audio output."
	}
	return feedback
}
