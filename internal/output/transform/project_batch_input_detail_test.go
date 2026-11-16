package transform

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestProjectBatchInputDetailRetainsContentAndEverySetting(t *testing.T) {
	input := gjson.Parse(`{"custom_id":"one","method":"POST","url":"/v1/responses","body":{"model":"synthetic","instructions":"Classify","input":[{"type":"input_image","image_url":"synthetic-image"}],"max_output_tokens":32,"reasoning":{"effort":"low"},"temperature":0,"future_setting":{"nested":true},"method":"body-value"}}`)
	value, err := ProjectBatchRecord(t.Context(), input, "input_detail")
	require.NoError(t, err)
	require.Equal(t, input.Get("body.input").Raw, value.Get("input").Raw)
	require.Equal(t, "Classify", value.Get("instructions").Str)
	require.Equal(t, "POST", value.Get("settings.method").Str)
	require.Equal(t, "body-value", value.Get("settings.parameters.method").Str)
	for _, key := range []string{"max_output_tokens", "temperature", "reasoning", "future_setting"} {
		require.Equal(t, input.Get("body."+key).Raw, value.Get("settings.parameters."+key).Raw)
	}
}

func TestProjectBatchInputDetailFallsBackWithoutDroppingUnknownEnvelopesOrDuplicateFields(t *testing.T) {
	for _, raw := range []string{
		`{"custom_id":"one","extra":42,"body":{"input":"hello"}}`,
		`{"custom_id":"one","body":{"input":"first","input":"second"}}`,
		`{"custom_id":"one","body":"unrecognized"}`,
	} {
		value, err := ProjectBatchRecord(t.Context(), gjson.Parse(raw), "input_detail")
		require.NoError(t, err)
		require.False(t, value.Exists(), "complete streaming fallback preserves all fields")
	}
}
