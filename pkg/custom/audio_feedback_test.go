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
		{401, "", "HTTP 401: Unauthorized.", "Check key, project, organization.\nSetup: openai help setup"},
		{403, "", "HTTP 403: Access denied.", "Check key permissions and project access."},
		{404, "", "HTTP 404: Model/API not found.", "Check Model, project access, and the configured API endpoint."},
		{404, "model_not_found", "HTTP 404: Model unavailable.", "Check Model and project access."},
		{400, "model_not_found", "HTTP 400: Model unavailable.", "Check Model and project access."},
		{400, "", "HTTP 400: Invalid speech settings.", "Review Text, Instructions, and Settings."},
		{422, "", "HTTP 422: Invalid speech settings.", "Review Text, Instructions, and Settings."},
		{429, "", "HTTP 429: Too many requests.", "Wait before another explicit generation."},
		{429, "insufficient_quota", "HTTP 429: API usage limit reached.", "Check project billing and usage limits."},
		{409, "", "HTTP 409: Request conflict.", "Check API status before generating again."},
		{408, "", "HTTP 408: Request timed out.", "The API may have received the request. Check usage before generating again."},
		{504, "", "HTTP 504: Request timed out.", "The API may have received the request. Check usage before generating again."},
		{503, "", "HTTP 503: Service unavailable.", "Check API status and usage before generating again."},
		{499, "", "HTTP 499: Speech request failed.", "The cause is unavailable. Check settings and API status before generating again."},
		{0, "", "Speech request failed.", "The cause is unavailable. Check settings and API status before generating again."},
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
				require.Equal(t, audioFeedback{Summary: tc.summary, Action: tc.action, RequestID: "req_synthetic"}, feedback)
				require.ErrorIs(t, failure, apierr)
				require.Equal(t, private, apierr.Message)
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
	require.Equal(t, "HTTP 401: Unauthorized.", audioFailureFeedback(command, "generate", &openai.Error{StatusCode: 401, Code: "model_not_found"}).Summary)
}

func TestAudioFeedbackRequestIDAndInvocation(t *testing.T) {
	command := audioGenerateTestCommand()
	command.Metadata = map[string]any{"help-invocation": "'/tmp/CLI trial/openai'"}
	apierr := requestIDError(t, 401, "req_synthetic")
	feedback := audioFailureFeedback(command, "generate", apierr)
	require.Equal(t, "Check key, project, organization.\nSetup: '/tmp/CLI trial/openai' help setup", feedback.Action)
	require.Equal(t, "req_synthetic", feedback.RequestID)
	for _, id := range []string{"req_bad\rforged", "https://synthetic.invalid/?key=private", strings.Repeat("a", 257), ""} {
		apierr.Response.Header.Set("X-Request-ID", id)
		require.Empty(t, audioFailureFeedback(command, "generate", apierr).RequestID)
	}
	apierr.Response.Header.Set("X-Request-ID", "req_synthetic")
	apierr.Request = &http.Request{Header: http.Header{"x-request-id": {"synthetic-private"}}}
	require.Empty(t, audioFailureFeedback(command, "generate", apierr).RequestID)
}

