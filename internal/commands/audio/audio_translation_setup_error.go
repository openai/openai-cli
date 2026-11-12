package audio

import (
	"net/http"
	"strings"

	"github.com/openai/openai-cli/internal/audiolive"
)

type translationServiceError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Message string `json:"message"`
}

func translationEventFailure(eventErr translationServiceError, configuring bool) error {
	switch eventErr.Code {
	case "invalid_api_key", "permission_denied":
		return translationFailure(0, eventErr.Code, nil)
	case "ip_not_authorized":
		return translationFailure(http.StatusUnauthorized, eventErr.Code, nil)
	case "insufficient_quota", "billing_hard_limit_reached":
		return audiolive.Failure("The project reached an API usage or billing limit. Check project billing and quota.")
	}
	switch {
	case eventErr.Type == "authentication_error":
		return translationFailure(http.StatusUnauthorized, "", nil)
	case eventErr.Type == "permission_error":
		return translationFailure(http.StatusForbidden, "", nil)
	case eventErr.Code == "server_error" || eventErr.Type == "server_error":
		return translationFailure(http.StatusInternalServerError, "", nil)
	case eventErr.Code == "rate_limit_exceeded" || eventErr.Type == "rate_limit_error":
		return audiolive.Failure("The API rate limit blocked translation. Wait before retrying.")
	}
	if configuring {
		switch strings.TrimPrefix(eventErr.Param, "session.") {
		case "audio.output.language":
			return audiolive.Failure("The API rejected the target language. Choose another language under Translate into.")
		case "audio.input.transcription", "audio.input.transcription.model":
			return audiolive.Failure("The API rejected the transcription model. Check the selected transcription model and project access.")
		case "audio.input.noise_reduction", "audio.input.noise_reduction.type":
			return audiolive.Failure("The API rejected noise reduction. Check the noise reduction setting.")
		}
	}
	if eventErr.Code == "model_not_found" || eventErr.Param == "model" {
		return audiolive.Failure("The selected model is unavailable. Check the model name and project access.")
	}
	// The public endpoint can reject authentication after upgrading the socket,
	// with only an invalid_request_error and a message. Never display the message.
	if !configuring && eventErr.Type == "invalid_request_error" && eventErr.Code == "" && eventErr.Param == "" {
		message := strings.ToLower(strings.TrimSpace(eventErr.Message))
		if strings.HasPrefix(message, "you didn't provide an api key.") ||
			strings.HasPrefix(message, "missing bearer or basic authentication in header") ||
			strings.HasPrefix(message, "authentication failed.") {
			return translationFailure(http.StatusUnauthorized, "", nil)
		}
		// No confirmed service cause. The application can still identify a missing local credential.
		return &translationAccessError{Failure: translationCreationRejected}
	}
	if configuring {
		return audiolive.Failure("The API rejected the translation settings. Review the session settings before retrying.")
	}
	return translationCreationRejected
}

const translationCreationRejected = audiolive.Failure("The API rejected the session before it received your settings. Check project access to live translation.")
