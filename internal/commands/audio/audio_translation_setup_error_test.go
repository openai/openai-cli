package audio

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTranslationSetupEventFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		fields      string
		configuring bool
		want        string
	}{
		{"target language", `"type":"invalid_request_error","param":"session.audio.output.language"`, true, "Choose another language"},
		{"transcription", `"param":"audio.input.transcription.model","code":"model_not_found"`, true, "transcription model"},
		{"noise reduction", `"param":"session.audio.input.noise_reduction.type"`, true, "noise reduction"},
		{"unavailable model", `"code":"model_not_found"`, false, "model is unavailable"},
		{"service failure without code", `"type":"server_error","code":null,"param":"audio.output.language"`, true, "Try again later"},
		{"rate limit", `"code":"rate_limit_exceeded"`, false, "Wait before retrying"},
		{"quota", `"code":"insufficient_quota","type":"rate_limit_error"`, false, "Check project billing"},
		{"error before settings", `"param":"session.audio.output.language"`, false, "before it received your settings"},
		{"message only", `"code":null`, true, "rejected the translation settings"},
		{"private fields", `"code":"private-code","type":"private-type","param":"session.audio.output.language\nprivate-param"`, true, "rejected the translation settings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var event translationEvent
			require.NoError(t, json.Unmarshal([]byte(`{"type":"error","error":{"message":"private-message",`+tc.fields+`}}`), &event))
			err := translationEventFailure(event.Error, tc.configuring)
			require.ErrorContains(t, err, tc.want)
			require.NotContains(t, err.Error(), "private")
		})
	}
}

func TestTranslationSetupEventAccess(t *testing.T) {
	for _, tc := range []struct {
		details translationServiceError
		status  int
		code    string
	}{
		{translationServiceError{Code: "invalid_api_key"}, http.StatusUnauthorized, "invalid_api_key"},
		{translationServiceError{Code: "ip_not_authorized"}, http.StatusUnauthorized, "ip_not_authorized"},
		{translationServiceError{Type: "authentication_error", Code: "private-code"}, http.StatusUnauthorized, ""},
		{translationServiceError{Type: "permission_error"}, http.StatusForbidden, ""},
	} {
		err := translationEventFailure(tc.details, false)
		var access *translationAccessError
		require.ErrorAs(t, err, &access)
		require.Equal(t, tc.status, access.status)
		require.Equal(t, tc.code, access.code)
	}
}

func TestTranslationMissingKeyAfterWebSocketUpgrade(t *testing.T) {
	var event translationEvent
	require.NoError(t, json.Unmarshal([]byte(`{"type":"error","error":{"type":"invalid_request_error","code":null,"param":null,"message":"You didn't provide an API key. Private diagnostic."}}`), &event))
	err := translationEventFailure(event.Error, false)
	var access *translationAccessError
	require.ErrorAs(t, err, &access)
	require.Equal(t, http.StatusUnauthorized, access.status)
	require.Empty(t, access.code)
	require.NotContains(t, err.Error(), "Private diagnostic")
	require.NotErrorAs(t, translationEventFailure(event.Error, true), &access)
	for _, message := range []string{"Your API key does not have access to this model.", "Authorization service is temporarily unavailable."} {
		event.Error.Message = message
		require.ErrorAs(t, translationEventFailure(event.Error, false), &access)
		require.Zero(t, access.status, "mentioning credentials does not establish an authentication failure")
	}
}
