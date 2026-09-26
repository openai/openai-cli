package transformers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCompletedImageResult(t *testing.T) {
	value, err := CompletedImageResult(t.Context(), gjson.Parse(`{"type":"image_generation.completed","b64_json":"a\\b\"c","future":{"opaque":true}}`))
	require.NoError(t, err)
	require.True(t, gjson.Valid(value.Raw))
	require.Equal(t, `a\b"c`, value.Get("data.0.b64_json").String())
	for _, raw := range []string{
		`{"type":"image_generation.partial_image","b64_json":"synthetic"}`,
		`{"type":"image_edit.partial_image","b64_json":"synthetic"}`,
		`{"type":"image_generation.completed","b64_json":null}`,
		`{"type":"image_generation.completed","b64_json":123}`,
		`{"type":"image_generation.completed","b64_json":""}`,
		`{"type":"image_generation.completed","b64_json":"synthetic-private",invalid}`,
	} {
		_, err := CompletedImageResult(t.Context(), gjson.Parse(raw))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "synthetic-private")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = CompletedImageResult(ctx, gjson.Result{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCompletedImageResultEditingAndGenerationCompatibility(t *testing.T) {
	event := gjson.Parse(`{"type":"image_edit.completed","b64_json":"synthetic"}`)
	result, err := CompletedImageResult(t.Context(), event)
	require.NoError(t, err)
	require.Equal(t, "synthetic", result.Get("data.0.b64_json").String())
	_, err = ImageGenerationResult(t.Context(), event)
	require.Error(t, err)
	generation := gjson.Parse(`{"type":"image_generation.completed","b64_json":"synthetic"}`)
	result, err = ImageGenerationResult(t.Context(), generation)
	require.NoError(t, err)
	require.Equal(t, "synthetic", result.Get("data.0.b64_json").String())
}
