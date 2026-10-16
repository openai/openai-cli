package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/openai/openai-cli/internal/audiohistory"
	"github.com/openai/openai-cli/internal/audiomedia"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestAudioFeedbackAPIStatusAndPrivacy(t *testing.T) {
	const private = "synthetic-private-prompt\x1b]52;c;c2VjcmV0\a\u202e"
	for _, tc := range []struct {
		status                int
		code, summary, action string
	}{
		{401, "", "The API could not verify your access.", "Check your API key and project access."},
		{401, "invalid_api_key", "The API did not accept this key.", "Check your API key and project access."},
		{403, "", "The API denied access to this request.", "Check your API key's permissions and project access."},
		{404, "", "The model or service was not found.", "Check Model, project access, and the configured API endpoint."},
		{404, "model_not_found", "This model is unavailable to your project.", "Check Model and project access."},
		{400, "model_not_found", "This model is unavailable to your project.", "Check Model and project access."},
		{400, "", "The API could not use these settings.", "Review Text, Instructions, and Settings."},
		{422, "", "The API could not use these settings.", "Review Text, Instructions, and Settings."},
		{429, "", "The API is limiting requests right now.", "Wait before generating again."},
		{429, "insufficient_quota", "An API usage or billing limit was reached.", "Check project billing and usage limits."},
		{409, "", "The request conflicts with the current service state.", "Check API status before generating again."},
		{408, "", "The speech request took too long.", "The API may have received the request. Check usage before generating again."},
		{504, "", "The speech request took too long.", "The API may have received the request. Check usage before generating again."},
		{503, "", "The speech service had a problem.", "Check API status and usage before generating again."},
		{499, "", "The API could not complete the request.", "The cause is unavailable. Check settings and API status before generating again."},
		{0, "", "The API could not complete the request.", "The cause is unavailable. Check settings and API status before generating again."},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.status, tc.code), func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, "https://synthetic-user:synthetic-password@synthetic.invalid/?token=synthetic-private", nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer synthetic-private-key")
			request.Header.Set("Cookie", private)
			apierr := &openai.Error{StatusCode: tc.status, Code: tc.code, Message: private, Param: private, Request: request,
				Response: &http.Response{Header: http.Header{"X-Request-Id": {"req_synthetic"}, "Set-Cookie": {private}, "Location": {private}}}}
			for _, failure := range []error{apierr, fmt.Errorf("%s: %w", private, apierr), errors.Join(errors.New(private), apierr)} {
				feedback := audioFailureFeedback(nil, "generate", failure)
				require.Equal(t, audioFeedback{Summary: tc.summary, Action: tc.action, RequestID: "req_synthetic", StatusCode: tc.status}, feedback)
				require.NotContains(t, feedback.Summary+feedback.Action, "HTTP")
				require.ErrorIs(t, failure, apierr)
				require.Equal(t, private, apierr.Message)
				require.Equal(t, tc.status, apierr.StatusCode)
			}
		})
	}
}

func TestAudioFeedbackUsesSafeParameterLabels(t *testing.T) {
	command := audioGenerateTestCommand()
	for param, label := range map[string]string{"input": "Text", "instructions": "Instructions", "model": "Model", "voice": "Voice", "response_format": "Format", "speed": "Speed"} {
		feedback := audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 400, Code: "invalid_value", Param: param})
		require.Equal(t, "Review "+label+" and its supported values.", feedback.Action)
		feedback = audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 400, Code: "missing_required_parameter", Param: param})
		require.Equal(t, "Provide "+label+" before generating.", feedback.Action)
	}
	for _, param := range []string{"synthetic-private", "input\nprivate", "input\x1b[2J", "output", "future_field", "https://synthetic.invalid/"} {
		feedback := audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 400, Code: "synthetic-private-code", Param: param})
		require.Equal(t, "Review Text, Instructions, and Settings.", feedback.Action)
	}
	for code, action := range map[string]string{
		"unsupported_parameter":    "Choose a Model that supports these settings.",
		"context_length_exceeded":  "Shorten Text or Instructions to fit the Model's limits.",
		"content_policy_violation": "Review Text and Instructions before generating again.",
	} {
		feedback := audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 400, Code: code})
		require.Equal(t, action, feedback.Action)
	}
	// Matching a code at an unrelated status must not invent a model failure.
	require.Equal(t, "The API could not verify your access.", audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 401, Code: "model_not_found"}).Summary)
}

