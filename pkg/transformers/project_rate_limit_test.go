package transformers

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectRateLimitExplicitUnitsAndExactValues(t *testing.T) {
	input := `{
  "object": "project.rate_limit",
  "id": "rl_full-copyable-id",
  "model": "synthetic-model-\u65e5\u672c\u8a9e-\u001b[31m\n",
  "max_requests_per_1_minute": 0,
  "max_tokens_per_1_minute": 9007199254740993,
  "max_images_per_1_minute": null,
  "max_audio_megabytes_per_1_minute": 17,
  "max_requests_per_1_day": 9223372036854775809,
  "batch_1_day_max_input_tokens": 999999999999999999999999999999999999,
  "max_tokens_per_1_day": 31,
  "future": { "number": 9007199254740995, "number": null, "array": [false, 1e40] }
}`
	want := `{
  "object": "project.rate_limit",
  "id": "rl_full-copyable-id",
  "model": "synthetic-model-\u65e5\u672c\u8a9e-\u001b[31m\n",
  "max_requests_per_minute": 0,
  "max_tokens_per_minute": 9007199254740993,
  "max_images_per_minute": null,
  "max_audio_megabytes_per_minute": 17,
  "max_requests_per_day": 9223372036854775809,
  "max_batch_input_tokens_per_day": 999999999999999999999999999999999999,
  "max_tokens_per_1_day": 31,
  "future": { "number": 9007199254740995, "number": null, "array": [false, 1e40] }
}`
	value := gjson.Parse(input)
	got, err := ProjectRateLimit(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, want, got.Raw)
	require.Equal(t, input, value.Raw, "The original response must remain unchanged.")
}

func TestProjectRateLimitAbsentNullZeroAndNegativeValues(t *testing.T) {
	for _, value := range []string{"null", "0", "-1", "-0", "123456789012345678901234567890"} {
		t.Run(value, func(t *testing.T) {
			input := `{"object":"project.rate_limit","max_requests_per_1_minute":` + value + `}`
			got, err := ProjectRateLimit(t.Context(), gjson.Parse(input))
			require.NoError(t, err)
			require.Equal(t, `{"object":"project.rate_limit","max_requests_per_minute":`+value+`}`, got.Raw)
			require.False(t, got.Get("max_tokens_per_minute").Exists(), "Absent limits must stay absent.")
		})
	}
	input := ` { "object": "project.rate_limit", "id": "rl_demo", "model": "synthetic", "future": null } `
	value := gjson.Parse(input)
	got, err := ProjectRateLimit(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, value, got, "Records without known limits need no projection.")
}

func TestProjectRateLimitFallbackForMalformedRecords(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `"project.rate_limit"`, `true`,
		`{"max_requests_per_1_minute":1}`,
		`{"object":"organization.project.rate_limit","max_requests_per_1_minute":1}`,
		`{"object":"project.rate_limit.extra","max_requests_per_1_minute":1}`,
		`{"object":null,"max_requests_per_1_minute":1}`,
		`{"object":{"name":"project.rate_limit"},"max_requests_per_1_minute":1}`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":1`,
		`{"object":"project.rate_limit","max_requests_per_1_minute":1,}`,
		`{"object":"project.rate_limit","id":42,"max_requests_per_1_minute":1}`,
		`{"object":"project.rate_limit","model":null,"max_requests_per_1_minute":1}`,
	} {
		t.Run(input, func(t *testing.T) {
			value := gjson.Parse(input)
			got, err := ProjectRateLimit(t.Context(), value)
			require.NoError(t, err)
			require.Equal(t, value, got)
		})
	}
	for _, invalid := range []string{`"1"`, `true`, `false`, `[]`, `{}`, `1.5`, `1.0`, `1e3`, `1E+3`} {
		t.Run(invalid, func(t *testing.T) {
			input := `{"object":"project.rate_limit","max_requests_per_1_minute":1,"max_tokens_per_1_minute":` + invalid + `}`
			got, err := ProjectRateLimit(t.Context(), gjson.Parse(input))
			require.NoError(t, err)
			require.Equal(t, input, got.Raw, "Malformed rates must prevent partial projection.")
		})
	}
}

func TestProjectRateLimitFallbackForDuplicatesAndCollisions(t *testing.T) {
	for _, fields := range []string{
		`"object":"project.rate_limit"`,
		`"object":"another.object"`,
		`"ob\u006aect":"project.rate_limit"`,
		`"max_requests_per_1_minute":1`,
		`"max_requests_per_1_min\u0075te":2`,
		`"max_requests_per_min\u0075te":2`,
		`"id":"one","id":"two"`,
		`"model":"one","model":"two"`,
		`"future":1,"future":2`,
	} {
		input := `{"object":"project.rate_limit","max_requests_per_1_minute":1,` + fields + `}`
		got, err := ProjectRateLimit(t.Context(), gjson.Parse(input))
		require.NoError(t, err)
		require.Equal(t, input, got.Raw, "Duplicate fields must keep the original record.")
	}
	for _, pair := range [][2]string{
		{"max_requests_per_1_minute", "max_requests_per_minute"},
		{"max_tokens_per_1_minute", "max_tokens_per_minute"},
		{"max_images_per_1_minute", "max_images_per_minute"},
		{"max_audio_megabytes_per_1_minute", "max_audio_megabytes_per_minute"},
		{"max_requests_per_1_day", "max_requests_per_day"},
		{"batch_1_day_max_input_tokens", "max_batch_input_tokens_per_day"},
	} {
		for _, fields := range []string{
			`"` + pair[0] + `":1,"` + pair[1] + `":2`,
			`"` + pair[1] + `":2,"` + pair[0] + `":1`,
		} {
			input := `{"object":"project.rate_limit",` + fields + `}`
			got, err := ProjectRateLimit(t.Context(), gjson.Parse(input))
			require.NoError(t, err)
			require.Equal(t, input, got.Raw, "Renamed fields must not obscure unfamiliar fields.")
		}
	}
}

func TestProjectRateLimitPreservesNestedRecordsAndEscapedKeys(t *testing.T) {
	input := `{"data":[{"ob\u006aect":"project.rate_limit","max_requests_per_1_min\u0075te":1,"future":{"max_tokens_per_1_minute":2}}]}`
	value := gjson.Parse(input).Get("data.0")
	original := value
	got, err := ProjectRateLimit(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, `{"ob\u006aect":"project.rate_limit","max_requests_per_minute":1,"future":{"max_tokens_per_1_minute":2}}`, got.Raw)
	require.Equal(t, original, value)
}

func TestProjectRateLimitLargeUnknownFieldAndCancellation(t *testing.T) {
	payload := strings.Repeat("synthetic-data-", 128*1024)
	input := `{"object":"project.rate_limit","max_requests_per_1_minute":1,"future":"` + payload + `"}`
	value := gjson.Parse(input)
	got, err := ProjectRateLimit(t.Context(), value)
	require.NoError(t, err)
	require.Equal(t, strings.Replace(input, "max_requests_per_1_minute", "max_requests_per_minute", 1), got.Raw)

	// Later checkpoints cancel during chunked copying of the unfamiliar field.
	for _, after := range []int32{1, 2, 4, 8, 12, 40} {
		ctx, cancel := context.WithCancel(t.Context())
		controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: after}
		got, err := ProjectRateLimit(controlled, value)
		cancel()
		require.ErrorIs(t, err, context.Canceled, "cancel after %d checks", after)
		require.Equal(t, gjson.Result{}, got, "Cancellation must not produce partial output or fallback.")
		require.Equal(t, input, value.Raw)
	}
}