func TestAudioFeedbackPreservesRetainedOperationProvenance(t *testing.T) {
	private := errors.New("synthetic-private-path-and-prompt")
	for _, tc := range []struct {
		name, operation string
		failure         error
		summary, action string
	}{
		{"measurement", "generate", audioFailure("Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", private), "Measurement failed.", "Retained audio remains available."},
		{"decoder", "generate", audioFailure("Audio is retained, but measurement failed. Compressed formats need FFmpeg on PATH.", audiomedia.ErrDecoderUnavailable), "Measurement needs FFmpeg.", "Retained audio remains available."},
		{"invalid audio", "generate", errors.Join(audiomedia.ErrInvalidAudio, private), "Audio could not be decoded.", "Export the retained file"},
		{"unsupported format", "open", errors.Join(audiomedia.ErrUnsupportedFormat, private), "Audio format is unsupported.", "Export the retained file"},
		{"analysis", "open", errors.Join(audiomedia.ErrAnalysisFailed, private), "Audio measurement failed.", "Export the retained file"},
		{"player unavailable", "play", &audiomedia.UnavailableError{Tool: "ffplay"}, "Playback needs FFplay.", "Retained audio remains available."},
		{"playback decoder unavailable", "play", audioFailure("Could not play the retained audio.", &audiomedia.UnavailableError{Tool: "ffmpeg"}), "Playback needs FFmpeg.", "Retained audio remains available."},
		{"player failed", "play", errors.Join(audiomedia.ErrPlaybackFailed, private), "Playback failed.", "Retained audio remains available."},
		{"history verification", "open", errors.Join(audiohistory.ErrInvalid, private), "History could not be verified.", "private cache permissions"},
		{"history replacement", "export", errors.Join(audiohistory.ErrChanged, private), "History changed during this action.", "select the result again"},
		{"history read", "history", private, "History failed.", "retained records"},
		{"cached audition", "generate", audioFailure("Could not inspect cached auditions. No speech request was sent.", private), "History failed.", "retained records"},
		{"open for saving", "export", audioFailure("Could not open retained audio for saving.", private), "History failed.", "retained records"},
		{"export", "export", &downloadSaveError{message: "synthetic-private", cause: private}, "Save failed.", "Retained audio remains available."},
		{"receipt", "export", &saveReceiptError{cause: private}, "Save receipt failed.", "Check the destination before saving again."},
		{"retention", "generate", audioFailure("Could not retain completed audio in private history.", private), "Audio retention failed.", "Check History and API usage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feedback := audioFailureFeedback(nil, tc.operation, tc.failure)
			require.Equal(t, tc.summary, feedback.Summary)
			require.Contains(t, feedback.Action, tc.action)
			require.NotContains(t, feedback.Summary+feedback.Action, "synthetic-private")
			require.NotContains(t, feedback.Summary, "Speech request")
			require.Empty(t, feedback.RequestID)
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
	feedback := audioFailureFeedback(nil, "generate", audioFailure("Could not retain completed audio in private history.", io.ErrUnexpectedEOF))
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
	feedback := audioFailureFeedback(nil, "generate", &openai.Error{StatusCode: 401, Code: "invalid_api_key", Message: "The synthetic key is expired"})
	require.Equal(t, "HTTP 401: Unauthorized.", feedback.Summary)
	for _, unsupported := range []string{"expired", "invalid", "wrong", "rejected", "environment", "OPENAI_API_KEY"} {
		require.NotContains(t, feedback.Summary+feedback.Action, unsupported)
	}
}

func TestAudioFeedbackValidationAndUnknownFailures(t *testing.T) {
	settings := audioDefaultSettings()
	require.Equal(t, "Text is empty.", audioFailureFeedback(nil, "generate", validateAudioSettings(settings)).Summary)
	settings.Input = "synthetic-private"
	settings.Speed = 5
	require.Equal(t, "Format or speed is invalid.", audioFailureFeedback(nil, "generate", validateAudioSettings(settings)).Summary)
	settings.Speed = 1
	settings.Model = "tts-1"
	settings.Instructions = "synthetic-private"
	require.Equal(t, "Model cannot use Instructions.", audioFailureFeedback(nil, "generate", validateAudioSettings(settings)).Summary)
	settings.Instructions = ""
	settings.Voice = "ballad"
	require.Equal(t, "Voice cannot use this Model.", audioFailureFeedback(nil, "generate", validateAudioSettings(settings)).Summary)
	for _, message := range []string{"The speech response did not contain a finite audio file.", "The speech request did not finish an audio file."} {
		require.Equal(t, "No complete audio file returned.", audioFailureFeedback(nil, "generate", audioFailure(message, nil)).Summary)
	}
	for _, failure := range []error{audioFeedbackOpaqueError{}, audioFailure("synthetic-private", audioFeedbackOpaqueError{})} {
		require.Equal(t, "Speech request failed.", audioFailureFeedback(nil, "generate", failure).Summary)
		feedback := audioFailureFeedback(nil, "synthetic-private-operation", failure)
		require.Equal(t, "Audio operation failed.", feedback.Summary)
		require.Equal(t, "The cause is unavailable. Review this action before repeating it.", feedback.Action)
	}
	require.Equal(t, audioFeedback{}, audioFailureFeedback(nil, "generate", nil))
}

// Calling Error would expose unknown local data. Classification must not call it.
type audioFeedbackOpaqueError struct{}

func (audioFeedbackOpaqueError) Error() string { panic("audio feedback called arbitrary Error") }

func TestAudioFeedbackPreservesJoinedExitAndCancellation(t *testing.T) {
	apierr := &openai.Error{StatusCode: 401}
	failure := errors.Join(cli.Exit("synthetic-private", 27), apierr)
	require.Equal(t, "HTTP 401: Unauthorized.", audioFailureFeedback(nil, "generate", failure).Summary)
	var exit cli.ExitCoder
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 27, exit.ExitCode())
	require.ErrorIs(t, failure, apierr)
	failure = errors.Join(failure, context.Canceled)
	feedback := audioFailureFeedback(nil, "generate", failure)
	require.Equal(t, "Audio operation canceled.", feedback.Summary)
	require.Empty(t, feedback.RequestID)
	require.ErrorIs(t, failure, context.Canceled)
	require.ErrorIs(t, failure, apierr)
	require.ErrorAs(t, failure, &exit)
	require.Equal(t, 27, exit.ExitCode())
}