func TestAudioFeedbackRequestIDStaysSeparateFromGuidance(t *testing.T) {
	command := audioGenerateTestCommand()
	command.Metadata = map[string]any{"help-invocation": "'/tmp/CLI trial/openai'"}
	apierr := requestIDError(t, 401, "req_synthetic")
	feedback := audioFailureFeedback(command, "generate", apierr)
	require.Equal(t, "Check your API key and project access.", feedback.Action)
	require.NotContains(t, feedback.Summary+feedback.Action, "req_synthetic")
	require.NotContains(t, feedback.Summary+feedback.Action, "/tmp/CLI trial")
	require.Equal(t, 401, feedback.StatusCode)
	require.Equal(t, "req_synthetic", feedback.RequestID)
	for _, id := range []string{"req_bad\rforged", "https://synthetic.invalid/?key=private", strings.Repeat("a", 257), ""} {
		apierr.Response.Header.Set("X-Request-ID", id)
		require.Empty(t, audioFailureFeedback(command, "generate", apierr).RequestID)
	}
	apierr.Response.Header.Set("X-Request-ID", "req_synthetic")
	apierr.Request = &http.Request{Header: http.Header{"x-request-id": {"synthetic-private"}}}
	require.Empty(t, audioFailureFeedback(command, "generate", apierr).RequestID)
}

func TestAudioFeedbackInvalidStatusRemainsUnknown(t *testing.T) {
	for _, status := range []int{-1, 0, 99, 600, 1000} {
		apierr := &openai.Error{StatusCode: status, Code: "synthetic-private", Message: "synthetic-private"}
		feedback := audioFailureFeedback(nil, "generate", apierr)
		require.Zero(t, feedback.StatusCode)
		require.Equal(t, "The API could not complete the request.", feedback.Summary)
		require.Equal(t, "The cause is unavailable. Check settings and API status before generating again.", feedback.Action)
		require.Equal(t, status, apierr.StatusCode, "presentation changed the original error")
	}
}

func TestAudioFeedbackPreservesRetainedOperationProvenance(t *testing.T) {
	private := errors.New("synthetic-private-path-and-prompt")
	for _, tc := range []struct {
		name, operation string
		failure         error
		summary, action string
	}{
		{"measurement", "generate", classifiedAudioFailure(audioMeasurementFailure, "Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", private), "Measurement failed.", "Retained audio remains available."},
		{"decoder", "generate", classifiedAudioFailure(audioMeasurementFailure, "Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", audiomedia.ErrDecoderUnavailable), "Measurement needs FFmpeg.", "Retained audio remains available."},
		{"invalid audio", "generate", errors.Join(audiomedia.ErrInvalidAudio, private), "Audio could not be decoded.", "Export the retained file"},
		{"unsupported format", "open", errors.Join(audiomedia.ErrUnsupportedFormat, private), "Audio format is unsupported.", "Export the retained file"},
		{"analysis", "open", errors.Join(audiomedia.ErrAnalysisFailed, private), "Audio measurement failed.", "Export the retained file"},
		{"player unavailable", "play", &audiomedia.UnavailableError{Tool: "ffplay"}, "Playback needs FFplay.", "Retained audio remains available."},
		{"playback decoder unavailable", "play", audioFailure("Could not play the retained audio.", &audiomedia.UnavailableError{Tool: "ffmpeg"}), "Playback needs FFmpeg.", "Retained audio remains available."},
		{"player failed", "play", errors.Join(audiomedia.ErrPlaybackFailed, private), "Playback failed.", "Retained audio remains available."},
		{"history verification", "open", errors.Join(audiohistory.ErrInvalid, private), "History could not be verified.", "private cache permissions"},
		{"history replacement", "export", errors.Join(audiohistory.ErrChanged, private), "History changed during this action.", "select the result again"},
		{"history read", "history", private, "History failed.", "retained records"},
		{"cached audition", "generate", classifiedAudioFailure(audioHistoryFailure, "Could not inspect cached auditions. No speech request was sent.", private), "History failed.", "retained records"},
		{"open for saving", "export", classifiedAudioFailure(audioHistoryFailure, "Could not open retained audio for saving.", private), "History failed.", "retained records"},
		{"export", "export", &downloadSaveError{message: "synthetic-private", cause: private}, "Save failed.", "Retained audio remains available."},
		{"receipt", "export", &saveReceiptError{cause: private}, "Save receipt failed.", "Check the destination before saving again."},
		{"retention", "generate", classifiedAudioFailure(audioCaptureFailure, "Could not retain completed audio in private history.", private), "Audio retention failed.", "Check History and API usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var local *localUtilityError
			if errors.As(tc.failure, &local) {
				local.message = "Updated local audio guidance."
			}
			feedback := audioFailureFeedback(nil, tc.operation, fmt.Errorf("outer context: %w", tc.failure))
			require.Equal(t, tc.summary, feedback.Summary)
			require.Contains(t, feedback.Action, tc.action)
			require.NotContains(t, feedback.Summary+feedback.Action, "synthetic-private")
			require.NotContains(t, feedback.Summary, "Speech request")
			require.Empty(t, feedback.RequestID)
			require.Zero(t, feedback.StatusCode)
		})
	}
}

func TestAudioFeedbackLocalFailuresHidePaths(t *testing.T) {
	for _, tc := range []struct {
		operation string
		cause     error
		summary   string
	}{
		{"export", os.ErrPermission, "Save: access denied."},
		{"history", os.ErrPermission, "History: access denied."},
		{"export", syscall.ENOSPC, "Save: storage is full."},
		{"history", os.ErrNotExist, "History: file unavailable."},
		{"export", os.ErrNotExist, "Save: file unavailable."},
		{"play", context.DeadlineExceeded, "Playback: timed out."},
		{"generate", context.DeadlineExceeded, "Speech request: timed out."},
	} {
		failure := &os.PathError{Op: "synthetic-private", Path: "/synthetic-private/prompt.wav", Err: tc.cause}
		feedback := audioFailureFeedback(nil, tc.operation, failure)
		require.Equal(t, tc.summary, feedback.Summary)
		require.NotContains(t, feedback.Action, "synthetic-private")
		require.ErrorIs(t, failure, tc.cause)
	}
	for _, failure := range []error{
		&url.Error{Op: "Post", URL: "https://synthetic-user:synthetic-password@synthetic.invalid/?token=private", Err: errors.New("synthetic-private")},
		&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic-private")},
	} {
		feedback := audioFailureFeedback(nil, "generate", failure)
		require.Equal(t, "Speech connection failed.", feedback.Summary)
		require.Equal(t, "Check the connection and API usage before generating again.", feedback.Action)
	}
	feedback := audioFailureFeedback(nil, "generate", classifiedAudioFailure(audioCaptureFailure, "Could not retain completed audio in private history.", io.ErrUnexpectedEOF))
	require.Equal(t, "Speech response was incomplete.", feedback.Summary)
	require.Equal(t, "Check API usage before generating again.", feedback.Action)
}

func TestAudioFeedbackPlaybackDoesNotInventRetention(t *testing.T) {
	failure := audioFailure("Could not play the retained audio.", &os.PathError{Op: "open", Path: "/synthetic-private/missing.wav", Err: os.ErrNotExist})
	feedback := audioFailureFeedback(nil, "play", failure)
	require.Equal(t, "Playback: file unavailable.", feedback.Summary)
	require.Equal(t, "Reopen History and select an available result.", feedback.Action)
	require.ErrorIs(t, failure, os.ErrNotExist)
	feedback = audioFailureFeedback(nil, "play", audioFailure("Could not play the retained audio.", errors.New("synthetic-private")))
	require.Equal(t, "Playback failed.", feedback.Summary)
	require.Equal(t, "Check History, the player, and audio output.", feedback.Action)
}

func TestAudioFeedbackAuthDoesNotInferCredentialCause(t *testing.T) {
	for _, tc := range []struct {
		status        int
		code, summary string
	}{
		{401, "invalid_api_key", "The API did not accept this key."},
		{401, "", "The API could not verify your access."},
		{401, "synthetic-private-unknown", "The API could not verify your access."},
		{401, "INVALID_API_KEY", "The API could not verify your access."},
		{401, "invalid_api_key ", "The API could not verify your access."},
		{401, "invalid_api_key\nsynthetic-private", "The API could not verify your access."},
		{403, "invalid_api_key", "The API denied access to this request."},
		{429, "invalid_api_key", "The API is limiting requests right now."},
		{400, "invalid_api_key", "The API could not use these settings."},
		{503, "invalid_api_key", "The speech service had a problem."},
	} {
		t.Run(fmt.Sprintf("%d/%q", tc.status, tc.code), func(t *testing.T) {
			apierr := &openai.Error{StatusCode: tc.status, Code: tc.code, Message: "The synthetic-private key is expired\x1b[2J"}
			failure := errors.Join(errors.New("synthetic-private wrapper"), apierr)
			feedback := audioFailureFeedback(nil, "generate", failure)
			require.Equal(t, tc.summary, feedback.Summary)
			require.Equal(t, tc.status, feedback.StatusCode)
			if tc.status == 401 {
				require.Equal(t, "Check your API key and project access.", feedback.Action)
			}
			for _, unsupported := range []string{"expired", "invalid", "wrong", "rejected", "environment", "OPENAI_API_KEY", "synthetic-private", "\x1b"} {
				require.NotContains(t, feedback.Summary+feedback.Action, unsupported)
			}
			require.ErrorIs(t, failure, apierr)
			require.Equal(t, tc.code, apierr.Code)
		})
	}
}

func TestAudioFeedbackValidationAndUnknownFailures(t *testing.T) {
	feedbackAfterRewording := func(failure error) audioFeedback {
		t.Helper()
		var local *localUtilityError
		require.ErrorAs(t, failure, &local)
		local.message = "Updated local validation guidance."
		require.EqualError(t, failure, local.message)
		return audioFailureFeedback(nil, "generate", fmt.Errorf("outer context: %w", failure))
	}
	settings := audioDefaultSettings()
	require.Equal(t, "Text is empty.", feedbackAfterRewording(validateAudioSettings(settings)).Summary)
	settings.Input = "synthetic-private"
	settings.Speed = 5
	require.Equal(t, "Format or speed is invalid.", feedbackAfterRewording(validateAudioSettings(settings)).Summary)
	settings.Speed = 1
	settings.Model = "tts-1"
	settings.Instructions = "synthetic-private"
	require.Equal(t, "Model cannot use Instructions.", feedbackAfterRewording(validateAudioSettings(settings)).Summary)
	settings.Instructions = ""
	settings.Voice = "ballad"
	require.Equal(t, "Voice cannot use this Model.", feedbackAfterRewording(validateAudioSettings(settings)).Summary)
	capture := &audioCapture{}
	response := &http.Response{Header: http.Header{"Content-Type": {"application/json"}}, Body: http.NoBody}
	require.Equal(t, "No complete audio file returned.", feedbackAfterRewording(capture.receive(response)).Summary)
	settings = audioDefaultSettings()
	settings.Input = "Synthetic audio request"
	_, failure := generateAudio(t.Context(), audioGenerateTestCommand(), func(context.Context, *cli.Command) error {
		return nil
	}, audioTestStore(t), settings)
	require.Equal(t, "No complete audio file returned.", feedbackAfterRewording(failure).Summary)
	for _, failure := range []error{audioFeedbackOpaqueError{}, audioFailure("synthetic-private", audioFeedbackOpaqueError{}), audioFailure("Enter text before generating speech.", nil)} {
		require.Equal(t, "Speech request failed.", audioFailureFeedback(nil, "generate", failure).Summary)
		feedback := audioFailureFeedback(nil, "synthetic-private-operation", failure)
		require.Equal(t, "Audio operation failed.", feedback.Summary)
		require.Equal(t, "The cause is unavailable. Review this action before repeating it.", feedback.Action)
		require.Zero(t, feedback.StatusCode)
	}
	require.Equal(t, audioFeedback{}, audioFailureFeedback(nil, "generate", nil))
}

// Calling Error would expose unknown local data. Classification must not call it.
type audioFeedbackOpaqueError struct{}

func (audioFeedbackOpaqueError) Error() string { panic("audio feedback called arbitrary Error") }

func TestAudioFeedbackPreservesJoinedExitAndCancellation(t *testing.T) {
	apierr := &openai.Error{StatusCode: 401}
	failure := errors.Join(cli.Exit("synthetic-private", 27), apierr)
	feedback := audioFailureFeedback(nil, "generate", failure)
	require.Equal(t, "The API could not verify your access.", feedback.Summary)
	require.Equal(t, 401, feedback.StatusCode)
	var exit cli.ExitCoder
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 27, exit.ExitCode())
	require.ErrorIs(t, failure, apierr)
	failure = errors.Join(failure, context.Canceled)
	feedback = audioFailureFeedback(nil, "generate", failure)
	require.Equal(t, "Audio operation canceled.", feedback.Summary)
	require.Empty(t, feedback.RequestID)
	require.Zero(t, feedback.StatusCode)
	require.ErrorIs(t, failure, context.Canceled)
	require.ErrorIs(t, failure, apierr)
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 27, exit.ExitCode())
}
